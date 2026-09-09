package resolve

import (
	"net/url"
	"time"

	"github.com/example42/piace/internal/config"
)

// Target is the complete, validated per-target model: every field is
// fully resolved from global defaults and per-target overrides, and
// every invariant has already been checked. Nothing downstream of
// ResolveTargets or Load needs to re-check source or API enum validity,
// glob syntax, duration or limit positivity, or path safety.
//
// Presence is the one thing scoped to the command that resolved it (see
// Command): a field the running command does not read may legitimately
// be empty, and is documented as such on the field. Everything that is
// present has been validated, whichever command asked.
type Target struct {
	Certname       string
	Candidate      Candidate
	Facts          Facts
	Baseline       Baseline
	Exclude        []config.ExclusionRule
	Redact         []config.RedactionSelector
	ImpactEstimate ImpactEstimate
	FailOnDiff     bool
}

// Candidate is the resolved candidate-compilation configuration for one
// target.
type Candidate struct {
	// Environment is empty for a command that does not compile a
	// candidate for the target's own environment: `capture catalog` names
	// the environment to snapshot on the command line, and `capture
	// facts` compiles nothing at all.
	Environment string
	// CatalogAPI is empty only for `capture facts`, which issues no
	// catalog request.
	CatalogAPI config.CatalogAPI
	// AllowV3Fallback is always false when CatalogAPI is v3; resolution
	// rejects the combination CatalogAPI=v3, AllowV3Fallback=true.
	AllowV3Fallback bool
	// TrustedFactsCompilerLookup mirrors
	// config.CandidateConfig.TrustedFactsCompilerLookup, resolved to its
	// explicit false default when unset. See that field's doc comment.
	TrustedFactsCompilerLookup bool
}

// Facts is the resolved fact-source configuration for one target. File is
// the fully resolved (relative-to-target-file-directory or explicit
// absolute) filesystem path, already `{certname}`-expanded and checked for
// directory escape; it is empty when Source is puppetdb.
type Facts struct {
	Source config.FactSourceKind
	File   string
}

// Baseline is the resolved baseline-catalog configuration for one target.
// File is resolved the same way as Facts.File.
type Baseline struct {
	// Source is empty only for `capture facts`, which has no baseline.
	Source config.BaselineSourceKind
	// Environment is empty for both capture commands: only a comparison
	// reads a baseline catalog and checks the environment it came from.
	// `capture catalog` uses Source and File as a destination.
	Environment string
	File        string
}

// ImpactEstimate is the resolved impact-estimation policy for one target.
// Timeout is a parsed, positive time.Duration; ResultLimit is a positive
// integer. Both are only required to be present when Enabled is true (see
// resolve.go's documented assumption), but if present they are always
// validated regardless of Enabled.
type ImpactEstimate struct {
	Enabled     bool
	Timeout     time.Duration
	ResultLimit int
}

// Endpoint is one resolved, validated service endpoint: an `https` URL and
// three required TLS file paths. Path fields are validated for syntactic
// well-formedness only (see doc.go); this package never checks that they
// exist or are readable, since that crosses into internal/transport's mTLS transport
// construction, which happens only after configuration is fully valid.
type Endpoint struct {
	URL        *url.URL
	CABundle   string
	ClientCert string
	PrivateKey string
	// Timeout is this service's configured default per-request deadline,
	// already parsed and checked for positivity. It is zero when the
	// services file did not name one, which the transport reads as "use
	// transport.DefaultTimeout": this package resolves configuration and
	// does not own the transport's default.
	Timeout time.Duration
}

// Services is the resolved, validated `--services` configuration. An
// endpoint the running command does not need (see Config.Required) is
// zero: it was neither required nor validated, and no client is built
// from it.
type Services struct {
	Compiler Endpoint
	PuppetDB Endpoint
}

// Config is the complete resolved and validated configuration for a PIACE
// run: every target in target-file order (already deduplicated-checked for
// certname uniqueness, not deduplicated in content) plus the resolved
// service endpoints.
type Config struct {
	Targets  []Target
	Services Services
	// Required names the endpoints this command needs, given what its
	// targets select. Only these were validated, only these are populated
	// in Services, and only these should have clients built for them.
	Required ServiceSet
}
