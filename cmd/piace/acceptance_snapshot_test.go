package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
)

// snapshotDefaults selects file-backed fact and baseline sources, whose
// paths the target file resolves relative to its own directory.
const snapshotDefaults = `  candidate:
    environment: feature-123
    catalog_api: v4
  facts:
    source: file
    file: snapshots/facts/{certname}.json
  baseline:
    source: file
    environment: production
    file: snapshots/catalogs/{certname}.json
  impact_estimate:
    enabled: false
    timeout: 5s
    result_limit: 2
  fail_on_diff: false
`

// TestAcceptance_SnapshotCaptureAndReuse covers the snapshot workflow as
// one story: capture facts from PuppetDB and a catalog from the
// compiler, then run a comparison that consumes both snapshots. This is
// the development-branch workflow snapshots exist for.
func TestAcceptance_SnapshotCaptureAndReuse(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	// The captured catalog is the production-environment one, so a later
	// comparison baselines against it rather than against a live catalog.
	h.compiler.catalogs[certname] = compilerCatalog(certname, "production", baseResources(), baseEdges())

	if err := os.MkdirAll(h.path("snapshots/facts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.path("snapshots/catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.writeConfigs(t, targetsYAML(snapshotDefaults, target(certname)))

	configArgs := []string{"--targets", h.path("targets.yaml"), "--services", h.path("services.yaml")}

	if _, stderr, code := captureRun(t, append([]string{"capture", "facts"}, configArgs...)); code != exitcode.Success {
		t.Fatalf("capture facts exit = %d: %s", code, stderr)
	}
	if _, stderr, code := captureRun(t,
		append(append([]string{"capture", "catalog"}, configArgs...), "--environment", "production")); code != exitcode.Success {
		t.Fatalf("capture catalog exit = %d: %s", code, stderr)
	}

	factSnapshot := h.path("snapshots/facts/" + certname + ".json")
	catalogSnapshot := h.path("snapshots/catalogs/" + certname + ".json")

	// Envelopes, not bare Puppet payloads, with the mandatory
	// catalog-snapshot metadata.
	assertEnvelope(t, factSnapshot, "factset", certname, nil)
	assertEnvelope(t, catalogSnapshot, "catalog", certname,
		[]string{"requested_environment", "compiler_api", "input_factset_identity"})

	// A catalog snapshot's requested_environment must describe the
	// catalog it actually holds. `capture catalog --environment ENV` once
	// recorded ENV in the envelope while requesting the target's own
	// candidate.environment, so a snapshot could claim production while
	// holding a feature-branch catalog — and every later check that
	// trusts that metadata, including the baseline-environment rule a
	// file-backed baseline runs, would validate against the label rather
	// than the catalog. Asserting the two agree is what names that bug if
	// it returns.
	var envelope map[string]any
	if err := json.Unmarshal([]byte(readFile(t, catalogSnapshot)), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["requested_environment"] != "production" {
		t.Errorf("requested_environment = %v, want the --environment value", envelope["requested_environment"])
	}
	payload, _ := envelope["payload"].(map[string]any)
	if payload["environment"] != "production" {
		t.Errorf("the captured catalog's own environment is %v but the envelope claims production: the snapshot is mislabeled",
			payload["environment"])
	}

	// Snapshots are written 0600.
	for _, path := range []string{factSnapshot, catalogSnapshot} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", filepath.Base(path), info.Mode().Perm())
		}
	}

	// Capture must never mutate PuppetDB: only the two documented read
	// paths may have been contacted.
	for _, path := range h.pdb.sortedPaths() {
		if !strings.HasPrefix(path, "/pdb/query/v4/") {
			t.Errorf("capture contacted a non-query PuppetDB path: %s", path)
		}
	}

	// Overwrite protection.
	if _, stderr, code := captureRun(t, append([]string{"capture", "facts"}, configArgs...)); code == exitcode.Success {
		t.Error("capture facts overwrote an existing snapshot without --replace")
	} else if !strings.Contains(stderr, "exists") {
		t.Errorf("overwrite refusal is not explained: %s", stderr)
	}

	// Now compare from the snapshots. The candidate catalog is served for
	// feature-123 and differs from the captured production baseline.
	h.compiler.catalogs[certname] = compilerCatalog(certname, "feature-123", []resourceSpec{
		{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}},
		{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "stopped", "enable": true}},
	}, baseEdges())

	got := h.compare(t)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.json, `"kind":"file"`) {
		t.Error("the comparison did not record file-backed source provenance")
	}
	if !strings.Contains(got.stdout, `Service[nginx] ensure: "running" -> "stopped"`) {
		t.Errorf("the snapshot-backed comparison did not find the expected difference:\n%s", got.stdout)
	}
}

