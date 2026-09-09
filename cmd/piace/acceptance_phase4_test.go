package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/limits"
)

// compilerOnlyServices writes a services file naming the compiler and
// nothing else: no `puppetdb:` section at all, not an empty one.
func (h *harness) compilerOnlyServices(t *testing.T) {
	t.Helper()
	writeFixtureFile(t, h.path("services.yaml"), []byte(fmt.Sprintf(`version: 1
compiler:
  endpoint: %s
  ca_bundle: %s
  client_cert: %s
  private_key: %s
`, h.compilerServer.URL, h.fixture.caBundle, h.fixture.clientCert, h.fixture.privateKey)))
}

// puppetDBOnlyServices writes a services file naming PuppetDB and
// nothing else.
func (h *harness) puppetDBOnlyServices(t *testing.T) {
	t.Helper()
	writeFixtureFile(t, h.path("services.yaml"), []byte(fmt.Sprintf(`version: 1
puppetdb:
  endpoint: %s
  ca_bundle: %s
  client_cert: %s
  private_key: %s
`, h.pdbServer.URL, h.fixture.caBundle, h.fixture.clientCert, h.fixture.privateKey)))
}

// TestAcceptance_FileBackedComparisonNeedsNoPuppetDBIdentity: a
// comparison whose facts and baseline are both file-backed, with the
// impact estimate disabled, contacts PuppetDB nowhere and must not
// demand a PuppetDB endpoint, certificate and key to start. Requiring
// them made an operator provision an identity for a service the run
// never speaks to.
//
// The snapshots are captured first, with a full services file, because
// that is the real workflow: capture refreshes them from a job that does
// have a PuppetDB identity, and the comparison job that consumes them
// does not need one.
func TestAcceptance_FileBackedComparisonNeedsNoPuppetDBIdentity(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	h.compiler.catalogs[certname] = compilerCatalog(certname, "production", baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(snapshotDefaults, target(certname)))

	configArgs := []string{"--targets", h.path("targets.yaml"), "--services", h.path("services.yaml")}
	if _, stderr, code := captureRun(t, append([]string{"capture", "facts"}, configArgs...)); code != exitcode.Success {
		t.Fatalf("capture facts: %s", stderr)
	}
	if _, stderr, code := captureRun(t,
		append(append([]string{"capture", "catalog"}, configArgs...), "--environment", "production")); code != exitcode.Success {
		t.Fatalf("capture catalog: %s", stderr)
	}

	h.compilerOnlyServices(t)
	before := h.pdb.count()
	h.compiler.catalogs[certname] = compilerCatalog(certname, "feature-123", baseResources(), baseEdges())

	got := h.compare(t)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0 for a comparison needing no PuppetDB\nstdout:\n%s\nstderr:\n%s",
			got.code, got.stdout, got.stderr)
	}
	if h.pdb.count() != before {
		t.Errorf("the comparison contacted PuppetDB %d times, want none", h.pdb.count()-before)
	}
}

// TestAcceptance_ImpactEstimateStillRequiresPuppetDB is the other half of
// the rule above: the PuppetDB endpoint is optional because of what the
// run does, not because it stopped being validated. Enabling the impact
// estimate over file-backed sources brings the requirement back.
func TestAcceptance_ImpactEstimateStillRequiresPuppetDB(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	h.compiler.catalogs[certname] = compilerCatalog(certname, "production", baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(
		strings.Replace(snapshotDefaults, "  impact_estimate:\n    enabled: false", "  impact_estimate:\n    enabled: true", 1),
		target(certname)))

	h.compilerOnlyServices(t)
	got := h.compare(t)
	if got.code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30 for an enabled impact estimate with no PuppetDB configured", got.code)
	}
	if !strings.Contains(got.stderr, "puppetdb") {
		t.Errorf("the diagnostic does not name the missing puppetdb configuration:\n%s", got.stderr)
	}
}

// factsOnlyDefaults configures a fact destination and nothing else: no
// candidate, no baseline, no impact estimate. `capture facts` reads none
// of those, so it must not require them.
const factsOnlyDefaults = `  facts:
    source: file
    file: snapshots/facts/{certname}.json
`

