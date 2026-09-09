package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/snapshot"
)

func contentHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestAcceptance_InvalidChecksumNeverBecomesPublishedEvidence(t *testing.T) {
	h := newHarness(t)
	name := "web-01.example.test"
	h.seedTarget(name,
		[]resourceSpec{{Type: "File", Title: "/app", Parameters: map[string]any{"checksum_value": contentHash("old")}}},
		[]resourceSpec{{Type: "File", Title: "/app", Parameters: map[string]any{"checksum_value": "audit-new-secret"}}}, nil)
	h.writeConfigs(t, targetsYAML(defaultDefaults, target(name)))
	got := h.compare(t, "--debug")
	if got.code != exitcode.OperationalError || !strings.Contains(got.json, `"state":"content_indeterminate"`) {
		t.Fatalf("invalid checksum accepted: %s", got.stdout)
	}
	for _, artifact := range got.all() {
		if strings.Contains(artifact, "audit-new-secret") {
			t.Fatal("invalid checksum disclosed")
		}
	}
}

func TestAcceptance_FailedV3StillReportsEffects(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		h := newHarness(t)
		name := "web-01.example.test"
		h.seedTarget(name, baseResources(), baseResources(), baseEdges())
		h.compiler.v3Status = 500
		defaults := v3Defaults
		if fallback {
			defaults = strings.Replace(fileBaselineDefaults(), "catalog_api: v4", "catalog_api: v4\n    allow_v3_fallback: true", 1)
			h.compiler.v4Status = 404
		}
		h.writeConfigs(t, targetsYAML(defaults, target(name)))
		freezeBaseline(t, h, name)
		got := h.compare(t)
		if got.code == exitcode.Success || !strings.Contains(got.json, `"effective_api":"v3"`) {
			t.Fatalf("failed v3 provenance lost: %s", got.stdout)
		}
		for _, artifact := range []string{got.stdout, got.json, got.html} {
			if !strings.Contains(artifact, "Persistence warning") {
				t.Fatal("failed v3 warning lost")
			}
		}
		out, errout, code := captureRun(t, []string{"capture", "catalog", "--targets", h.path("targets.yaml"), "--services", h.path("services.yaml"), "--environment", "production"})
		if code == exitcode.Success || !strings.Contains(out, "effective catalog API v3") || !strings.Contains(errout, "Persistence warning") {
			t.Fatalf("failed capture effects hidden: %s %s", out, errout)
		}
	}
}

func fileBaselineDefaults() string {
	return strings.Replace(defaultDefaults, "  baseline:\n    source: puppetdb", "  baseline:\n    source: file\n    file: snapshots/{certname}.json", 1)
}

