package main

import (
	"html/template"
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/report"
)

// impactDefaults enables impact estimation with a result limit of 2.
var impactDefaults = strings.Replace(defaultDefaults, "    enabled: false", "    enabled: true", 1)

// changedResources differs from baseResources() in one Service parameter,
// giving impact estimation exactly one identity to query.
func changedResources() []resourceSpec {
	return []resourceSpec{
		{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}},
		{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "stopped", "enable": true}},
	}
}

// TestAcceptance_ImpactEstimateBoundsAndLabelling covers requirements.md
// 9.2-9.7: the bounded query, the truncation rule, the deterministic
// sample, the reported PQL, and the mandatory label.
func TestAcceptance_ImpactEstimateBoundsAndLabelling(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), changedResources(), baseEdges())
	// Four matches against a result limit of 2: more than limit+1, served
	// out of order so the local sort is exercised.
	h.pdb.impactCertnames = []string{"db-02.example.test", "db-04.example.test", "db-01.example.test", "db-03.example.test"}
	h.writeConfigs(t, targetsYAML(impactDefaults, target("web-01.example.test")))

	got := h.compare(t)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.code, got.stdout, got.stderr)
	}

	// requirements.md 9.3: the label, in every format.
	for name, artifact := range got.all() {
		if !strings.Contains(artifact, report.ImpactEstimateLabel) {
			t.Errorf("the %s artifact does not label the estimate section %q", name, report.ImpactEstimateLabel)
		}
	}
	// requirements.md 9.3 again: no format may say the nodes will change.
	// The fixed note is stripped first — it is the one place a report is
	// allowed to use the phrase, because it says PIACE does *not* claim
	// it. The HTML-escaped form is stripped as well, since html/template
	// rewrites the note's apostrophes before it reaches the page.
	for name, artifact := range got.all() {
		scanned := strings.ReplaceAll(artifact, report.ImpactEstimateNote, "")
		scanned = strings.ReplaceAll(scanned, template.HTMLEscapeString(report.ImpactEstimateNote), "")
		scanned = strings.ToLower(scanned)
		for _, forbidden := range []string{"will change", "affected nodes", "blast radius"} {
			if strings.Contains(scanned, forbidden) {
				t.Errorf("the %s artifact uses the forbidden wording %q", name, forbidden)
			}
		}
	}

	// requirements.md 9.4: the exact generated PQL is reported.
	wantPQL := `resources[certname] { type = "Service" and title = "nginx" }`
	if !strings.Contains(got.stdout, wantPQL) {
		t.Errorf("the report does not carry the exact generated PQL:\n%s", got.stdout)
	}

	// requirements.md 9.6: truncation is marked and the sample is the
	// deterministic, locally sorted prefix.
	if !strings.Contains(got.json, `"truncated":true`) {
		t.Error("an over-limit estimate was not marked truncated")
	}
	if !strings.Contains(got.stdout, "db-01.example.test, db-02.example.test") {
		t.Errorf("the truncated sample is not the sorted prefix:\n%s", got.stdout)
	}
	if strings.Contains(got.stdout, "db-03.example.test") {
		t.Error("the sample exceeded the configured result limit")
	}

	// Only the changed identity is estimated: design.md section 8 runs
	// estimation for resource additions, removals, and parameter changes,
	// never for an unchanged resource.
	if len(h.pdb.impactQueries) != 1 {
		t.Fatalf("issued %d impact queries, want exactly 1: %+v", len(h.pdb.impactQueries), h.pdb.impactQueries)
	}
}