// TestAcceptance_CaptureFactsNeedsOnlyItsOwnInputs verifies fact capture
// runs from a target file describing only where the factset goes and a
// services file naming only where it comes from.
func TestAcceptance_CaptureFactsNeedsOnlyItsOwnInputs(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.pdb.factsets[certname] = pdbFactset(certname, true)
	h.writeConfigs(t, targetsYAML(factsOnlyDefaults, target(certname)))
	h.puppetDBOnlyServices(t)

	stdout, stderr, code := captureRun(t, []string{"capture", "facts",
		"--targets", h.path("targets.yaml"), "--services", h.path("services.yaml")})
	if code != exitcode.Success {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	assertEnvelope(t, h.path(filepath.Join("snapshots/facts", certname+".json")), "factset", certname, nil)
}

// catalogCaptureDefaults names a catalog API and a snapshot destination
// but no candidate environment: `capture catalog --environment` supplies
// that, and the target's own candidate environment is the environment
// under test in a comparison, a different value entirely.
const catalogCaptureDefaults = `  candidate:
    catalog_api: v4
  facts:
    source: puppetdb
  baseline:
    source: file
    file: snapshots/catalogs/{certname}.json
`

// TestAcceptance_CaptureCatalogNeedsNoCandidateEnvironment verifies
// catalog capture uses its own --environment without an unrelated
// candidate value having to be present to satisfy validation.
func TestAcceptance_CaptureCatalogNeedsNoCandidateEnvironment(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.pdb.factsets[certname] = pdbFactset(certname, true)
	h.compiler.catalogs[certname] = compilerCatalog(certname, "production", baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(catalogCaptureDefaults, target(certname)))

	stdout, stderr, code := captureRun(t, []string{"capture", "catalog",
		"--targets", h.path("targets.yaml"), "--services", h.path("services.yaml"),
		"--environment", "production"})
	if code != exitcode.Success {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	path := h.path(filepath.Join("snapshots/catalogs", certname+".json"))
	assertEnvelope(t, path, "catalog", certname,
		[]string{"requested_environment", "capture", "input_factset_identity"})
}

// TestAcceptance_CompareStillRequiresItsOwnFields keeps the relaxation
// honest in the other direction: presence is scoped per command, so a
// target file good enough for fact capture is still rejected by the
// command that reads the fields it omits.
func TestAcceptance_CompareStillRequiresItsOwnFields(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(factsOnlyDefaults, target(certname)))

	got := h.compare(t)
	if got.code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30 for a target file with no candidate or baseline", got.code)
	}
	for _, want := range []string{"candidate.environment", "baseline.source"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("the diagnostic does not mention %q:\n%s", want, got.stderr)
		}
	}
}

// TestAcceptance_MixedSourcesStillBuildTheirAdapters guards the seam
// between the requirement predicate and the per-target source selection.
// They are two expressions of one rule, and a run whose configuration
// concluded no PuppetDB client was needed, while one target then asks
// for PuppetDB facts, would have no adapter to ask.
func TestAcceptance_MixedSourcesStillBuildTheirAdapters(t *testing.T) {
	h := newHarness(t)
	fileBacked, live := "web-01.example.test", "web-02.example.test"
	for _, certname := range []string{fileBacked, live} {
		h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
		h.compiler.catalogs[certname] = compilerCatalog(certname, "production", baseResources(), baseEdges())
	}
	h.writeConfigs(t, targetsYAML(snapshotDefaults, target(fileBacked), target(live)))

	configArgs := []string{"--targets", h.path("targets.yaml"), "--services", h.path("services.yaml")}
	if _, stderr, code := captureRun(t, append([]string{"capture", "facts"}, configArgs...)); code != exitcode.Success {
		t.Fatalf("capture facts: %s", stderr)
	}
	if _, stderr, code := captureRun(t,
		append(append([]string{"capture", "catalog"}, configArgs...), "--environment", "production")); code != exitcode.Success {
		t.Fatalf("capture catalog: %s", stderr)
	}

	// The second target keeps its snapshots on disk but reads both its
	// facts and its baseline from PuppetDB, so this run needs both a
	// file source and a live adapter at once.
	live2 := fmt.Sprintf(`  - certname: %s
    facts:
      source: puppetdb
    baseline:
      source: puppetdb
      environment: production
`, live)
	h.writeConfigs(t, targetsYAML(snapshotDefaults, target(fileBacked), live2))
	for _, certname := range []string{fileBacked, live} {
		h.compiler.catalogs[certname] = compilerCatalog(certname, "feature-123", baseResources(), baseEdges())
	}

	got := h.compare(t)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0 for a mixed-source comparison\nstdout:\n%s\nstderr:\n%s",
			got.code, got.stdout, got.stderr)
	}
	for _, certname := range []string{fileBacked, live} {
		if !strings.Contains(got.json, certname) {
			t.Errorf("the report does not cover %s", certname)
		}
	}
}