func freezeBaseline(t *testing.T, h *harness, name string) {
	t.Helper()
	payload, err := json.Marshal(h.pdb.catalogs[name])
	if err != nil {
		t.Fatal(err)
	}
	sum, err := snapshot.Checksum(payload)
	if err != nil {
		t.Fatal(err)
	}
	env := snapshot.Envelope{FormatVersion: snapshot.FormatVersion, Kind: snapshot.KindCatalog, Target: name, Source: snapshot.Source{Kind: "compiler"}, CapturedAt: fixedTimestamp.Format(time.RFC3339), RequestedEnvironment: "production", CompilerAPIVersion: snapshot.CompilerAPIv4, InputFactsetIdentity: "fixture-facts", PayloadChecksum: sum, Payload: payload}
	if err := snapshot.Write(h.path("snapshots/"+name+".json"), env, false); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptance_UnchangedSourceUsesHistoricalEvidence(t *testing.T) {
	for _, evidence := range []string{"missing", "checksum", "static"} {
		t.Run(evidence, func(t *testing.T) {
			h := newHarness(t)
			name := "web-01.example.test"
			params := map[string]any{"source": "puppet:///modules/app/config"}
			before := map[string]any{"source": params["source"]}
			if evidence == "checksum" {
				before["checksum_value"] = contentHash("old module content")
			}
			h.seedTarget(name, []resourceSpec{{Type: "File", Title: "/app", Parameters: before}}, []resourceSpec{{Type: "File", Title: "/app", Parameters: params}}, nil)
			if evidence == "static" {
				h.pdb.catalogs[name].(map[string]any)["metadata"] = map[string]any{"/app": map[string]any{"type": "file", "checksum": map[string]any{"type": "sha256", "value": "{sha256}" + contentHash("old module content")}}}
			}
			h.compiler.fileContent["modules/app/config"] = "new module content"
			h.writeConfigs(t, targetsYAML(defaultDefaults, target(name)))
			got := h.compare(t, "--debug")
			var report model.Result
			if err := json.Unmarshal([]byte(got.json), &report); err != nil {
				t.Fatal(err)
			}
			change := report.Targets[0].NodeDiff.ResourceChanges[0].FileContent
			if evidence == "missing" {
				if got.code != exitcode.OperationalError || change.State != model.FileContentIndeterminate {
					t.Fatalf("historical evidence silently invented: %s", got.stdout)
				}
			} else if got.code != exitcode.Success || change.State != model.FileContentChanged || !change.Before.Verified {
				t.Fatalf("module edit missed: %s", got.stdout)
			}
			if change.Before.Context.Environment != "production" || change.After.Context.Environment != "feature-123" {
				t.Fatal("wrong evidence contexts")
			}
			for _, artifact := range got.all() {
				if strings.Contains(artifact, "old module content") || strings.Contains(artifact, "new module content") {
					t.Fatal("managed bytes published")
				}
			}
		})
	}
}

func TestAcceptance_CaptureRetainsHistoricalSourceDigest(t *testing.T) {
	h := newHarness(t)
	name := "web-01.example.test"
	resources := []resourceSpec{{Type: "File", Title: "/app", Parameters: map[string]any{"source": "puppet:///modules/app/config"}}}
	h.pdb.factsets[name] = pdbFactset(name, true)
	h.compiler.catalogs[name] = compilerCatalog(name, "production", resources, nil)
	h.compiler.fileContent["modules/app/config"] = "historical-bytes"
	defaults := strings.Replace(defaultDefaults, "  baseline:\n    source: puppetdb", "  baseline:\n    source: file\n    file: snapshots/{certname}.json", 1)
	h.writeConfigs(t, targetsYAML(defaults, target(name)))
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	code := run([]string{"capture", "catalog", "--targets", h.path("targets.yaml"), "--services", h.path("services.yaml"), "--environment", "production"}, out, out)
	if code != exitcode.Success {
		data, _ := os.ReadFile(out.Name())
		t.Fatalf("capture: %s", data)
	}
	env, err := snapshot.Load(h.path("snapshots/" + name + ".json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env.Payload), contentHash("historical-bytes")) {
		t.Fatal("capture omitted digest")
	}
	h.compiler.catalogs[name] = compilerCatalog(name, "feature-123", resources, nil)
	h.compiler.fileContent["modules/app/config"] = "current-bytes"
	got := h.compare(t)
	if got.code != exitcode.Success || !strings.Contains(got.json, `"state":"changed"`) || !strings.Contains(got.json, `"source":"captured_digest"`) {
		t.Fatalf("snapshot comparison: %s", got.stdout)
	}
}

func TestAcceptance_V3PuppetDBPolicyFailsBeforeNetwork(t *testing.T) {
	for _, candidate := range []string{"catalog_api: v3", "catalog_api: v4\n    allow_v3_fallback: true"} {
		h := newHarness(t)
		h.writeConfigs(t, targetsYAML(strings.Replace(defaultDefaults, "catalog_api: v4", candidate, 1), target("web-01.example.test")))
		got := h.compare(t)
		if got.code != exitcode.OperationalError || h.compiler.count() != 0 || h.pdb.count() != 0 || !strings.Contains(got.stderr, "baseline.source must be file") {
			t.Fatalf("unsafe v3 policy reached network: %s", got.stderr)
		}
	}
}

func TestAcceptance_CaptureReportsEffectiveV3AndPersistence(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		h := newHarness(t)
		name := "web-01.example.test"
		h.pdb.factsets[name] = pdbFactset(name, true)
		h.compiler.catalogs[name] = compilerCatalog(name, "production", baseResources(), baseEdges())
		defaults := v3Defaults
		if fallback {
			defaults = strings.Replace(fileBaselineDefaults(), "catalog_api: v4", "catalog_api: v4\n    allow_v3_fallback: true", 1)
			h.compiler.v4Status = 404
		}
		h.writeConfigs(t, targetsYAML(defaults, target(name)))
		out, errout, code := captureRun(t, []string{"capture", "catalog", "--targets", h.path("targets.yaml"), "--services", h.path("services.yaml"), "--environment", "production"})
		if code != exitcode.Success || !strings.Contains(out, "effective catalog API v3") || !strings.Contains(errout, "Persistence warning") {
			t.Fatalf("capture hid v3 effects: %s %s", out, errout)
		}
		env, err := snapshot.Load(h.path("snapshots/" + name + ".json"))
		if err != nil || env.CompilerAPIVersion != snapshot.CompilerAPIv3 {
			t.Fatalf("capture API attribution: %v %+v", err, env)
		}
	}
}

func TestAcceptance_StaticCompilerMetadataSurvivesCaptureAndReuse(t *testing.T) {
	h := newHarness(t)
	name := "web-01.example.test"
	data, err := os.ReadFile("../../internal/compiler/testdata/static-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]any
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	catalog["environment"] = "production"
	h.pdb.factsets[name] = pdbFactset(name, true)
	h.compiler.catalogs[name] = catalog
	h.writeConfigs(t, targetsYAML(fileBaselineDefaults(), target(name)))
	_, stderr, code := captureRun(t, []string{"capture", "catalog", "--targets", h.path("targets.yaml"), "--services", h.path("services.yaml"), "--environment", "production"})
	if code != exitcode.Success {
		t.Fatalf("capture static metadata: %s", stderr)
	}
	env, err := snapshot.Load(h.path("snapshots/" + name + ".json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env.Payload), `"recursive_metadata"`) || !strings.Contains(string(env.Payload), `"content_uri"`) {
		t.Fatal("compiler metadata was discarded")
	}
	catalog["environment"] = "feature-123"
	catalog["metadata"].(map[string]any)["/tmp/foo"].(map[string]any)["checksum"].(map[string]any)["value"] = "{sha256}" + contentHash("new static bytes")
	got := h.compare(t)
	if got.code != exitcode.Success || !strings.Contains(got.json, `"state":"changed"`) || !strings.Contains(got.json, `"evidence_source":"static_metadata"`) {
		t.Fatalf("static comparison: %s", got.stdout)
	}
	for _, p := range h.compiler.sortedPaths() {
		if strings.Contains(p, "file_content") {
			t.Fatal("static evidence caused mutable retrieval")
		}
	}
}

func TestAcceptance_DirectoryRecursiveAndMixedContent(t *testing.T) {
	for _, kind := range []string{"directory", "recursive", "recursive reference", "mixed"} {
		h := newHarness(t)
		before := map[string]any{"source": "puppet:///modules/app/old"}
		after := map[string]any{"source": "puppet:///modules/app/old"}
		want := exitcode.OperationalError
		switch kind {
		case "directory":
			before["ensure"] = "directory"
			after["ensure"] = "directory"
		case "recursive", "recursive reference":
			before["recurse"] = true
			after["recurse"] = true
			before["sourceselect"] = "all"
			after["sourceselect"] = "all"
			if kind == "recursive reference" {
				after["source"] = "puppet:///modules/app/new"
				want = exitcode.Success
			}
		case "mixed":
			before = map[string]any{"content": "old inline bytes"}
			want = exitcode.Success
		}
		h.seedTarget("web-01.example.test", []resourceSpec{{Type: "File", Title: "/app", Parameters: before}}, []resourceSpec{{Type: "File", Title: "/app", Parameters: after}}, nil)
		h.compiler.fileContent["modules/app/old"] = "new retrieved bytes"
		h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))
		got := h.compare(t)
		if got.code != want {
			t.Fatalf("%s: %s", kind, got.stdout)
		}
		if kind == "mixed" {
			if !strings.Contains(got.json, `"state":"changed"`) || !strings.Contains(got.json, `"evidence_source":"mixed"`) {
				t.Fatal("mixed evidence missed")
			}
		} else {
			if !strings.Contains(got.stdout, "byte-level content comparison is unsupported") {
				t.Fatal("directory limitation hidden")
			}
			for _, p := range h.compiler.sortedPaths() {
				if strings.Contains(p, "file_content") {
					t.Fatal("directory source fetched as a file")
				}
			}
		}
	}
}
