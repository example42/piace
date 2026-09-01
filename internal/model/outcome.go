package model

import (
	"fmt"
	"sort"

	"github.com/example42/piace/internal/exitcode"
)

// diagnosticOutcome maps a DiagnosticOperation to the outcome class an
// error-severity diagnostic of that operation contributes.
//
// Every operation except OperationRequestCandidate is an operational
// error. OperationRequestCandidate is a compilation failure: a compiler
// request is rejected or fails, candidate identity or environment does
// not match, or v4 trusted-fact requirements are unmet. Its
// transport-level sibling OperationRequestCandidateTransport is
// deliberately separate and stays operational; see that constant's doc
// comment in diagnostic.go.
var diagnosticOutcome = map[DiagnosticOperation]exitcode.Outcome{
	OperationConfigure:                 exitcode.OutcomeOperationalError,
	OperationLoadFacts:                 exitcode.OutcomeOperationalError,
	OperationLoadBaseline:              exitcode.OutcomeOperationalError,
	OperationNormalize:                 exitcode.OutcomeOperationalError,
	OperationVerifyContent:             exitcode.OutcomeOperationalError,
	OperationEstimateImpact:            exitcode.OutcomeOperationalError,
	OperationSnapshot:                  exitcode.OutcomeOperationalError,
	OperationRequestCandidateTransport: exitcode.OutcomeOperationalError,
	OperationRequestCandidate:          exitcode.OutcomeCompilationFailure,
}

// OutcomeForDiagnostic returns the outcome class d contributes, and
// whether it contributes one at all.
//
// A SeverityWarning diagnostic contributes nothing: a reported v3
// compatibility warning alone does not change exit status, it makes
// trust semantics explicitly reviewable. An error-severity diagnostic
// whose operation is not in the table above contributes an operational
// error rather than nothing, mirroring exitcode.ForOutcome's rule that
// an unrecognized classification is never silently downgraded to
// success.
func OutcomeForDiagnostic(d Diagnostic) (exitcode.Outcome, bool) {
	if d.Severity != SeverityError {
		return "", false
	}
	if outcome, ok := diagnosticOutcome[d.Operation]; ok {
		return outcome, true
	}
	return exitcode.OutcomeOperationalError, true
}

// ClassifyOutcome sets t.Outcome from t's own diagnostics, node diff,
// and resolved policy. It is the single place a target's outcome class
// is decided, so text, JSON, HTML, and the process exit code cannot
// disagree about one target.
//
// The order below is the precedence applied within one target: any error
// diagnostic outranks any difference verdict, and the most severe error
// diagnostic wins among several.
//
// Two cases are non-clean without an error diagnostic of their own.
// PIACE must never report a clean outcome when one or more targets have
// an unreported retrieval, compilation, or normalization failure:
//
//   - A nil NodeDiff means this target was never compared. Reaching here
//     with no diagnostic would mean the pipeline abandoned a target
//     silently; that is reported as an operational error rather than
//     folded into a clean run.
//   - A surviving FileContentIndeterminate classification means content
//     evidence was never established. internal/filecontent always pairs
//     that state with an error-severity verify_content diagnostic today,
//     so this branch is redundant with the diagnostic scan above. But it
//     is the invariant everything else depends on, that an indeterminate
//     comparison cannot silently collapse into an unchanged file, and it
//     is enforced here rather than left resting on a
//     cross-package pairing no single package's tests cover.
func (t *TargetResult) ClassifyOutcome() {
	worst := exitcode.Outcome("")
	for _, d := range t.Diagnostics {
		outcome, contributes := OutcomeForDiagnostic(d)
		if !contributes {
			continue
		}
		if worst == "" {
			worst = outcome
			continue
		}
		worst = exitcode.Reduce(worst, outcome)
	}
	if worst != "" {
		t.Outcome = worst
		return
	}

	if t.NodeDiff == nil {
		t.Outcome = exitcode.OutcomeOperationalError
		return
	}
	if hasIndeterminateContent(*t.NodeDiff) {
		t.Outcome = exitcode.OutcomeOperationalError
		return
	}

	if !t.NodeDiff.HasDifference {
		t.Outcome = exitcode.OutcomeClean
		return
	}
	if t.Config != nil && t.Config.FailOnDiff {
		t.Outcome = exitcode.OutcomePolicyDisallowedDifference
		return
	}
	t.Outcome = exitcode.OutcomeDifferencesAllowed
}

// hasIndeterminateContent reports whether any surviving (non-excluded)
// change in nd carries File-content evidence that never established a
// comparison.
func hasIndeterminateContent(nd NodeDiff) bool {
	for _, change := range nd.ResourceChanges {
		if change.FileContent != nil && change.FileContent.State == FileContentIndeterminate {
			return true
		}
	}
	return false
}

