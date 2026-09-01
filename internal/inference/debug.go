// This file implements the operator-facing `--debug` observation seam for
// the one service internal/transport does not carry: the inference
// endpoint. It is deliberately a parallel implementation rather than a
// reuse of internal/transport's seam, for the same reason this whole
// package is separate — see the Client doc comment and
// docs/adr/0003-authenticate-the-inference-service-with-a-bearer-token.md.
// internal/inference must not import internal/transport.
//
// Redaction boundary (requirements.md 3.5): an Event carries only safe
// metadata by default — method, URL, host, status, duration, body sizes,
// content type, and the response body's *top-level JSON member names*.
// Member names, not values: an Anthropic error body yields ["type",
// "error"], enough to see the shape without putting an account or quota
// detail into a CI log.
//
// Raw bodies are carried only when a caller opts in with
// WithBodyCapture. That is a deliberate bypass: the request body is the
// catalog-derived payload internal/assess assembled, and a failed
// response body routinely names the account behind the token. cmd/piace
// only enables it for --debug-dump-dir, which writes 0600 files in an
// operator-named directory and never to stdout/stderr. Headers are never
// captured, so the bearer token cannot reach a dump file.
package inference

import (
	"bytes"
	"encoding/json"
	"time"
)

// maxTopLevelKeys bounds how many top-level member names one Event
// reports, so a pathological response cannot turn one debug line into
// thousands of columns.
const maxTopLevelKeys = 64

// BodyShape classifies a response body's outermost JSON structure. The
// values match internal/transport.BodyShape so cmd/piace can render an
// inference Event and a transport Event with one code path.
type BodyShape string

const (
	ShapeEmpty   BodyShape = "empty"
	ShapeObject  BodyShape = "object"
	ShapeArray   BodyShape = "array"
	ShapeScalar  BodyShape = "scalar"
	ShapeNonJSON BodyShape = "non-json"
)

// Event is one observed inference request/response. Every field except
// RequestBody/ResponseBody is safe to print to a CI log.
type Event struct {
	Method     string
	URL        string
	Host       string
	StatusCode int // zero when no response was received (Err is set)
	Duration   time.Duration

	RequestBodyBytes  int
	ResponseBodyBytes int
	ContentType       string
	Shape             BodyShape
	// TopLevelKeys holds the response body's top-level JSON member names in
	// wire order (names only, never values), truncated at maxTopLevelKeys.
	// Empty unless Shape is ShapeObject.
	TopLevelKeys  []string
	KeysTruncated bool
	// Err is the transport failure, when the request produced no response.
	Err error

	// RequestBody and ResponseBody are populated only when the Client was
	// built WithBodyCapture(true). They are raw and unredacted: see this
	// file's package comment.
	RequestBody  []byte
	ResponseBody []byte
}

// Observer receives one Event per call to Complete. It is invoked
// synchronously from Complete, after the response body has been read.
type Observer func(Event)

// Option configures a Client at construction. Options are applied after
// the validated defaults, so a nil Observer leaves observation off.
type Option func(*Client)

// WithObserver installs obs on the Client. A nil obs disables
// observation, so a caller can pass one through unconditionally.
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
// object, collects its top-level member names. Member values are decoded
// as json.RawMessage and discarded, so no value is ever interpreted or
// returned.
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
		if len(keys) >= maxTopLevelKeys {
			truncated = true
			continue
		}
		name, _ := nameTok.(string)
		keys = append(keys, name)
	}
	return ShapeObject, keys, truncated
}

// emit sends one Event to the observer, if any. It strips the raw bodies
// unless body capture was requested, so a caller can always populate
// them and let this decide.
func (c *Client) emit(ev Event) {
	if c.observer == nil {
		return
	}
	if !c.captureBodies {
		ev.RequestBody = nil
		ev.ResponseBody = nil
	}
	c.observer(ev)
}
