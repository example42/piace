// Package model defines the versioned normalized catalog schema shared by
// the differ, aggregate builder, and result renderers.
//
// This package defines shape only. The normalization algorithm itself,
// canonical parameter values, tag and source-line stripping, and
// identity construction from raw catalog documents, is implemented by
// internal/normalize.
package model

// Value is a canonical, JSON-compatible parameter value: string, bool,
// nil (Puppet undef), Number, []Value, or map[string]Value. The
// normalizer is responsible for producing values restricted to this
// domain and for exact decimal number comparison rather than machine
// floating point; this package only carries the resulting value through
// diffing and serialization.
type Value = any

// Number is a canonical decimal number value within the Value domain:
// the exact base-10 digits of a catalog parameter's numeric value, with
// no machine floating point rounding. Two Number values are equal, via
// Go's == in a comparable context or reflect.DeepEqual for one nested
// inside a map or slice Value, if and only if they denote the same exact
// decimal number, regardless of how the original JSON numeric literal
// was spelled ("1.50" vs "1.5", "1e2" vs "100"): number comparisons use
// exact normalized decimal values rather than machine floating point.
// internal/normalize constructs Number values using the same canonical
// decimal algorithm internal/snapshot's canonical JSON encoder
// implements for snapshot payload checksums, so there is exactly one
// canonicalization behavior across the codebase and results stay
// deterministic.
type Number string

// MarshalJSON emits n's canonical decimal digits directly as a JSON
// number token (never a quoted string), so a Number retains its JSON
// number type in serialized reports.
func (n Number) MarshalJSON() ([]byte, error) {
	if n == "" {
		return []byte("0"), nil
	}
	return []byte(n), nil
}

// ResourceIdentity is the exact Puppet resource identity `Type[title]`.
// Type and Title participate in identity with no case folding.
type ResourceIdentity struct {
	Type  string `json:"type"`
	Title string `json:"title"`
}

// String renders the identity in its canonical `Type[title]` form.
func (r ResourceIdentity) String() string {
	return r.Type + "[" + r.Title + "]"
}

// Resource is one normalized catalog resource. Parameters holds only
// canonical values within the Value domain; tags, source file/line, and
// other non-semantic metadata are discarded by the normalizer before a
// Resource is constructed.
type Resource struct {
	Identity            ResourceIdentity    `json:"identity"`
	Parameters          map[string]Value    `json:"parameters,omitempty"`
	SensitiveParameters []string            `json:"sensitive_parameters,omitempty"`
	StaticContent       *StaticFileMetadata `json:"-"`
	CapturedContent     *ContentDigest      `json:"-"`
	RecursiveContent    bool                `json:"-"`
}

// Edge is a normalized dependency graph edge. Source and Target are the
// `Type[title]` identity strings of the edge endpoints; direction is
// significant.
type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// NormalizedCatalog is a catalog reduced to its semantic graph: a
// resource map and an edge set. Resources are sorted by Identity and
// Edges are sorted by (Source, Target) before comparison and
// serialization.
type NormalizedCatalog struct {
	Certname       string         `json:"certname"`
	Environment    string         `json:"environment,omitempty"`
	Resources      []Resource     `json:"resources"`
	Edges          []Edge         `json:"edges"`
	ContentContext ContentContext `json:"-"`
}

// ContentContext identifies the catalog whose desired bytes are being checked.
// Historical catalogs must never be resolved against mutable environment files.
type ContentContext struct {
	Source          string `json:"source,omitempty"`
	Environment     string `json:"environment,omitempty"`
	Historical      bool   `json:"historical"`
	CatalogIdentity string `json:"catalog_identity,omitempty"`
}

// ContentDigest is persisted inside the checksummed snapshot payload.
type ContentDigest struct {
	Algorithm string `json:"algorithm"`
	Digest    string `json:"digest"`
}

