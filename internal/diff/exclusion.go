package diff

import (
	"path"
	"sort"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/model"
)

// applyExclusions implements pass 2 (see doc.go): it removes every
// resource-added, resource-removed or parameter-changed entry whose
// Identity matches an exclusion rule, and every edge-added or
// edge-removed entry whose Source or Target endpoint matches one.
// Exclusion evaluation matches resource identities and removes matching
// resource differences, and it also suppresses any edge difference
// attached to an excluded identity.
//
// matchedIdentities is every resource identity present in either
// catalog (not only those with a change) that matches at least one
// rule: an edge's Source/Target is a bare `Type[title]` string, not a
// structured identity, so edge suppression is decided by exact string
// membership in this set's stringified form rather than re-parsing the
// edge endpoint.
//
// Returned exclusions is sorted by rule order, the defaults-then-target
// append order resolve.Target.Exclude already preserves, and always
// includes every rule that matched at least one difference, with its
// exact suppressed counts. A rule that matched zero differences is
// omitted rather than reported with all-zero counts: deterministic
// counts by rule and by suppressed kind are about rules that did
// something, not about every configured rule regardless of effect.
func applyExclusions(
	rules []config.ExclusionRule,
	resourceChanges []model.ResourceChange,
	edgeChanges []model.EdgeChange,
	allIdentities []model.ResourceIdentity,
) ([]model.ResourceChange, []model.EdgeChange, []model.ExclusionOutcome) {
	if len(rules) == 0 {
		return resourceChanges, edgeChanges, nil
	}

	// ruleIndexForIdentity maps each excluded identity to the index of
	// the first rule (in configured order) that matches it. Matching an
	// identity against multiple rules attributes its suppressed
	// differences to only the first matching rule, avoiding
	// double-counting a single suppressed difference across rules.
	excludedIdentityRule := make(map[model.ResourceIdentity]int, len(allIdentities))
	for _, identity := range allIdentities {
		for i, rule := range rules {
			if matchesExclusionRule(rule, identity) {
				excludedIdentityRule[identity] = i
				break
			}
		}
	}
	excludedIdentityStrings := make(map[string]bool, len(excludedIdentityRule))
	for identity := range excludedIdentityRule {
		excludedIdentityStrings[identity.String()] = true
	}

	counts := make([]model.ExclusionOutcome, len(rules))
	for i, rule := range rules {
		counts[i].Rule = model.ExclusionRuleRef{Type: rule.Type, Title: rule.Title}
	}

	keptResourceChanges := make([]model.ResourceChange, 0, len(resourceChanges))
	for _, change := range resourceChanges {
		ruleIndex, excluded := excludedIdentityRule[change.Identity]
		if !excluded {
			keptResourceChanges = append(keptResourceChanges, change)
			continue
		}
		switch change.Kind {
		case model.ChangeResourceAdded, model.ChangeResourceRemoved:
			counts[ruleIndex].SuppressedResources++
		case model.ChangeParameterChanged:
			counts[ruleIndex].SuppressedParameters++
		}
	}

	keptEdgeChanges := make([]model.EdgeChange, 0, len(edgeChanges))
	for _, change := range edgeChanges {
		sourceExcluded := excludedIdentityStrings[change.Edge.Source]
		targetExcluded := excludedIdentityStrings[change.Edge.Target]
		if !sourceExcluded && !targetExcluded {
			keptEdgeChanges = append(keptEdgeChanges, change)
			continue
		}
		// Attribute the suppressed edge to whichever endpoint's rule
		// index is lowest (i.e. the rule that would be reported first),
		// so a single edge is never counted twice when both endpoints
		// are excluded by different rules.
		ruleIndex := edgeSuppressingRuleIndex(excludedIdentityRule, change.Edge, sourceExcluded, targetExcluded)
		counts[ruleIndex].SuppressedEdges++
	}

	var outcomes []model.ExclusionOutcome
	for _, c := range counts {
		if c.SuppressedResources > 0 || c.SuppressedParameters > 0 || c.SuppressedEdges > 0 {
			outcomes = append(outcomes, c)
		}
	}

	return keptResourceChanges, keptEdgeChanges, outcomes
}

// edgeSuppressingRuleIndex picks the deterministic rule index to
// attribute a suppressed edge to, when one or both endpoints are
// excluded.
func edgeSuppressingRuleIndex(
	excludedIdentityRule map[model.ResourceIdentity]int,
	edge model.Edge,
	sourceExcluded, targetExcluded bool,
) int {
	var candidates []int
	if sourceExcluded {
		candidates = append(candidates, ruleIndexForEdgeEndpoint(excludedIdentityRule, edge.Source))
	}
	if targetExcluded {
		candidates = append(candidates, ruleIndexForEdgeEndpoint(excludedIdentityRule, edge.Target))
	}
	sort.Ints(candidates)
	return candidates[0]
}

// ruleIndexForEdgeEndpoint looks up the rule index recorded for the
// resource identity matching endpoint's exact `Type[title]` string. An
// edge endpoint is always exactly one identity's String() form (see
// internal/normalize/catalog.go), so parsing back is unnecessary: the
// caller already knows endpoint is a key of excludedIdentityStrings, and
// the same key space is used here by re-deriving the identity via
// direct string equality against every excluded identity's own
// String(). Since excludedIdentityRule is keyed by structured
// model.ResourceIdentity, a small linear scan is required to recover
// the index by the endpoint's string form; this is bounded by the
// (typically small) number of excluded identities, not by the full
// catalog.
func ruleIndexForEdgeEndpoint(excludedIdentityRule map[model.ResourceIdentity]int, endpoint string) int {
	for identity, ruleIndex := range excludedIdentityRule {
		if identity.String() == endpoint {
			return ruleIndex
		}
	}
	// Unreachable: the caller only invokes this for an endpoint already
	// confirmed present in excludedIdentityStrings, which is derived
	// from the same excludedIdentityRule map's keys.
	return 0
}

// matchesExclusionRule implements the rule semantics exactly: Type is an
// exact, case-sensitive match; Title is a case-sensitive path.Match
// glob, matching internal/config/resolve/validate.go's validation
// dialect.
func matchesExclusionRule(rule config.ExclusionRule, identity model.ResourceIdentity) bool {
	if rule.Type != identity.Type {
		return false
	}
	matched, err := path.Match(rule.Title, identity.Title)
	if err != nil {
		// A malformed glob is rejected during configuration resolution
		// (internal/config/resolve/validate.go); reaching an invalid
		// pattern here would mean that invariant was violated upstream.
		// Treat it as a non-match rather than panicking or silently
		// excluding everything.
		return false
	}
	return matched
}
