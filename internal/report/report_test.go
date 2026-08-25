package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/example42/piace/internal/model"
)

func TestSampleResult_ReducesAsExpected(t *testing.T) {
	if got := sampleResult().Outcome; got != wantOutcome {
		t.Fatalf("Outcome = %q, want %q", got, wantOutcome)
	}
}

// TestJSON_IsValidAndCarriesTheSchemaVersion checks the JSON artifact
// parses and keeps the versioned envelope requirements.md 8.2 requires.
func TestJSON_IsValidAndCarriesTheSchemaVersion(t *testing.T) {
	data, err := JSON(sampleResult())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var decoded model.Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.SchemaVersion != model.ResultSchemaVersion {
		t.Errorf("SchemaVersion = %d", decoded.SchemaVersion)
	}
	if decoded.ExitCode != 30 || decoded.Outcome != wantOutcome {
		t.Errorf("Outcome/ExitCode = %q/%d", decoded.Outcome, decoded.ExitCode)
	}
	if len(decoded.Targets) != 2 || len(decoded.Aggregate.Groups) != 2 || len(decoded.ImpactEstimates) != 2 {
		t.Errorf("document is missing sections: %d targets, %d groups, %d estimates",
			len(decoded.Targets), len(decoded.Aggregate.Groups), len(decoded.ImpactEstimates))
	}
	if !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("JSON artifact does not end with a single trailing newline")
	}
}

// TestJSON_IsByteIdenticalForIdenticalInput is design.md's Property 1
// applied to the JSON artifact. Rendering the same document twice must
// produce the same bytes despite the map fields in ConfigProvenance,
// whose Go iteration order is randomized.
func TestJSON_IsByteIdenticalForIdenticalInput(t *testing.T) {
	first, err := JSON(sampleResult())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	for i := 0; i < 20; i++ {
		next, err := JSON(sampleResult())
		if err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if string(next) != string(first) {
			t.Fatalf("render %d differs from the first render", i)
		}
	}
}

// TestJSON_CanonicalizesNumberSpelling verifies the canonical encoder is
// actually in the path: two spellings of the same number must produce
// identical bytes (design.md section 6/7.1).
func TestJSON_CanonicalizesNumberSpelling(t *testing.T) {
	render := func(number model.Number) string {
		r := model.NewResult("test", "2026-08-25T12:00:00Z")
		r.Targets = []model.TargetResult{{
			Certname: "a",
			Config:   &model.ConfigProvenance{},
			NodeDiff: &model.NodeDiff{
				HasDifference: true,
				ResourceChanges: []model.ResourceChange{{
					Kind:      model.ChangeParameterChanged,
					Identity:  model.ResourceIdentity{Type: "Notify", Title: "n"},
					Parameter: "port", Before: model.Number("1"), After: number,
				}},
			},
		}}
		r.Reduce()
		data, err := JSON(r)
		if err != nil {
			t.Fatalf("JSON: %v", err)
		}
		return string(data)
	}
	if render(model.Number("1.50")) != render(model.Number("1.5")) {
		t.Error(`"1.50" and "1.5" rendered differently`)
	}
}

// TestText_SectionOrder locks design.md section 9's fixed text order:
// final outcome first, then per-target status, node changes,
// warnings/errors, aggregate summary, impact summary.
func TestText_SectionOrder(t *testing.T) {
	data, err := Text(sampleResult())
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := string(data)

	previous := -1
	for _, marker := range []string{"outcome:", "targets (", "aggregate diff (", ImpactEstimateLabel} {
		index := strings.Index(out, marker)
		if index < 0 {
			t.Fatalf("text report is missing %q", marker)
		}
		if index <= previous {
			t.Errorf("%q appears out of order", marker)
		}
		previous = index
	}
	if !strings.HasPrefix(out, "PIACE test (2026-08-25T12:00:00Z)\noutcome: operational_error (exit 30)\n") {
		t.Errorf("text report does not open with the outcome")
	}
}

// TestText_RequiredContent checks the elements requirements.md 2.5, 6.5,
// 9.3, 9.4, and 10.2 require to be visible in the CI log.
func TestText_RequiredContent(t *testing.T) {
	data, err := Text(sampleResult())
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := string(data)

	for _, want := range []string{
		"reason:",                                // 10.2
		model.V3TrustedFactWarning,               // 2.5
		"Package[*]: 2 resource(s)",              // 6.5
		ImpactEstimateLabel,                      // 9.3
		ImpactEstimateNote,                       // 9.3
		`resources[certname] { type = "Service"`, // 9.4
		"truncated",                              // 9.6
		"ERROR [load_baseline]",                  // 8.2/10.5
		`~ Service[nginx] ensure: "stopped" -> "running"`,
		"~ File[/etc/motd] content: changed (via inline_content) sha256 aaaa -> bbbb",
		"+edge Class[a] -> Class[b]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text report is missing %q\n---\n%s", want, out)
		}
	}
}

// TestText_NeverClaimsEstimatedNodesWillChange guards requirements.md
// 9.3's prohibition and CONTEXT.md's _Avoid_ wording for impact
// estimates.
func TestText_NeverClaimsEstimatedNodesWillChange(t *testing.T) {
	data, err := Text(sampleResult())
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	// The fixed note is excluded from the scan: it is the one place the
	// report is allowed to say "will change", because it says PIACE does
	// *not* claim it.
	out := strings.ToLower(strings.ReplaceAll(string(data), ImpactEstimateNote, ""))
	for _, forbidden := range []string{"will change", "affected nodes", "blast radius", "impacted nodes"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("text report contains forbidden impact wording %q", forbidden)
		}
	}
}

// TestText_IsByteIdenticalForIdenticalInput is design.md's Property 1
// applied to the text report, which iterates provenance maps.
func TestText_IsByteIdenticalForIdenticalInput(t *testing.T) {
	first, err := Text(sampleResult())
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	for i := 0; i < 20; i++ {
		next, err := Text(sampleResult())
		if err != nil {
			t.Fatalf("Text: %v", err)
		}
		if string(next) != string(first) {
			t.Fatalf("render %d differs from the first render", i)
		}
	}
}