// Reduce classifies every target, folds in run-level diagnostics, and
// populates Outcome, ExitCode, and the ordered Reasons list. It is the
// whole of internal/report's "apply outcome precedence" step and the only call a
// caller needs after populating Targets, Diagnostics, Aggregate, and
// ImpactEstimates.
func (r *Result) Reduce() {
	for i := range r.Targets {
		r.Targets[i].ClassifyOutcome()
	}
	r.Finalize()
	r.Reasons = r.buildReasons()
}

// reason is one entry of the ordered reason list, carried with its
// precedence rank and certname so the list can be sorted deterministically
// rather than in whatever order the pipeline happened to append.
type reason struct {
	rank     int
	certname string
	text     string
}

// buildReasons produces the ordered reason list explaining r.Outcome. It
// always returns at least one entry: a clean run still has to be able to
// say why it is clean.
//
// Entries are sorted by outcome precedence, most severe first, then by
// certname, then by text. Run-level entries carry an empty certname and
// therefore sort ahead of target entries within the same rank. Nothing
// in the ordering depends on target-file order or on map iteration, so
// the list is byte-identical across runs with identical inputs.
func (r *Result) buildReasons() []string {
	rank := func(o exitcode.Outcome) int {
		for i, p := range exitcode.Precedence {
			if p == o {
				return i
			}
		}
		return 0
	}

	var reasons []reason
	for _, d := range r.Diagnostics {
		outcome, contributes := OutcomeForDiagnostic(d)
		if !contributes {
			continue
		}
		reasons = append(reasons, reason{
			rank: rank(outcome),
			text: fmt.Sprintf("%s (%s): %s", outcome, d.Operation, d.Message),
		})
	}

	for _, t := range r.Targets {
		switch t.Outcome {
		case exitcode.OutcomeOperationalError, exitcode.OutcomeCompilationFailure:
			reasons = append(reasons, reason{
				rank:     rank(t.Outcome),
				certname: t.Certname,
				text:     fmt.Sprintf("target %s: %s: %s", t.Certname, t.Outcome, outcomeMessage(t)),
			})
		case exitcode.OutcomePolicyDisallowedDifference:
			reasons = append(reasons, reason{
				rank:     rank(t.Outcome),
				certname: t.Certname,
				text:     fmt.Sprintf("target %s: non-excluded difference with fail_on_diff enabled", t.Certname),
			})
		case exitcode.OutcomeDifferencesAllowed:
			reasons = append(reasons, reason{
				rank:     rank(t.Outcome),
				certname: t.Certname,
				text:     fmt.Sprintf("target %s: non-excluded difference allowed by policy", t.Certname),
			})
		}
	}

	sort.SliceStable(reasons, func(i, j int) bool {
		if reasons[i].rank != reasons[j].rank {
			return reasons[i].rank < reasons[j].rank
		}
		if reasons[i].certname != reasons[j].certname {
			return reasons[i].certname < reasons[j].certname
		}
		return reasons[i].text < reasons[j].text
	})

	out := make([]string, 0, len(reasons)+1)
	for _, entry := range reasons {
		out = append(out, entry.text)
	}
	if len(out) == 0 {
		return []string{fmt.Sprintf("%d target(s) compared with no non-excluded differences", len(r.Targets))}
	}
	return out
}

// outcomeMessage returns the message explaining t's classified outcome.
//
// It selects the first diagnostic whose own classification *matches*
// t.Outcome rather than simply the first error-severity diagnostic,
// because ClassifyOutcome picks the most severe diagnostic, not the
// earliest. With one target carrying both a compilation failure and an
// operational error, quoting the earliest would name a failure that is
// not the one the outcome reports. It falls back to any error message,
// then to a fixed explanation for the two cases ClassifyOutcome treats as
// non-clean without a diagnostic of their own.
//
// Only the message is quoted: a diagnostic never carries raw response
// bodies, credentials, or unredacted values (see Diagnostic's doc
// comment), so reproducing it in a reason line discloses nothing a report
// does not already show.
func outcomeMessage(t TargetResult) string {
	fallback := ""
	for _, d := range t.Diagnostics {
		outcome, contributes := OutcomeForDiagnostic(d)
		if !contributes {
			continue
		}
		if outcome == t.Outcome {
			return d.Message
		}
		if fallback == "" {
			fallback = d.Message
		}
	}
	if fallback != "" {
		return fallback
	}
	if t.NodeDiff == nil {
		return "no node diff was produced for this target"
	}
	return "managed File content could not be compared (content_indeterminate)"
}
