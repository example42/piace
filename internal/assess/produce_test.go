package assess

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/example42/piace/internal/inference"
	"github.com/example42/piace/internal/model"
)

// fakeService returns canned responses in order, recording every request.
type fakeService struct {
	replies []string
	errs    []error
	calls   []inference.Request
}

func (f *fakeService) Complete(_ context.Context, req inference.Request) ([]byte, error) {
	f.calls = append(f.calls, req)
	i := len(f.calls) - 1
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i < len(f.replies) {
		return []byte(f.replies[i]), nil
	}
	return nil, errors.New("fake service: no reply configured")
}

func goodReply() string {
	return `{"run":{"risk":"medium","summary":"ok","review_focus":[]},
	         "groups":[{"id":"g001","risk":"low","rationale":"fine","review_focus":[]},
	                   {"id":"g002","risk":"low","rationale":"fine","review_focus":[]},
	                   {"id":"g003","risk":"low","rationale":"fine","review_focus":[]}]}`
}

func testMeta() Meta {
	return Meta{
		GeneratedAt:          "2026-08-29T00:00:00Z",
		ModelID:              "test-model",
		EndpointAuthority:    "api.example.com",
		SourceReportChecksum: "sha256:abc",
	}
}

func TestProduceReturnsAnAssessmentAndCallsTheServiceOnce(t *testing.T) {
	f := &fakeService{replies: []string{goodReply()}}
	a, diags := Produce(context.Background(), f, assessableResult(), ChangeContext{}, testConfig(), testMeta())

	if len(f.calls) != 1 {
		t.Errorf("service calls = %d, want 1", len(f.calls))
	}
	if hasError(diags) {
		t.Errorf("diagnostics = %+v", diags)
	}
	if a.Run.Risk != RiskMedium || len(a.Groups) != 3 {
		t.Errorf("assessment = %+v", a)
	}
	if a.ModelID != "test-model" || a.EndpointAuthority != "api.example.com" || a.SourceReportChecksum != "sha256:abc" {
		t.Errorf("metadata not stamped: %+v", a)
	}
	if a.AISchemaVersion != AISchemaVersion {
		t.Errorf("AISchemaVersion = %d", a.AISchemaVersion)
	}
	if a.GroupsTotal != 3 || a.GroupsAssessed != 3 || a.GroupsTruncated {
		t.Errorf("group accounting = %d/%d truncated=%v", a.GroupsAssessed, a.GroupsTotal, a.GroupsTruncated)
	}
}

// Slice 8.3's core: an unreachable service still produces a complete
// artifact, with every group recorded as unknown rather than missing.
func TestProduceStillProducesAnArtifactWhenTheServiceFails(t *testing.T) {
	f := &fakeService{errs: []error{errors.New("api.example.com returned status 500")}}
	a, diags := Produce(context.Background(), f, assessableResult(), ChangeContext{}, testConfig(), testMeta())

	if !hasError(diags) {
		t.Error("a failed inference request produced no error diagnostic")
	}
	if a.Run.Risk != RiskUnknown {
		t.Errorf("Run.Risk = %q, want unknown", a.Run.Risk)
	}
	if len(a.Groups) != 3 {
		t.Fatalf("Groups = %d, want every planned group accounted for", len(a.Groups))
	}
	for _, g := range a.Groups {
		if g.Risk != RiskUnknown {
			t.Errorf("group %s = %q, want unknown", g.ID, g.Risk)
		}
	}
	if a.ModelID == "" || a.SourceReportChecksum == "" {
		t.Error("metadata is missing from a degraded assessment")
	}
	if len(f.calls) != 1 {
		t.Errorf("service calls = %d; a transport failure is not retried", len(f.calls))
	}
}

// Slice 4.5: exactly one retry, carrying the validation error.
func TestProduceRetriesOnceOnAnUnusableResponse(t *testing.T) {
	f := &fakeService{replies: []string{"I'm sorry, I can't help with that.", goodReply()}}
	a, diags := Produce(context.Background(), f, assessableResult(), ChangeContext{}, testConfig(), testMeta())

	if len(f.calls) != 2 {
		t.Fatalf("service calls = %d, want 2", len(f.calls))
	}
	retry := f.calls[1]
	last := retry.Messages[len(retry.Messages)-1]
	if !strings.Contains(strings.ToLower(last.Content), "json") {
		t.Errorf("the retry does not carry the validation error: %q", last.Content)
	}
	if a.Run.Risk != RiskMedium {
		t.Errorf("Run.Risk = %q, want the retry's answer", a.Run.Risk)
	}
	if hasError(diags) {
		t.Errorf("a successful retry left an error diagnostic: %+v", diags)
	}
}

func TestProduceGivesUpAfterOneRetry(t *testing.T) {
	f := &fakeService{replies: []string{"nope", "still nope"}}
	a, diags := Produce(context.Background(), f, assessableResult(), ChangeContext{}, testConfig(), testMeta())

	if len(f.calls) != 2 {
		t.Errorf("service calls = %d, want 2 — no backoff ladder", len(f.calls))
	}
	if !hasError(diags) {
		t.Error("giving up produced no error diagnostic")
	}
	if a.Run.Risk != RiskUnknown || len(a.Groups) != 3 {
		t.Errorf("assessment = %+v", a)
	}
}

