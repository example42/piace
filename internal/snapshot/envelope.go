// Package snapshot defines the versioned PIACE snapshot envelope schema
// used for captured factsets and catalogs, and implements the canonical
// JSON encoder, SHA-256 checksum, atomic 0600 writer, and loader/
// validator that operate on it.
//
// See canonical.go and checksum.go for the canonical JSON encoding and
// checksum algorithm, and store.go for Write/Load/Validate. The
// file-backed FactSource/CatalogSource adapters that select this package
// as a target's fact or baseline source (per target.Facts.Source ==
// config.FactSourceFile / target.Baseline.Source ==
// config.BaselineSourceFile) live in internal/puppetdb, not here, to
// avoid this package importing puppetdb's adapter interfaces; see
// internal/puppetdb/filesource.go.
//
// # Checksum scope
//
// PayloadChecksum covers the canonical encoding of Payload and nothing
// else. Captured content evidence therefore sits inside the checksum,
// because capture places it inside the payload (see
// internal/capture/content.go) rather than beside it in envelope
// metadata. Envelope metadata itself is deliberately outside the
// checksum: it would otherwise have to be canonicalized and hashed as
// part of the same value it describes, and a checksum cannot vouch for
// the metadata that names it. Metadata is checked a different way, by
// Validate, which requires the envelope's own claims to agree with the
// payload they describe (target/certname, requested environment, and
// compiler API provenance consistency).
//
// # Integrity and attribution are separate checks
//
// Load performs the integrity check: a supported format_version and a
// payload whose canonical encoding hashes to the declared
// payload_checksum. It applies to every envelope regardless of why it is
// being read, and it is what detects a truncated, corrupted, or edited
// payload.
//
// Validate performs the attribution check: this envelope is the kind of
// snapshot the caller asked for, for the target the caller asked about,
// produced by the source kind that kind of snapshot must come from, and
// carrying provenance metadata that is internally consistent and
// consistent with the payload. A valid checksum over a payload
// describing a different node is an intact snapshot of the wrong thing,
// which is why the two checks do not collapse into one.
package snapshot

import "encoding/json"

// FormatVersion is the only supported `format_version` value for a
// snapshot envelope.
//
// Version 2 replaced version 1's single `compiler_api` string with the
// `capture` provenance object (see CaptureProvenance): a snapshot has to
// record the API that actually compiled its payload *and* the API that
// was requested, which one field cannot do without ambiguity. There is
// no version-1 reader: PIACE has no deployments, and a stored envelope
// declaring format_version 1 is rejected by Load rather than upgraded in
// place, so a snapshot written by an older build is recaptured rather
// than reinterpreted under a schema it was not written against.
const FormatVersion = 2

// Kind distinguishes a factset snapshot from a catalog snapshot within one
// envelope schema.
type Kind string

const (
	KindFactset Kind = "factset"
	KindCatalog Kind = "catalog"
)

// CompilerAPI mirrors config.CatalogAPI for the subset of values valid in
// a snapshot envelope (v3/v4). It is a distinct type rather than a reused
// alias because snapshot envelopes are a persisted on-disk contract with
// its own versioning (FormatVersion), independent of the target-file
// config schema's evolution.
type CompilerAPI string

const (
	CompilerAPIv3 CompilerAPI = "v3"
	CompilerAPIv4 CompilerAPI = "v4"
)

// TrustedFactsSource records how a v4 candidate request obtained the
// target's trusted facts, mirroring model.TrustedFactsSource for the
// same reason CompilerAPI mirrors config.CatalogAPI: the persisted
// contract versions independently of the in-memory model.
type TrustedFactsSource string

const (
	TrustedFactsProvided       TrustedFactsSource = "provided"
	TrustedFactsCompilerLookup TrustedFactsSource = "compiler_lookup"
)

// FactSourceKind records which fact source supplied the input factset a
// captured catalog was compiled from.
type FactSourceKind string

const (
	FactSourcePuppetDB FactSourceKind = "puppetdb"
	FactSourceFile     FactSourceKind = "file"
)

// Source records the adapter and producer identity that produced the
// snapshot payload, when supplied by the service.
type Source struct {
	Kind     string `json:"kind"`
	Producer string `json:"producer,omitempty"`
}

// CaptureProvenance records how a captured catalog payload was actually
// obtained, as reported by the compiler adapter that obtained it, rather
// than as requested by configuration. RequestedAPI and EffectiveAPI
// differ exactly when a permitted v4-to-v3 fallback executed, which is
// the case a single API field could not express: a snapshot compiled
// through v3 after a v4 request has v3's trust semantics and v3's
// PuppetDB persistence consequences, whatever the target file asked for.
//
// It deliberately carries no warning text. model.V3TrustedFactWarning is
// a single-sourced constant, and a reader derives the warning from
// EffectiveAPI being v3 instead of reading a copy frozen into the file:
// otherwise the constant could never be reworded without contradicting
// every snapshot already on disk, and Validate would end up asserting
// prose.
type CaptureProvenance struct {
	RequestedAPI   CompilerAPI `json:"requested_api"`
	EffectiveAPI   CompilerAPI `json:"effective_api"`
	FellBackFromV4 bool        `json:"fell_back_from_v4,omitempty"`
	// TrustedFactsSource is set for an effective-v4 capture and empty for
	// v3, which has no trusted-fact request field at all.
	TrustedFactsSource TrustedFactsSource `json:"trusted_facts_source,omitempty"`
	FactSource         FactSourceKind     `json:"fact_source"`
}

// Envelope is the on-disk PIACE snapshot file shape: a UTF-8 JSON
// document wrapping a validated PIACE service projection with
// integrity and capture metadata. See the package comment for the
// checksum scope and for why integrity and attribution are checked
// separately.
type Envelope struct {
	FormatVersion int    `json:"format_version"`
	Kind          Kind   `json:"kind"`
	Target        string `json:"target"`
	Source        Source `json:"source"`
	// CapturedAt is RFC 3339 in UTC.
	CapturedAt string `json:"captured_at"`

	// RequestedEnvironment, Capture, and InputFactsetIdentity are
	// mandatory for catalog snapshots and omitted for factset snapshots.
	RequestedEnvironment string             `json:"requested_environment,omitempty"`
	Capture              *CaptureProvenance `json:"capture,omitempty"`
	// InputFactsetIdentity records which factset produced this catalog. It
	// is an audit record, not an enforced relationship: nothing at
	// comparison time holds the original factset to recompute it against,
	// so Validate checks that it is present, not that it matches anything.
	InputFactsetIdentity string `json:"input_factset_identity,omitempty"`

	// PayloadChecksum is "sha256:<hex>" over the compact canonical JSON
	// encoding of Payload alone.
	PayloadChecksum string `json:"payload_checksum"`

	// Payload retains the typed service projection, including raw resources
	// and their sensitivity metadata. json.RawMessage defers decoding
	// to the factset/catalog-specific consumer.
	Payload json.RawMessage `json:"payload"`
}
