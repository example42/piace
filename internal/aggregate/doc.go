// Package aggregate implements PIACE's aggregate builder: the first half
// of task 10 ("Build deterministic aggregate diffs and optional impact
// estimates"), design.md's Architecture component "aggregate builder",
// design.md sections 7.1 ("Equivalent aggregate keys...") and 9
// ("aggregate groups sorted by kind and canonical identity"), and
// requirements.md 7.1-7.4.
//
// # Scope
//
// Build is the single entry point: given every target's model.NodeDiff
// in target-file order, it groups equivalent changes across targets into
// one model.AggregateDiff. It performs no I/O, makes no service call,
// and reads nothing but the node diffs handed to it. Impact estimation
// is the separate internal/impact package (design.md's Architecture
// shows "aggregate builder -> impact estimator" as two components), and
// assembling the shared result document is task 11.
//
// Exclusions need no handling here: internal/diff already removed every
// excluded difference from the model.NodeDiff it returned, so
// requirements.md 7's "group equivalent changes" and design.md section
// 7.3's "policy evaluation and aggregate building consume only the
// remaining differences" are satisfied by construction rather than by a
// second filter that could drift from the first.
//
// # Equivalence, and why it is decided on a fingerprint
//
// design.md section 7.1 defines aggregate equivalence as "kind,
// identity, parameter name when relevant, and the unredacted canonical
// comparison evidence". This package never sees unredacted evidence:
// internal/diff redacts at its own boundary (see that package's
// "Aggregate grouping across the redaction boundary" section) and hands
// forward model.ResourceChange.Fingerprint, a digest over the
// pre-redaction evidence that preserves equality without carrying any
// recoverable value. Two changes group together when their kind,
// identity, parameter, and Fingerprint all match — so two targets whose
// same parameter changed to *different* secrets never merge into one
// group even though both projections read model.RedactedValue.
//
// An empty Fingerprint means "cannot group" (internal/diff sets it only
// when canonical encoding failed, alongside an error-severity
// diagnostic). Such a change is placed in a group of its own rather than
// merged with every other unfingerprintable change sharing its
// identity — honoring the contract internal/diff/diff.go states.
//
// An edge change carries no fingerprint and needs none: an edge's whole
// semantic content is its kind plus its ordered (source, target) pair,
// so the key is already complete evidence. requirements.md 7.4 requires
// edge changes to survive aggregation as a distinct kind, which
// model.AggregateChangeKey represents with its Edge field (exactly one
// of Identity and Edge is set, selected by Kind).
//
// # Determinism (design.md Property 1)
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
// NodeChangeRef, so requirements.md 7.3's "link or otherwise identify
// the underlying node diffs" never silently drops one.
package aggregate
