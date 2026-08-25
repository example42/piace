// This file centralizes request/response metadata redaction, per this
// task's brief and design.md section 11 ("TLS private keys and raw
// sensitive data have no String/marshal paths") and requirements.md 3.5
// ("SHALL NOT log private keys, certificate private material, request
// authorization headers, or unredacted sensitive catalog parameter
// values").
//
// Tasks 4-6's protocol adapters must build their model.Diagnostic values
// for a service failure through Describe/SafeMessage in this file rather
// than formatting request/response details ad hoc at each call site.
package transport

import (
	"regexp"
	"time"

	"github.com/example42/piace/internal/model"
)

// authorizationHeaderPattern matches an "Authorization: <value>" header
// line (case-insensitive header name, any header value up to end of line)
// so its value can be replaced before any string reaches a log line, error
// message, or Diagnostic.Message.
var authorizationHeaderPattern = regexp.MustCompile(`(?i)authorization:\s*\S+`)

// pemBlockPattern matches a full PEM block (certificate, private key, or
// any other PEM-encoded material) so it can be replaced before any string
// reaches a log line, error message, or Diagnostic.Message. It matches
// across newlines ("(?s)") since a PEM block is always multi-line.
var pemBlockPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]+-----.*?-----END [A-Z0-9 ]+-----`)

// Sanitize strips any Authorization header value and any PEM-encoded block
// (private key or certificate material) from s, replacing each with a
// fixed marker. It is defense in depth: this package never builds a
// Diagnostic or *Error from a raw response body or a captured header set
// in the first place (see doc.go and client.go), but Sanitize is applied
// to every string SafeMessage/Describe compose from a lower-level error's
// text, since a wrapped net/http or crypto/tls error could in principle
// echo header content.
//
// Sanitize never receives, and must never be given, a raw response body:
// catalog data can contain Puppet Sensitive values, and no regex-based
// scrub over arbitrary catalog JSON is a substitute for the value-level
// redaction boundary in design.md section 7.3. Callers must not pass
// response bodies to this function as a way to "make it safe."
func Sanitize(s string) string {
	s = authorizationHeaderPattern.ReplaceAllString(s, "Authorization: <redacted>")
	s = pemBlockPattern.ReplaceAllString(s, "<redacted-pem-block>")
	return s
}

// Summary is the safe metadata this package retains about one request/
// response for logging or diagnostic use: target host, status code (zero
// when no response was received), operation, and timing. It never
// contains headers or body content.
type Summary struct {
	Host       string
	StatusCode int
	Duration   time.Duration
}

// Describe formats s as a short, safe, single-line string suitable for a
// log line or Diagnostic.Message suffix. It never includes header or body
// content because Summary cannot hold any.
func (s Summary) Describe() string {
	if s.StatusCode == 0 {
		return "host=" + s.Host + " duration=" + s.Duration.String()
	}
	return "host=" + s.Host +
		" status=" + itoa(s.StatusCode) +
		" duration=" + s.Duration.String()
}

// itoa avoids importing strconv solely for one call site's readability;
// kept trivial and allocation-light.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// SafeMessage builds a redaction-safe message for a Diagnostic from a raw
// error's text: it applies Sanitize and then discards everything after the
// first line, so a multi-line stdlib error (which can occasionally embed
// low-level buffer/context content) cannot smuggle extra material into a
// single-line diagnostic. Callers should prefer a *Error's own Message
// field (already secret-free by construction) and use SafeMessage only
// when composing a diagnostic from an error this package did not itself
// classify.
func SafeMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := Sanitize(err.Error())
	for i, r := range msg {
		if r == '\n' {
			return msg[:i]
		}
	}
	return msg
}

// Diagnostic builds a model.Diagnostic for a service failure using only
// safe metadata: operation, certname, target host, and a redaction-safe
// message. It is the single reusable entry point tasks 4-6's adapters
// should call to report a compiler/PuppetDB transport failure, rather than
// formatting request/response details ad hoc at each call site.
//
// message must already be safe (e.g. a *Error's Message field, or the
// result of SafeMessage); Diagnostic applies Sanitize once more as defense
// in depth but this is not a substitute for callers avoiding raw
// header/body content in message in the first place.
func Diagnostic(op model.DiagnosticOperation, certname string, summary Summary, message string) model.Diagnostic {
	safe := Sanitize(message)
	full := safe
	if full != "" {
		full += " (" + summary.Describe() + ")"
	} else {
		full = summary.Describe()
	}
	return model.Diagnostic{
		Severity:  model.SeverityError,
		Operation: op,
		Certname:  certname,
		Source:    summary.Host,
		Message:   full,
	}
}
