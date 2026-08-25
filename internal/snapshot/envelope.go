// Package snapshot defines the versioned PIACE snapshot envelope schema
// used for captured factsets and catalogs, and implements the canonical
// JSON encoder, SHA-256 checksum, atomic 0600 writer, and loader/
// validator that operate on it.
//
// Design reference: design.md section 6 ("Snapshot envelopes and
// capture"). See canonical.go/checksum.go for the canonical JSON encoding
// and checksum algorithm, and store.go for Write/Load/Validate. The
// file-backed FactSource/CatalogSource adapters that select this package
// as a target's fact/baseline source (per target.Facts.Source ==
// config.FactSourceFile / target.Baseline.Source ==
// config.BaselineSourceFile) live in internal/puppetdb, not here, to
// avoid this package importing puppetdb's adapter interfaces; see
// internal/puppetdb/filesource.go.
package snapshot

import "encoding/json"

// FormatVersion is the only supported `format_version` value for a
// snapshot envelope. See requirements.md 11.5 and design.md section 6.
const FormatVersion = 1

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

// Source records the adapter and producer identity that produced the
// snapshot payload, when supplied by the service.
type Source struct {
	Kind     string `json:"kind"`
	Producer string `json:"producer,omitempty"`
}

// Envelope is the on-disk PIACE snapshot file shape: a UTF-8 JSON document
// wrapping an unmodified validated service payload with integrity and
// capture metadata. See design.md section 6 for the exact field set and
// checksum scope (SHA-256 over the canonical encoding of Payload only,
// excluding envelope metadata).
type Envelope struct {
	FormatVersion int    `json:"format_version"`
	Kind          Kind   `json:"kind"`
	Target        string `json:"target"`
	Source        Source `json:"source"`
	// CapturedAt is RFC 3339 in UTC.
	CapturedAt string `json:"captured_at"`

	// RequestedEnvironment, CompilerAPIVersion, and InputFactsetIdentity
	// are mandatory for catalog snapshots and omitted for factset
	// snapshots, per design.md section 6.
	RequestedEnvironment string      `json:"requested_environment,omitempty"`
	CompilerAPIVersion   CompilerAPI `json:"compiler_api,omitempty"`
	InputFactsetIdentity string      `json:"input_factset_identity,omitempty"`

	// PayloadChecksum is "sha256:<hex>" over the compact canonical JSON
	// encoding of Payload alone.
	PayloadChecksum string `json:"payload_checksum"`

	// Payload retains the original validated service document verbatim;
	// it is not a lossy normalized form. json.RawMessage defers decoding
	// to the factset/catalog-specific consumer.
	Payload json.RawMessage `json:"payload"`
}
