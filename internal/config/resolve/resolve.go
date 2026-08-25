package resolve

import (
	"github.com/example42/piace/internal/config"
)

// Documented assumptions made in this package where design.md/
// requirements.md leave a gap (see also doc.go and validate.go):
//
//  1. Service-level request deadline: design.md section 3.2 rule 7 says
//     "the effective network request deadline is the smaller of the
//     service deadline and the target impact timeout", but
//     config.ServicesFile (established by task 1) has no service-level
//     deadline field, and design.md section 2.2 only says the transport
//     layer "applies timeouts ... at the transport boundary" without
//     naming a config source. This package resolves and validates only
//     the target's own impact_estimate.timeout; combining it with a
//     service-level deadline is left as a gap for task 3/10, which will
//     either introduce that field or source the deadline from the
//     transport layer itself. This package must not invent a new
//     ServicesFile field unilaterally.
//  2. impact_estimate presence is only required when Enabled resolves to
//     true. design.md section 3.2 rule 6 lists the fields "every target
//     needs" (environment, fact source, baseline source, baseline
//     environment, API version, fail_on_diff) and separately says
//     "Impact limits must be positive" without listing impact_estimate
//     among the always-required fields. Since impact estimation is
//     itself togglable per requirements.md 9.1, a disabled target has no
//     use for a timeout/limit and none is required; when enabled, both
//     must resolve to positive values.
//  3. certname format: see validate.go's validateCertname doc comment.
//  4. fail_on_diff defaults to false when unset by both defaults and
//     target, matching requirements.md's use of "global fail_on_diff
//     setting and a per-target override" without a stated default; this
//     task's brief states the same default explicitly.
//  5. ServicesFile TLS/endpoint fields are validated for syntax only
//     (non-empty, https, no NUL bytes); file existence/readability is
//     task 3's concern, per design.md's "before any service call" framing
//     for this task.

// resolvedScalars is the fully merged, pre-validation view of one target's
// scalar/object configuration: global defaults with each field replaced by
// a non-nil per-target override. Exclude/Redact are handled separately
// (append-only, not "replace"), per design.md section 3.2 rule 2.
type resolvedScalars struct {
	candidate      config.CandidateConfig
	facts          config.FactsConfig
	baseline       config.BaselineConfig
	impactEstimate config.ImpactEstimateConfig
	failOnDiff     bool
}

// mergeScalars applies design.md section 3.2 rule 1: "Resolve global
// defaults first, then replace each scalar or object field with a target
// override." Object-valued fields (Candidate/Facts/Baseline/ImpactEstimate)
// replace wholesale when the target supplies the object at all — the target
// schema (task 1) already models "no override" as a nil pointer for these,
// distinct from "override to the zero value", so there is no field-by-field
// merge within one object: a target that sets `candidate:` at all is
// expected to supply every field it cares about, consistent with the
// example in requirements.md section 8 where per-target `candidate` blocks
// repeat both environment and catalog_api.
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

// mergeExclusions implements design.md section 3.2 rule 2 for exclusion
// rules: the global list is prepended to the per-target list, never
// replaced, with duplicates retained once in first-seen order.
//
// "Duplicates retained once in first-seen order" (design.md's exact
// phrasing) is interpreted as: the merged list is exactly
// append(defaults, target...) with exact duplicate entries collapsed to
// their first occurrence, preserving that first occurrence's position.
// This differs from "retained" meaning "kept as literal repeats" — a
// literal duplicate would defeat the purpose of tracking one rule's
// suppressed-count in provenance (see model.ExclusionOutcome), since two
// identical rule entries would double-count the same suppressions under
// the same rule identity. Deduplication is by exact (Type, Title) equality
// only; it never merges rules with different titles for the same type.
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