// TestAcceptance_InvalidSnapshotIsRejected: a tampered envelope never
// reaches normalization.
func TestAcceptance_InvalidSnapshotIsRejected(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(envelope map[string]any)
		want   string
	}{
		{"payload tampering breaks the checksum", func(e map[string]any) {
			payload, _ := e["payload"].(map[string]any)
			payload["environment"] = "tampered"
		}, "checksum"},
		{"a foreign target identity is rejected", func(e map[string]any) {
			e["target"] = "someone-else.example.test"
		}, "target"},
		{"an unknown format version is rejected", func(e map[string]any) {
			e["format_version"] = 99
		}, "format_version"},
		{"a baseline from another environment is rejected", func(e map[string]any) {
			e["requested_environment"] = "some-other-env"
		}, "environment"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			certname := "web-01.example.test"
			h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
			h.compiler.catalogs[certname] = compilerCatalog(certname, "production", baseResources(), baseEdges())

			if err := os.MkdirAll(h.path("snapshots/facts"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(h.path("snapshots/catalogs"), 0o755); err != nil {
				t.Fatal(err)
			}
			h.writeConfigs(t, targetsYAML(snapshotDefaults, target(certname)))
			configArgs := []string{"--targets", h.path("targets.yaml"), "--services", h.path("services.yaml")}
			if _, stderr, code := captureRun(t, append([]string{"capture", "facts"}, configArgs...)); code != exitcode.Success {
				t.Fatalf("capture facts: %s", stderr)
			}
			if _, stderr, code := captureRun(t,
				append(append([]string{"capture", "catalog"}, configArgs...), "--environment", "production")); code != exitcode.Success {
				t.Fatalf("capture catalog: %s", stderr)
			}

			path := h.path("snapshots/catalogs/" + certname + ".json")
			var envelope map[string]any
			if err := json.Unmarshal([]byte(readFile(t, path)), &envelope); err != nil {
				t.Fatal(err)
			}
			tc.mutate(envelope)
			raw, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, path, raw)

			got := h.compare(t)
			if got.code != exitcode.OperationalError {
				t.Fatalf("exit = %d, want 30 for a tampered snapshot\nstdout:\n%s", got.code, got.stdout)
			}
			if !strings.Contains(strings.ToLower(got.stdout), tc.want) {
				t.Errorf("the diagnostic does not mention %q:\n%s", tc.want, got.stdout)
			}
		})
	}
}

// assertEnvelope checks that path holds a PIACE envelope of the given
// kind and target with every required field populated.
func assertEnvelope(t *testing.T, path, kind, target string, extraFields []string) {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &envelope); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	if envelope["kind"] != kind {
		t.Errorf("%s kind = %v, want %q", path, envelope["kind"], kind)
	}
	if envelope["target"] != target {
		t.Errorf("%s target = %v, want %q", path, envelope["target"], target)
	}
	required := append([]string{"format_version", "source", "captured_at", "payload_checksum", "payload"}, extraFields...)
	for _, field := range required {
		value, ok := envelope[field]
		if !ok || value == nil || value == "" {
			t.Errorf("%s is missing the required field %q", path, field)
		}
	}
	if checksum, _ := envelope["payload_checksum"].(string); !strings.HasPrefix(checksum, "sha256:") {
		t.Errorf("%s payload_checksum = %q, want a sha256: prefix", path, checksum)
	}
}