// StaticFileMetadata retains the compiler's single-file evidence projection.
// Source paths and content_uri stay in the raw snapshot, never in a report.
type StaticFileMetadata struct {
	Type     string `json:"type"`
	Checksum struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"checksum"`
}

type FileSideEvidence struct {
	Context  ContentContext            `json:"context"`
	Source   FileContentEvidenceSource `json:"source,omitempty"`
	Verified bool                      `json:"verified"`
}

// FileContentState classifies the evidence available for a managed File
// resource's effective content comparison.
type FileContentState string

const (
	// Added/Removed describe verified evidence on the sole catalog side that
	// exists, not a prediction of filesystem creation or deletion.
	FileContentAdded   FileContentState = "resource_added"
	FileContentRemoved FileContentState = "resource_removed"
	// FileContentUnchanged means compared digests or checksums matched.
	FileContentUnchanged FileContentState = "unchanged"
	// FileContentChanged means a verified cryptographic digest or
	// authoritative checksum comparison found a difference.
	FileContentChanged FileContentState = "changed"
	// FileContentReferenceChanged means the content source/reference
	// changed but comparable bytes or a checksum were not available.
	FileContentReferenceChanged FileContentState = "reference_changed"
	// FileContentIndeterminate means content retrieval or comparison
	// could not establish the evidence required for a verified
	// comparison; it must never be reported as a clean/unchanged result.
	FileContentIndeterminate FileContentState = "content_indeterminate"
	// FileContentNotManaged means the catalog manages no bytes for this
	// File on at least one of the sides being compared, because its
	// ensure value is absent or link. That is a determinate answer and
	// not a failed comparison: Puppet ignores content, source and
	// checksum_value for such a resource, so there are no desired bytes
	// to compare. An ensure value that changes between the two catalogs
	// is reported on its own as a parameter change.
	FileContentNotManaged FileContentState = "not_managed"
)

// FileContentEvidenceSource records which resolution step in the
// file-content priority order produced FileContentEvidence.
type FileContentEvidenceSource string

const (
	FileContentEvidenceInline            FileContentEvidenceSource = "inline_content"
	FileContentEvidenceCompiledChecksum  FileContentEvidenceSource = "compiled_checksum"
	FileContentEvidenceCompilerRetrieval FileContentEvidenceSource = "compiler_retrieval"
	FileContentEvidenceStaticMetadata    FileContentEvidenceSource = "static_metadata"
	FileContentEvidenceCaptured          FileContentEvidenceSource = "captured_digest"
	FileContentEvidenceMixed             FileContentEvidenceSource = "mixed"
)

// FileContentEvidence is the redaction-safe evidence attached to a File
// parameter or membership change. Membership changes populate only the
// existing catalog side. It never carries managed content bytes; sensitivity
// or a content selector suppresses even the digest.
type FileContentEvidence struct {
	State            FileContentState          `json:"state"`
	EvidenceSource   FileContentEvidenceSource `json:"evidence_source,omitempty"`
	Algorithm        string                    `json:"algorithm,omitempty"`
	BeforeDigest     string                    `json:"before_digest,omitempty"`
	AfterDigest      string                    `json:"after_digest,omitempty"`
	Redacted         bool                      `json:"redacted,omitempty"`
	Before           *FileSideEvidence         `json:"before,omitempty"`
	After            *FileSideEvidence         `json:"after,omitempty"`
	ReferenceChanged bool                      `json:"reference_changed,omitempty"`
}

// FileContentSummary is aggregate and assessment evidence without content
// digests or target-specific catalog provenance. Individual change references
// retain access to that provenance in the report.
type FileContentSummary struct {
	State            FileContentState          `json:"state"`
	EvidenceSource   FileContentEvidenceSource `json:"evidence_source,omitempty"`
	Redacted         bool                      `json:"redacted,omitempty"`
	ReferenceChanged bool                      `json:"reference_changed,omitempty"`
	Before           *FileSideSummary          `json:"before,omitempty"`
	After            *FileSideSummary          `json:"after,omitempty"`
}

type FileSideSummary struct {
	Source   FileContentEvidenceSource `json:"source,omitempty"`
	Verified bool                      `json:"verified"`
}
