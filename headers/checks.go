package headers

import (
	"net/mail"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/model"
)

const fixOriginal = "Save the message from the recipient's mailbox (in Gmail: Show original, then Download original), not from the sent folder, and run the check on that file."

func (r *Result) checkAuth() {
	a := r.Auth
	if a.Server == "" && len(a.Methods) == 0 {
		r.issue("auth", model.Notice, "No Authentication-Results header",
			"The message carries no receiver verdict, so SPF, DKIM and DMARC results can't be read; only the signatures and the route are checked.", fixOriginal)
		return
	}
	src := a.Server
	if a.ARC {
		src += " (ARC)"
	}
	switch a.DMARC {
	case "fail":
		r.issue("dmarc", model.Error, "DMARC failed",
			src+" reports dmarc=fail for "+r.FromDomain+": neither SPF nor DKIM passed for a domain aligned with From. Mail like this goes to spam or is rejected under p=quarantine/reject.",
			"Run `rhythm echo check "+r.FromDomain+"` with this message's sender, then publish the DKIM records of the sending service under "+r.FromDomain+".")
	case "temperror", "permerror":
		r.issue("dmarc", model.Warning, "DMARC could not be evaluated",
			src+" reports dmarc="+a.DMARC+"; a permerror usually means a malformed _dmarc record.",
			"Check the TXT record at _dmarc."+r.FromDomain+" with `rhythm echo check "+r.FromDomain+"`.")
	case "none", "":
		r.issue("dmarc", model.Warning, "Receiver found no DMARC policy",
			src+" didn't apply DMARC to "+r.FromDomain+" (dmarc=none or no result), so it has no policy to protect it.",
			`Publish a TXT record at _dmarc.`+r.FromDomain+` such as "v=DMARC1; p=none; rua=mailto:dmarc@`+dns.OrgDomain(r.FromDomain)+`".`)
	}
	mailfrom := r.Sender.ReturnPathDomain
	switch a.SPF {
	case "fail":
		r.issue("spf", model.Error, "SPF failed",
			src+" reports spf=fail: the relay "+orUnknown(r.Sender.SMTPHost)+" isn't allowed to send for "+mailfrom+".",
			"Add the sending service's include (or the server's ip4:) to the SPF record at "+mailfrom+".")
	case "softfail":
		r.issue("spf", model.Warning, "SPF softfail",
			src+" reports spf=softfail: the SPF record at "+mailfrom+" doesn't list the relay "+orUnknown(r.Sender.SMTPHost)+" and ends in ~all.",
			"Add the sending service's include (or the server's ip4:) to the SPF record at "+mailfrom+".")
	case "neutral":
		r.issue("spf", model.Notice, "SPF result is neutral",
			"The SPF record at "+mailfrom+" doesn't say whether the relay may send (?all or no matching term).",
			"List the sending service in the SPF record at "+mailfrom+" and end it with ~all.")
	case "none":
		r.issue("spf", model.Warning, "SPF found no record",
			"The envelope sender's domain "+orUnknown(mailfrom)+" publishes no SPF record.",
			"Publish a v=spf1 TXT record at "+orUnknown(mailfrom)+" listing the sending service.")
	case "permerror":
		r.issue("spf", model.Error, "SPF record is broken (permerror)",
			"The SPF record at "+mailfrom+" can't be evaluated: a syntax error, more than 10 lookups, or multiple records.",
			"Run `rhythm echo check "+mailfrom+"` to find the problem in the SPF record.")
	case "temperror":
		r.issue("spf", model.Warning, "SPF could not be evaluated",
			"The receiver hit a DNS error looking up SPF for "+mailfrom+".", "Check the domain's nameservers answer TXT queries reliably.")
	}
	switch a.DKIM {
	case "fail", "neutral", "permerror", "policy":
		d := a.prop("dkim", "header.d")
		r.issue("dkim", model.Error, "DKIM signature failed",
			src+" reports dkim="+a.DKIM+" for d="+orUnknown(d)+": the key is missing or the message was changed after signing (a footer added by a plugin or a mailing list).",
			"Check the key at "+orUnknown(a.prop("dkim", "header.s"))+"._domainkey."+orUnknown(d)+" with `rhythm echo check`, and that nothing modifies mail after the service signs it.")
	}
}

func (r *Result) checkSignatures() {
	if len(r.Signatures) == 0 {
		r.issue("dkim", model.Error, "Message is not DKIM-signed",
			"There's no DKIM-Signature header. Without one DMARC can pass only on SPF, which breaks when mail is forwarded, and Gmail and Yahoo require DKIM from bulk senders.",
			"Send through a service that signs mail (or enable DKIM signing on the server) and publish its key under "+orUnknown(r.FromDomain)+".")
		return
	}
	for _, s := range r.Signatures {
		if s.Algorithm == "rsa-sha1" {
			r.issue("dkim", model.Warning, "DKIM signature uses SHA-1",
				"The signature for d="+s.Domain+" uses rsa-sha1, which RFC 8301 forbids; receivers treat it as unsigned.",
				"Switch the signer to rsa-sha256.")
		}
		if s.Length {
			r.issue("dkim", model.Notice, "DKIM signature covers only part of the body",
				"The signature for d="+s.Domain+" has l=, so content appended after signing still verifies, which lets others reuse the signature.",
				"Turn off body-length limits in the signer.")
		}
		if len(s.Headers) > 0 && !contains(s.Headers, "from") {
			r.issue("dkim", model.Error, "DKIM signature does not cover From",
				"The signature for d="+s.Domain+" doesn't sign the From header, so it is invalid (RFC 6376 5.4).",
				"Fix the signer's header list (h=) to include from.")
		}
	}
}

