package report

import (
	"regexp"
	"strings"
	"testing"

	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/model"
)

// TestHTML_IsSelfContained checks the self-containment claim
// structurally: the artifact must open over `file://` with no HTTP
// server, CDN, network access, or sibling assets. Nothing in the
// document may reference an external resource or execute script.
//
// Slice 7.6 runs it over both renderings. Self-containment asserted only
// against the assessment-free page would pass vacuously the moment the
// change-assessment section exists, and that section is the one part of
// the document built from text a remote service wrote.
func TestHTML_IsSelfContained(t *testing.T) {
	a := sampleAssessment()
	for _, tc := range []struct {
		name       string
		assessment *assess.Assessment
	}{{"no assessment", nil}, {"with a change assessment", &a}} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := HTML(sampleResult(), tc.assessment)
			if err != nil {
				t.Fatalf("HTML: %v", err)
			}
			out := strings.ToLower(string(data))

			// An untrusted value containing "<script" is escaped to
			// "&lt;script", so a whole-document tag scan is meaningful:
			// any hit is real markup.
			for _, forbidden := range []string{"<script", "<link", "<iframe", "<img", "<object", "<embed", "<a "} {
				if strings.Contains(out, forbidden) {
					t.Errorf("HTML report contains the external/executable element %q", forbidden)
				}
			}
			// URL schemes are checked only over the page's own markup: the
			// embedded JSON is escaped text and may legitimately quote a
			// producer hostname or a service-reported URL from a diagnostic.
			markup := out[:strings.Index(out, "<h2>result document</h2>")]
			for _, forbidden := range []string{"http://", "https://", "url("} {
				if strings.Contains(markup, forbidden) {
					t.Errorf("HTML report markup references external content: %q", forbidden)
				}
			}
			if !strings.Contains(out, "<style>") {
				t.Error("HTML report has no inlined stylesheet")
			}
		})
	}
}

// TestHTML_EscapesUntrustedValues verifies contextual escaping actually
// runs over Puppet-supplied text. sampleResult deliberately contains a
// resource title of `</script><img src=x>`, and sampleAssessment a run
// summary containing a script element.
//
// A change assessment's prose is written by a remote service over text
// PIACE itself sent from a catalog, so it is untrusted twice over. It is
// never wrapped in template.HTML, however convenient that would be for
// line breaks in a rationale.
func TestHTML_EscapesUntrustedValues(t *testing.T) {
	a := sampleAssessment()
	data, err := HTML(sampleResult(), &a)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	out := string(data)

	if strings.Contains(out, "<img src=x>") {
		t.Error("an untrusted resource title was emitted as live markup")
	}
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Error("a model-generated run summary was emitted as live markup")
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("the model-generated run summary does not appear escaped in the document")
	}
	if !strings.Contains(out, "&lt;/script&gt;&lt;img src=x&gt;") &&
		!strings.Contains(out, "&lt;/script&gt;&lt;img src=x>") {
		t.Errorf("the untrusted resource title does not appear escaped in the document")
	}
}

