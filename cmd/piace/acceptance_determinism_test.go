package main

import (
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
)

// TestAcceptance_ReportsAreByteIdenticalForIdenticalInputs is
// requirements.md 8.6 and design.md's Property 1 checked at the level the
// requirement actually states: "deterministic for identical input
// catalogs and configuration", not merely deterministic when re-rendering
// one in-memory result.
//
// The whole pipeline runs twice, and the second run serves the same
// catalogs with a different resource, parameter, and edge *insertion
// order* — the nondeterminism a real PuppetDB can exhibit and the one a
// re-render test cannot catch. Every artifact must come out
// byte-identical.
//
// The second run also lists the same two targets in the opposite order in
// its target file. That is deliberately a step beyond requirement 8.6's
// "identical configuration": the resolved target *set* is identical, the
// file order is not. It holds because design.md section 9 makes the
// document target-sorted, and asserting it here is what keeps target-file
// order from leaking into a report and making two equivalent CI configs
// produce different artifacts.
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

	// Both runs go through ONE harness, so the two service endpoints —
	// and therefore the run-level provenance the report records — are
	// identical. Two harnesses would listen on different random ports,
	// which is genuinely different configuration and would make the
	// comparison test the wrong thing.
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
