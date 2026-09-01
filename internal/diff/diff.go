package diff

import (
	"context"

	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/filecontent"
	"github.com/example42/piace/internal/model"
)

// Diff compares one target's baseline and candidate normalized catalogs
// and returns that target's complete model.NodeDiff plus every
// diagnostic produced while resolving File-content evidence. It is this
// package's only entry point; see doc.go for the fixed three-pass
// ordering it implements and why that ordering is required.
//
// target supplies the certname the result is labeled with and the
// resolved exclusion rules and redaction selectors to apply. before and
// after are already-normalized catalogs (internal/normalize) for that
// same certname; Diff does not fetch, normalize, or re-validate them.
// retriever is passed through to internal/filecontent for File resources
// whose content evidence needs compiler-backed retrieval, and may be nil
// when no retrieval is available — internal/filecontent then reports
// content_indeterminate with a diagnostic rather than claiming a
// verified comparison.
//
// Diff never returns an error: every failure it can encounter is a
// per-target condition that belongs in the returned diagnostics, and the
// caller's outcome reducer (internal/report) classifies them. A normalization
// failure upstream means Diff is not called for that target at all.
func Diff(
	ctx context.Context,
	target resolve.Target,
	before, after model.NormalizedCatalog,
	retriever filecontent.ContentRetriever,
) (model.NodeDiff, []model.Diagnostic) {
	beforeResources := indexResources(before.Resources)
	afterResources := indexResources(after.Resources)

	// Pass 1: full graph diff, computed with no knowledge of exclusion
	// or redaction configuration.
	resourceChanges, diagnostics := diffResources(ctx, target.Certname, after.Environment,
		beforeResources, afterResources, retriever)
	edgeChanges := diffEdges(before.Edges, after.Edges)

	// Fingerprints digest the unredacted evidence and must therefore be
	// computed before pass 3. They are computed before pass 2 as well,
	// which costs a little work on differences that are about to be
	// suppressed but keeps the "unredacted evidence is available exactly
	// here and nowhere later" boundary in one place.
	for i := range resourceChanges {
		fingerprint, err := fingerprintResourceChange(resourceChanges[i])
		if err != nil {
			// A canonical-encoding failure means a value escaped the
			// model.Value domain internal/normalize is required to
			// enforce, so this branch is unreachable for any catalog
			// that normalization accepted. If it is ever reached, the
			// change is still reported — dropping it would hide a real
			// difference — but with an empty Fingerprint, which the
			// aggregate builder treats as "cannot group" rather than as a
			// token every other unfingerprintable change shares. The
			// error-severity diagnostic already forces an operational
			// failure outcome, so no result relying on that grouping
			// can be reported as clean.
			diagnostics = append(diagnostics, model.Diagnostic{
				Severity:  model.SeverityError,
				Operation: model.OperationNormalize,
				Certname:  target.Certname,
				Message:   err.Error(),
			})
			continue
		}
		resourceChanges[i].Fingerprint = fingerprint
	}

	// Pass 2: exclusion evaluation, over every identity present in
	// either catalog rather than only those carrying a difference.
	allIdentities := unionIdentities(beforeResources, afterResources)
	resourceChanges, edgeChanges, exclusions := applyExclusions(
		target.Exclude, resourceChanges, edgeChanges, allIdentities)

	// HasDifference is fixed here, from the surviving non-excluded
	// differences only, and before pass 3 can touch any value.
	nodeDiff := model.NodeDiff{
		Certname:      target.Certname,
		EdgeChanges:   edgeChanges,
		Exclusions:    exclusions,
		HasDifference: len(resourceChanges) > 0 || len(edgeChanges) > 0,
	}

	// Pass 3: redaction, strictly after HasDifference is already set.
	nodeDiff.ResourceChanges = redactChanges(target.Redact, resourceChanges)

	return nodeDiff, diagnostics
}
