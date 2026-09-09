package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
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
