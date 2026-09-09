// This file implements the operator-facing `--debug` observation seam.
//
// Client.Do is the single chokepoint every compiler and PuppetDB request
// in PIACE passes through, so one Observer wired at construction covers
// both services and all three subcommands without any adapter needing a
// debug field of its own.
//
// Redaction boundary (see this package's Sanitize contract in
// redact.go): an Event carries only safe metadata by default, being
// method, URL, host, status, duration, body sizes, content type, and the
// response body's *top-level JSON member names*. Member names, not
// member values: `{"catalog": {...}}` yields ["catalog"], which is
// enough to diagnose a wire-shape mismatch without putting one byte of
// catalog content into a CI log.
//
// Raw bodies are carried only when a caller explicitly opts in with
// WithBodyCapture. That is a deliberate, operator-requested bypass of
// the redaction boundary: a captured body can contain Puppet Sensitive
// values and unredacted catalog parameters. cmd/piace only enables it
// for --debug-dump-dir, which writes to 0600 files in an operator-named
// directory and never to stdout/stderr.
package transport

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/example42/piace/internal/safemeta"
)

// maxTopLevelKeys bounds how many top-level member names one Event
// reports, so a pathological response cannot turn one debug line into
// thousands of columns. A catalog document has fewer than ten.
const maxTopLevelKeys = 64

// BodyShape classifies a response body's outermost JSON structure.
type BodyShape string

const (
	// ShapeEmpty is a zero-length body.
	ShapeEmpty BodyShape = "empty"
	// ShapeObject is a JSON object; Event.TopLevelKeys names its members.
	ShapeObject BodyShape = "object"
	// ShapeArray is a JSON array.
	ShapeArray BodyShape = "array"
	// ShapeScalar is a bare JSON string/number/bool/null.
	ShapeScalar BodyShape = "scalar"
	// ShapeNonJSON is a body that does not parse as JSON. That is an
	// ordinary, expected outcome for an endpoint that does not serve JSON,
	// the compiler's file_content endpoint returning
	// application/octet-stream, so the name states the fact rather than
	// implying a fault.
	ShapeNonJSON BodyShape = "non-json"
)

// Event is one observed request/response. Every field except
// RequestBody/ResponseBody is safe to print to a CI log.
type Event struct {
	Method string
	// URL is the safe projection defined by internal/safemeta, with
	// userinfo, fragments and arbitrary query values omitted.
	URL  string
	Host string
	// StatusCode is zero when no response was received (Err is set).
	StatusCode int
	Duration   time.Duration
	// RequestBodyBytes is the request body length, taken from
	// http.Request.ContentLength; -1 when unknown.
	RequestBodyBytes  int64
	ResponseBodyBytes int
	ContentType       string
	Shape             BodyShape
	// TopLevelKeys holds the response body's top-level JSON member names in
	// wire order, member *names* only and never values, truncated at
	// maxTopLevelKeys. Empty unless Shape is ShapeObject.
	TopLevelKeys []string
	// KeysTruncated reports that TopLevelKeys was cut at maxTopLevelKeys.
	KeysTruncated bool
	// Err is the transport failure, when the request produced no response.
	Err error

	// RequestBody and ResponseBody are populated only when the Client was
	// built WithBodyCapture(true). They are raw and unredacted: see this
	// file's package comment.
	RequestBody  []byte
	ResponseBody []byte
}

// Observer receives one Event per request executed by a Client. It is
// called synchronously from Do, after the response body has been read.
type Observer func(Event)

// WithObserver installs obs on the Client. A nil obs disables
// observation (the zero value), so callers can pass one through
// unconditionally.
func WithObserver(obs Observer) Option {
	return func(c *Client) { c.observer = obs }
}

// WithBodyCapture makes the Client include raw request and response
// bodies in every Event it emits. Off by default. See this file's
// package comment for why enabling it is a deliberate redaction bypass.
func WithBodyCapture(enabled bool) Option {
	return func(c *Client) { c.captureBodies = enabled }
}

// describeBody classifies body's outermost JSON structure and, for an
// object, collects its top-level member names. It decodes member values
// as json.RawMessage rather than into any typed structure, so no member
// value is ever interpreted, retained, or returned.
func describeBody(body []byte) (BodyShape, []string, bool) {
	if len(body) == 0 {
		return ShapeEmpty, nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return ShapeNonJSON, nil, false
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return ShapeScalar, nil, false
	}
	if delim != '{' {
		return ShapeArray, nil, false
	}

	var keys []string
	truncated := false
	for dec.More() {
		nameTok, err := dec.Token()
		if err != nil {
			return ShapeNonJSON, keys, truncated
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return ShapeNonJSON, keys, truncated
		}
		name, _ := nameTok.(string)
		if len(keys) >= maxTopLevelKeys {
			truncated = true
			continue
		}
		keys = append(keys, safemeta.Text(name))
	}
	return ShapeObject, keys, truncated
}
