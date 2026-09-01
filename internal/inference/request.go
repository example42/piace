// Package inference is PIACE's client for an OpenAI-compatible inference
// service. It knows how to send a request and read a response, and
// nothing about catalogs, targets, or Puppet: internal/assess decides
// what may leave the process, this package only carries it. Reviewing
// what PIACE discloses therefore means reviewing internal/assess.
//
// It is also the one place in PIACE that sets an Authorization header;
// internal/transport strips that header from every request it makes. See
// CONTEXT.md for the scope of that exception.
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
// The output-token bound is carried by exactly one of MaxTokens or
// MaxCompletionTokens, never both: OpenAI's GPT-5 family rejects
// `max_tokens` outright and requires `max_completion_tokens`, while
// OpenAI-compatible servers other than current OpenAI (Ollama, vLLM,
// llama.cpp) only understand `max_tokens`. internal/assess picks the
// field from services.inference.token_limit_param.
//
// Temperature is a pointer and omitted when nil. PIACE sends no sampling
// parameter unless one is configured: Claude 4+ and GPT-5 reject any
// non-default temperature with a 400, and pinning it never made a
// model-generated assessment reproducible anyway, since a provider-side
// model revision still moves the bytes. There is deliberately no Seed
// field: Anthropic's compat endpoint ignores it, OpenAI deprecated it,
// and reasoning models reject it.
type Request struct {
	Model               string          `json:"model"`
	Messages            []Message       `json:"messages"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	ResponseFormat      *ResponseFormat `json:"response_format,omitempty"`
}
