package transport

import "fmt"

// ErrorKind classifies an Error produced by this package. See doc.go
// decision 4 for why every kind here is an operational error, and why
// this package never itself produces a compilation-failure
// classification.
type ErrorKind string

const (
	// KindConfig is a client-construction failure: a non-https endpoint,
	// an empty ServerName/host, or a malformed CA/certificate/key file.
	KindConfig ErrorKind = "config"
	// KindTLS is a TLS handshake failure (certificate rejected, protocol
	// version mismatch, etc.) surfaced while executing a request.
	KindTLS ErrorKind = "tls"
	// KindConnect is a network-level failure to reach the endpoint: DNS
	// resolution failure, connection refused, connection reset, and
	// similar dial-time errors.
	KindConnect ErrorKind = "connect"
	// KindTimeout is a context deadline exceeded or client-timeout
	// failure: the request, including any redirect following, did not
	// complete within its deadline.
	KindTimeout ErrorKind = "timeout"
	// KindRedirectRejected is returned when the server attempted to
	// redirect the request to a different scheme/host/port than the
	// original request's authority.
	KindRedirectRejected ErrorKind = "redirect_rejected"
	// KindResponseTooLarge is returned when a response body exceeded the
	// configured maximum size.
	KindResponseTooLarge ErrorKind = "response_too_large"
	// KindUnknown covers any other transport-level failure not otherwise
	// classified above (still an operational error, never a compilation
	// failure).
	KindUnknown ErrorKind = "unknown"
)

// Error is a structured, redaction-safe transport failure. It never embeds
// request/response headers or bodies; Message and the wrapped Err must not
// be built from raw response content (see Redact in redact.go for the one
// place safe metadata is derived from a request/response).
//
// Every Error produced by this package is an operational error. Task 6's
// compiler adapter consults Kind only to decide how to present a
// low-level transport failure; it does not need to (and must not)
// reclassify any Error from this package as a compilation failure — see
// doc.go decision 4.
type Error struct {
	Kind ErrorKind
	// Host is the target authority (host:port) the request was addressed
	// to, safe to log.
	Host string
	// Message is a short, secret-free description of what failed.
	Message string
	// Err is the underlying error, if any. It is included in Error() but
	// callers building a model.Diagnostic should prefer Message plus safe
	// fields over calling Err.Error() directly on an unclassified error
	// from elsewhere, since this package cannot guarantee an error it did
	// not construct is secret-free.
	Err error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err != nil {
		return fmt.Sprintf("transport: %s: %s: %v", e.Kind, e.Message, e.Err)
	}
	return fmt.Sprintf("transport: %s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// newError constructs an *Error, the sole constructor used throughout this
// package so every transport failure carries a Kind.
func newError(kind ErrorKind, host, message string, err error) *Error {
	return &Error{Kind: kind, Host: host, Message: message, Err: err}
}
