package puppetdb

import (
	"context"
	"encoding/json"

	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
)

// FactSource retrieves a target's fact data: `FactSource.Load(target) ->
// Factset, Provenance`. The PuppetDB-backed implementation in this
// package (*Adapter) and the file-backed one in filesource.go are
// interchangeable behind this interface; a caller selects between them
// using target.Facts.Source.
type FactSource interface {
	// Load retrieves target's latest factset. It returns a non-nil
	// diagnostic (and a zero Factset/Provenance) on any retrieval,
	// not-found, or decoding failure; it never returns a zero-value
	// Factset as a silent success.
	Load(ctx context.Context, target resolve.Target) (Factset, model.SourceProvenance, *model.Diagnostic)
}

// CatalogSource retrieves a target's baseline catalog:
// `CatalogSource.LoadBaseline(target) -> Catalog, Provenance`. The
// PuppetDB-backed implementation in this package (*Adapter) and the
// file-backed one in filesource.go are interchangeable behind this
// interface; a caller selects between them using target.Baseline.Source.
type CatalogSource interface {
	// LoadBaseline retrieves target's latest baseline catalog. It returns
	// a non-nil diagnostic (and a zero Catalog/Provenance) on any
	// retrieval, not-found, decoding, or baseline-environment-mismatch
	// failure (see doc.go); it never returns a zero-value Catalog as a
	// silent success.
	LoadBaseline(ctx context.Context, target resolve.Target) (Catalog, model.SourceProvenance, *model.Diagnostic)
}

// Factset is the raw carrier for a PuppetDB factset response, promoting
// only the fields this package commits to as named fields. See doc.go
// for the documented PuppetDB v4 response-shape assumption. Facts retains
// the full "facts" payload (the {href, data} expansion) as raw JSON for a
// later stage (candidate compilation, internal/compiler) to parse; this package does
// not interpret individual fact values.
type Factset struct {
	Certname          string          `json:"certname"`
	Environment       string          `json:"environment"`
	Timestamp         string          `json:"timestamp"`
	ProducerTimestamp string          `json:"producer_timestamp"`
	Producer          string          `json:"producer"`
	Hash              string          `json:"hash"`
	Facts             json.RawMessage `json:"facts"`
}

// Catalog is the raw carrier for a PuppetDB catalog response, promoting
// only the fields this package commits to as named fields. See doc.go
// for the documented PuppetDB v4 response-shape assumption. Resources and
// Edges retain the full {href, data} expansions as raw JSON; normalizing
// them into model.NormalizedCatalog is internal/normalize's job, performed on this
// carrier's Resources/Edges fields.
type Catalog struct {
	Certname          string                         `json:"certname"`
	Version           string                         `json:"version"`
	Environment       string                         `json:"environment"`
	Hash              string                         `json:"hash"`
	TransactionUUID   string                         `json:"transaction_uuid"`
	CatalogUUID       string                         `json:"catalog_uuid"`
	CodeID            string                         `json:"code_id"`
	ProducerTimestamp string                         `json:"producer_timestamp"`
	Producer          string                         `json:"producer"`
	Resources         json.RawMessage                `json:"resources"`
	Edges             json.RawMessage                `json:"edges"`
	Metadata          json.RawMessage                `json:"metadata,omitempty"`
	RecursiveMetadata json.RawMessage                `json:"recursive_metadata,omitempty"`
	CapturedContent   map[string]model.ContentDigest `json:"captured_content,omitempty"`

	// StringifiedRich records that this catalog's parameter values
	// passed through Puppet's ToStringifiedConverter, so every rich
	// value in it is a lossy string rather than a Pcore typed value.
	// It is true of a catalog read back from PuppetDB and false of one
	// a compiler returned; see model.ProjectStringifiedRich for what
	// the difference is and why comparison has to know.
	//
	// It is not part of the wire format, and it is not part of a
	// snapshot payload either: whichever loader produced the catalog
	// sets it, so it cannot be forged by a stored document. A snapshot
	// carries the same fact in its envelope's source kind, which is
	// inside the checksum.
	StringifiedRich bool `json:"-"`
}