// TestAcceptance_ConfiguredServiceTimeoutIsAccepted covers the services
// file's new per-service `timeout`, end to end through configuration
// resolution and client construction. Its precedence against a caller's
// own deadline is covered as a unit in internal/transport.
func TestAcceptance_ConfiguredServiceTimeoutIsAccepted(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target(certname)))

	services := readFile(t, h.path("services.yaml"))
	writeFixtureFile(t, h.path("services.yaml"),
		[]byte(strings.ReplaceAll(services, "  ca_bundle:", "  timeout: 90s\n  ca_bundle:")))

	if got := h.compare(t); got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0 with a configured service timeout\nstderr:\n%s", got.code, got.stderr)
	}

	writeFixtureFile(t, h.path("services.yaml"),
		[]byte(strings.ReplaceAll(services, "  ca_bundle:", "  timeout: nonsense\n  ca_bundle:")))
	got := h.compare(t)
	if got.code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30 for an invalid service timeout", got.code)
	}
	if !strings.Contains(got.stderr, "timeout") {
		t.Errorf("the diagnostic does not mention the timeout:\n%s", got.stderr)
	}
}

// TestAcceptance_ExplainRefusesToOverwriteItsInput is the acceptance
// case for destination validation: an assessment written over the result
// document it was produced from destroys the record of the comparison,
// and the check runs before anything is sent to an inference service.
func TestAcceptance_ExplainRefusesToOverwriteItsInput(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target(certname)))
	got := h.compare(t)
	if got.code != exitcode.Success {
		t.Fatalf("compare: %s", got.stderr)
	}
	reportPath := h.path("report0.json")
	original := readFile(t, reportPath)

	stub := newInferenceStub(t)
	for name, aiOut := range map[string]string{
		"the same path": reportPath,
		"a symlink to it": func() string {
			link := h.path("linked-report.json")
			if err := os.Symlink(reportPath, link); err != nil {
				t.Skipf("this platform cannot create symlinks: %v", err)
			}
			return link
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PIACE_TEST_INFERENCE_TOKEN", "a-bearer-token")
			previous := inferenceHTTPClient
			inferenceHTTPClient = stub.server.Client()
			t.Cleanup(func() { inferenceHTTPClient = previous })

			_, stderr, code := captureRun(t, []string{"explain",
				"--json-in", reportPath,
				"--services", h.inferenceServices(t, "inference.yaml", stub, ""),
				"--ai-out", aiOut})
			if code != exitcode.OperationalError {
				t.Fatalf("exit = %d, want 30 for an assessment written over its own input", code)
			}
			if !strings.Contains(stderr, "--json-in") {
				t.Errorf("the diagnostic does not name the input it would destroy:\n%s", stderr)
			}
			if len(stub.requests) != 0 {
				t.Errorf("the run sent %d inference requests before failing, want none", len(stub.requests))
			}
			if readFile(t, reportPath) != original {
				t.Error("the result document was modified")
			}
		})
	}
}

// TestAcceptance_CollidingReportDestinationsAreRejected covers the other
// direction: two artifacts of one run cannot share a destination, since
// whichever is written second is the only one that survives.
func TestAcceptance_CollidingReportDestinationsAreRejected(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target(certname)))

	shared := h.path("everything.out")
	_, stderr, code := captureRun(t, []string{"compare",
		"--targets", h.path("targets.yaml"), "--services", h.path("services.yaml"),
		"--json-out", shared, "--html-out", shared, "--text-out", h.path("report.txt")})
	if code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30 for two artifacts sharing a destination", code)
	}
	if !strings.Contains(stderr, "--json-out") || !strings.Contains(stderr, "--html-out") {
		t.Errorf("the diagnostic does not name both artifacts:\n%s", stderr)
	}
	if _, err := os.Stat(shared); err == nil {
		t.Error("a rejected run wrote an artifact anyway")
	}
}

