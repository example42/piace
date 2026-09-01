// Package config defines the versioned schema types for PIACE's target
// file and service configuration files.
//
// This package defines shape only: decoding (including unknown-field
// rejection), default resolution, path/glob validation, and cross-field
// validation are implemented separately (, "Target configuration
// resolution"). Keeping the schema and the resolver in separate concerns
// lets later work build a resolved model on top of this stable wire
// shape without churning the wire shape itself.
package config

// TargetFileVersion is the only supported `version` value for a target
// file. A future incompatible schema change would introduce a new integer
// value and an explicit migration path, not silent reinterpretation.
const TargetFileVersion = 1

// TargetFile is the root document of a `--targets` YAML file.
type TargetFile struct {
	Version  int      `json:"version" yaml:"version"`
	Defaults Defaults `json:"defaults" yaml:"defaults"`
	Targets  []Target `json:"targets" yaml:"targets"`
}

// Defaults holds the global defaults merged into every target before
// its own overrides are applied.
type Defaults struct {
	Candidate      CandidateConfig      `json:"candidate,omitempty" yaml:"candidate,omitempty"`
	Facts          FactsConfig          `json:"facts,omitempty" yaml:"facts,omitempty"`
	Baseline       BaselineConfig       `json:"baseline,omitempty" yaml:"baseline,omitempty"`
	Exclude        []ExclusionRule      `json:"exclude,omitempty" yaml:"exclude,omitempty"`
	Redact         []RedactionSelector  `json:"redact,omitempty" yaml:"redact,omitempty"`
	ImpactEstimate ImpactEstimateConfig `json:"impact_estimate,omitempty" yaml:"impact_estimate,omitempty"`
	FailOnDiff     *bool                `json:"fail_on_diff,omitempty" yaml:"fail_on_diff,omitempty"`
}

// Target is one per-target entry. Any field left zero-valued/nil is
// resolved from Defaults; certname is the only field a target must always
// supply itself.
type Target struct {
	Certname       string                `json:"certname" yaml:"certname"`
	Candidate      *CandidateConfig      `json:"candidate,omitempty" yaml:"candidate,omitempty"`
	Facts          *FactsConfig          `json:"facts,omitempty" yaml:"facts,omitempty"`
	Baseline       *BaselineConfig       `json:"baseline,omitempty" yaml:"baseline,omitempty"`
	Exclude        []ExclusionRule       `json:"exclude,omitempty" yaml:"exclude,omitempty"`
	Redact         []RedactionSelector   `json:"redact,omitempty" yaml:"redact,omitempty"`
	ImpactEstimate *ImpactEstimateConfig `json:"impact_estimate,omitempty" yaml:"impact_estimate,omitempty"`
	FailOnDiff     *bool                 `json:"fail_on_diff,omitempty" yaml:"fail_on_diff,omitempty"`
}

// CatalogAPI is the compiler catalog API version selected for candidate
// compilation.
type CatalogAPI string

const (
	CatalogAPIv3 CatalogAPI = "v3"
	CatalogAPIv4 CatalogAPI = "v4"
)

// CandidateConfig configures candidate catalog compilation.
type CandidateConfig struct {
	Environment string     `json:"environment,omitempty" yaml:"environment,omitempty"`
	CatalogAPI  CatalogAPI `json:"catalog_api,omitempty" yaml:"catalog_api,omitempty"`
	// AllowV3Fallback is only meaningful when CatalogAPI is v4. It defaults
	// to false and must be explicitly enabled;
	AllowV3Fallback *bool `json:"allow_v3_fallback,omitempty" yaml:"allow_v3_fallback,omitempty"`
	// TrustedFactsCompilerLookup is only meaningful when CatalogAPI is v4.
	// It asserts an operator-confirmed fact PIACE cannot itself observe:
	// that the configured compiler is set up to obtain a target's trusted
	// facts from PuppetDB when a v4 request omits the `trusted_facts` field.
	// PIACE uses the documented v4 omitted-field behavior only when that is
	// true, and the field defaults to false, so PIACE never assumes the
	// compiler-side configuration exists.
	TrustedFactsCompilerLookup *bool `json:"trusted_facts_compiler_lookup,omitempty" yaml:"trusted_facts_compiler_lookup,omitempty"`
}

// FactSourceKind selects where a target's factset is loaded from.
type FactSourceKind string

const (
	FactSourcePuppetDB FactSourceKind = "puppetdb"
	FactSourceFile     FactSourceKind = "file"
)

// FactsConfig configures the fact source used to compile a candidate.
type FactsConfig struct {
	Source FactSourceKind `json:"source,omitempty" yaml:"source,omitempty"`
	File   string         `json:"file,omitempty" yaml:"file,omitempty"`
}

// BaselineSourceKind selects where a target's baseline catalog is loaded
// from.
type BaselineSourceKind string

const (
	BaselineSourcePuppetDB BaselineSourceKind = "puppetdb"
	BaselineSourceFile     BaselineSourceKind = "file"
)

// BaselineConfig configures the baseline catalog source and its expected
// environment. A PuppetDB baseline whose returned
// environment differs from Environment to fail the target before diffing.
type BaselineConfig struct {
	Source      BaselineSourceKind `json:"source,omitempty" yaml:"source,omitempty"`
	Environment string             `json:"environment,omitempty" yaml:"environment,omitempty"`
	File        string             `json:"file,omitempty" yaml:"file,omitempty"`
}

// ExclusionRule suppresses matching resource (and connected edge)
// differences from the displayed and evaluated result. Type is an exact,
// case-sensitive Puppet resource type; Title is a case-sensitive
// `path.Match` glob pattern.
type ExclusionRule struct {
	Type  string `json:"type" yaml:"type"`
	Title string `json:"title" yaml:"title"`
}

// RedactionSelector replaces every matching parameter value with a
// stable redaction marker in all output formats. Both fields are exact,
// case-sensitive names.
type RedactionSelector struct {
	Type      string `json:"type" yaml:"type"`
	Parameter string `json:"parameter" yaml:"parameter"`
}

// ImpactEstimateConfig configures the optional PuppetDB-backed
// stored-catalog footprint estimate.
type ImpactEstimateConfig struct {
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// Timeout is a Go duration string (e.g. "10s"), resolved and bounded by
	// the resolver, not this schema type.
	Timeout string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	// ResultLimit bounds the number of certnames retained in an impact
	// estimate sample; the adapter requests ResultLimit+1 to detect
	// truncation.
	ResultLimit *int `json:"result_limit,omitempty" yaml:"result_limit,omitempty"`
}
