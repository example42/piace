package resolve

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/config"
)

// Default inference request options. Timeout is generous because an
// inference service is slower than a catalog compile and a slow answer is
// still an answer; the change assessment gates nothing.
const (
	DefaultInferenceTimeout   = 60 * time.Second
	DefaultInferenceMaxTokens = 4000
)

// Inference is a resolved, validated inference service configuration.
// Token is the bearer token's value, read from the environment variable
// or file the services file named; it is never logged, never rendered,
// and never written to an artifact.
type Inference struct {
	URL       *url.URL
	Token     string
	Timeout   time.Duration
	Assess    assess.Config
	Authority string
}

// LoadInferenceFile decodes a services file and resolves only its
// `inference:` section. The compiler and puppetdb sections are neither
// required nor validated, so a services file containing nothing but
// `inference:` loads. That is what lets `piace explain` run with a file
// naming no Puppet infrastructure at all.
func LoadInferenceFile(path string) (Inference, error) {
	f, err := os.Open(path)
	if err != nil {
		return Inference{}, fmt.Errorf("opening services file: %w", err)
	}
	defer f.Close()

	sf, err := decodeServicesFile(f)
	if err != nil {
		return Inference{}, err
	}
	return ResolveInference(sf, filepath.Dir(path))
}

// ResolveInference validates the inference section and reads the bearer
// token it references. dir is the services file's directory, against
// which a relative policy_notes_file resolves, the same rule every path
// in a config file follows.
func ResolveInference(sf config.ServicesFile, dir string) (Inference, error) {
	var c errorCollector

	if sf.Version != config.ServicesFileVersion {
		c.addf("services file: unsupported version %d, expected %d", sf.Version, config.ServicesFileVersion)
	}
	if sf.Inference == nil {
		c.addf("services.inference: missing; `piace explain` needs an inference service")
		return Inference{}, c.result()
	}
	in := *sf.Inference

	u, err := validateHTTPSEndpoint(in.Endpoint)
	if err != nil {
		c.addf("services.inference.endpoint: %s", err)
	}
	if strings.TrimSpace(in.Model) == "" {
		c.addf("services.inference.model: missing")
	}

	token := resolveInferenceToken(in, dir, &c)

	timeout := DefaultInferenceTimeout
	if in.Timeout != "" {
		d, err := time.ParseDuration(in.Timeout)
		switch {
		case err != nil:
			c.addf("services.inference.timeout: %s", err)
		case d <= 0:
			c.addf("services.inference.timeout: must be positive, got %s", in.Timeout)
		default:
			timeout = d
		}
	}

	maxTokens := in.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultInferenceMaxTokens
	} else if maxTokens < 0 {
		c.addf("services.inference.max_tokens: must be positive, got %d", maxTokens)
	}
	maxGroups := in.MaxGroups
	if maxGroups == 0 {
		maxGroups = assess.DefaultMaxGroups
	} else if maxGroups < 0 {
		c.addf("services.inference.max_groups: must be positive, got %d", maxGroups)
	}

	tokenLimitParam := "max_tokens"
	switch in.TokenLimitParam {
	case "", "max_tokens":
	case "max_completion_tokens":
		tokenLimitParam = "max_completion_tokens"
	default:
		c.addf(`services.inference.token_limit_param: must be "max_tokens" or "max_completion_tokens", got %q`, in.TokenLimitParam)
	}

	if in.Temperature != nil && *in.Temperature < 0 {
		c.addf("services.inference.temperature: must not be negative, got %v", *in.Temperature)
	}

	var notes string
	if in.PolicyNotesFile != "" {
		path := resolveAgainst(dir, in.PolicyNotesFile)
		raw, err := os.ReadFile(path)
		if err != nil {
			c.addf("services.inference.policy_notes_file: %s", err)
		} else {
			notes = string(raw)
		}
	}

	if c.hasErrors() {
		return Inference{}, c.result()
	}
	return Inference{
		URL:       u,
		Token:     token,
		Timeout:   timeout,
		Authority: u.Host,
		Assess: assess.Config{
			Model:            in.Model,
			MaxTokens:        maxTokens,
			TokenLimitParam:  tokenLimitParam,
			Temperature:      in.Temperature,
			MaxGroups:        maxGroups,
			Pseudonymize:     boolOrDefault(in.Pseudonymize, true),
			StructuredOutput: boolOrDefault(in.StructuredOutput, true),
			PolicyNotes:      notes,
		},
	}, nil
}

// resolveInferenceToken reads the bearer token from exactly one of the
// two references a services file may carry. Naming both is a
// configuration error rather than a precedence rule nobody remembers, and
// naming neither is refused outright: PIACE never mints or discovers a
// credential on its own.
//
// dir is the services file's directory, against which a relative
// token_file resolves, the same rule policy_notes_file and the TLS paths
// follow.
func resolveInferenceToken(in config.InferenceSection, dir string, c *errorCollector) string {
	switch {
	case in.TokenEnv != "" && in.TokenFile != "":
		c.addf("services.inference: set token_env or token_file, not both")
		return ""
	case in.TokenEnv != "":
		token := os.Getenv(in.TokenEnv)
		if token == "" {
			c.addf("services.inference.token_env: environment variable %s is unset or empty", in.TokenEnv)
		}
		return token
	case in.TokenFile != "":
		path := resolveAgainst(dir, in.TokenFile)
		raw, err := os.ReadFile(path)
		if err != nil {
			c.addf("services.inference.token_file: %s", err)
			return ""
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			c.addf("services.inference.token_file: %s is empty", path)
		}
		return token
	default:
		c.addf("services.inference: set token_env or token_file")
		return ""
	}
}

func boolOrDefault(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// resolveAgainst applies the one path rule a config file follows: a
// relative path resolves against the directory of the file that named it,
// an absolute one is taken as written.
func resolveAgainst(dir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}
