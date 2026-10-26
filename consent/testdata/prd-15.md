
> **What a pass means.** In these runs, from this machine, with this browser and these settings, no tracker on Echo's list sent a request or set a cookie before the visitor chose or after Reject. It does not mean the site complies with the GDPR, ePrivacy, CCPA/CPRA or any other law. Evidence, not legal advice.
>
> **One visitor, one place.** Banners differ by country, language and device. Geo-targeted banners may not show to a CI runner in the US; Echo can set the browser's language and time zone (`--geo`) but not its IP address. Run it from the region that matters, or through a proxy there.
>
> **Blind spots.** Trackers that are not on the list; server-side tagging on your own domain when its requests don't look like GA4 or Meta; fingerprinting without network requests; tags that wait longer than the settle time or need more interaction than a scroll and one more page; sites that hide tags from automated browsers.
>
> **Consent Mode advanced.** Google says its tags in advanced mode "send measurements without cookies" while consent is denied. Whether that is acceptable before consent is disputed. Echo reports these pings as `consent/cookieless-ping` under the default policy `advanced`; `--consent-mode basic` or `strict` counts them as hits.
>
> **gcd is unofficial.** Google doesn't document the `gcd` parameter. Echo shows the raw value next to a reading based on community documentation and treats letters it doesn't know as unknown.
>
> **Pseudonymised, not anonymised.** Masked or hashed identifiers in reports can still be personal data. Treat reports like logs.
>
> **What leaves the machine.** Requests to the site and to the scripts it loads (which shows this machine's IP address to those servers). No tracking hits unless `--send-hits`. Nothing goes to Rhythm. The update check contacts GitHub (`RHYTHM_OFFLINE=1` turns it off).
>
> **Banners Echo can't handle** are reported as "couldn't check" (exit 3), never as a pass. Tested CMPs and the dates their fixtures were captured are in the benchmark.
>
> **A signature** proves who made the evidence file and that it hasn't changed. It doesn't prove the site behaves the same for every visitor, region or day. Keyless signing in the Action writes the repository and workflow name to the public Rekor log.

