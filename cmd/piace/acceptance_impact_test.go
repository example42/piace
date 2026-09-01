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

// TestAcceptance_ImpactEstimateBoundsAndLabelling covers the bounded
// query, the truncation rule, the deterministic sample, the reported
// PQL, and the mandatory label.
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

	// The label, in every format.
	for name, artifact := range got.all() {
		if !strings.Contains(artifact, report.ImpactEstimateLabel) {
			t.Errorf("the %s artifact does not label the estimate section %q", name, report.ImpactEstimateLabel)
		}
	}
	// No format may say the nodes will change. The fixed note is stripped
	// first: it is the one place a report is allowed to use the phrase,
	// because it says PIACE does *not* claim it. The HTML-escaped form is
	// stripped as well, since html/template rewrites the note's apostrophes
	// before it reaches the page.
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

	// The exact generated PQL is reported. It is discharged by the JSON
	// report and by the canonical JSON the HTML artifact embeds; the text
	// report and the HTML reading path omit it as repeated bulk (see
	// internal/report's doc.go). Asserting it here against both artifacts is
	// what keeps that trade honest: the obligation moved, it did not lapse.
	wantPQL := `resources[certname] { type = \"Service\" and title = \"nginx\" }`
	if !strings.Contains(got.json, wantPQL) {
		t.Errorf("the JSON report does not carry the exact generated PQL:\n%s", got.json)
	}
	if !strings.Contains(got.html, template.HTMLEscapeString(wantPQL)) {
		t.Errorf("the HTML artifact does not embed the exact generated PQL")
	}
	if strings.Contains(got.stdout, "resources[certname]") {
		t.Errorf("the text report still prints the PQL:\n%s", got.stdout)
	}

	// Truncation is marked and the sample is the deterministic, locally
	// sorted prefix.
	if !strings.Contains(got.json, `"truncated":true`) {
		t.Error("an over-limit estimate was not marked truncated")
	}
	if !strings.Contains(got.stdout, "db-01.example.test, db-02.example.test") {
		t.Errorf("the truncated sample is not the sorted prefix:\n%s", got.stdout)
	}
	if strings.Contains(got.stdout, "db-03.example.test") {
		t.Error("the sample exceeded the configured result limit")
	}

	// Only the changed identity is estimated: estimation runs for resource
	// additions, removals, and parameter changes, never for an unchanged
	// resource.
	if len(h.pdb.impactQueries) != 1 {
		t.Fatalf("issued %d impact queries, want exactly 1: %+v", len(h.pdb.impactQueries), h.pdb.impactQueries)
	}
}

// TestAcceptance_FailedImpactEstimateIsOperational covers the rule that
// an enabled estimate's failure is requested analysis that was not
// delivered, and is reported separately from catalog differences.
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

// TestAcceptance_DisabledImpactEstimateIssuesNoQuery covers the rule
// that a disabled estimate produces no request and no failure.
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
// IMPORTANT: this test proves PIACE sends what it says it sends. It
// cannot prove a deployed PuppetDB *accepts* it, so two assumptions
// remain outstanding:
//
//  1. The PQL text is sent to the root /pdb/query/v4 endpoint rather than
//     to /pdb/query/v4/resources, which takes AST rather than a PQL
//     string naming its own entity. Whether the root endpoint accepts
//     this exact text against the deployed PuppetDB version is
//     unconfirmed.
//  2. `limit` and `order_by` are sent as URL parameters beside `query`.
//     Whether they are honored there is unconfirmed, and per
//     internal/impact/doc.go, an unhonored `order_by` makes a *truncated*
//     sample non-reproducible when a deterministic one is what the
//     estimate promises. An untruncated sample stays reproducible either
//     way.
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
	// The limit is result_limit + 1, so truncation is detected without a
	// second round trip or a true-total request.
	if q["limit"] != "3" {
		t.Errorf("limit = %q, want 3 (result_limit 2 + 1)", q["limit"])
	}
	if q["order_by"] != `[{"field":"certname","order":"asc"}]` {
		t.Errorf("order_by = %q", q["order_by"])
	}
}

// TestAcceptance_ImpactNodesControlsTheCertnameSample covers the
// `--impact-nodes` option end to end.
//
// The default is capped because a bounded estimate may hold as many
// certnames as its configured `result_limit`, a thousand in a realistic
// deployment, and a section of several hundred estimates each naming a
// thousand nodes is not a CI log anyone reads. What the cap must never
// do is understate the estimate, so the count stays exact in both forms
// and only the names are elided.
func TestAcceptance_ImpactNodesControlsTheCertnameSample(t *testing.T) {
	// A result limit above the returned count keeps the estimate
	// untruncated, so this exercises the display cap rather than the query
	// bound. They are two different elisions and must not be confused.
	defaults := strings.Replace(impactDefaults, "    result_limit: 2", "    result_limit: 50", 1)

	nodes := []string{
		"db-01.example.test", "db-02.example.test", "db-03.example.test",
		"db-04.example.test", "db-05.example.test", "db-06.example.test",
		"db-07.example.test",
	}

	// Both runs share ONE harness: an artifact records the service
	// authorities it talked to, and a second harness listens on different
	// ports, so two harnesses could never produce identical HTML however
	// inert the flag was.
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), changedResources(), baseEdges())
	h.pdb.impactCertnames = nodes
	h.writeConfigs(t, targetsYAML(defaults, target("web-01.example.test")))

	run := func(t *testing.T, extra ...string) artifacts {
		t.Helper()
		got := h.compare(t, extra...)
		if got.code != exitcode.Success {
			t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
		}
		return got
	}

	byDefault := run(t)
	withFlag := run(t, "--impact-nodes")

	if !strings.Contains(byDefault.stdout, "7 nodes") {
		t.Errorf("the capped line does not state the full node count:\n%s", byDefault.stdout)
	}
	if !strings.Contains(byDefault.stdout, "(+2 more)") {
		t.Errorf("the capped line does not count the elided certnames:\n%s", byDefault.stdout)
	}
	if strings.Contains(byDefault.stdout, "db-07.example.test") {
		t.Errorf("a certname past the cap was named:\n%s", byDefault.stdout)
	}
	// The elision is display-only: the artifact still holds every name.
	if !strings.Contains(byDefault.json, "db-07.example.test") {
		t.Error("the JSON report lost a certname the text report elided")
	}

	if !strings.Contains(withFlag.stdout, "db-07.example.test") {
		t.Errorf("--impact-nodes did not name every certname:\n%s", withFlag.stdout)
	}
	if strings.Contains(withFlag.stdout, "more)") {
		t.Errorf("--impact-nodes still elided part of the sample:\n%s", withFlag.stdout)
	}

	// The flag is a text-report control. The HTML artifact names every
	// certname either way -- it collapses the list rather than capping it
	// -- so the two runs must produce the same page, byte for byte.
	// Asserting only that both contain db-07 would keep passing if display
	// options were ever threaded back into report.HTML and capped there;
	// asserting equality is what actually pins the documented contract.
	if !strings.Contains(byDefault.html, "db-07.example.test") {
		t.Error("the HTML report capped the certname list; it should disclose all of them")
	}
	if byDefault.html != withFlag.html {
		t.Error("--impact-nodes changed the HTML artifact; it is a text-report control")
	}
}
