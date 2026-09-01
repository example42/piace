package resolve

import (
	"github.com/example42/piace/internal/config"
)

// Documented assumptions this package makes where the configuration
// rules leave a gap (see also doc.go and validate.go):
//
//  1. Service-level request deadline. The effective network request
//     deadline is the smaller of the service deadline and the target
//     impact timeout, but config.ServicesFile has no service-level
//     deadline field, and the transport layer applies timeouts at the
//     transport boundary without naming a config source for one. This
//     package resolves and validates only the target's own
//     impact_estimate.timeout; combining it with a service-level deadline
//     stays a gap, to be closed either by introducing that field or by
//     sourcing the deadline from the transport layer itself. This package
//     must not invent a new ServicesFile field unilaterally.
//  2. impact_estimate presence is only required when Enabled resolves to
//     true. Every target needs an environment, a fact source, a baseline
//     source, a baseline environment, an API version and fail_on_diff,
//     and impact limits must be positive, but impact_estimate is not
//     among the always-required fields. Since impact estimation is
//     itself togglable, a disabled target has no use for a timeout or
//     limit and none is required; when enabled, both must resolve to
//     positive values.
//  3. certname format: see validate.go's validateCertname doc comment.
//  4. fail_on_diff defaults to false when unset by both defaults and
//     target. There is a global fail_on_diff setting and a per-target
//     override, with no stated default, and false is the safe one.
//  5. ServicesFile TLS and endpoint fields are validated for syntax only
//     (non-empty, https, no NUL bytes); file existence and readability
//     belong to the transport layer, which builds the clients before any
//     service call.

// resolvedScalars is the fully merged, pre-validation view of one
// target's scalar/object configuration: global defaults with each field
// replaced by a non-nil per-target override. Exclude/Redact are handled
// separately (append-only, not "replace").
type resolvedScalars struct {
	candidate      config.CandidateConfig
	facts          config.FactsConfig
	baseline       config.BaselineConfig
	impactEstimate config.ImpactEstimateConfig
	failOnDiff     bool
}

// mergeScalars resolves global defaults first, then replaces each scalar
// or object field with a target override. Object-valued fields
// (Candidate, Facts, Baseline, ImpactEstimate) replace wholesale when
// the target supplies the object at all: the target schema already
// models "no override" as a nil pointer for these, distinct from
// "override to the zero value", so there is no field-by-field merge
// within one object. A target that sets `candidate:` at all is expected
// to supply every field it cares about.
func mergeScalars(defaults config.Defaults, target config.Target) resolvedScalars {
	rs := resolvedScalars{
		candidate:      defaults.Candidate,
		facts:          defaults.Facts,
		baseline:       defaults.Baseline,
		impactEstimate: defaults.ImpactEstimate,
		failOnDiff:     false,
	}
	if defaults.FailOnDiff != nil {
		rs.failOnDiff = *defaults.FailOnDiff
	}

	if target.Candidate != nil {
		rs.candidate = *target.Candidate
	}
	if target.Facts != nil {
		rs.facts = *target.Facts
	}
	if target.Baseline != nil {
		rs.baseline = *target.Baseline
	}
	if target.ImpactEstimate != nil {
		rs.impactEstimate = *target.ImpactEstimate
	}
	if target.FailOnDiff != nil {
		rs.failOnDiff = *target.FailOnDiff
	}

	return rs
}

// mergeExclusions prepends the global exclusion list to the per-target
// list, never replacing it, with duplicates retained once in first-seen
// order.
//
// "Duplicates retained once in first-seen order" means the merged list
// is exactly append(defaults, target...) with exact duplicate entries
// collapsed to their first occurrence, preserving that first
// occurrence's position. It does not mean keeping literal repeats: a
// literal duplicate would defeat the purpose of tracking one rule's
// suppressed count in provenance (see model.ExclusionOutcome), since two
// identical rule entries would double-count the same suppressions under
// the same rule identity. Deduplication is by exact (Type, Title)
// equality only; it never merges rules with different titles for the
// same type.
func mergeExclusions(defaults, target []config.ExclusionRule) []config.ExclusionRule {
	return dedupExclusions(append(append([]config.ExclusionRule(nil), defaults...), target...))
}

func dedupExclusions(rules []config.ExclusionRule) []config.ExclusionRule {
	seen := make(map[config.ExclusionRule]bool, len(rules))
	out := make([]config.ExclusionRule, 0, len(rules))
	for _, r := range rules {
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// mergeRedactions applies the same append-only, first-seen-order-dedup
// rule as mergeExclusions, for redaction selectors.
func mergeRedactions(defaults, target []config.RedactionSelector) []config.RedactionSelector {
	return dedupRedactions(append(append([]config.RedactionSelector(nil), defaults...), target...))
}

func dedupRedactions(sels []config.RedactionSelector) []config.RedactionSelector {
	seen := make(map[config.RedactionSelector]bool, len(sels))
	out := make([]config.RedactionSelector, 0, len(sels))
	for _, s := range sels {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
