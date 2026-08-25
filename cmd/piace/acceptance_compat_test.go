package main

import (
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

// v3Defaults selects the v3 catalog API for every target.
var v3Defaults = strings.Replace(defaultDefaults, "catalog_api: v4", "catalog_api: v3", 1)

// TestAcceptance_V3WarningAppearsInEveryFormat covers requirements.md
// 2.5-2.6: a v3 request emits a prominent trusted-fact compatibility
// warning in text, JSON, and HTML, and that warning alone does not change
// the exit status (design.md section 10).
func TestAcceptance_V3WarningAppearsInEveryFormat(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(v3Defaults, target("web-01.example.test")))

	textOut := h.path("report.txt")
	got := h.compare(t, "--text-out", textOut)

	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0: a v3 warning alone must not change exit status\nstderr:\n%s", got.code, got.stderr)
	}
	if h.compiler.count() != 1 || h.compiler.sortedPaths()[0] != "/puppet/v3/catalog/web-01.example.test" {
		t.Errorf("compiler paths = %v, want only the v3 catalog endpoint", h.compiler.sortedPaths())
	}

	// The exact constant must appear in all three formats. The HTML is
	// checked for a distinctive fragment rather than the whole string,
	// since html/template escapes the apostrophes in it.
	if !strings.Contains(got.text, model.V3TrustedFactWarning) {
		t.Error("the text report is missing the v3 trusted-fact warning")
	}
	if !strings.Contains(got.json, "trusted-fact compatibility warning") {
		t.Error("the JSON report is missing the v3 trusted-fact warning")
	}
	if !strings.Contains(got.html, "Trusted-fact compatibility warning") ||
		!strings.Contains(got.html, "can observe the catalog-reader") {
		t.Error("the HTML report does not visibly mark the v3 trusted-fact warning (requirements.md 8.5)")
	}
}

// TestAcceptance_V4ToV3Fallback covers design.md section 3.1's fallback
// conditions from both sides: a verified-unsupported v4 response falls
// back only when the target opted in, and produces the same
// non-suppressible warning.
func TestAcceptance_V4ToV3Fallback(t *testing.T) {
	cases := []struct {
		name          string
		v4Status      int
		allowFallback bool
		wantCode      exitcode.Code
		wantFellBack  bool
	}{
		{"404 with fallback enabled falls back to v3", 404, true, exitcode.Success, true},
		{"501 with fallback enabled falls back to v3", 501, true, exitcode.Success, true},
		{"404 without opt-in is a compilation failure", 404, false, exitcode.CompilationFailure, false},
		{"500 never falls back even with the opt-in", 500, true, exitcode.CompilationFailure, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
			h.compiler.v4Status = tc.v4Status

			defaults := defaultDefaults
			if tc.allowFallback {
				defaults = strings.Replace(defaults, "    catalog_api: v4",
					"    catalog_api: v4\n    allow_v3_fallback: true", 1)
			}
			h.writeConfigs(t, targetsYAML(defaults, target("web-01.example.test")))

			got := h.compare(t)
			if got.code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s", got.code, tc.wantCode, got.stdout)
			}
			if tc.wantFellBack {
				if !strings.Contains(got.json, `"fell_back_from_v4":true`) {
					t.Error("candidate provenance does not record the v4-to-v3 fallback")
				}
				if !strings.Contains(got.json, "trusted-fact compatibility warning") {
					t.Error("a fallback catalog did not carry the v3 trusted-fact warning")
				}
				if !contains(h.compiler.sortedPaths(), "/puppet/v3/catalog/web-01.example.test") {
					t.Errorf("compiler paths = %v, want the v3 endpoint after fallback", h.compiler.sortedPaths())
				}
			} else if contains(h.compiler.sortedPaths(), "/puppet/v3/catalog/web-01.example.test") {
				t.Errorf("compiler paths = %v, want no v3 request", h.compiler.sortedPaths())
			}
		})
	}
}

// TestAcceptance_V4WithoutTrustedFactSourceFailsCompilation covers
// design.md section 5's rule that PIACE fails compilation rather than
// inventing trusted facts, and its escape hatch: an explicit
// trusted_facts_compiler_lookup opt-in.
func TestAcceptance_V4WithoutTrustedFactSourceFailsCompilation(t *testing.T) {
	t.Run("no trusted fact and no lookup opt-in fails", func(t *testing.T) {
		h := newHarness(t)
		h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
		h.pdb.factsets["web-01.example.test"] = pdbFactset("web-01.example.test", false)
		h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))

		got := h.compare(t)
		if got.code != exitcode.CompilationFailure {
			t.Fatalf("exit = %d, want 20\nstdout:\n%s", got.code, got.stdout)
		}
		if h.compiler.count() != 0 {
			t.Errorf("a v4 request was issued with no available trusted-fact source (%d requests)", h.compiler.count())
		}
	})

	t.Run("the compiler-lookup opt-in permits the request", func(t *testing.T) {
		h := newHarness(t)
		h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
		h.pdb.factsets["web-01.example.test"] = pdbFactset("web-01.example.test", false)
		defaults := strings.Replace(defaultDefaults, "    catalog_api: v4",
			"    catalog_api: v4\n    trusted_facts_compiler_lookup: true", 1)
		h.writeConfigs(t, targetsYAML(defaults, target("web-01.example.test")))

		got := h.compare(t)
		if got.code != exitcode.Success {
			t.Fatalf("exit = %d, want 0\nstdout:\n%s", got.code, got.stdout)
		}
		if !strings.Contains(got.json, `"trusted_facts_source":"compiler_lookup"`) {
			t.Error("candidate provenance does not record trusted_facts_source=compiler_lookup")
		}
		if _, sent := h.compiler.v4Bodies[0]["trusted_facts"]; sent {
			t.Error("the v4 request sent a trusted_facts field when relying on compiler lookup")
		}
	})
}

// TestAcceptance_CandidateIdentityAndEnvironmentAreVerified covers
// requirements.md 1.5 and design.md's Property 3: a response naming a
// different certname or environment is never diffed.
func TestAcceptance_CandidateIdentityAndEnvironmentAreVerified(t *testing.T) {
	cases := []struct {
		name        string
		certname    string
		environment string
	}{
		{"certname mismatch", "someone-else.example.test", "feature-123"},
		{"environment mismatch", "web-01.example.test", "production"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
			h.compiler.catalogs["web-01.example.test"] = compilerCatalog(tc.certname, tc.environment, baseResources(), baseEdges())
			h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))

			got := h.compare(t)
			if got.code != exitcode.CompilationFailure {
				t.Fatalf("exit = %d, want 20\nstdout:\n%s", got.code, got.stdout)
			}
		})
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
