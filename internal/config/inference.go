package config

// InferenceSection is the `inference:` block of a services file: the one
// external service PIACE contacts that is not the compiler or PuppetDB.
//
// It loads independently of the compiler and puppetdb sections, so a
// services file containing only this block is valid for `piace explain` —
// which needs no mTLS identity and constructs no compiler or PuppetDB
// client. See CONTEXT.md.
//
// The token is never written here. TokenEnv names an environment variable
// and TokenFile names a path, mirroring the discipline that a services
// file holds references to credentials and never credential material.
type InferenceSection struct {
	Endpoint  string `json:"endpoint" yaml:"endpoint"`
	Model     string `json:"model" yaml:"model"`
	TokenEnv  string `json:"token_env" yaml:"token_env"`
	TokenFile string `json:"token_file" yaml:"token_file"`

	Timeout   string `json:"timeout" yaml:"timeout"`
	MaxTokens int    `json:"max_tokens" yaml:"max_tokens"`
	MaxGroups int    `json:"max_groups" yaml:"max_groups"`

	// TokenLimitParam selects the request field that carries the
	// output-token bound: "max_tokens" (the default; what OpenAI-compatible
	// servers other than current OpenAI expect) or "max_completion_tokens"
	// (required by OpenAI's GPT-5 family, also accepted by Anthropic's
	// OpenAI-compatible endpoint). The value in MaxTokens is unchanged;
	// only the field name on the wire differs.
	TokenLimitParam string `json:"token_limit_param" yaml:"token_limit_param"`

	// Temperature, when set, is sent as the request's sampling temperature.
	// Left unset (the default) PIACE sends no temperature at all: Claude 4+
	// and GPT-5 reject any non-default value with a 400, and pinning it
	// never made a model-generated assessment reproducible. A pointer so an
	// explicit 0 is distinguishable from unset.
	Temperature *float64 `json:"temperature" yaml:"temperature"`

	// Pseudonymize and StructuredOutput are pointers so an unset field is
	// distinguishable from an explicit `false`; both default to true.
	Pseudonymize     *bool `json:"pseudonymize" yaml:"pseudonymize"`
	StructuredOutput *bool `json:"structured_output" yaml:"structured_output"`

	PolicyNotesFile string `json:"policy_notes_file" yaml:"policy_notes_file"`
}
