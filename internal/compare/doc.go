// Package compare implements `piace compare`: the target work queue, and
// the assembly of the shared result document.
//
// It owns no domain logic of its own. Every rule it depends on already
// lives in the package that owns it: baseline-environment rejection in
// internal/puppetdb, v3 and v4 policy and candidate identity
// verification in internal/compiler, the value domain in
// internal/normalize, the fixed diff, exclude and redact ordering in
// internal/diff, grouping in internal/aggregate, bounded PQL in
// internal/impact, and outcome precedence in internal/model's Reduce.
// This package sequences those calls, attributes each diagnostic to the
// right place in the shared result document, and nothing else. It
// validates fact inputs with puppetdb.ValidateFactset and checks catalog
// attribution at the workflow boundary. The baseline is normalized before
// compilation, so invalid baseline structure cannot cause a candidate request.
//
// # Per-target isolation
//
// The failure model is fixed: a target error is captured in that
// target's node result and processing continues for the other valid
// targets, and global configuration failure is the sole fail-fast
// condition. Configuration has already been resolved and validated
// before Run is called (internal/config/resolve), so within Run there is
// no fail-fast path at all: every failure is one target's diagnostic,
// and the loop always completes.
//
// Within one target the steps are sequential and stop at the first
// failure. Requesting a candidate catalog for a target whose baseline
// could not be loaded would spend a compiler compilation on a comparison
// that cannot happen, and would add a second diagnostic that says nothing
// the first does not. The reported diagnostic is therefore the first
// failure, and the target's outcome class follows from it via
// model.TargetResult.ClassifyOutcome.
//
// Targets are processed one at a time, which is the v1 default. A future
// explicit --parallel option may raise it, but would have to preserve
// target-order result emission.
//
// # Determinism
//
// Identical inputs must produce byte-identical output. Two things in
// this package could break that, and are handled here rather than in a
// renderer:
//
//   - The invocation timestamp. Now is injectable exactly as
//     capture.Workflow.Now is, so a test, including the byte-identical
//     ordering check, can fix the clock.
//   - Target result order. The document owes a deterministic,
//     target-sorted node result for every selected target, so the emitted
//     results are sorted by certname (unique, hence a total order) and
//     the document does not depend on how the target file was
//     ordered. The work itself still runs in target-file order, because
//     impact.EstimateAll resolves an identity exhibited by several
//     targets to "the first target in target-file order that both enables
//     estimation and exhibits that identity", so sorting the output rather
//     than the input keeps that documented tie-break intact.
//
// # Impact estimation and the nil querier
//
// impact.EstimateAll is called once, after every target has finished,
// with the run's node diffs. That is the shape estimation requires,
// since identities are deduplicated run-wide and an estimate failure
// contributes an operational outcome after all other targets finish. Its
// diagnostics carry no certname and land in model.Result.Diagnostics,
// where model.Result.Finalize folds them in.
//
// A nil ImpactQuerier with at least one target that enables estimation
// is a wiring error, not a configuration error: estimation is
// configuration-driven, so an enabled estimate that silently issued no
// query would be an unreported omission of requested analysis. It is
// reported as a run-level estimate_impact error rather than panicking or
// being skipped.
package compare
