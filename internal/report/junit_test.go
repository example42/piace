package report

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

func TestJUnitOutcomeMapping(t *testing.T) {
	r := model.NewResult("test", "2026-10-08T12:00:00Z")
	r.Targets = []model.TargetResult{
		{Certname: "clean", NodeDiff: &model.NodeDiff{}},
		{Certname: "allowed", NodeDiff: &model.NodeDiff{HasDifference: true}},
		{Certname: "policy", Config: &model.ConfigProvenance{FailOnDiff: true}, NodeDiff: &model.NodeDiff{HasDifference: true}},
		{Certname: "compile", Diagnostics: []model.Diagnostic{{Severity: model.SeverityError, Operation: model.OperationRequestCandidate, Message: "compilation rejected"}}},
		{Certname: "operational", Diagnostics: []model.Diagnostic{{Severity: model.SeverityError, Operation: model.OperationLoadBaseline, Message: "baseline unavailable"}}},
	}
	r.Reduce()
	data, err := JUnit(r)
	if err != nil {
		t.Fatal(err)
	}
	var got junitSuites
	if err := xml.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, data)
	}
	if got.Tests != 5 || got.Failures != 1 || got.Errors != 2 || got.Suite.Tests != 5 || got.Suite.Failures != 1 || got.Suite.Errors != 2 {
		t.Fatalf("incorrect counts: %+v", got)
	}
	if got.Suite.Name != "piace.compare" || got.Suite.Timestamp != r.Invocation.TimestampUTC || len(got.Suite.Cases) != 5 {
		t.Fatalf("incorrect suite: %+v", got.Suite)
	}
	for i, c := range got.Suite.Cases {
		if c.Name != r.Targets[i].Certname || c.Classname != "piace.target" {
			t.Errorf("case identity = %s/%s", c.Classname, c.Name)
		}
		if (c.Failure != nil) != (i == 2) || (c.Error != nil) != (i >= 3) {
			t.Errorf("incorrect outcome for %s: %+v", c.Name, c)
		}
		if c.Error != nil && !strings.Contains(c.Error.Details, r.Targets[i].Diagnostics[0].Message) {
			t.Errorf("missing error details for %s", c.Name)
		}
	}
	if !bytes.HasPrefix(data, []byte(xml.Header)) || !bytes.HasSuffix(data, []byte("\n")) {
		t.Errorf("missing XML declaration or trailing newline")
	}
}

func TestJUnitRunDiagnostics(t *testing.T) {
	for _, severity := range []model.DiagnosticSeverity{model.SeverityWarning, model.SeverityError} {
		t.Run(string(severity), func(t *testing.T) {
			r := model.NewResult("test", "")
			r.Targets = []model.TargetResult{{Certname: "policy", Config: &model.ConfigProvenance{FailOnDiff: true}, NodeDiff: &model.NodeDiff{HasDifference: true}}}
			r.Diagnostics = []model.Diagnostic{{Severity: severity, Operation: model.OperationEstimateImpact, Message: "impact query unavailable"}}
			r.Reduce()
			data, err := JUnit(r)
			if err != nil {
				t.Fatal(err)
			}
			var got junitSuites
			if err := xml.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Suite.Cases) != 2 || got.Failures != 1 {
				t.Fatalf("incorrect cases: %+v", got)
			}
			wantErrors := 0
			if severity == model.SeverityError {
				wantErrors = 1
			}
			if got.Errors != wantErrors || got.Suite.Errors != wantErrors {
				t.Errorf("errors = %d/%d, want %d", got.Errors, got.Suite.Errors, wantErrors)
			}
			c := got.Suite.Cases[1]
			if c.Name != "run diagnostics" || c.Classname != "piace.run" || c.Failure != nil || (c.Error != nil) != (severity == model.SeverityError) {
				t.Errorf("incorrect run diagnostic classification: %+v", c)
			}
			if !strings.Contains(c.Output, "impact query unavailable") {
				t.Error("run diagnostic missing")
			}
		})
	}
}

func TestJUnitEvidenceAndRoundTrip(t *testing.T) {
	r := sampleResult()
	r.Targets[0].NodeDiff.ResourceChanges[0].Identity.Title = "<notify>&\"日本語\x00\x1b"
	first, err := JUnit(r)
	if err != nil {
		t.Fatal(err)
	}
	var got junitSuites
	if err := xml.Unmarshal(first, &got); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	output := got.Suite.Cases[0].Output
	for _, want := range []string{"<notify>&\"日本語", `\x00`, `\x1b`, model.RedactedValue, model.V3TrustedFactWarning, "Class[a] -> Class[b]", "sha256 aaaa -> bbbb"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in case output:\n%s", want, output)
		}
	}
	if !strings.Contains(got.Suite.Output, ImpactEstimateLabel) || !strings.Contains(got.Suite.Output, ImpactEstimateNote) {
		t.Error("impact labelling missing")
	}
	stored, err := JSON(r)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(stored)
	if err != nil {
		t.Fatal(err)
	}
	second, err := JUnit(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("JUnit differs after JSON round trip")
	}
}

func TestJUnitEmptyResultAndUnknownOutcome(t *testing.T) {
	r := model.NewResult("test", "")
	r.Reduce()
	data, err := JUnit(r)
	if err != nil {
		t.Fatal(err)
	}
	var got junitSuites
	if err := xml.Unmarshal(data, &got); err != nil || got.Tests != 0 || len(got.Suite.Cases) != 0 {
		t.Fatalf("empty result: %+v, %v", got, err)
	}
	r.Targets = []model.TargetResult{{Certname: "node", Outcome: exitcode.Outcome("unknown")}}
	if _, err := JUnit(r); err == nil {
		t.Fatal("unknown outcome silently passed")
	}
}
