package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/report"
)

// TestAcceptance_ReportsAreByteIdenticalForIdenticalInputs checks
// determinism at the level that matters: identical input catalogs and
// configuration produce identical bytes, not merely a deterministic
// re-render of one in-memory result.
//
// The clock is frozen here, as it is for every case in this suite, so
// what this establishes is that nothing *except* the invocation
// timestamp can differ between two runs over the same inputs. A
// production run stamps a real timestamp and two runs are never at the
// same instant, so this is not on its own the claim that a comparison
// reproduces:
// TestAcceptance_TheComparisonIsReproducibleAcrossClocks below is, and
// it runs under two different clocks deliberately.
//
// The whole pipeline runs twice, and the second run serves the same
// catalogs with a different resource, parameter, and edge *insertion
// order*: the nondeterminism a real PuppetDB can exhibit, and the one a
// re-render test cannot catch. Every artifact must come out
// byte-identical.
//
// The second run also lists the same two targets in the opposite order
// in its target file. That is deliberately a step beyond identical
// configuration: the resolved target *set* is identical, the file order
// is not. It holds because the result document is target-sorted, and
// asserting it here is what keeps target-file order from leaking into a
// report and making two equivalent CI configs produce different
// artifacts.
func TestAcceptance_ReportsAreByteIdenticalForIdenticalInputs(t *testing.T) {
	forward := []resourceSpec{
		{Type: "Service", Title: "nginx", Parameters: map[string]any{
			"ensure": "running", "enable": true, "port": 8080, "aliases": []any{"a", "b"}}},
		{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}},
		{Type: "File", Title: "/etc/motd", Parameters: map[string]any{"content": "before"}},
	}
	// Same catalog, different order, and numbers spelled differently.
	reversed := []resourceSpec{
		{Type: "File", Title: "/etc/motd", Parameters: map[string]any{"content": "before"}},
		{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}},
		{Type: "Service", Title: "nginx", Parameters: map[string]any{
			"aliases": []any{"a", "b"}, "port": 8080.0, "enable": true, "ensure": "running"}},
	}
	candidateForward := []resourceSpec{
		{Type: "Service", Title: "nginx", Parameters: map[string]any{
			"ensure": "stopped", "enable": true, "port": 8080, "aliases": []any{"a", "b"}}},
		{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}},
		{Type: "File", Title: "/etc/motd", Parameters: map[string]any{"content": "after"}},
	}
	candidateReversed := []resourceSpec{
		{Type: "File", Title: "/etc/motd", Parameters: map[string]any{"content": "after"}},
		{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}},
		{Type: "Service", Title: "nginx", Parameters: map[string]any{
			"aliases": []any{"a", "b"}, "port": 8080.0, "enable": true, "ensure": "stopped"}},
	}
	edgesForward := []edgeSpec{
		{SourceType: "Notify", SourceTitle: "hello", TargetType: "Service", TargetTitle: "nginx"},
		{SourceType: "Service", SourceTitle: "nginx", TargetType: "File", TargetTitle: "/etc/motd"},
	}
	edgesReversed := []edgeSpec{edgesForward[1], edgesForward[0]}

	// Both runs go through ONE harness, so the two service endpoints, and
	// therefore the run-level provenance the report records, are identical.
	// Two harnesses would listen on different random ports, which is
	// genuinely different configuration and would make the comparison test
	// the wrong thing.
	h := newHarness(t)
	h.pdb.impactCertnames = []string{"db-02.example.test", "db-01.example.test"}

	seed := func(baseline, candidate []resourceSpec, edges []edgeSpec, order []string) artifacts {
		t.Helper()
		for _, certname := range []string{"web-01.example.test", "web-02.example.test"} {
			h.pdb.factsets[certname] = pdbFactset(certname, true)
			h.pdb.catalogs[certname] = pdbCatalog(certname, "production", baseline, edges)
			h.compiler.catalogs[certname] = compilerCatalog(certname, "feature-123", candidate, edges)
		}
		entries := make([]string, 0, len(order))
		for _, certname := range order {
			entries = append(entries, target(certname))
		}
		h.writeConfigs(t, targetsYAML(impactDefaults, entries...))

		textOut := h.path("report.txt")
		got := h.compare(t, "--text-out", textOut)
		if got.code != exitcode.Success {
			t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
		}
		return got
	}

	first := seed(forward, candidateForward, edgesForward,
		[]string{"web-01.example.test", "web-02.example.test"})
	second := seed(reversed, candidateReversed, edgesReversed,
		[]string{"web-02.example.test", "web-01.example.test"})

	// Sanity: an empty or difference-free report would make the
	// comparison below vacuous.
	if !strings.Contains(first.text, `Service[nginx] ensure: "running" -> "stopped"`) {
		t.Fatalf("the run produced no differences to compare deterministically:\n%s", first.text)
	}

	for _, format := range []struct {
		name string
		a, b string
	}{
		{"text", first.text, second.text},
		{"json", first.json, second.json},
		{"html", first.html, second.html},
	} {
		if format.a != format.b {
			t.Errorf("the %s artifact is not byte-identical across two runs over identical inputs\nfirst:\n%s\nsecond:\n%s",
				format.name, format.a, format.b)
		}
	}
}

// The claim the byte-identical test above cannot make.
//
// That test freezes the clock, because the harness does, and every
// artifact then matches to the byte. Production does not freeze the
// clock: two runs are never at the same instant, so the documentation's
// old promise that identical catalogs produce identical artifact bytes
// was true only of the test harness. What is actually reproducible is
// the comparison, and this runs the whole pipeline twice under two
// different clocks to say so: the artifacts differ, and everything in
// them except the invocation metadata does not.
func TestAcceptance_TheComparisonIsReproducibleAcrossClocks(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname,
		[]resourceSpec{{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "running"}}},
		[]resourceSpec{{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "stopped"}}},
		baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target(certname)))

	run := func(at time.Time) artifacts {
		t.Helper()
		previous := clock
		clock = func() time.Time { return at }
		defer func() { clock = previous }()
		got := h.compare(t)
		if got.code != exitcode.Success {
			t.Fatalf("exit = %d:\n%s", got.code, got.stderr)
		}
		return got
	}

	first := run(time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC))
	second := run(time.Date(2026, 11, 2, 17, 43, 11, 0, time.UTC))

	if first.json == second.json {
		t.Fatal("two runs at different instants produced identical documents, so this asserts nothing")
	}

	firstSemantic := semanticProjection(t, first.json)
	secondSemantic := semanticProjection(t, second.json)
	if firstSemantic != secondSemantic {
		t.Errorf("the comparison did not reproduce across two clocks:\n%s\n%s", firstSemantic, secondSemantic)
	}
	if !strings.Contains(firstSemantic, `"ensure"`) {
		t.Fatalf("the projection kept no comparison to compare:\n%s", firstSemantic)
	}
}

// semanticProjection decodes a stored document and re-encodes it without
// its invocation metadata, which is what report.SemanticProjection does
// for a live result and what `jq -S 'del(.invocation)'` does for a
// reader with two files.
func semanticProjection(t *testing.T, document string) string {
	t.Helper()
	var decoded model.Result
	if err := json.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatal(err)
	}
	projected, err := report.SemanticProjection(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(projected)
}
