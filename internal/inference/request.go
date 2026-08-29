// Package inference is PIACE's client for an OpenAI-compatible inference
// service. It knows how to send a request and read a response, and
// nothing about catalogs, targets, or Puppet: internal/assess decides
// what may leave the process, this package only carries it. Reviewing
// what PIACE discloses therefore means reviewing internal/assess.
//
// It is also the one place in PIACE that sets an Authorization header;
// internal/transport strips that header from every request it makes. See
// docs/adr/0003-authenticate-the-inference-service-with-a-bearer-token.md.
package inference

// Message is one chat message. Role is "system" or "user".
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// JSONSchema is the structured-output schema an inference service is
// asked to conform to. The field names and their nesting follow the
// OpenAI API reference for POST /v1/chat/completions:
//
//	"response_format": {
//	  "type": "json_schema",
//	  "json_schema": {"name": ..., "strict": true, "schema": {...}}
//	}
//
// Strict is sent explicitly because Chat Completions requests are
// non-strict by default. Under strict adherence every property must be
// listed in `required` and `additionalProperties` must be false, which is
// why the schema internal/assess builds has no optional member.
type JSONSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

// ResponseFormat is the structured-output request field.
type ResponseFormat struct {
	Type       string     `json:"type"`
	JSONSchema JSONSchema `json:"json_schema"`
}

// Request is one chat-completions request body.
//
// Temperature and Seed are always serialized, never omitted: they are
// fixed at zero and are not configurable. Zero temperature does not make
// a change assessment deterministic — a provider-side model revision
// still moves the bytes — but it is what makes re-running `piace explain`
// over the same report give a reader the same reading.
type Request struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature"`
	Seed           int             `json:"seed"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}
