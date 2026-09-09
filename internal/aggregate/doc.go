// Package aggregate implements PIACE's aggregate builder: it groups
// equivalent changes across targets into one cross-target view.
//
// # Scope
//
// Build is the single entry point: given every target's model.NodeDiff
// in target-file order, it groups equivalent changes across targets into
// one model.AggregateDiff. It performs no I/O, makes no service call,
// and reads nothing but the node diffs handed to it. Impact estimation
// is the separate internal/impact package, and assembling the shared
// result document happens above both.
//
// Exclusions need no handling here: internal/diff already removed every
// excluded difference from the model.NodeDiff it returned, so grouping
// consumes only the remaining differences by construction rather than
// through a second filter that could drift from the first.
//
// # Equivalence, and why it is decided on a fingerprint
//
// Aggregate equivalence is kind, identity, parameter name when relevant,
// and the unredacted canonical comparison evidence. This package never
// sees unredacted evidence: internal/diff redacts at its own boundary
// (see that package's "Aggregate grouping across the redaction boundary"
// section) and hands forward model.ResourceChange.Fingerprint, a digest
// over the pre-redaction evidence that preserves equality without
// carrying any recoverable value. Two changes group together when their
// kind, identity, parameter, and Fingerprint all match, so two targets
// whose same parameter changed to *different* secrets never merge into
// one group even though both projections read model.RedactedValue.
//
// Disclosure is independent of equivalence. Each group's before/after
// projection combines every member's redaction markers recursively: a marker
// masks the corresponding subtree for the whole group. This applies equally
// to sensitivity metadata, nested wrappers, and configured selectors. Public
// siblings remain visible, and the per-target projections are never modified.
// Renaming or reordering targets therefore cannot weaken group disclosure.
// File-content summaries retain state, evidence sources, verification and
// redaction status, but no digests or target-specific catalog provenance.
// Sources that differ across equivalent members are reported as mixed;
// verification requires every member to be verified. NodeChangeRefs retain
// access to each member's full publishable evidence. Resource additions and
// removals group by their complete managed parameter evidence.
//
// An empty Fingerprint means "cannot group": internal/diff sets it only
// when canonical encoding failed, alongside an error-severity
// diagnostic. Such a change is placed in a group of its own rather than
// merged with every other unfingerprintable change sharing its identity,
// which is the contract internal/diff/diff.go states.
//
// An edge change carries no fingerprint and needs none: an edge's whole
// semantic content is its kind plus its ordered (source, target) pair,
// so the key is already complete evidence. Edge changes survive
// aggregation as a distinct kind, which model.AggregateChangeKey
// represents with its Edge field: exactly one of Identity and Edge is
// set, selected by Kind.
//
// # Determinism
//
// Groups are emitted sorted by kind, then by canonical identity or
// ordered edge pair, then by parameter name, then by fingerprint. That
// last tiebreaker is load-bearing rather than cosmetic: two groups with
// an identical model.AggregateChangeKey and different fingerprints are
// exactly the distinct-sensitive-changes case above, and without it
// their relative order would fall out of Go map iteration order and the
// report would not be byte-identical across runs.
//
// Within a group, certnames are sorted and node-change references are
// emitted in that same certname order. A certname appears at most once
// in Certnames even if the same target somehow contributed two
// equivalent changes; every contributing change still gets its own
// NodeChangeRef, so the link back to the underlying node diffs never
// silently drops one.
package aggregate
