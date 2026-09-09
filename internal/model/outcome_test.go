package model

import (
	"testing"

	"github.com/example42/piace/internal/exitcode"
)

// TestOutcomeForDiagnostic_OperationMapping locks the error taxonomy,
// including the split diagnostic.go documents: request_candidate is a
// compilation failure while its transport sibling stays operational.
func TestOutcomeForDiagnostic_OperationMapping(t *testing.T) {
	cases := []struct {
		operation DiagnosticOperation
		want      exitcode.Outcome
	}{
		{OperationConfigure, exitcode.OutcomeOperationalError},
		{OperationLoadFacts, exitcode.OutcomeOperationalError},
		{OperationLoadBaseline, exitcode.OutcomeOperationalError},
		{OperationNormalize, exitcode.OutcomeOperationalError},
		{OperationVerifyContent, exitcode.OutcomeOperationalError},
		{OperationEstimateImpact, exitcode.OutcomeOperationalError},
		{OperationSnapshot, exitcode.OutcomeOperationalError},
		{OperationRequestCandidateTransport, exitcode.OutcomeOperationalError},
		{OperationRequestCandidate, exitcode.OutcomeCompilationFailure},
		{DiagnosticOperation("something_new"), exitcode.OutcomeOperationalError},
	}
	for _, tc := range cases {
		got, contributes := OutcomeForDiagnostic(Diagnostic{Severity: SeverityError, Operation: tc.operation})
		if !contributes {
			t.Errorf("%s: contributes = false, want true", tc.operation)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: outcome = %q, want %q", tc.operation, got, tc.want)
		}
	}
}

// TestOutcomeForDiagnostic_WarningContributesNothing locks the rule that
// a reported v3 compatibility warning alone does not change exit status.
func TestOutcomeForDiagnostic_WarningContributesNothing(t *testing.T) {
	_, contributes := OutcomeForDiagnostic(Diagnostic{
		Severity:  SeverityWarning,
		Operation: OperationRequestCandidate,
		Message:   V3TrustedFactWarning,
	})
	if contributes {
		t.Fatal("a warning-severity diagnostic contributed an outcome")
	}
}

func TestTargetResult_ClassifyOutcome(t *testing.T) {
	cases := []struct {
		name   string
		target TargetResult
		want   exitcode.Outcome
	}{
		{
			name: "no difference is clean",
			target: TargetResult{
				NodeDiff: &NodeDiff{HasDifference: false},
				Config:   &ConfigProvenance{FailOnDiff: true},
			},
			want: exitcode.OutcomeClean,
		},
		{
			name: "difference with fail_on_diff is policy-disallowed",
			target: TargetResult{
				NodeDiff: &NodeDiff{HasDifference: true},
				Config:   &ConfigProvenance{FailOnDiff: true},
			},
			want: exitcode.OutcomePolicyDisallowedDifference,
		},
		{
			name: "difference without fail_on_diff is allowed",
			target: TargetResult{
				NodeDiff: &NodeDiff{HasDifference: true},
				Config:   &ConfigProvenance{FailOnDiff: false},
			},
			want: exitcode.OutcomeDifferencesAllowed,
		},
		{
			name: "a warning never masks a clean comparison",
			target: TargetResult{
				NodeDiff:    &NodeDiff{HasDifference: false},
				Config:      &ConfigProvenance{},
				Diagnostics: []Diagnostic{{Severity: SeverityWarning, Operation: OperationRequestCandidate}},
			},
			want: exitcode.OutcomeClean,
		},
		{
			name: "a compilation failure outranks a difference verdict",
			target: TargetResult{
				NodeDiff:    &NodeDiff{HasDifference: true},
				Config:      &ConfigProvenance{FailOnDiff: true},
				Diagnostics: []Diagnostic{{Severity: SeverityError, Operation: OperationRequestCandidate}},
			},
			want: exitcode.OutcomeCompilationFailure,
		},
		{
			name: "the most severe diagnostic wins among several",
			target: TargetResult{
				NodeDiff: &NodeDiff{},
				Config:   &ConfigProvenance{},
				Diagnostics: []Diagnostic{
					{Severity: SeverityError, Operation: OperationRequestCandidate},
					{Severity: SeverityError, Operation: OperationLoadBaseline},
				},
			},
			want: exitcode.OutcomeOperationalError,
		},
		{
			name:   "an uncompared target is never clean",
			target: TargetResult{Config: &ConfigProvenance{}},
			want:   exitcode.OutcomeOperationalError,
		},
		{
			// A File whose content could not be verified may have
			// changed, so it is reported as a difference and the
			// target's policy decides the rest.
			name: "indeterminate file content is a difference",
			target: TargetResult{
				Config: &ConfigProvenance{},
				NodeDiff: &NodeDiff{
					HasDifference: true,
					ResourceChanges: []ResourceChange{{
						Kind:        ChangeParameterChanged,
						Identity:    ResourceIdentity{Type: "File", Title: "/etc/motd"},
						Parameter:   "content",
						FileContent: &FileContentEvidence{State: FileContentIndeterminate},
					}},
				},
			},
			want: exitcode.OutcomeDifferencesAllowed,
		},
		{
			name: "indeterminate file content under fail_on_diff",
			target: TargetResult{
				Config: &ConfigProvenance{FailOnDiff: true},
				NodeDiff: &NodeDiff{
					HasDifference: true,
					ResourceChanges: []ResourceChange{{
						Kind:        ChangeParameterChanged,
						Identity:    ResourceIdentity{Type: "File", Title: "/etc/motd"},
						Parameter:   "content",
						FileContent: &FileContentEvidence{State: FileContentIndeterminate},
					}},
				},
			},
			want: exitcode.OutcomePolicyDisallowedDifference,
		},
		{
			// The invariant the old mapping existed to protect: a
			// differ that produced indeterminate evidence for a change
			// it did not report has contradicted itself, and that is
			// not a comparison result.
			name: "indeterminate file content can never be clean",
			target: TargetResult{
				Config: &ConfigProvenance{},
				NodeDiff: &NodeDiff{
					HasDifference: false,
					ResourceChanges: []ResourceChange{{
						Kind:        ChangeParameterChanged,
						Identity:    ResourceIdentity{Type: "File", Title: "/etc/motd"},
						Parameter:   "content",
						FileContent: &FileContentEvidence{State: FileContentIndeterminate},
					}},
				},
			},
			want: exitcode.OutcomeOperationalError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.target
			target.ClassifyOutcome()
			if target.Outcome != tc.want {
				t.Errorf("Outcome = %q, want %q", target.Outcome, tc.want)
			}
		})
	}
}

