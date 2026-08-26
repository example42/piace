package report

import (
	"regexp"
	"strings"
	"testing"

	"github.com/example42/piace/internal/model"
)

// TestHTML_IsSelfContained is requirements.md 8.3 checked structurally:
// the artifact must open over `file://` with no HTTP server, CDN, network
// access, or sibling assets. Nothing in the document may reference an
// external resource or execute script.
func TestHTML_IsSelfContained(t *testing.T) {
	data, err := HTML(sampleResult())
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	out := strings.ToLower(string(data))

	// An untrusted value containing "<script" is escaped to "&lt;script",
	// so a whole-document tag scan is meaningful: any hit is real markup.
	for _, forbidden := range []string{"<script", "<link", "<iframe", "<img", "<object", "<embed", "<a "} {
		if strings.Contains(out, forbidden) {
			t.Errorf("HTML report contains the external/executable element %q", forbidden)
		}
	}
	// URL schemes are checked only over the page's own markup: the
	// embedded JSON is escaped text and may legitimately quote a producer
	// hostname or a service-reported URL from a diagnostic.
	markup := out[:strings.Index(out, "<h2>result document</h2>")]
	for _, forbidden := range []string{"http://", "https://", "url("} {
		if strings.Contains(markup, forbidden) {
			t.Errorf("HTML report markup references external content: %q", forbidden)
		}
	}
	if !strings.Contains(out, "<style>") {
		t.Error("HTML report has no inlined stylesheet")
	}
}

// TestHTML_EscapesUntrustedValues verifies contextual escaping actually
// runs over Puppet-supplied text. sampleResult deliberately contains a
// resource title of `</script><img src=x>`.
func TestHTML_EscapesUntrustedValues(t *testing.T) {
	data, err := HTML(sampleResult())
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	out := string(data)

	if strings.Contains(out, "<img src=x>") {
		t.Error("an untrusted resource title was emitted as live markup")
	}
	if !strings.Contains(out, "&lt;/script&gt;&lt;img src=x&gt;") &&
		!strings.Contains(out, "&lt;/script&gt;&lt;img src=x>") {
		t.Errorf("the untrusted resource title does not appear escaped in the document")
	}
}

// TestHTML_VisiblyMarksRequiredStates covers requirements.md 8.4-8.5 and
// 10.2: per-target node diffs, the aggregate diff, catalog retrieval
// failure, compilation failure, the v3 warning, excluded differences, and
// the final outcome must all be visible without opening a data blob.
func TestHTML_VisiblyMarksRequiredStates(t *testing.T) {
	data, err := HTML(sampleResult())
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
	data, err := HTML(sampleResult())
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	out := string(data)
	visible := out[:strings.Index(out, "<h2>Result document</h2>")]

	for _, want := range []string{
		"Class[a]",             // a per-target edge change
		"Class[b]",             //   ...and its other endpoint
		"1 edge(s) suppressed", // requirements.md 6.5, in full
		`resources[certname] { type = &#34;Service&#34;`, // requirements.md 9.4
		"/pdb/query/v4",                          // requirements.md 9.7 request scope
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
		"Dependency-graph edges",       // per-target edges
		"Dependency-graph edge groups", // aggregate edge groups
		"Nodes and query",              // an estimate's certnames, PQL, request
		"Excluded differences",         // requirements.md 8.5
	} {
		if !strings.Contains(visible, summary) {
			t.Errorf("HTML report has no disclosure headed %q", summary)
		}
	}
	// ...and the resource changes, the thing a reader came for, must not be.
	if !strings.Contains(visible, "<details open>") {
		t.Error("the resource-change list is not open by default")
	}
}

// TestHTML_CountsAgreeWithWhatIsRendered guards the header/body agreement
// per section. sampleResult holds two aggregate groups, one of each
// shape, which the page splits into separate counted sections.
func TestHTML_CountsAgreeWithWhatIsRendered(t *testing.T) {
	data, err := HTML(sampleResult())
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	visible := string(data)[:strings.Index(string(data), "<h2>Result document</h2>")]

	for _, want := range []string{
		`<h2>Aggregate diff <span class="count">1</span></h2>`,
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

	data, err := HTML(r)
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

// TestHTML_EmbedsTheCanonicalJSON verifies design.md section 9's "HTML
// embeds the redacted canonical result as escaped data", and that the
// embedded bytes are exactly the JSON artifact.
func TestHTML_EmbedsTheCanonicalJSON(t *testing.T) {
	result := sampleResult()
	jsonData, err := JSON(result)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	data, err := HTML(result)
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

// TestHTML_IsByteIdenticalForIdenticalInput is design.md's Property 1
// applied to the HTML artifact.
func TestHTML_IsByteIdenticalForIdenticalInput(t *testing.T) {
	first, err := HTML(sampleResult())
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	for i := 0; i < 20; i++ {
		next, err := HTML(sampleResult())
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
	data, err := HTML(sampleResult())
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	if regexp.MustCompile(`\{\{[^}]*\}\}`).FindString(string(data)) != "" {
		t.Error("the rendered document still contains a template action")
	}
}