// TestHTML_VisiblyMarksRequiredStates: per-target node diffs, the
// aggregate diff, catalog retrieval failure, compilation failure, the v3
// warning, excluded differences, and the final outcome must all be
// visible without opening a data blob.
func TestHTML_VisiblyMarksRequiredStates(t *testing.T) {
	data, err := HTML(sampleResult(), nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	out := string(data)
	// The embedded canonical JSON would satisfy a naive substring search
	// for anything, so it is removed before checking what the page shows.
	visible := out[:strings.Index(out, "<h2>Result document</h2>")]

	for _, want := range []string{
		"operational_error",              // 10.2 final outcome
		"web-01.example.test",            // 8.4 per-target
		"Service[nginx]",                 // 8.4 node diff content
		"Aggregate diff",                 // 8.4
		"Trusted-fact compatibility",     // 8.5 v3 warning
		"load_baseline failed",           // 8.5 retrieval failure
		"Excluded differences",           // 8.5
		ImpactEstimateLabel,              // 9.3
		"no baseline catalog stored for", // the failure's reason
	} {
		if !strings.Contains(visible, want) {
			t.Errorf("HTML report does not visibly show %q", want)
		}
	}
}

// TestAggregateKeyLabel_HandlesAnEdgeKeysNilIdentity guards the shape
// split in model.AggregateChangeKey: an edge key sets Edge and leaves
// Identity nil, so a caller that dereferences Identity unconditionally
// panics.
//
// The HTML and text renderers no longer reach this branch — they filter
// edge groups out first — but the hazard is structural, not situational,
// so it is tested directly at the helper rather than through a format
// that happens to exercise it today.
func TestAggregateKeyLabel_HandlesAnEdgeKeysNilIdentity(t *testing.T) {
	key := model.AggregateChangeKey{Kind: model.ChangeEdgeRemoved, Edge: &model.Edge{Source: "Class[a]", Target: "Class[b]"}}
	if got := aggregateKeyLabel(key); got != "edge_removed Class[a] -> Class[b]" {
		t.Errorf("aggregateKeyLabel = %q", got)
	}
}

// TestHTML_KeepsEverythingBehindDisclosure is this format's half of the
// display contract. Text drops edge changes, an estimate's PQL and
// request options, and the certnames past a cap; HTML keeps all of it and
// uses <details> instead — so every string the text test asserts is
// ABSENT must be present here, in the page itself rather than only in the
// canonical JSON embedded at the bottom.
func TestHTML_KeepsEverythingBehindDisclosure(t *testing.T) {
	data, err := HTML(sampleResult(), nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	out := string(data)
	visible := out[:strings.Index(out, "<h2>Result document</h2>")]

	for _, want := range []string{
		"Class[a]",             // a per-target edge change
		"Class[b]",             //   ...and its other endpoint
		"1 edge(s) suppressed", // the suppression count, in full
		`resources[certname] { type = &#34;Service&#34;`, // the exact generated PQL
		"/pdb/query/v4",                          // the request scope
		"order_by",                               //   ...and its options
		"db-01.example.test, db-02.example.test", // the uncapped certname sample
	} {
		if !strings.Contains(visible, want) {
			t.Errorf("HTML report does not show %q above the result document", want)
		}
	}

	// Present is not enough: the bulk has to be collapsed, or the page is
	// just the old wall of text with nicer colors.
	for _, summary := range []string{
		"Resource changes",             // per-target resource changes
		"Dependency-graph edges",       // per-target edges
		"Grouped resource changes",     // aggregate resource groups
		"Dependency-graph edge groups", // aggregate edge groups
		"Queried resources",            // the estimate list
		"Excluded differences",         // visibly marked, not disclosed
	} {
		if !strings.Contains(visible, summary) {
			t.Errorf("HTML report has no disclosure headed %q", summary)
		}
	}
	// ...and no list of rows is in the scanning path: a section left open
	// buries every section after it, which on a real run means four
	// figures of rows above the outcome a reader came for.
	if strings.Contains(visible, "<details open") {
		t.Error("a disclosure is open by default, putting a row list in the scanning path")
	}
}

// TestHTML_KeepsFailuresOutOfDisclosure is the floor under the collapse.
// Catalog retrieval failure, compilation failure and the v3 trusted-fact
// warning have to be *visibly* marked, and a mark inside a closed
// <details> is not visible. Everything else on a target may collapse;
// these may not.
//
// TestHTML_VisiblyMarksRequiredStates cannot catch this — it substring
// searches, and collapsed content still matches.
func TestHTML_KeepsFailuresOutOfDisclosure(t *testing.T) {
	data, err := HTML(sampleResult(), nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	out := string(data)

	for _, mark := range []string{
		"Trusted-fact compatibility", // the v3 warning banner
		"load_baseline failed",       // a per-target error banner
	} {
		at := strings.Index(out, mark)
		if at < 0 {
			t.Errorf("HTML report does not show %q at all", mark)
			continue
		}
		// The mark belongs to a target card, so the enclosing card is the
		// last one opened before it; any <details> between that card's
		// start and the mark would be hiding it.
		card := strings.LastIndex(out[:at], `<section class="card">`)
		if card < 0 {
			t.Errorf("%q is not inside a target card", mark)
			continue
		}
		if strings.Contains(out[card:at], "<details") {
			t.Errorf("%q is inside a disclosure; it has to be visibly marked", mark)
		}
	}
}

// TestHTML_MarksAFailedEstimateOnTheClosedList is the estimate section's
// share of the visibility floor. An estimate list is a disclosure like
// every other list of rows, so a failed estimate sits two levels deep;
// the count on the closed summary is what keeps it in the scanning path.
// sampleResult holds one failed estimate of two.
func TestHTML_MarksAFailedEstimateOnTheClosedList(t *testing.T) {
	data, err := HTML(sampleResult(), nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	out := string(data)

	if !strings.Contains(out, `<span class="badge operational">1 failed</span>`) {
		t.Error("the closed estimate list does not mark that one estimate failed")
	}
	// The failed entry keeps its place in the list rather than being
	// hoisted out of it, so its reason is still one click away.
	if !strings.Contains(out, "puppetdb returned status 503") {
		t.Error("the failed estimate's reason is not on the page")
	}
}

// TestHTML_CountsAgreeWithWhatIsRendered guards the header/body agreement
// per section. sampleResult holds two aggregate groups, one of each
// shape, which the page splits into separate counted sections.
func TestHTML_CountsAgreeWithWhatIsRendered(t *testing.T) {
	data, err := HTML(sampleResult(), nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	visible := string(data)[:strings.Index(string(data), "<h2>Result document</h2>")]

	for _, want := range []string{
		`<h2>Aggregate diff <span class="count">1</span></h2>`,
		`Grouped resource changes <span class="count">1</span>`,
		`Dependency-graph edge groups <span class="count">1</span>`,
		`Resource changes <span class="count">4</span>`,
		`Dependency-graph edges <span class="count">1</span>`,
	} {
		if !strings.Contains(visible, want) {
			t.Errorf("HTML report is missing the counted heading %q", want)
		}
	}
}

// TestHTML_EdgeOnlyTargetShowsItsEdges is the report/exit-code contract
// for this format. A target whose only differences are edges still drove
// the run's outcome; because HTML keeps edge changes, it discharges that
// by rendering them rather than by the note the text report needs.
func TestHTML_EdgeOnlyTargetShowsItsEdges(t *testing.T) {
	r := model.NewResult("test", "2026-08-25T12:00:00Z")
	r.Targets = []model.TargetResult{{
		Certname: "web-01.example.test",
		Config:   &model.ConfigProvenance{},
		NodeDiff: &model.NodeDiff{
			Certname:      "web-01.example.test",
			HasDifference: true,
			EdgeChanges: []model.EdgeChange{
				{Kind: model.ChangeEdgeAdded, Edge: model.Edge{Source: "Class[a]", Target: "Class[b]"}},
			},
		},
	}}
	r.Reduce()

	data, err := HTML(r, nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	visible := string(data)[:strings.Index(string(data), "<h2>Result document</h2>")]

	if strings.Contains(visible, "No non-excluded differences") {
		t.Errorf("an edge-only difference was reported as no changes\n---\n%s", visible)
	}
	if !strings.Contains(visible, `Dependency-graph edges <span class="count">1</span>`) {
		t.Errorf("the target's edge changes are not shown\n---\n%s", visible)
	}
}

// TestHTML_EmbedsTheCanonicalJSON verifies that the HTML embeds the
// redacted canonical result as escaped data, and that the embedded bytes
// are exactly the JSON artifact.
func TestHTML_EmbedsTheCanonicalJSON(t *testing.T) {
	result := sampleResult()
	jsonData, err := JSON(result)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	data, err := HTML(result, nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	escaped := strings.ReplaceAll(strings.ReplaceAll(string(jsonData), "&", "&amp;"), "<", "&lt;")
	escaped = strings.ReplaceAll(escaped, ">", "&gt;")
	escaped = strings.ReplaceAll(escaped, `"`, "&#34;")
	if !strings.Contains(string(data), strings.TrimSpace(escaped)[:200]) {
		t.Error("the HTML artifact does not embed the canonical JSON document")
	}
}

// TestHTML_IsByteIdenticalForIdenticalInput applies the determinism
// property to the HTML artifact.
func TestHTML_IsByteIdenticalForIdenticalInput(t *testing.T) {
	first, err := HTML(sampleResult(), nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	for i := 0; i < 20; i++ {
		next, err := HTML(sampleResult(), nil)
		if err != nil {
			t.Fatalf("HTML: %v", err)
		}
		if string(next) != string(first) {
			t.Fatalf("render %d differs from the first render", i)
		}
	}
}

// TestHTML_HasNoRemainingTemplateActions catches a malformed template
// expression that html/template would render as literal text.
func TestHTML_HasNoRemainingTemplateActions(t *testing.T) {
	data, err := HTML(sampleResult(), nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	if regexp.MustCompile(`\{\{[^}]*\}\}`).FindString(string(data)) != "" {
		t.Error("the rendered document still contains a template action")
	}
}