// TestResult_Reduce_RunDiagnosticOutranksCleanTargets verifies that an
// enabled impact estimate's failure contributes an operational outcome
// after all other targets finish, even when every target compared
// cleanly.
func TestResult_Reduce_RunDiagnosticOutranksCleanTargets(t *testing.T) {
	r := NewResult("dev", "2026-08-25T00:00:00Z")
	r.Targets = []TargetResult{{
		Certname: "web-01.example.test",
		NodeDiff: &NodeDiff{Certname: "web-01.example.test"},
		Config:   &ConfigProvenance{},
	}}
	r.Diagnostics = []Diagnostic{{
		Severity:  SeverityError,
		Operation: OperationEstimateImpact,
		Message:   "impact query timed out",
	}}
	r.Reduce()

	if r.Targets[0].Outcome != exitcode.OutcomeClean {
		t.Errorf("target outcome = %q, want clean", r.Targets[0].Outcome)
	}
	if r.Outcome != exitcode.OutcomeOperationalError || r.ExitCode != 30 {
		t.Fatalf("Outcome/ExitCode = %q/%d, want operational_error/30", r.Outcome, r.ExitCode)
	}
	if len(r.Reasons) != 1 || r.Reasons[0] != "operational_error (estimate_impact): impact query timed out" {
		t.Errorf("Reasons = %#v", r.Reasons)
	}
}

// TestResult_Reduce_ReasonsAreOrderedAndIndependentOfTargetOrder locks
// the ordered reason list: most severe first, then certname, regardless
// of the order targets were appended in.
func TestResult_Reduce_ReasonsAreOrderedAndIndependentOfTargetOrder(t *testing.T) {
	build := func(order []TargetResult) []string {
		r := NewResult("dev", "2026-08-25T00:00:00Z")
		r.Targets = order
		r.Reduce()
		return r.Reasons
	}

	allowed := TargetResult{
		Certname: "a.example.test",
		NodeDiff: &NodeDiff{HasDifference: true},
		Config:   &ConfigProvenance{FailOnDiff: false},
	}
	disallowed := TargetResult{
		Certname: "b.example.test",
		NodeDiff: &NodeDiff{HasDifference: true},
		Config:   &ConfigProvenance{FailOnDiff: true},
	}
	failed := TargetResult{
		Certname:    "c.example.test",
		Config:      &ConfigProvenance{},
		Diagnostics: []Diagnostic{{Severity: SeverityError, Operation: OperationLoadBaseline, Message: "baseline not found"}},
	}

	want := []string{
		"target c.example.test: operational_error: baseline not found",
		"target b.example.test: non-excluded difference with fail_on_diff enabled",
		"target a.example.test: non-excluded difference allowed by policy",
	}

	for _, order := range [][]TargetResult{
		{allowed, disallowed, failed},
		{failed, allowed, disallowed},
	} {
		got := build(order)
		if len(got) != len(want) {
			t.Fatalf("Reasons = %#v, want %#v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Reasons[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	}
}

// TestResult_Reduce_CleanRunStillReportsAReason: outcome and reason are
// reported in every case, success included.
func TestResult_Reduce_CleanRunStillReportsAReason(t *testing.T) {
	r := NewResult("dev", "2026-08-25T00:00:00Z")
	r.Targets = []TargetResult{{Certname: "a", NodeDiff: &NodeDiff{}, Config: &ConfigProvenance{}}}
	r.Reduce()
	if r.Outcome != exitcode.OutcomeClean {
		t.Fatalf("Outcome = %q", r.Outcome)
	}
	if len(r.Reasons) != 1 {
		t.Fatalf("Reasons = %#v, want exactly one", r.Reasons)
	}
}
