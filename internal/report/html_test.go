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

// TestHTML_EdgeGroupRendersWithoutAResourceIdentity guards the shape
// split in model.AggregateChangeKey: an edge group has a nil Identity and
// no before/after values, and must render rather than panic.
func TestHTML_EdgeGroupRendersWithoutAResourceIdentity(t *testing.T) {
	r := model.NewResult("test", "2026-08-25T12:00:00Z")
	r.Aggregate = model.AggregateDiff{Groups: []model.AggregateGroup{{
		Key:       model.AggregateChangeKey{Kind: model.ChangeEdgeRemoved, Edge: &model.Edge{Source: "Class[a]", Target: "Class[b]"}},
		Certnames: []string{"web-01.example.test"},
	}}}
	r.Reduce()

	data, err := HTML(r)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	if !strings.Contains(string(data), "edge_removed Class[a] -&gt; Class[b]") {
		t.Errorf("edge group did not render: %s", string(data))
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
