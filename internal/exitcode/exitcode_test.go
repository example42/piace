package exitcode

import "testing"

// TestForOutcome_StableMapping locks the exact outcome-to-exit-code
// mapping. CI pipelines depend on these values; this test must fail
// loudly if any mapping is ever changed accidentally.
func TestForOutcome_StableMapping(t *testing.T) {
	cases := []struct {
		outcome Outcome
		want    Code
	}{
		{OutcomeClean, 0},
		{OutcomeDifferencesAllowed, 0},
		{OutcomePolicyDisallowedDifference, 10},
		{OutcomeCompilationFailure, 20},
		{OutcomeOperationalError, 30},
	}
	for _, c := range cases {
		if got := ForOutcome(c.outcome); got != c.want {
			t.Errorf("ForOutcome(%q) = %d, want %d", c.outcome, got, c.want)
		}
	}
}

// TestForOutcome_UnknownIsOperationalError ensures an unrecognized outcome
// never silently maps to success.
func TestForOutcome_UnknownIsOperationalError(t *testing.T) {
	if got := ForOutcome(Outcome("something_new")); got != OperationalError {
		t.Errorf("ForOutcome(unknown) = %d, want %d (OperationalError)", got, OperationalError)
	}
}

// TestReduce_Precedence checks every pairwise combination against the
// fixed precedence order:
//
//	operational error > compilation failure > policy-disallowed difference
//	  > differences_allowed > clean
func TestReduce_Precedence(t *testing.T) {
	ordered := []Outcome{
		OutcomeOperationalError,
		OutcomeCompilationFailure,
		OutcomePolicyDisallowedDifference,
		OutcomeDifferencesAllowed,
		OutcomeClean,
	}
	for i, higher := range ordered {
		for j, lower := range ordered {
			if i == j {
				continue
			}
			// The earlier entry in `ordered` always has higher (or equal,
			// for i<j meaning higher precedence) precedence.
			var want Outcome
			if i < j {
				want = higher
			} else {
				want = lower
			}
			if got := Reduce(higher, lower); got != want {
				t.Errorf("Reduce(%q, %q) = %q, want %q", higher, lower, got, want)
			}
			if got := Reduce(lower, higher); got != want {
				t.Errorf("Reduce(%q, %q) = %q, want %q", lower, higher, got, want)
			}
		}
	}
}

// TestReduceAll_EmptyIsClean ensures an empty target set (which cannot
// have any reported failure or difference) reduces to a clean outcome.
func TestReduceAll_EmptyIsClean(t *testing.T) {
	if got := ReduceAll(nil); got != OutcomeClean {
		t.Errorf("ReduceAll(nil) = %q, want %q", got, OutcomeClean)
	}
}

// TestReduceAll_MostSevereWins exercises ReduceAll across a mixed slice
// of outcomes.
func TestReduceAll_MostSevereWins(t *testing.T) {
	outcomes := []Outcome{
		OutcomeClean,
		OutcomeDifferencesAllowed,
		OutcomePolicyDisallowedDifference,
		OutcomeClean,
	}
	if got := ReduceAll(outcomes); got != OutcomePolicyDisallowedDifference {
		t.Errorf("ReduceAll(%v) = %q, want %q", outcomes, got, OutcomePolicyDisallowedDifference)
	}

	outcomes = append(outcomes, OutcomeOperationalError, OutcomeCompilationFailure)
	if got := ReduceAll(outcomes); got != OutcomeOperationalError {
		t.Errorf("ReduceAll(%v) = %q, want %q", outcomes, got, OutcomeOperationalError)
	}
}

// TestPrecedence_MatchesCodeSeverityOrder ensures the Precedence slice and
// the numeric exit codes never disagree about relative severity: a lower
// index in Precedence must never map to a lower-or-equal exit code than a
// later index (except for the two zero-exit-code outcomes, which are
// intentionally distinguished only by their position, not by exit code).
func TestPrecedence_MatchesCodeSeverityOrder(t *testing.T) {
	for i := 0; i < len(Precedence)-1; i++ {
		a, b := Precedence[i], Precedence[i+1]
		ca, cb := ForOutcome(a), ForOutcome(b)
		if ca < cb {
			t.Errorf("Precedence order violated: %q (exit %d) ranked above %q (exit %d)", a, ca, b, cb)
		}
	}
}
