package model

import "github.com/example42/piace/internal/config"

// SourceKind identifies where a factset or baseline catalog was loaded
// from: PuppetDB or a local snapshot file. See requirements.md 1.2, 2.2
// and design.md section 6.
type SourceKind string

const (
	SourceKindPuppetDB SourceKind = "puppetdb"
	SourceKindFile     SourceKind = "file"
)

// SourceProvenance records where a factset or catalog came from, without
// any secret material. Populated for both baseline catalogs and factsets;
// CatalogIdentity and ProducerTimestamp are omitted when the source does
// not supply them. See requirements.md 1.2 and 2.2.
type SourceProvenance struct {
	Kind              SourceKind `json:"kind"`
	Certname          string     `json:"certname"`
	Environment       string     `json:"environment,omitempty"`
	ProducerTimestamp string     `json:"producer_timestamp,omitempty"`
	// CatalogIdentity is a catalog identity or content hash when available
	// from the source (e.g. PuppetDB's catalog_uuid or hash).
	CatalogIdentity string `json:"catalog_identity,omitempty"`
	Producer        string `json:"producer,omitempty"`
}

// TrustedFactsSource records how a v4 candidate request obtained target
// trusted facts, per design.md section 5. It never records trusted-fact
// values themselves.
type TrustedFactsSource string

const (
	TrustedFactsProvided       TrustedFactsSource = "provided"
	TrustedFactsCompilerLookup TrustedFactsSource = "compiler_lookup"
)

// CandidateProvenance records how a target's candidate catalog was
// obtained: effective API version, environment, fact source identity, and
// trusted-fact handling. See design.md section 5.
type CandidateProvenance struct {
	RequestedAPI       config.CatalogAPI  `json:"requested_api"`
	EffectiveAPI       config.CatalogAPI  `json:"effective_api"`
	Environment        string             `json:"environment"`
	FactSource         SourceKind         `json:"fact_source"`
	FactsetIdentity    string             `json:"factset_identity,omitempty"`
	TrustedFactsSource TrustedFactsSource `json:"trusted_facts_source,omitempty"`
	// FellBackFromV4 is true when this result used a permitted v4-to-v3
	// fallback; it always accompanies a V3Warning.
	FellBackFromV4 bool `json:"fell_back_from_v4,omitempty"`
	// V3Warning is the prominent, non-suppressible trusted-fact
	// compatibility warning text for v3 (or v4-fallback) requests, per
	// requirements.md 2.5-2.6 and design.md section 5.
	V3Warning string `json:"v3_warning,omitempty"`
}

// V3TrustedFactWarning is the exact, non-suppressible warning text
// attached to every v3 candidate request and every permitted v4-to-v3
// fallback, per requirements.md 2.5-2.6 and design.md section 5: "the
// catalog-reader certificate can make $trusted reflect the service
// identity rather than the target." It is a package-level constant, not
// built ad hoc at each call site, so the compiler adapter, the shared
// result document, and every renderer (text/JSON/HTML) present the exact
// same wording — design.md section 5's "the same warning appears in the
// shared result, text, JSON, and HTML" is only true if there is exactly
// one string to reuse.
const V3TrustedFactWarning = "trusted-fact compatibility warning: this candidate catalog was compiled using the v3 catalog API authenticated by the catalog-reader certificate, not the target's own certificate. Puppet code or Hiera data that reads $trusted can observe the catalog-reader's identity rather than this target's identity. Review any $trusted-dependent logic before trusting this comparison."

// ConfigProvenance is the redacted projection of resolved configuration
// retained for reporting, per design.md section 3.2: source choices,
// paths, API selection, and policy values, but never endpoint credentials
// or private key paths.
type ConfigProvenance struct {
	Candidate map[string]any     `json:"candidate,omitempty"`
	Facts     map[string]any     `json:"facts,omitempty"`
	Baseline  map[string]any     `json:"baseline,omitempty"`
	Exclude   []ExclusionRuleRef `json:"exclude,omitempty"`
	// Redact records the configured redaction selectors that were in
	// force for this target. design.md section 3.2 includes "matching
	// rules" in resolved configuration provenance, and a report that
	// shows `<redacted>` without saying which rule produced it is not
	// reviewable. A selector names a resource type and a parameter name
	// only — never a value — so recording it discloses nothing.
	Redact         []RedactionSelectorRef `json:"redact,omitempty"`
	ImpactEstimate map[string]any         `json:"impact_estimate,omitempty"`
	FailOnDiff     bool                   `json:"fail_on_diff"`
}

// RedactionSelectorRef identifies a configured redaction selector for
// provenance/reporting, mirroring ExclusionRuleRef. Both fields are the
// exact, case-sensitive names from configuration.
type RedactionSelectorRef struct {
	Type      string `json:"type"`
	Parameter string `json:"parameter"`
}