// TestAcceptance_ReportOverASnapshotInputIsRejected: a report written
// over the baseline snapshot it was compared against destroys the input
// of every later run, and the failure would surface at the next
// comparison rather than at this one.
func TestAcceptance_ReportOverASnapshotInputIsRejected(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	h.compiler.catalogs[certname] = compilerCatalog(certname, "production", baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(snapshotDefaults, target(certname)))

	configArgs := []string{"--targets", h.path("targets.yaml"), "--services", h.path("services.yaml")}
	if _, stderr, code := captureRun(t, append([]string{"capture", "facts"}, configArgs...)); code != exitcode.Success {
		t.Fatalf("capture facts: %s", stderr)
	}
	if _, stderr, code := captureRun(t,
		append(append([]string{"capture", "catalog"}, configArgs...), "--environment", "production")); code != exitcode.Success {
		t.Fatalf("capture catalog: %s", stderr)
	}

	baseline := h.path(filepath.Join("snapshots/catalogs", certname+".json"))
	before := readFile(t, baseline)
	_, stderr, code := captureRun(t, append(append([]string{"compare"}, configArgs...), "--json-out", baseline))
	if code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30 for a report written over a snapshot input", code)
	}
	if !strings.Contains(stderr, "--json-out") {
		t.Errorf("the diagnostic does not name the artifact:\n%s", stderr)
	}
	if readFile(t, baseline) != before {
		t.Error("the baseline snapshot was overwritten")
	}
}

// TestAcceptance_CaptureRejectsTwoTargetsSharingOneSnapshot: a defaults
// block naming a literal path rather than a {certname} template makes
// every target write the same file, so all but the last capture is lost.
func TestAcceptance_CaptureRejectsTwoTargetsSharingOneSnapshot(t *testing.T) {
	h := newHarness(t)
	for _, certname := range []string{"web-01.example.test", "web-02.example.test"} {
		h.pdb.factsets[certname] = pdbFactset(certname, true)
	}
	defaults := strings.Replace(factsOnlyDefaults, "snapshots/facts/{certname}.json", "snapshots/facts/all.json", 1)
	h.writeConfigs(t, targetsYAML(defaults, target("web-01.example.test"), target("web-02.example.test")))

	_, stderr, code := captureRun(t, []string{"capture", "facts",
		"--targets", h.path("targets.yaml"), "--services", h.path("services.yaml")})
	if code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30 for two targets sharing one snapshot path", code)
	}
	if !strings.Contains(stderr, "web-01.example.test") || !strings.Contains(stderr, "web-02.example.test") {
		t.Errorf("the diagnostic does not name both targets:\n%s", stderr)
	}
	if _, err := os.Stat(h.path("snapshots/facts/all.json")); err == nil {
		t.Error("a rejected capture wrote a snapshot anyway")
	}
}

// TestAcceptance_ExplainRefusesAnInconsistentDocument: a stored result
// document is an input, and `explain` transmits what it reads to an
// inference service. A document that decodes but describes no coherent
// comparison is refused before anything leaves the building.
func TestAcceptance_ExplainRefusesAnInconsistentDocument(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname, baseResources(), []resourceSpec{
		{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}},
		{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "stopped", "enable": true}},
	}, baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target(certname)))
	if got := h.compare(t); got.code != exitcode.Success {
		t.Fatalf("compare: %s", got.stderr)
	}
	reportPath := h.path("report0.json")

	cases := map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"a document describing no comparison": {
			mutate: func(doc map[string]any) {
				for key := range doc {
					if key != "schema_version" {
						delete(doc, key)
					}
				}
			},
			want: "records no targets",
		},
		"an outcome contradicting its targets": {
			mutate: func(doc map[string]any) { doc["outcome"] = "clean" },
			want:   "reduce to",
		},
		"an aggregate group naming an absent target": {
			mutate: func(doc map[string]any) {
				groups, _ := doc["aggregate"].(map[string]any)["groups"].([]any)
				if len(groups) == 0 {
					t.Fatal("the fixture produced no aggregate group to corrupt")
				}
				group := groups[0].(map[string]any)
				group["certnames"] = []any{"ghost.example.test"}
				refs := group["node_change_refs"].([]any)
				refs[0].(map[string]any)["certname"] = "ghost.example.test"
			},
			want: "does not contain",
		},
		"a forged sensitivity wrapper": {
			mutate: func(doc map[string]any) {
				targets := doc["targets"].([]any)
				changes := targets[0].(map[string]any)["node_diff"].(map[string]any)["resource_changes"].([]any)
				changes[0].(map[string]any)["after"] = map[string]any{"__ptype": "Sensitive", "__pvalue": "s3cr3t"}
			},
			want: "Sensitive wrapper",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal([]byte(readFile(t, reportPath)), &doc); err != nil {
				t.Fatal(err)
			}
			tc.mutate(doc)
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			corrupted := h.path("corrupted.json")
			writeFixtureFile(t, corrupted, raw)

			stub := newInferenceStub(t)
			t.Setenv("PIACE_TEST_INFERENCE_TOKEN", "a-bearer-token")
			previous := inferenceHTTPClient
			inferenceHTTPClient = stub.server.Client()
			t.Cleanup(func() { inferenceHTTPClient = previous })

			_, stderr, code := captureRun(t, []string{"explain",
				"--json-in", corrupted,
				"--services", h.inferenceServices(t, "inference.yaml", stub, ""),
				"--ai-out", h.path("assessment.json")})
			if code != exitcode.OperationalError {
				t.Fatalf("exit = %d, want 30 for an inconsistent stored document", code)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("the diagnostic does not mention %q:\n%s", tc.want, stderr)
			}
			if len(stub.requests) != 0 {
				t.Errorf("the run sent %d inference requests, want none", len(stub.requests))
			}
		})
	}
}

