// Package exitcode defines PIACE's stable process exit codes and the
// outcome classes that map to them.
//
// Design reference: design.md section 10 "Error taxonomy and outcomes".
// The mapping and precedence order below are part of PIACE's public,
// versioned contract: CI pipelines depend on these exact numeric values.
package exitcode

// Code is a process exit status. Values are part of PIACE's stable public
// contract and MUST NOT change once released.
type Code int

const (
	// Success is returned for a clean comparison or for a comparison whose
	// only differences are allowed by every affected target's policy.
	Success Code = 0
	// PolicyDisallowedDifference is returned when at least one target has
	// fail_on_diff enabled and a non-excluded semantic difference.
	PolicyDisallowedDifference Code = 10
	// CompilationFailure is returned when a candidate catalog could not be
	// obtained or verified for at least one target, and no higher-precedence
	// outcome applies.
	CompilationFailure Code = 20
	// OperationalError is returned for configuration, transport, snapshot,
	// normalization, content-verification, or enabled impact-estimate
	// failures, and takes precedence over every other outcome.
	OperationalError Code = 30
)

// Outcome is the reported classification of a PIACE run or a single target
// within it. Outcome values are part of the shared result document and
// text/HTML output; they are strings so that JSON/HTML output remains
// self-describing without a lookup table.
type Outcome string

const (
	// OutcomeClean means every target was fully retrieved, compiled,
	// normalized, and compared with no non-excluded semantic difference.
	OutcomeClean Outcome = "clean"
	// OutcomeDifferencesAllowed means non-excluded semantic differences were
	// present but every affected target's fail_on_diff policy allowed them.
	OutcomeDifferencesAllowed Outcome = "differences_allowed"
	// OutcomePolicyDisallowedDifference means a target with fail_on_diff
	// enabled had a non-excluded semantic difference.
	OutcomePolicyDisallowedDifference Outcome = "policy_disallowed_difference"
	// OutcomeCompilationFailure means a candidate catalog request failed or
	// could not be verified for at least one target.
	OutcomeCompilationFailure Outcome = "compilation_failure"
	// OutcomeOperationalError means a configuration, transport, snapshot,
	// normalization, content-verification, or enabled impact-estimate
	// failure occurred.
	OutcomeOperationalError Outcome = "operational_error"
)

// Precedence lists every outcome from highest to lowest precedence, matching
// design.md section 10:
//
//	operational error (30) > compilation failure (20)
//	  > policy-disallowed difference (10) > differences_allowed (0) > clean (0)
var Precedence = []Outcome{
	OutcomeOperationalError,
	OutcomeCompilationFailure,
	OutcomePolicyDisallowedDifference,
	OutcomeDifferencesAllowed,
	OutcomeClean,
}

// rank maps each outcome to its position in Precedence; lower rank is more
// severe/higher precedence.
var rank = func() map[Outcome]int {
	m := make(map[Outcome]int, len(Precedence))
	for i, o := range Precedence {
		m[o] = i
	}
	return m
}()

// codeForOutcome is the fixed outcome-to-exit-code mapping.
var codeForOutcome = map[Outcome]Code{
	OutcomeClean:                      Success,
	OutcomeDifferencesAllowed:         Success,
	OutcomePolicyDisallowedDifference: PolicyDisallowedDifference,
	OutcomeCompilationFailure:         CompilationFailure,
	OutcomeOperationalError:           OperationalError,
}

// ForOutcome returns the stable exit code for a given outcome. An unknown
// outcome is treated as an operational error: PIACE must never silently
// report success for a classification it does not recognize.
func ForOutcome(o Outcome) Code {
	if c, ok := codeForOutcome[o]; ok {
		return c
	}
	return OperationalError
}

// Reduce returns whichever of a and b has higher precedence per Precedence.
// An unrecognized outcome is treated as OutcomeOperationalError, the most
// severe class, so that an unknown classification is never silently
// downgraded to success.
func Reduce(a, b Outcome) Outcome {
	ra, aok := rank[a]
	rb, bok := rank[b]
	if !aok {
		return a
	}
	if !bok {
		return b
	}
	if ra <= rb {
		return a
	}
	return b
}

// ReduceAll returns the highest-precedence outcome among outcomes. It
// returns OutcomeClean if outcomes is empty, since an empty target set has
// no reported difference or failure.
func ReduceAll(outcomes []Outcome) Outcome {
	result := OutcomeClean
	for i, o := range outcomes {
		if i == 0 {
			result = o
			continue
		}
		result = Reduce(result, o)
	}
	return result
}
