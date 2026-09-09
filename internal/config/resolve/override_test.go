package resolve

import (
	"strings"
	"testing"
)

// overridableTargetsYAML has three targets covering the three shapes an
// override has to reach: no `candidate:` block at all, which inherits
// the defaults; a block naming a different environment; and a block
// naming none, which, because a per-target block replaces the defaults
// wholesale, would otherwise resolve to no environment at all.
const overridableTargetsYAML = `
version: 1
defaults:
  candidate:
    environment: feature-123
    catalog_api: v4
  facts:
    source: puppetdb
  baseline:
    source: puppetdb
    environment: production
targets:
  - certname: web-01.example.test
  - certname: web-02.example.test
    candidate:
      environment: some-other-environment
      catalog_api: v4
  - certname: web-03.example.test
    candidate:
      catalog_api: v3
`

func TestLoadTargetFile_CandidateEnvironmentOverrideWinsEverywhere(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "targets.yaml", overridableTargetsYAML)

	targets, err := LoadTargetFile(path, CommandCompare, Overrides{CandidateEnvironment: "pr-441"})
	if err != nil {
		t.Fatalf("LoadTargetFile: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("len(targets) = %d, want 3", len(targets))
	}
	for _, target := range targets {
		if target.Candidate.Environment != "pr-441" {
			t.Errorf("%s: Candidate.Environment = %q, want %q",
				target.Certname, target.Candidate.Environment, "pr-441")
		}
	}
	// The override replaces the environment and nothing else: a target
	// that chose its own catalog API keeps it.
	if got := targets[2].Candidate.CatalogAPI; got != "v3" {
		t.Errorf("web-03 CatalogAPI = %q, want v3 (the override must not replace the whole block)", got)
	}
}

func TestLoadTargetFile_ZeroOverridesLeavesTheFileAlone(t *testing.T) {
	dir := t.TempDir()
	// web-03 is dropped here: a per-target block that names no
	// environment is invalid without an override, which is the subject of
	// TestLoadTargetFile_OverrideSuppliesAnOtherwiseMissingEnvironment.
	path := writeTempFile(t, dir, "targets.yaml",
		strings.TrimSuffix(overridableTargetsYAML, "  - certname: web-03.example.test\n    candidate:\n      catalog_api: v3\n"))

	targets, err := LoadTargetFile(path, CommandCompare, Overrides{})
	if err != nil {
		t.Fatalf("LoadTargetFile: %v", err)
	}
	if targets[0].Candidate.Environment != "feature-123" {
		t.Errorf("web-01 Candidate.Environment = %q, want feature-123", targets[0].Candidate.Environment)
	}
	if targets[1].Candidate.Environment != "some-other-environment" {
		t.Errorf("web-02 Candidate.Environment = %q, want some-other-environment", targets[1].Candidate.Environment)
	}
}

// A target file that names no candidate environment anywhere is invalid
// on its own and valid under an override: the override is applied before
// resolution, so the required-field rule sees the effective value. This
// is what lets a committed target file leave the per-pipeline value out
// entirely rather than carry a placeholder for CI to rewrite.
func TestLoadTargetFile_OverrideSuppliesAnOtherwiseMissingEnvironment(t *testing.T) {
	const noEnvironmentYAML = `
version: 1
defaults:
  candidate:
    catalog_api: v4
  facts:
    source: puppetdb
  baseline:
    source: puppetdb
    environment: production
targets:
  - certname: web-01.example.test
`
	dir := t.TempDir()
	path := writeTempFile(t, dir, "targets.yaml", noEnvironmentYAML)

	if _, err := LoadTargetFile(path, CommandCompare, Overrides{}); err == nil {
		t.Fatal("expected candidate.environment to be required without an override, got nil")
	} else if !strings.Contains(err.Error(), "candidate.environment is required") {
		t.Errorf("error = %v, want it to name the missing candidate.environment", err)
	}

	targets, err := LoadTargetFile(path, CommandCompare, Overrides{CandidateEnvironment: "pr-441"})
	if err != nil {
		t.Fatalf("LoadTargetFile with an override: %v", err)
	}
	if targets[0].Candidate.Environment != "pr-441" {
		t.Errorf("Candidate.Environment = %q, want pr-441", targets[0].Candidate.Environment)
	}
}
