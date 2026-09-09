package model

// RedactedValue is the stable redaction marker substituted for a Puppet
// `Sensitive`-wrapped value or a value matched by a configured
// config.RedactionSelector, in every output format: "Configured
// selectors replace matched values with the constant `"<redacted>"`."
// The differ writes this marker into ResourceChange.Before/After or
// FileContentEvidence; aggregation combines member markers in its projection.
// This package defines the shared literal so every consumer
// (JSON/text/HTML renderers, aggregate builder) recognizes exactly one
// marker value.
const RedactedValue = "<redacted>"

// ChangeKind is the kind of a single semantic difference. Design.md
// section 7.1 fixes exactly four kinds; File-content evidence rides along
// with a ParameterChanged entry for a `File` resource's content-bearing
// parameter rather than introducing a fifth kind.
type ChangeKind string

const (
	ChangeResourceAdded    ChangeKind = "resource_added"
	ChangeResourceRemoved  ChangeKind = "resource_removed"
	ChangeParameterChanged ChangeKind = "parameter_changed"
	ChangeEdgeAdded        ChangeKind = "edge_added"
	ChangeEdgeRemoved      ChangeKind = "edge_removed"
)

// ResourceChange is one resource-level or parameter-level difference for a
// single target's node diff.
type ResourceChange struct {
	Kind      ChangeKind       `json:"kind"`
	Identity  ResourceIdentity `json:"identity"`
	Parameter string           `json:"parameter,omitempty"`
	// Before/After carry the redaction-safe canonical value projection. Raw
	// unredacted values are held only in short-lived comparison structures
	// upstream of this type;
	Before any `json:"before,omitempty"`
	After  any `json:"after,omitempty"`
	// For additions/removals, Before/After holds the existing side's parameter
	// map. File content-bearing parameters carry markers, never bytes or digests.
	// FileContent also describes one-sided evidence for File additions/removals.
	FileContent *FileContentEvidence `json:"file_content,omitempty"`
	// Fingerprint is a stable, equality-preserving digest of this change's
	// *unredacted* canonical comparison evidence, computed by the differ
	// (internal/diff) before its redaction pass runs.
	//
	// Equivalent aggregate keys include kind, identity, parameter name when
	// relevant, and the unredacted canonical comparison evidence. Redaction
	// has to happen before result serialization, template data, diagnostic
	// composition, and rendering, has to avoid merging distinct sensitive
	// changes in aggregate groups, and must retain no secret material in
	// logs or aggregate keys.
	//
	// Those three constraints have exactly one solution shape: the aggregate
	// builder (internal/aggregate) needs to decide *equality* of the
	// unredacted evidence, not to read it. Fingerprint carries that equality
	// and nothing else. Two changes whose unredacted evidence is identical
	// share a Fingerprint; two distinct sensitive values do not, so they can
	// never merge into one aggregate group even though both Before and After
	// projections read RedactedValue.
	//
	// It is `json:"-"`: it never reaches a serialized report, a template,
	// a log line, or persistent aggregate state, per section 7.1's "raw
	// values never enter logs, PQL, serialized reports, templates, or
	// persistent aggregate state." Consumers must treat it as an opaque
	// grouping token, never as recoverable evidence.
	Fingerprint string `json:"-"`
}

// EdgeChange is one edge-level difference for a single target's node diff.
type EdgeChange struct {
	Kind ChangeKind `json:"kind"`
	Edge Edge       `json:"edge"`
}

// ExclusionOutcome records one applied exclusion rule's identity and how
// many differences it suppressed.
type ExclusionOutcome struct {
	Rule                 ExclusionRuleRef `json:"rule"`
	SuppressedResources  int              `json:"suppressed_resources"`
	SuppressedParameters int              `json:"suppressed_parameters"`
	SuppressedEdges      int              `json:"suppressed_edges"`
}

// ExclusionRuleRef identifies an exclusion rule for provenance/reporting
// without re-embedding full configuration objects.
type ExclusionRuleRef struct {
	Type  string `json:"type"`
	Title string `json:"title"`
}

// NodeDiff is the complete comparison result for one target, produced
// independently of every other target.
type NodeDiff struct {
	Certname        string             `json:"certname"`
	ResourceChanges []ResourceChange   `json:"resource_changes,omitempty"`
	EdgeChanges     []EdgeChange       `json:"edge_changes,omitempty"`
	Exclusions      []ExclusionOutcome `json:"exclusions,omitempty"`
	HasDifference   bool               `json:"has_difference"`
}

// AggregateChangeKey identifies one aggregate group. Groups share kind,
// identity (or edge endpoints), and (when relevant) parameter name;
// equivalence for a resource-kind group additionally requires equal raw
// canonical before/after evidence, decided via the redaction-safe
// ResourceChange.Fingerprint rather than by carrying the evidence in the
// key. The key alone therefore remains a stable, redaction-safe grouping
// label.
//
// Exactly one of Identity and Edge is set, determined by Kind:
// ResourceAdded, ResourceRemoved and ParameterChanged set Identity;
// EdgeAdded and EdgeRemoved set Edge. Edge changes survive aggregation
// as a distinct change kind, and an edge has no single resource identity
// to key on: its equivalence is the ordered (source, target) pair, which
// is already complete evidence, so an edge group needs no fingerprint.
type AggregateChangeKey struct {
	Kind ChangeKind `json:"kind"`
	// Identity is set for the three resource-level kinds.
	Identity *ResourceIdentity `json:"identity,omitempty"`
	// Parameter is set only for ParameterChanged.
	Parameter string `json:"parameter,omitempty"`
	// Edge is set for the two edge-level kinds.
	Edge *Edge `json:"edge,omitempty"`
}

// AggregateGroup groups equivalent non-excluded node changes across
// targets. Before/After apply the most restrictive member disclosure at each
// subtree, independently of the raw-evidence equality used for grouping.
type AggregateGroup struct {
	Key         AggregateChangeKey  `json:"key"`
	FileContent *FileContentSummary `json:"file_content,omitempty"`
	Before      any                 `json:"before,omitempty"`
	After       any                 `json:"after,omitempty"`
	Certnames   []string            `json:"certnames"`
	// NodeChangeRefs links this group to the underlying per-target
	// ResourceChange/EdgeChange entries it was built from.
	NodeChangeRefs []NodeChangeRef `json:"node_change_refs"`
}

// NodeChangeRef links an aggregate group entry back to one target's node
// diff.
type NodeChangeRef struct {
	Certname string `json:"certname"`
	// Index is the position of the referenced change within that target's
	// NodeDiff: in ResourceChanges for the three resource-level kinds, and
	// in EdgeChanges for the two edge-level kinds. The containing
	// AggregateGroup's Key.Kind selects which slice, since a group is always
	// of exactly one kind.
	Index int `json:"index"`
}

// AggregateDiff is the full cross-target aggregate view, sorted by kind
// and canonical identity.
type AggregateDiff struct {
	Groups []AggregateGroup `json:"groups"`
}