// checkAlignment applies DMARC's relaxed alignment to what the message
// carries, independent of the receiver's verdict.
func (r *Result) checkAlignment() {
	if r.FromDomain == "" {
		return
	}
	dkim := false
	for _, s := range r.Signatures {
		if dns.Aligned(s.Domain, r.FromDomain, false) {
			dkim = true
		}
	}
	spf := dns.Aligned(r.Sender.ReturnPathDomain, r.FromDomain, false)
	if r.Sender.ReturnPathDomain == "" && len(r.Signatures) == 0 {
		return
	}
	var ds []string
	for _, s := range r.Signatures {
		ds = append(ds, s.Domain)
	}
	detail := "From domain " + r.FromDomain + "; DKIM d=" + orUnknown(strings.Join(ds, ", ")) + "; Return-Path domain " + orUnknown(r.Sender.ReturnPathDomain) + "."
	who := orService(r.Provider)
	switch {
	case !dkim && !spf:
		r.issue("alignment", model.Error, "From domain is not aligned with SPF or DKIM",
			"Neither the DKIM signature nor the Return-Path uses "+r.FromDomain+", so DMARC fails even if SPF and DKIM pass (inboxes show \"via\" the service). "+detail,
			"Set up domain authentication in "+who+": publish its DKIM records under "+r.FromDomain+" so it signs with d="+r.FromDomain+".")
	case !dkim && spf && len(r.Signatures) > 0:
		r.issue("alignment", model.Warning, "DKIM is not aligned with the From domain",
			"DMARC passes only through the Return-Path, which forwarding breaks. "+detail,
			"Publish "+who+"'s DKIM records under "+r.FromDomain+" so it signs with d="+r.FromDomain+".")
	}
}

var (
	rePHPVersion = regexp.MustCompile(`(?i)\bphp/\d+(\.\d+)*`)
	reBulk       = regexp.MustCompile(`(?i)^(bulk|list|junk)$`)
)

func (r *Result) checkHeaders(h mail.Header) {
	if r.From == "" {
		r.issue("headers", model.Error, "From header is missing or invalid",
			"The From header is "+quoteOr(h.Get("From"))+"; receivers reject messages without a valid From address.",
			"Set a From address at your domain in the site's mailer settings.")
	}
	if r.MessageID == "" {
		r.issue("headers", model.Warning, "Message has no Message-ID",
			"Receivers score messages without a Message-ID as spam, and Gmail may reject them.",
			"Send through a mailer that sets Message-ID (any SMTP service or library does).")
	}
	if h.Get("Date") == "" {
		r.issue("headers", model.Warning, "Message has no Date",
			"A Date header is required (RFC 5322 3.6); receivers penalize mail without one.",
			"Send through a mailer that sets Date.")
	}
	if v := h.Get("X-Php-Originating-Script"); v != "" {
		r.issue("leak", model.Warning, "Mail header leaks a server script path",
			"X-PHP-Originating-Script: "+v+" tells every recipient the server user ID and the script that sent the mail.",
			"Set mail.add_x_header = Off in php.ini, or send through an SMTP or API mailer instead of mail().")
	}
	if m := rePHPVersion.FindString(h.Get("X-Mailer")); m != "" {
		r.issue("leak", model.Notice, "Mail header reveals the PHP version",
			"X-Mailer: "+h.Get("X-Mailer")+" shows the server runs "+m+".",
			"Remove or override the X-Mailer header in the mailer configuration.")
	}
	if r.Sender.Provider == "php-mail" {
		r.issue("sender", model.Warning, "Sent with PHP mail() from the web server",
			"The message was handed to the web server's local mail system, which doesn't sign it with DKIM and sends from an IP that mailbox providers distrust.",
			"Send through an authenticated SMTP or API service (WP Mail SMTP, FluentSMTP or Laravel's mailers) with DKIM set up for "+orUnknown(r.FromDomain)+".")
	}
	unsub := h.Get("List-Unsubscribe")
	bulk := reBulk.MatchString(strings.TrimSpace(h.Get("Precedence"))) || h.Get("List-Id") != "" ||
		r.Sender.Provider == "mailchimp" || h.Get("X-Campaign") != "" || h.Get("X-Campaignid") != ""
	if unsub == "" && bulk {
		r.issue("unsubscribe", model.Notice, "Bulk mail has no List-Unsubscribe header",
			"The message looks like bulk mail (Precedence, List-Id or a campaign header), but has no List-Unsubscribe; Gmail and Yahoo require one-click unsubscribe from bulk senders.",
			"Add List-Unsubscribe: <https://…/unsubscribe?id=…>, <mailto:…> and List-Unsubscribe-Post: List-Unsubscribe=One-Click.")
	}
	if strings.Contains(strings.ToLower(unsub), "https://") && !strings.EqualFold(strings.TrimSpace(h.Get("List-Unsubscribe-Post")), "List-Unsubscribe=One-Click") {
		r.issue("unsubscribe", model.Notice, "List-Unsubscribe has no one-click option",
			"There's an HTTPS List-Unsubscribe link but no List-Unsubscribe-Post header, so inboxes can't unsubscribe in one click (RFC 8058).",
			"Add List-Unsubscribe-Post: List-Unsubscribe=One-Click and accept POST on the unsubscribe URL.")
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

func orService(label string) string {
	if label == "" {
		return "the sending service"
	}
	return label
}

func quoteOr(s string) string {
	if s == "" {
		return "missing"
	}
	return `"` + s + `"`
}