// TestAcceptance_FailedImpactEstimateIsOperational covers design.md
// section 8's rule that an enabled estimate's failure is requested
// analysis that was not delivered, and requirements.md 9.7's requirement
// to report it separately from catalog differences.
func TestAcceptance_FailedImpactEstimateIsOperational(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), changedResources(), baseEdges())
	h.pdb.impactStatus = 503
	h.writeConfigs(t, targetsYAML(impactDefaults, target("web-01.example.test")))

	got := h.compare(t)
	if got.code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30\nstdout:\n%s", got.code, got.stdout)
	}
	// The catalog difference is still reported: a failed estimate must
	// not swallow the comparison it accompanies.
	if !strings.Contains(got.stdout, `Service[nginx] ensure: "running" -> "stopped"`) {
		t.Errorf("the catalog difference was lost when the estimate failed:\n%s", got.stdout)
	}
	if !strings.Contains(got.json, `"status":"failed"`) {
		t.Error("the failed estimate is not reported as its own state")
	}
}

// TestAcceptance_DisabledImpactEstimateIssuesNoQuery covers
// requirements.md 9.1 and design.md section 8's "disabled estimates
// produce no request and no failure".
func TestAcceptance_DisabledImpactEstimateIssuesNoQuery(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), changedResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))

	got := h.compare(t)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0", got.code)
	}
	if len(h.pdb.impactQueries) != 0 {
		t.Errorf("estimation is disabled but %d queries were issued", len(h.pdb.impactQueries))
	}
	if strings.Contains(got.stdout, report.ImpactEstimateLabel) {
		t.Error("a disabled estimate still produced an estimate section")
	}
}

// TestAcceptance_ImpactQueryWireShape records, at the wire, exactly what
// PIACE sends for an impact estimate. It exists to make the two
// assumptions internal/impact/doc.go documents inspectable rather than
// buried, and to fail loudly if a future change alters them silently.
//
// IMPORTANT — this test does NOT discharge task 12's second acceptance
// condition. It proves PIACE sends what it says it sends; it cannot prove
// a deployed PuppetDB *accepts* it. Both assumptions remain outstanding:
//
//  1. design.md section 8's PQL text is sent to the root /pdb/query/v4
//     endpoint, not to /pdb/query/v4/resources as requirements.md 9.2
//     names (that endpoint takes AST, not a PQL string naming its own
//     entity). Whether the root endpoint accepts this exact text against
//     the deployed PuppetDB version is unconfirmed.
//  2. `limit` and `order_by` are sent as URL parameters beside `query`.
//     Whether they are honored there is unconfirmed — and per
//     internal/impact/doc.go, an unhonored `order_by` makes a *truncated*
//     sample non-reproducible, which requirements.md 9.6 assumes it is.
//     An untruncated sample stays reproducible either way.
func TestAcceptance_ImpactQueryWireShape(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), changedResources(), baseEdges())
	h.pdb.impactCertnames = []string{"db-01.example.test"}
	h.writeConfigs(t, targetsYAML(impactDefaults, target("web-01.example.test")))

	if got := h.compare(t); got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0", got.code)
	}

	if !contains(h.pdb.sortedPaths(), "/pdb/query/v4") {
		t.Fatalf("the estimate was not sent to the root query endpoint: %v", h.pdb.sortedPaths())
	}
	if contains(h.pdb.sortedPaths(), "/pdb/query/v4/resources") {
		t.Error("the estimate was sent to the entity-scoped resources endpoint, which takes AST rather than PQL")
	}
	if len(h.pdb.impactQueries) != 1 {
		t.Fatalf("recorded %d queries, want 1", len(h.pdb.impactQueries))
	}
	q := h.pdb.impactQueries[0]
	if q["query"] != `resources[certname] { type = "Service" and title = "nginx" }` {
		t.Errorf("query = %q", q["query"])
	}
	// design.md section 8: limit is result_limit + 1, so truncation is
	// detected without a second round trip or a true-total request.
	if q["limit"] != "3" {
		t.Errorf("limit = %q, want 3 (result_limit 2 + 1)", q["limit"])
	}
	if q["order_by"] != `[{"field":"certname","order":"asc"}]` {
		t.Errorf("order_by = %q", q["order_by"])
	}
}
