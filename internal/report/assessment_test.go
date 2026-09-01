package report

import (
	"os"
	"strings"

	"github.com/example42/piace/internal/assess"
	"testing"
)

// Slice 7.1: a nil change assessment means today's output exactly.
//
// The golden was captured from the v0.1.0 renderer before this
// parameter existed, so it is an independent source of truth rather
// than a restatement of what the code now emits. Any byte this feature
// adds to a report rendered without an assessment fails here, including
// the stray newline a naively guarded template block emits.
func TestHTMLWithNoAssessmentIsByteIdenticalToTheV010Report(t *testing.T) {
	want, err := os.ReadFile("testdata/sample_report.golden.html")
	if err != nil {
		t.Fatalf("reading the v0.1.0 golden report: %v", err)
	}

	got, err := HTML(sampleResult(), nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("HTML(r, nil) is not byte-identical to the v0.1.0 report: got %d bytes, want %d", len(got), len(want))
	}
}

// sampleAssessment is one change assessment exercising every element the
// renderers have to handle: a run risk indication with review focus, a
// group carrying a rationale and one carrying none, a truncated
// selection, a partial input, an error diagnostic, and model-generated
// free text shaped like markup.
func sampleAssessment() assess.Assessment {
	return assess.Assessment{
		AISchemaVersion:      assess.AISchemaVersion,
		GeneratedAt:          "2026-08-25T12:05:00Z",
		ModelID:              "some-model-id",
		EndpointAuthority:    "api.example.com",
		SourceReportChecksum: "sha256:deadbeef",
		SourceReportOutcome:  string(wantOutcome),
		Run: assess.RunAssessment{
			Risk:        assess.RiskMedium,
			Summary:     "A service restart and a motd change. <script>alert(1)</script>",
			ReviewFocus: []string{"Service[nginx]", "web-01.example.test"},
		},
		Groups: []assess.GroupAssessment{
			{
				ID: "g1", Kind: "parameter_changed",
				Identity: "Service[nginx]", Parameter: "ensure",
				Certnames:   []string{"web-01.example.test"},
				Risk:        assess.RiskHigh,
				Rationale:   "Stopping nginx interrupts traffic.",
				ReviewFocus: []string{"Service[nginx]"},
			},
			{
				ID: "g2", Kind: "edge_added",
				Identity:  "Class[a] -> Class[b]",
				Certnames: []string{"web-01.example.test"},
				Risk:      assess.RiskUnknown,
			},
		},
		GroupsTotal: 5, GroupsAssessed: 2, GroupsTruncated: true,
		InputPartial: true,
		Diagnostics: []assess.Diagnostic{
			{Severity: assess.SeverityError, Message: "requesting a change assessment: 500"},
		},
	}
}

// Slice 7.2: the assessment is advisory and the comparison is not. A
// reader must reach every deterministic section — the outcome, the
// targets, the aggregate diff, the run diagnostics — before a model's
// opinion about them.
func TestHTMLRendersTheAssessmentBelowTheDeterministicOutcome(t *testing.T) {
	a := sampleAssessment()
	data, err := HTML(sampleResult(), &a)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	doc := string(data)

	section := strings.Index(doc, AssessmentLabel)
	if section < 0 {
		t.Fatalf("the rendered report carries no %q section", AssessmentLabel)
	}
	for _, above := range []string{"Aggregate diff", "Run diagnostics"} {
		at := strings.Index(doc, ">"+above+" ")
		if at < 0 {
			t.Fatalf("the rendered report carries no %q heading", above)
		}
		if section < at {
			t.Errorf("the %s section renders above %q", AssessmentLabel, above)
		}
	}
}

// Slice 7.3: the run risk indication is an outcome badge and lives where
// every other outcome badge lives — outside every disclosure.
//
// A substring search cannot establish this: collapsed content matches
// just as well as visible content. So the assertion walks the document
// to the badge counting open disclosures, exactly the way a reader's
// browser does, and requires the depth there to be zero.
func TestHTMLKeepsTheRunRiskIndicationOutOfDisclosure(t *testing.T) {
	a := sampleAssessment()
	data, err := HTML(sampleResult(), &a)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	doc := string(data)

	heading := strings.Index(doc, "<h2>"+AssessmentLabel)
	if heading < 0 {
		t.Fatalf("the rendered report carries no %q heading", AssessmentLabel)
	}
	badge := strings.Index(doc[heading:], `<span class="badge`)
	if badge < 0 {
		t.Fatalf("the %s heading carries no risk badge", AssessmentLabel)
	}
	if depth := disclosureDepthAt(doc, heading+badge); depth != 0 {
		t.Errorf("the run risk indication sits %d disclosure(s) deep; it belongs in the scanning path", depth)
	}

	// The counterpart, asserted here so the check above cannot pass
	// because the detector is broken: per-group rationale is detail, and
	// detail is disclosed. One of the two badges must be inside a
	// <details>, and it is not the run's.
	group := strings.Index(doc, "Stopping nginx interrupts traffic.")
	if group < 0 {
		t.Fatalf("the rendered report carries no group rationale")
	}
	if depth := disclosureDepthAt(doc, group); depth == 0 {
		t.Error("per-group rationale is in the scanning path; it belongs behind a disclosure")
	}
}

