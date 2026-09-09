package model

import "github.com/example42/piace/internal/config"

// SourceKind identifies where a factset or baseline catalog was loaded
// from: PuppetDB or a local snapshot file.
type SourceKind string

const (
	SourceKindPuppetDB SourceKind = "puppetdb"
	SourceKindFile     SourceKind = "file"
)

// SourceProvenance records where a factset or catalog came from, without
// any secret material. Populated for both baseline catalogs and
// factsets; CatalogIdentity and ProducerTimestamp are omitted when the
// source does not supply them.
type SourceProvenance struct {
	Kind              SourceKind `json:"kind"`
	Certname          string     `json:"certname"`
	Environment       string     `json:"environment,omitempty"`
	ProducerTimestamp string     `json:"producer_timestamp,omitempty"`
	// CatalogIdentity is a catalog identity or content hash when available
	// from the source (e.g. PuppetDB's catalog_uuid or hash).
	CatalogIdentity string `json:"catalog_identity,omitempty"`
	Producer        string `json:"producer,omitempty"`
	// Capture records how a file-backed baseline's payload was obtained,
	// at capture time, as its snapshot envelope recorded it. It is nil
	// for a PuppetDB baseline, which was not captured by PIACE, and for
	// a factset snapshot, which no compiler API produced.
	//
	// It exists because a comparison's trust semantics are not only its
	// own. A baseline compiled through v3 was compiled with the
	// catalog-reader's identity in $trusted, and that is a property of
	// the bytes being compared, not of the run comparing them. Without
	// this, a result document built from such a snapshot looked exactly
	// like one built from a v4 capture, and the only place the
	// difference survived was the snapshot file itself.
	Capture *BaselineCapture `json:"capture,omitempty"`
}

// BaselineCapture is the result document's projection of a catalog
// snapshot's capture provenance. It mirrors snapshot.CaptureProvenance,
// which is the on-disk contract, and adds the derived warning so every
// renderer states the same thing without deriving it separately.
type BaselineCapture struct {
	RequestedAPI   config.CatalogAPI `json:"requested_api"`
	EffectiveAPI   config.CatalogAPI `json:"effective_api"`
	FellBackFromV4 bool              `json:"fell_back_from_v4,omitempty"`
	// TrustedFactsSource is set for an effective-v4 capture and empty
	// for v3, which has no trusted-fact request field at all.
	TrustedFactsSource TrustedFactsSource `json:"trusted_facts_source,omitempty"`
	FactSource         SourceKind         `json:"fact_source,omitempty"`
	// V3Warning is present exactly when EffectiveAPI is v3, and is
	// V3TrustedFactWarning. It is the same constant a v3 candidate
	// carries, because it describes the same thing about the catalog:
	// what $trusted could have held while it was compiled, and what the
	// compilation may have stored in PuppetDB.
	V3Warning string `json:"v3_warning,omitempty"`
}

// TrustedFactsSource records how a v4 candidate request obtained target
// trusted facts. It never records trusted-fact values themselves.
type TrustedFactsSource string

const (
	TrustedFactsProvided       TrustedFactsSource = "provided"
	TrustedFactsCompilerLookup TrustedFactsSource = "compiler_lookup"
)

// CandidateProvenance records how a target's candidate catalog was
// obtained: effective API version, environment, fact source identity,
// and trusted-fact handling.
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
	// compatibility warning text for v3 (or v4-fallback) requests.
	V3Warning string `json:"v3_warning,omitempty"`
}

// V3TrustedFactWarning is the exact, non-suppressible warning text
// attached to every v3 candidate request and every permitted v4-to-v3
// fallback: "the catalog-reader certificate can make $trusted reflect
// the service identity rather than the target." It is a package-level
// constant, not built ad hoc at each call site, so the compiler adapter,
// the shared result document, and every renderer present the exact same
// wording. The same warning appearing in the shared result, text, JSON,
// and HTML is only true if there is exactly one string to reuse.
const V3TrustedFactWarning = "trusted-fact compatibility warning: a v3 catalog request was attempted using the catalog-reader certificate, not the target's own certificate. Puppet code or Hiera data that reads $trusted can observe the catalog-reader's identity rather than this target's identity. Persistence warning: v3 can store submitted facts and the compiled catalog in PuppetDB, including when the request ultimately fails. A file baseline protects comparison input, not other PuppetDB consumers. Review these effects before trusting this comparison."

// ConfigProvenance is the redacted projection of resolved configuration
// retained for reporting: source choices, paths, API selection, and
// policy values, but never endpoint credentials or private key paths.
type ConfigProvenance struct {
	Candidate map[string]any     `json:"candidate,omitempty"`
	Facts     map[string]any     `json:"facts,omitempty"`
	Baseline  map[string]any     `json:"baseline,omitempty"`
	Exclude   []ExclusionRuleRef `json:"exclude,omitempty"`
	// Redact records the configured redaction selectors that were in force
	// for this target. Matching rules are part of resolved configuration
	// provenance, and a report that shows `<redacted>` without saying which
	// rule produced it is not reviewable. A selector names a resource type
	// and a parameter name only, never a value, so recording it discloses
	// nothing.
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
