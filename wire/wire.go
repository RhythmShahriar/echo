// Package wire is the small JSON protocol between Echo and the connectors
// inside a site (the WordPress mu-plugin, the Laravel package, the Node
// package): a connector reports each email and each outbound HTTP
// request, and for HTTP requests Echo answers whether to let it through or
// what to answer instead. Keep it stable: connectors in other languages
// implement it.
//
//	POST /v1/capture   Capture → Decision
package wire

// Version is sent by connectors and checked by Echo.
const Version = 1

// RunHeader carries "<run>/<action>" from the thing that triggered a
// request (Soundcheck's browser, `echo run`) to the connector, which copies
// it into every capture, so messages are tied to the action that caused
// them. Connectors also accept it as a cookie of the same name.
const RunHeader = "X-Echo-Run"

// TokenHeader authenticates connectors when Echo listens beyond loopback.
const TokenHeader = "X-Echo-Token" //nolint:gosec // a header name, not a credential

// Phases of an HTTP capture.
const (
	Before = "before" // about to send: Echo decides
	After  = "after"  // sent (observe mode, or an allowed host): the response
)

// Capture is one email or HTTP request a connector saw.
type Capture struct {
	Version int    `json:"v"`
	Kind    string `json:"kind"`            // email or http
	Phase   string `json:"phase,omitempty"` // http only: before or after
	ID      string `json:"id,omitempty"`    // after: the ID Echo returned for before
	Run     string `json:"run,omitempty"`   // from X-Echo-Run, "<run>/<action>"
	Source  Source `json:"source"`
	Email   *Email `json:"email,omitempty"`
	HTTP    *HTTP  `json:"http,omitempty"`
	Error   string `json:"error,omitempty"` // the send failed (wp_mail_failed, a transport error)
}

// Source describes the connector and what it hooked.
type Source struct {
	Via    string `json:"via"`              // wordpress, laravel, node, smtp, proxy
	Stack  string `json:"stack,omitempty"`  // wordpress, laravel, node, php
	Plugin string `json:"plugin,omitempty"` // best guess at the sender: a plugin slug, a package
	Hook   string `json:"hook,omitempty"`   // wp_mail, pre_http_request, fetch, nodemailer…
	Host   string `json:"host,omitempty"`   // the site's own host (always allowed)
}

// Email is a message handed to the site's mailer.
type Email struct {
	From    string            `json:"from,omitempty"`
	To      []string          `json:"to,omitempty"`
	CC      []string          `json:"cc,omitempty"`
	BCC     []string          `json:"bcc,omitempty"`
	ReplyTo string            `json:"reply_to,omitempty"`
	Subject string            `json:"subject,omitempty"`
	Text    string            `json:"text,omitempty"`
	HTML    string            `json:"html,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Raw     string            `json:"raw,omitempty"` // full MIME, when the connector has it
}

// HTTP is an outbound request and, after sending, its response.
type HTTP struct {
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Status   int               `json:"status,omitempty"` // after
	Response string            `json:"response,omitempty"`
}

// Decisions for an HTTP capture.
const (
	Allow = "allow" // send it for real
	Fake  = "fake"  // don't send; answer with Response
	Block = "block" // don't send; fail the request
)

// Decision is Echo's answer to a capture.
type Decision struct {
	ID       string    `json:"id"`
	Mode     string    `json:"mode"`               // seal or observe
	Action   string    `json:"action,omitempty"`   // http: allow, fake or block; email: fake (don't send) or allow
	Response *Response `json:"response,omitempty"` // for fake
}

// Response is the answer a connector gives the site instead of sending.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}