// disclosureDepthAt reports how many <details> elements enclose the byte
// at index i.
func disclosureDepthAt(doc string, i int) int {
	depth := strings.Count(doc[:i], "<details") - strings.Count(doc[:i], "</details>")
	if depth < 0 {
		return 0
	}
	return depth
}

// Slice 7.4: a reader who scans only this section must be unable to
// mistake it for the comparison. It names the model that produced it,
// says in the page that it is advisory, model-generated and not
// deterministic, and — when only part of the run was assessed — says so
// rather than reading as a complete review.
//
// Every one of these is outside a disclosure, for the same reason
// failures are: a mark a reader has to go looking for is not a visible
// mark.
func TestHTMLMarksTheAssessmentAdvisoryAndNamesItsModel(t *testing.T) {
	a := sampleAssessment()
	data, err := HTML(sampleResult(), &a)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	doc := string(data)

	for _, want := range []string{
		AssessmentNote,    // advisory, model-generated, not deterministic
		"some-model-id",   // which model said it
		"api.example.com", // and from where
		"2 of 5",          // groups_assessed of groups_total
		"sha256:deadbeef", // the result document it was derived from
	} {
		at := strings.Index(doc, want)
		if at < 0 {
			t.Errorf("the %s section does not carry %q", AssessmentLabel, want)
			continue
		}
		if depth := disclosureDepthAt(doc, at); depth != 0 {
			t.Errorf("%q sits %d disclosure(s) deep; a mark a reader has to open is not visible", want, depth)
		}
	}

	for _, marker := range []string{"advisory", "not deterministic"} {
		if !strings.Contains(strings.ToLower(AssessmentNote), marker) {
			t.Errorf("AssessmentNote does not say the assessment is %q", marker)
		}
	}
}

// A complete assessment must not claim it was truncated, or the marking
// in the test above is decoration rather than information.
func TestHTMLSaysNothingAboutTruncationWhenEveryGroupWasAssessed(t *testing.T) {
	a := sampleAssessment()
	a.GroupsTotal, a.GroupsAssessed, a.GroupsTruncated = 2, 2, false
	a.InputPartial = false
	data, err := HTML(sampleResult(), &a)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	if strings.Contains(string(data), "2 of 5") {
		t.Error("an untruncated assessment reports a truncation")
	}
	for _, unwanted := range []string{"only the ", "partial"} {
		if strings.Contains(strings.ToLower(string(data)), unwanted) {
			t.Errorf("a complete assessment of a complete input says %q", unwanted)
		}
	}
}

// Slice 7.5: the text report is a CI log — a linear read with no way to
// expand a section — so it carries the run risk indication and review
// focus and stops there. Per-group rationale is model prose, one
// paragraph per group, and a hundred of them between the aggregate diff
// and the end of the log is the wall of text the text format exists to
// avoid. It is in the HTML report and in the JSON artifact, both of
// which a reader can navigate.
//
// This is display policy of exactly the kind Options describes, and the
// same rule as the edge changes and PQL text already omits: the JSON
// artifact stays the complete record.
func TestTextCarriesTheRunRiskIndicationAndNotPerGroupRationale(t *testing.T) {
	a := sampleAssessment()
	data, err := Text(sampleResult(), &a, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := string(data)

	for _, want := range []string{
		AssessmentLabel,
		AssessmentNote,
		"medium",         // the run risk indication
		"Service[nginx]", // the review focus
		"some-model-id",  // which model said it
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the text report does not carry %q", want)
		}
	}

	if strings.Contains(out, "Stopping nginx interrupts traffic.") {
		t.Error("the text report carries a per-group rationale")
	}
}

// And with no assessment it is byte-identical to what it renders today,
// for the same reason HTML is.
func TestTextWithNoAssessmentIsUnchanged(t *testing.T) {
	r := sampleResult()
	for _, opts := range []Options{{}, {ImpactNodes: true}} {
		with, err := Text(r, nil, opts)
		if err != nil {
			t.Fatalf("Text: %v", err)
		}
		if strings.Contains(string(with), AssessmentLabel) {
			t.Errorf("Text(r, nil, %+v) renders an assessment section", opts)
		}
	}
}
