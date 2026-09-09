package report

import (
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

// validResult is a small but complete comparison: two targets, one with
// a difference, an aggregate group referring to it, and a reduced
// outcome. Each test below breaks exactly one thing about it.
func validResult() model.Result {
	diff := model.NodeDiff{
		Certname: "web-01.example.test",
		ResourceChanges: []model.ResourceChange{{
			Kind:      model.ChangeParameterChanged,
			Identity:  model.ResourceIdentity{Type: "Service", Title: "nginx"},
			Parameter: "ensure",
			Before:    "running",
			After:     "stopped",
		}},
		HasDifference: true,
	}
	clean := model.NodeDiff{Certname: "web-02.example.test"}

	r := model.NewResult("test", "2026-09-09T00:00:00Z")
	r.Targets = []model.TargetResult{
		{Certname: "web-01.example.test", Outcome: exitcode.OutcomeDifferencesAllowed, NodeDiff: &diff},
		{Certname: "web-02.example.test", Outcome: exitcode.OutcomeClean, NodeDiff: &clean},
	}
	r.Aggregate = model.AggregateDiff{Groups: []model.AggregateGroup{{
		Key: model.AggregateChangeKey{
			Kind:      model.ChangeParameterChanged,
			Identity:  &model.ResourceIdentity{Type: "Service", Title: "nginx"},
			Parameter: "ensure",
		},
		Before:         "running",
		After:          "stopped",
		Certnames:      []string{"web-01.example.test"},
		NodeChangeRefs: []model.NodeChangeRef{{Certname: "web-01.example.test", Index: 0}},
	}}}
	r.Finalize()
	return r
}

func TestValidate_AcceptsACompleteComparison(t *testing.T) {
	if err := Validate(validResult()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestValidate_AcceptsAPartialComparison: a target whose compilation
// failed carries no node diff, and that is a complete report of a
// partial comparison rather than a broken document.
func TestValidate_AcceptsAPartialComparison(t *testing.T) {
	r := validResult()
	r.Targets[1] = model.TargetResult{
		Certname: "web-02.example.test",
		Outcome:  exitcode.OutcomeCompilationFailure,
		Diagnostics: []model.Diagnostic{{
			Severity:  model.SeverityError,
			Operation: model.OperationRequestCandidate,
			Certname:  "web-02.example.test",
			Message:   "the compiler rejected the candidate catalog request",
		}},
	}
	r.Finalize()
	if err := Validate(r); err != nil {
		t.Fatalf("Validate rejected a legitimate partial comparison: %v", err)
	}
}

func TestValidate_RejectsInconsistentDocuments(t *testing.T) {
	cases := map[string]struct {
		mutate func(*model.Result)
		want   string
	}{
		"a document with nothing in it": {
			mutate: func(r *model.Result) { *r = model.Result{SchemaVersion: model.ResultSchemaVersion} },
			want:   "records no targets",
		},
		"a missing invocation timestamp": {
			mutate: func(r *model.Result) { r.Invocation.TimestampUTC = "" },
			want:   "timestamp_utc",
		},
		"a repeated target": {
			mutate: func(r *model.Result) { r.Targets[1].Certname = r.Targets[0].Certname },
			want:   "duplicate certname",
		},
		"a node diff belonging to another target": {
			mutate: func(r *model.Result) { r.Targets[0].NodeDiff.Certname = "someone-else.example.test" },
			want:   "does not match the target",
		},
		"an outcome contradicting its own targets": {
			mutate: func(r *model.Result) {
				r.Outcome = exitcode.OutcomeClean
				r.ExitCode = int(exitcode.ForOutcome(exitcode.OutcomeClean))
			},
			want: "reduce to",
		},
		"an exit code contradicting its outcome": {
			mutate: func(r *model.Result) { r.ExitCode = 30 },
			want:   "exit_code",
		},
		"a clean target carrying differences": {
			mutate: func(r *model.Result) { r.Targets[1].NodeDiff.HasDifference = true },
			want:   "has_difference",
		},
		"a failure outcome nothing recorded": {
			mutate: func(r *model.Result) {
				r.Targets[1] = model.TargetResult{Certname: "web-02.example.test", Outcome: exitcode.OutcomeOperationalError}
				r.Finalize()
			},
			want: "no error diagnostic",
		},
		"a parameter change with no parameter": {
			mutate: func(r *model.Result) { r.Targets[0].NodeDiff.ResourceChanges[0].Parameter = "" },
			want:   "carries no parameter name",
		},
		"an unknown change kind": {
			mutate: func(r *model.Result) { r.Targets[0].NodeDiff.ResourceChanges[0].Kind = "resource_rearranged" },
			want:   "is not a resource change",
		},
		"a group naming a target that is not here": {
			mutate: func(r *model.Result) {
				r.Aggregate.Groups[0].NodeChangeRefs[0].Certname = "ghost.example.test"
				r.Aggregate.Groups[0].Certnames = []string{"ghost.example.test"}
			},
			want: "does not contain",
		},
		"a group referring past the end of a node diff": {
			mutate: func(r *model.Result) { r.Aggregate.Groups[0].NodeChangeRefs[0].Index = 7 },
			want:   "out of range",
		},
		"a group listing a certname it does not reference": {
			mutate: func(r *model.Result) {
				r.Aggregate.Groups[0].Certnames = append(r.Aggregate.Groups[0].Certnames, "web-02.example.test")
			},
			want: "no matching change reference",
		},
		"an edge group keyed on a resource": {
			mutate: func(r *model.Result) { r.Aggregate.Groups[0].Key.Kind = model.ChangeEdgeAdded },
			want:   "must key on an edge",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := validResult()
			tc.mutate(&r)
			err := Validate(r)
			if err == nil {
				t.Fatal("accepted an inconsistent document")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the diagnostic does not mention %q:\n%v", tc.want, err)
			}
		})
	}
}

// TestValidate_RejectsUndisclosableEvidence covers the rule that a
// stored document is an input like any other: evidence redaction would
// have removed cannot enter a renderer, or an inference request, merely
// by arriving in a file.
func TestValidate_RejectsUndisclosableEvidence(t *testing.T) {
	sensitive := map[string]any{"__ptype": "Sensitive", "__pvalue": "s3cr3t"}

	cases := map[string]struct {
		mutate func(*model.Result)
		want   string
	}{
		"a forged wrapper in a target change": {
			mutate: func(r *model.Result) { r.Targets[0].NodeDiff.ResourceChanges[0].After = sensitive },
			want:   "Sensitive wrapper",
		},
		"a forged wrapper nested in a group projection": {
			mutate: func(r *model.Result) {
				r.Aggregate.Groups[0].After = map[string]any{"outer": []any{sensitive}}
			},
			want: "Sensitive wrapper",
		},
		"raw File content in a parameter change": {
			mutate: func(r *model.Result) {
				r.Targets[0].NodeDiff.ResourceChanges[0].Identity = model.ResourceIdentity{Type: "File", Title: "/etc/app.conf"}
				r.Targets[0].NodeDiff.ResourceChanges[0].Parameter = "content"
				r.Targets[0].NodeDiff.ResourceChanges[0].After = "the managed bytes"
			},
			want: "publishes the value of the File content parameter",
		},
		"raw File content in an added resource's parameters": {
			mutate: func(r *model.Result) {
				r.Targets[0].NodeDiff.ResourceChanges[0] = model.ResourceChange{
					Kind:     model.ChangeResourceAdded,
					Identity: model.ResourceIdentity{Type: "File", Title: "/etc/app.conf"},
					After:    map[string]any{"owner": "root", "source": "puppet:///modules/app/private"},
				}
				r.Aggregate = model.AggregateDiff{}
			},
			want: "publishes the value of the File source parameter",
		},
		"a redaction claim over a real digest": {
			mutate: func(r *model.Result) {
				r.Targets[0].NodeDiff.ResourceChanges[0].FileContent = &model.FileContentEvidence{
					State:        model.FileContentChanged,
					Redacted:     true,
					Algorithm:    "sha256",
					BeforeDigest: strings.Repeat("a", 64),
				}
			},
			want: "redacted",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := validResult()
			tc.mutate(&r)
			err := Validate(r)
			if err == nil {
				t.Fatal("accepted a document carrying evidence redaction would have removed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the diagnostic does not mention %q:\n%v", tc.want, err)
			}
		})
	}
}

// TestValidate_AcceptsRedactionMarkers is the counterpart: a redacted
// entry is present and marked, not absent, and must stay readable.
func TestValidate_AcceptsRedactionMarkers(t *testing.T) {
	r := validResult()
	r.Targets[0].NodeDiff.ResourceChanges[0].Identity = model.ResourceIdentity{Type: "File", Title: "/etc/app.conf"}
	r.Targets[0].NodeDiff.ResourceChanges[0].Parameter = "content"
	r.Targets[0].NodeDiff.ResourceChanges[0].Before = model.RedactedValue
	r.Targets[0].NodeDiff.ResourceChanges[0].After = model.RedactedValue
	r.Targets[0].NodeDiff.ResourceChanges[0].FileContent = &model.FileContentEvidence{
		State:        model.FileContentChanged,
		Redacted:     true,
		BeforeDigest: model.RedactedValue,
		AfterDigest:  model.RedactedValue,
		Before:       &model.FileSideEvidence{Source: model.FileContentEvidenceInline, Verified: true},
		After:        &model.FileSideEvidence{Source: model.FileContentEvidenceInline, Verified: true},
	}
	r.Aggregate = model.AggregateDiff{}
	if err := Validate(r); err != nil {
		t.Fatalf("Validate rejected properly redacted evidence: %v", err)
	}
}

func TestDecodeJSON_RejectsAnOversizedDocument(t *testing.T) {
	data := make([]byte, MaxDocumentBytes+1)
	if _, err := DecodeJSON(data); err == nil {
		t.Fatal("accepted a document past the size limit")
	} else if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %v, want it to name the limit", err)
	}
}

// TestDecodeJSON_ValidatesWhatItDecodes: the sparse document that
// motivated this check reaches no consumer.
func TestDecodeJSON_ValidatesWhatItDecodes(t *testing.T) {
	if _, err := DecodeJSON([]byte(`{"schema_version":2}`)); err == nil {
		t.Fatal("accepted a document describing no comparison")
	}
}