// TestAcceptance_ExplainAcceptsAPartialComparison keeps the check from
// becoming a demand for a perfect run: a report whose targets failed is
// a complete record of a partial comparison, and is exactly the kind of
// run an operator wants explained.
func TestAcceptance_ExplainAcceptsAPartialComparison(t *testing.T) {
	h := newHarness(t)
	good, failing := "web-01.example.test", "web-02.example.test"
	h.seedTarget(good, baseResources(), []resourceSpec{
		{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}},
		{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "stopped", "enable": true}},
	}, baseEdges())
	h.pdb.factsets[failing] = pdbFactset(failing, true)
	h.pdb.catalogs[failing] = pdbCatalog(failing, "production", baseResources(), baseEdges())
	// No compiler catalog for the second target: its candidate compilation
	// fails and the report records why.
	h.writeConfigs(t, targetsYAML(defaultDefaults, target(good), target(failing)))

	got := h.compare(t)
	if got.code != exitcode.CompilationFailure {
		t.Fatalf("exit = %d, want a compilation failure\nstdout:\n%s", got.code, got.stdout)
	}
	stub := newInferenceStub(t)
	assessed := h.explain(t, stub, h.path("report0.json"))
	if assessed.code != exitcode.Success {
		t.Fatalf("explain exit = %d, want 0 for a partial comparison\nstderr:\n%s", assessed.code, assessed.stderr)
	}
	if len(stub.requests) != 1 {
		t.Errorf("the run sent %d inference requests, want 1", len(stub.requests))
	}
}

// TestAcceptance_AdversarialNumberFailsWithADiagnostic: eight bytes of
// JSON (`1e-10000`) expand into ten thousand bytes of exact decimal, and
// a catalog is free to contain as many of them as it likes. The run
// stops with a diagnostic naming the value rather than working through
// them.
func TestAcceptance_AdversarialNumberFailsWithADiagnostic(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	hostile := []resourceSpec{{
		Type:  "Exec",
		Title: "tune",
		Parameters: map[string]any{
			"threshold": json.RawMessage("1e-10000"),
		},
	}}
	h.pdb.factsets[certname] = pdbFactset(certname, true)
	h.pdb.catalogs[certname] = pdbCatalog(certname, "production", baseResources(), baseEdges())
	h.compiler.catalogs[certname] = compilerCatalog(certname, "feature-123", hostile, nil)
	h.writeConfigs(t, targetsYAML(defaultDefaults, target(certname)))

	got := h.compare(t)
	if got.code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30 for a numeric value past the budget\nstdout:\n%s", got.code, got.stdout)
	}
	if !strings.Contains(got.stdout, "exponent") {
		t.Errorf("the diagnostic does not explain what was refused:\n%s", got.stdout)
	}
}