// Slice 8.5: a report built on failed retrievals is assessed, and says
// its input was partial.
func TestProduceRecordsThatItsInputWasPartial(t *testing.T) {
	r := assessableResult()
	r.Diagnostics = append(r.Diagnostics, model.Diagnostic{
		Severity: model.SeverityError, Operation: model.OperationLoadBaseline,
		Message: "no baseline catalog stored for this certname",
	})
	r.Reduce()

	f := &fakeService{replies: []string{goodReply()}}
	a, _ := Produce(context.Background(), f, r, ChangeContext{}, testConfig(), testMeta())
	if !a.InputPartial {
		t.Error("InputPartial is false for a report with a retrieval failure")
	}
}

// Slice 3.2 end to end: truncation is carried into the artifact.
func TestProduceCarriesTruncationIntoTheArtifact(t *testing.T) {
	cfg := testConfig()
	cfg.MaxGroups = 1
	f := &fakeService{replies: []string{`{"run":{"risk":"low","summary":"","review_focus":[]},
	    "groups":[{"id":"g001","risk":"low","rationale":"","review_focus":[]}]}`}}

	a, _ := Produce(context.Background(), f, assessableResult(), ChangeContext{}, cfg, testMeta())
	if !a.GroupsTruncated || a.GroupsTotal != 3 || a.GroupsAssessed != 1 {
		t.Errorf("group accounting = %d/%d truncated=%v", a.GroupsAssessed, a.GroupsTotal, a.GroupsTruncated)
	}
}

// A warning does not make the source document partial. model.Result emits
// warnings on complete runs — a v3 compatibility notice, a directory
// content source — and an assessment that called every such run's input
// incomplete would tell the reader of a successful comparison the
// opposite of the truth.
func TestOnlyAnErrorDiagnosticMakesTheInputPartial(t *testing.T) {
	warn := model.Diagnostic{Severity: model.SeverityWarning, Operation: model.OperationRequestCandidate, Message: "v3 trusted-fact warning"}
	fail := model.Diagnostic{Severity: model.SeverityError, Operation: model.OperationLoadBaseline, Message: "baseline not found"}

	cases := []struct {
		name string
		with func(*model.Result)
		want bool
	}{
		{"no diagnostics", func(*model.Result) {}, false},
		{"a run warning", func(r *model.Result) { r.Diagnostics = append(r.Diagnostics, warn) }, false},
		{"a target warning", func(r *model.Result) { r.Targets[0].Diagnostics = append(r.Targets[0].Diagnostics, warn) }, false},
		{"a run error", func(r *model.Result) { r.Diagnostics = append(r.Diagnostics, fail) }, true},
		{"a target error", func(r *model.Result) { r.Targets[0].Diagnostics = append(r.Targets[0].Diagnostics, fail) }, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := assessableResult()
			tc.with(&r)
			f := &fakeService{replies: []string{goodReply()}}
			a, _ := Produce(context.Background(), f, r, ChangeContext{}, testConfig(), testMeta())
			if a.InputPartial != tc.want {
				t.Errorf("InputPartial = %v, want %v", a.InputPartial, tc.want)
			}
		})
	}
}

// Slice 2.5, the half the opt-out test could not state at the request
// seam: pseudonyms exist only in the request body, so the two runsdiffer in
// what left the process and not in what they wrote.
func TestPseudonymizationOptOutProducesAnIdenticalArtifact(t *testing.T) {
	alias := newPseudonyms(assessableResult(), true).Of(realCertname)
	replyNaming := func(node string) string {
		return `{"run":{"risk":"medium","summary":"` + node + ` changes first","review_focus":["` + node + `"]},
		         "groups":[{"id":"g001","risk":"low","rationale":"` + node + ` only","review_focus":[]},
		                   {"id":"g002","risk":"low","rationale":"fine","review_focus":[]},
		                   {"id":"g003","risk":"low","rationale":"fine","review_focus":[]}]}`
	}

	cfg := testConfig()
	on := &fakeService{replies: []string{replyNaming(alias)}}
	withPseudonyms, _ := Produce(context.Background(), on, assessableResult(), ChangeContext{}, cfg, testMeta())

	cfg.Pseudonymize = false
	off := &fakeService{replies: []string{replyNaming(realCertname)}}
	withRealNames, _ := Produce(context.Background(), off, assessableResult(), ChangeContext{}, cfg, testMeta())

	// What left the process differed.
	if sent := on.calls[0].Messages[1].Content; strings.Contains(sent, realCertname) {
		t.Error("the pseudonymized request carried a real certname")
	}
	if sent := off.calls[0].Messages[1].Content; !strings.Contains(sent, realCertname) {
		t.Error("the opt-out request did not carry the real certname")
	}

	// What was written did not.
	if !reflect.DeepEqual(withPseudonyms, withRealNames) {
		t.Errorf("the artifact differs with pseudonymization off:\n on: %+v\noff: %+v", withPseudonyms, withRealNames)
	}
	if !strings.Contains(withPseudonyms.Run.Summary, realCertname) {
		t.Errorf("the artifact does not carry the real certname: %q", withPseudonyms.Run.Summary)
	}
}
