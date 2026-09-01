package model

import (
	"encoding/json"
	"testing"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/exitcode"
)

// TestResult_JSONRoundTrip verifies the shared result document (schema
// version, invocation metadata, per-target results, aggregate diff,
// outcome/exit code/reasons) marshals and unmarshals via encoding/json
// without field loss.
func TestResult_JSONRoundTrip(t *testing.T) {
	r := NewResult("dev", "2026-08-24T00:00:00Z")
	if r.SchemaVersion != ResultSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", r.SchemaVersion, ResultSchemaVersion)
	}

	r.Targets = []TargetResult{
		{
			Certname: "web-01.example.test",
			Outcome:  exitcode.OutcomeClean,
			Baseline: &SourceProvenance{
				Kind:              SourceKindFile,
				Certname:          "web-01.example.test",
				Environment:       "production",
				ProducerTimestamp: "2026-08-20T00:00:00Z",
			},
			Facts: &SourceProvenance{
				Kind:     SourceKindPuppetDB,
				Certname: "web-01.example.test",
			},
			Candidate: &CandidateProvenance{
				RequestedAPI: config.CatalogAPIv4,
				EffectiveAPI: config.CatalogAPIv4,
				Environment:  "feature-123",
				FactSource:   SourceKindPuppetDB,
			},
			NodeDiff: &NodeDiff{Certname: "web-01.example.test", HasDifference: false},
		},
		{
			Certname: "web-02.example.test",
			Outcome:  exitcode.OutcomePolicyDisallowedDifference,
			NodeDiff: &NodeDiff{Certname: "web-02.example.test", HasDifference: true},
			Diagnostics: []Diagnostic{
				{Severity: SeverityWarning, Operation: OperationRequestCandidate, Certname: "web-02.example.test", Message: "v3 trusted-fact warning"},
			},
		},
	}
	r.Aggregate = AggregateDiff{Groups: []AggregateGroup{
		{
			Key:            AggregateChangeKey{Kind: ChangeResourceAdded, Identity: &ResourceIdentity{Type: "Notify", Title: "new"}},
			Certnames:      []string{"web-02.example.test"},
			NodeChangeRefs: []NodeChangeRef{{Certname: "web-02.example.test", Index: 0}},
		},
	}}
	r.Finalize()
	r.Reasons = []string{"target web-02.example.test has a non-excluded difference and fail_on_diff is enabled"}

	if r.Outcome != exitcode.OutcomePolicyDisallowedDifference {
		t.Fatalf("Outcome = %q, want %q", r.Outcome, exitcode.OutcomePolicyDisallowedDifference)
	}
	if r.ExitCode != 10 {
		t.Fatalf("ExitCode = %d, want 10", r.ExitCode)
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.SchemaVersion != ResultSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", decoded.SchemaVersion, ResultSchemaVersion)
	}
	if decoded.ExitCode != 10 || decoded.Outcome != exitcode.OutcomePolicyDisallowedDifference {
		t.Errorf("Outcome/ExitCode = %q/%d, want %q/10", decoded.Outcome, decoded.ExitCode, exitcode.OutcomePolicyDisallowedDifference)
	}
	if len(decoded.Targets) != 2 {
		t.Fatalf("Targets = %+v", decoded.Targets)
	}
	if decoded.Targets[0].Candidate == nil || decoded.Targets[0].Candidate.RequestedAPI != config.CatalogAPIv4 {
		t.Errorf("Targets[0].Candidate = %+v", decoded.Targets[0].Candidate)
	}
	if decoded.Targets[1].Outcome != exitcode.OutcomePolicyDisallowedDifference {
		t.Errorf("Targets[1].Outcome = %q", decoded.Targets[1].Outcome)
	}
	if len(decoded.Targets[1].Diagnostics) != 1 {
		t.Errorf("Targets[1].Diagnostics = %+v", decoded.Targets[1].Diagnostics)
	}
	if len(decoded.Aggregate.Groups) != 1 {
		t.Errorf("Aggregate.Groups = %+v", decoded.Aggregate.Groups)
	}
	if len(decoded.Reasons) != 1 {
		t.Errorf("Reasons = %+v", decoded.Reasons)
	}
}

// TestResult_Finalize_OperationalErrorWins verifies Finalize applies the
// exitcode package's fixed precedence: operational error outranks every
// other target outcome.
func TestResult_Finalize_OperationalErrorWins(t *testing.T) {
	r := NewResult("dev", "2026-08-24T00:00:00Z")
	r.Targets = []TargetResult{
		{Certname: "a", Outcome: exitcode.OutcomeClean},
		{Certname: "b", Outcome: exitcode.OutcomeCompilationFailure},
		{Certname: "c", Outcome: exitcode.OutcomeOperationalError},
	}
	r.Finalize()
	if r.Outcome != exitcode.OutcomeOperationalError {
		t.Errorf("Outcome = %q, want %q", r.Outcome, exitcode.OutcomeOperationalError)
	}
	if r.ExitCode != 30 {
		t.Errorf("ExitCode = %d, want 30", r.ExitCode)
	}
}

// TestResult_Finalize_NoTargetsIsClean verifies an empty target set
// finalizes to a clean, exit-0 outcome.
func TestResult_Finalize_NoTargetsIsClean(t *testing.T) {
	r := NewResult("dev", "2026-08-24T00:00:00Z")
	r.Finalize()
	if r.Outcome != exitcode.OutcomeClean || r.ExitCode != 0 {
		t.Errorf("Outcome/ExitCode = %q/%d, want %q/0", r.Outcome, r.ExitCode, exitcode.OutcomeClean)
	}
}