// TestAcceptance_OversizedSnapshotFailsBeforeItIsRead: a snapshot is
// PIACE's own format, which is not a promise about the size of the file
// a repository happens to have at that path.
func TestAcceptance_OversizedSnapshotFailsBeforeItIsRead(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(snapshotDefaults, target(certname)))
	if err := os.MkdirAll(h.path("snapshots/facts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.path("snapshots/catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A file one byte past the limit, written without going through the
	// snapshot writer, which is what a truncated download or a wrong path
	// produces in practice.
	path := h.path(filepath.Join("snapshots/facts", certname+".json"))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(int64(limits.Snapshot) + 1); err != nil {
		f.Close()
		t.Skipf("this filesystem cannot make a sparse file that large: %v", err)
	}
	f.Close()

	got := h.compare(t)
	if got.code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30 for an oversized snapshot", got.code)
	}
	if !strings.Contains(got.stdout, "limit") {
		t.Errorf("the diagnostic does not name the limit:\n%s", got.stdout)
	}
}

// TestAcceptance_LargeComparisonProducesABoundedRequest is the end-to-end
// half of the request budget: a comparison whose evidence is far larger
// than one inference request may carry still produces a request inside
// the budget, and the artifact says how much of the evidence went with
// it instead of reading as a full review.
//
// The values are large rather than numerous on purpose. Group and node
// counts are already budgeted before this point; the case the byte
// budget exists for is a handful of groups carrying values nobody sized.
func TestAcceptance_LargeComparisonProducesABoundedRequest(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"

	const (
		resources = 6
		valueSize = 200 * 1024
	)
	baseline := make([]resourceSpec, 0, resources)
	candidate := make([]resourceSpec, 0, resources)
	for i := 0; i < resources; i++ {
		title := fmt.Sprintf("payload-%02d", i)
		baseline = append(baseline, resourceSpec{
			Type: "Notify", Title: title,
			Parameters: map[string]any{"message": strings.Repeat("b", valueSize)},
		})
		candidate = append(candidate, resourceSpec{
			Type: "Notify", Title: title,
			Parameters: map[string]any{"message": strings.Repeat("c", valueSize)},
		})
	}
	h.seedTarget(certname, baseline, candidate, nil)
	h.writeConfigs(t, targetsYAML(defaultDefaults, target(certname)))

	stub := newInferenceStub(t)
	got := h.explain(t, stub, h.storedReport(t))

	if got.code != exitcode.Success {
		t.Fatalf("explain exited %d, want %d\nstderr: %s", got.code, exitcode.Success, got.stderr)
	}
	if stub.count() != 1 {
		t.Fatalf("explain made %d inference requests, want exactly 1", stub.count())
	}
	if sent := len(stub.requests[0]); sent > limits.InferenceRequest {
		t.Errorf("the request body is %d bytes, past the %d-byte budget", sent, limits.InferenceRequest)
	}
	if !strings.Contains(stub.requests[0], "omitted: inference request size budget") {
		t.Error("the request sheds evidence without saying so in the payload")
	}

	var assessment struct {
		GroupsTotal     int  `json:"groups_total"`
		GroupsAssessed  int  `json:"groups_assessed"`
		GroupsTruncated bool `json:"groups_truncated"`
		ValuesOmitted   int  `json:"values_omitted"`
		Groups          []struct {
			ID string `json:"id"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(got.assessment), &assessment); err != nil {
		t.Fatalf("decoding the change assessment: %v", err)
	}
	if assessment.GroupsTotal != resources {
		t.Errorf("groups_total = %d, want %d", assessment.GroupsTotal, resources)
	}
	if assessment.ValuesOmitted == 0 {
		t.Error("the assessment claims a full review of evidence the request could not carry")
	}
	if assessment.ValuesOmitted > assessment.GroupsAssessed {
		t.Errorf("values_omitted = %d over groups_assessed = %d; a group that was never sent is counted as one reviewed without its values",
			assessment.ValuesOmitted, assessment.GroupsAssessed)
	}
	if len(assessment.Groups) != assessment.GroupsAssessed {
		t.Errorf("the artifact carries %d group judgements for %d assessed groups",
			len(assessment.Groups), assessment.GroupsAssessed)
	}
	if ids := groupIDsIn(stub.requests[0]); len(ids) != assessment.GroupsAssessed {
		t.Errorf("the request carried %d groups, the artifact reports %d assessed", len(ids), assessment.GroupsAssessed)
	}
}
