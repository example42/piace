package impact

import (
	"context"
	"sort"

	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
)

// EstimateAll runs the impact estimates for one PIACE invocation and
// returns them sorted by canonical identity, together with every
// diagnostic they produced.
//
// targets must be in target-file order and nodeDiffs must be the
// corresponding per-target results; they are paired by certname, which
// internal/config/resolve has already guaranteed unique.
//
// # Which resource identities are estimated
//
// Impact estimation runs only for non-excluded resource additions,
// removals, and parameter changes, never for edge-only differences, once
// per unique exact `Type[title]`. Excluded differences are already
// absent from a model.NodeDiff (see internal/diff), so every
// resource-kind change present here qualifies, deduplicated run-wide by
// identity.
//
// # Whose configuration applies
//
// Identities are deduplicated run-wide, but resolve.Target carries
// impact policy per target, so when one identity changed on several
// targets with different timeouts or limits, or where only some of them
// enable estimation at all, nothing says whose configuration wins. Left
// to emerge from iteration order that would be nondeterministic, so it
// is fixed here:
//
//   - An identity is estimated if at least one target that exhibits it
//     has impact estimation enabled. An identity exhibited only by
//     targets with estimation disabled produces no request and no
//     failure.
//   - The limits used are those of the first target in target-file order
//     that both enables estimation and exhibits that identity. Target-file
//     order is operator-authored and stable, so the choice is reproducible
//     and explainable rather than dependent on map iteration or on which
//     target happened to be diffed first.
//
// Estimates are issued sequentially, which is expressly permitted
// (queries are bounded and may be sequential in v1 to limit PuppetDB
// load) and which keeps PuppetDB load proportional to the number of
// distinct changed identities rather than to target count.
func EstimateAll(
	ctx context.Context,
	querier ImpactQuerier,
	targets []resolve.Target,
	nodeDiffs []model.NodeDiff,
) ([]model.ImpactEstimate, []model.Diagnostic) {
	type targetPolicy struct {
		order   int
		enabled bool
		limits  Limits
	}
	policies := make(map[string]targetPolicy, len(targets))
	for i, t := range targets {
		policies[t.Certname] = targetPolicy{
			order:   i,
			enabled: t.ImpactEstimate.Enabled,
			limits:  LimitsFrom(t.ImpactEstimate),
		}
	}

	// selected maps each estimable identity to the winning target's order
	// and limits together, so recovering the limits never has to hop back
	// through the targets slice by index. A duplicate certname, which
	// resolution rejects but which this package should not silently
	// mis-attribute if it ever appeared, cannot then select one target's
	// index and another's configuration.
	type winner struct {
		order  int
		limits Limits
	}
	selected := make(map[model.ResourceIdentity]winner)
	for _, nd := range nodeDiffs {
		policy, known := policies[nd.Certname]
		if !known {
			// A node diff with no matching target cannot be attributed
			// to any configuration; skip rather than guess. Callers pair
			// the two slices from the same resolved config, so this is
			// defensive only.
			continue
		}
		if !policy.enabled {
			continue
		}
		for _, change := range nd.ResourceChanges {
			if isEdgeKind(change.Kind) {
				continue
			}
			if prior, seen := selected[change.Identity]; !seen || policy.order < prior.order {
				selected[change.Identity] = winner{order: policy.order, limits: policy.limits}
			}
		}
	}

	identities := make([]model.ResourceIdentity, 0, len(selected))
	for identity := range selected {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].Type != identities[j].Type {
			return identities[i].Type < identities[j].Type
		}
		return identities[i].Title < identities[j].Title
	})

	estimates := make([]model.ImpactEstimate, 0, len(identities))
	var diagnostics []model.Diagnostic
	for _, identity := range identities {
		estimate, diag := querier.Estimate(ctx, identity, selected[identity].limits)
		estimates = append(estimates, estimate)
		if diag != nil {
			diagnostics = append(diagnostics, *diag)
		}
	}
	return estimates, diagnostics
}

// isEdgeKind reports whether kind is one of the two edge-level change
// kinds, which are excluded from impact estimation. A model.NodeDiff
// keeps edge changes in their own slice, so this is defensive against a
// resource-change entry carrying an edge kind.
func isEdgeKind(kind model.ChangeKind) bool {
	return kind == model.ChangeEdgeAdded || kind == model.ChangeEdgeRemoved
}
