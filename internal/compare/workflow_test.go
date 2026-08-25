package compare

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
)

// newWorkflow builds a Workflow whose fact source, baseline source, and
// compiler are all fakes, with a fixed clock.
func newWorkflow(facts fakeFactSource, baseline fakeCatalogSource, compiler fakeCompiler) *Workflow {
	return &Workflow{
		PuppetDBFacts:    facts,
		FileFacts:        facts,
		PuppetDBBaseline: baseline,
		FileBaseline:     baseline,
		Compiler:         compiler,
		ToolVersion:      "test",
		Now:              fixedClock,
	}
}

// TestRun_CleanComparison verifies the happy path end to end: provenance
// from all three sources, a node diff with no difference, and a clean
// exit-0 outcome.
func TestRun_CleanComparison(t *testing.T) {
	resources := []testResource{{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}}}
	w := newWorkflow(
		fakeFactSource{factset: puppetdb.Factset{Hash: "sha256:facts", Producer: "puppet.example.test"}},
		fakeCatalogSource{resources: resources},
		fakeCompiler{resources: resources},
	)

	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{testTarget("web-01.example.test")}})

	if result.Outcome != exitcode.OutcomeClean || result.ExitCode != 0 {
		t.Fatalf("Outcome/ExitCode = %q/%d, want clean/0", result.Outcome, result.ExitCode)
	}
	target := findTarget(t, result, "web-01.example.test")
	if target.Facts == nil || target.Baseline == nil || target.Candidate == nil {
		t.Fatalf("provenance incomplete: facts=%v baseline=%v candidate=%v", target.Facts, target.Baseline, target.Candidate)
	}
	if target.Config == nil || target.Config.Candidate["environment"] != "feature-123" {
		t.Errorf("Config provenance = %+v", target.Config)
	}
	if target.NodeDiff == nil || target.NodeDiff.HasDifference {
		t.Errorf("NodeDiff = %+v, want a difference-free diff", target.NodeDiff)
	}
	if result.Invocation.TimestampUTC != "2026-08-25T12:00:00Z" {
		t.Errorf("TimestampUTC = %q", result.Invocation.TimestampUTC)
	}
}

// TestRun_RecordsServiceProvenance verifies design.md section 9's
// run-level "resolved safe provenance": the report names the two service
// authorities it was allowed to reach, and no TLS file path.
func TestRun_RecordsServiceProvenance(t *testing.T) {
	resources := []testResource{{Type: "Notify", Title: "hello"}}
	w := newWorkflow(fakeFactSource{}, fakeCatalogSource{resources: resources}, fakeCompiler{resources: resources})

	compilerURL, err := url.Parse("https://compiler.example.test:8140")
	if err != nil {
		t.Fatal(err)
	}
	puppetDBURL, err := url.Parse("https://puppetdb.example.test:8081")
	if err != nil {
		t.Fatal(err)
	}

	result := w.Run(context.Background(), resolve.Config{
		Targets: []resolve.Target{testTarget("web-01.example.test")},
		Services: resolve.Services{
			Compiler: resolve.Endpoint{URL: compilerURL, ClientCert: "/etc/piace/reader.pem", PrivateKey: "/etc/piace/reader.key"},
			PuppetDB: resolve.Endpoint{URL: puppetDBURL, ClientCert: "/etc/piace/reader.pem", PrivateKey: "/etc/piace/reader.key"},
		},
	})

	services := result.Invocation.Services
	if services == nil {
		t.Fatal("Invocation.Services is nil")
	}
	if services.Compiler != "compiler.example.test:8140" || services.PuppetDB != "puppetdb.example.test:8081" {
		t.Errorf("Services = %+v", services)
	}

	rendered, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"reader.pem", "reader.key", "/etc/piace"} {
		if strings.Contains(string(rendered), secret) {
			t.Errorf("the result document leaked the TLS path %q", secret)
		}
	}
}

// TestRun_ParameterChangeAggregatesAcrossTargets verifies a difference
// reaches both the node diff and the aggregate view, and that
// fail_on_diff drives the outcome.
func TestRun_ParameterChangeAggregatesAcrossTargets(t *testing.T) {
	w := newWorkflow(
		fakeFactSource{},
		fakeCatalogSource{resources: []testResource{{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "stopped"}}}},
		fakeCompiler{resources: []testResource{{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "running"}}}},
	)
	failOnDiff := func(target *resolve.Target) { target.FailOnDiff = true }

	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{
		testTarget("web-02.example.test", failOnDiff),
		testTarget("web-01.example.test", failOnDiff),
	}})

	if result.Outcome != exitcode.OutcomePolicyDisallowedDifference || result.ExitCode != 10 {
		t.Fatalf("Outcome/ExitCode = %q/%d, want policy_disallowed_difference/10", result.Outcome, result.ExitCode)
	}
	// design.md section 9: the document is target-sorted regardless of
	// target-file order.
	if result.Targets[0].Certname != "web-01.example.test" || result.Targets[1].Certname != "web-02.example.test" {
		t.Errorf("target order = %q, %q", result.Targets[0].Certname, result.Targets[1].Certname)
	}
	if len(result.Aggregate.Groups) != 1 {
		t.Fatalf("Aggregate.Groups = %+v, want one group", result.Aggregate.Groups)
	}
	group := result.Aggregate.Groups[0]
	if len(group.Certnames) != 2 {
		t.Errorf("group certnames = %v, want both targets", group.Certnames)
	}
	if group.Key.Identity == nil || group.Key.Identity.Title != "nginx" || group.Key.Parameter != "ensure" {
		t.Errorf("group key = %+v", group.Key)
	}
}

// TestRun_TargetFailureDoesNotStopOtherTargets covers design.md's
// Architecture rule: one target's error is captured in its own result and
// processing continues, and requirements.md 10.5 — the run is never clean
// while a failure is reported.
func TestRun_TargetFailureDoesNotStopOtherTargets(t *testing.T) {
	resources := []testResource{{Type: "Notify", Title: "hello"}}
	failing := &model.Diagnostic{
		Severity:  model.SeverityError,
		Operation: model.OperationLoadBaseline,
		Message:   "no baseline catalog stored for this certname",
	}

	w := &Workflow{
		PuppetDBFacts:    fakeFactSource{},
		FileFacts:        fakeFactSource{},
		PuppetDBBaseline: fakeCatalogSource{diag: failing},
		FileBaseline:     fakeCatalogSource{resources: resources},
		Compiler:         fakeCompiler{resources: resources},
		ToolVersion:      "test",
		Now:              fixedClock,
	}

	fileBaseline := func(target *resolve.Target) {
		target.Baseline = resolve.Baseline{Source: config.BaselineSourceFile, Environment: "production", File: "/snapshots/web-01.json"}
	}

	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{
		testTarget("broken.example.test"),
		testTarget("healthy.example.test", fileBaseline),
	}})

	if result.Outcome != exitcode.OutcomeOperationalError || result.ExitCode != 30 {
		t.Fatalf("Outcome/ExitCode = %q/%d, want operational_error/30", result.Outcome, result.ExitCode)
	}
	broken := findTarget(t, result, "broken.example.test")
	if broken.NodeDiff != nil {
		t.Errorf("failed target produced a node diff: %+v", broken.NodeDiff)
	}
	if len(broken.Diagnostics) != 1 || broken.Diagnostics[0].Operation != model.OperationLoadBaseline {
		t.Errorf("broken.Diagnostics = %+v", broken.Diagnostics)
	}
	healthy := findTarget(t, result, "healthy.example.test")
	if healthy.Outcome != exitcode.OutcomeClean || healthy.NodeDiff == nil {
		t.Errorf("healthy target = %+v", healthy)
	}
}

// TestRun_CompilationFailureIsNotOperational verifies the taxonomy split:
// a rejected compiler request is exit 20, not exit 30.
func TestRun_CompilationFailureIsNotOperational(t *testing.T) {
	w := newWorkflow(
		fakeFactSource{},
		fakeCatalogSource{resources: []testResource{{Type: "Notify", Title: "hello"}}},
		fakeCompiler{diag: &model.Diagnostic{
			Severity:  model.SeverityError,
			Operation: model.OperationRequestCandidate,
			Message:   "compiler rejected the catalog request",
		}},
	)

	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{testTarget("web-01.example.test")}})

	if result.Outcome != exitcode.OutcomeCompilationFailure || result.ExitCode != 20 {
		t.Fatalf("Outcome/ExitCode = %q/%d, want compilation_failure/20", result.Outcome, result.ExitCode)
	}
	target := findTarget(t, result, "web-01.example.test")
	if target.Baseline == nil {
		t.Error("baseline provenance was dropped for a target that failed at the compiler step")
	}
}

// TestRun_V3WarningSurvivesToTheResult verifies the non-suppressible v3
// trusted-fact warning reaches the shared result and does not, by itself,
// change the outcome (design.md section 10).
func TestRun_V3WarningSurvivesToTheResult(t *testing.T) {
	resources := []testResource{{Type: "Notify", Title: "hello"}}
	w := newWorkflow(
		fakeFactSource{},
		fakeCatalogSource{resources: resources},
		fakeCompiler{resources: resources, v3Warning: model.V3TrustedFactWarning, warnings: []string{"fell back from v4 to v3"}},
	)

	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{
		testTarget("web-01.example.test", func(target *resolve.Target) { target.Candidate.CatalogAPI = config.CatalogAPIv3 }),
	}})

	if result.Outcome != exitcode.OutcomeClean {
		t.Fatalf("Outcome = %q, want clean: a warning alone must not change exit status", result.Outcome)
	}
	target := findTarget(t, result, "web-01.example.test")
	if target.Candidate == nil || target.Candidate.V3Warning != model.V3TrustedFactWarning {
		t.Errorf("V3Warning = %+v", target.Candidate)
	}
	if len(target.Diagnostics) != 1 || target.Diagnostics[0].Severity != model.SeverityWarning {
		t.Errorf("Diagnostics = %+v, want one warning-severity entry", target.Diagnostics)
	}
}

// TestRun_ImpactEstimatesAreRunLevelAndDeduplicated verifies the estimate
// stage runs once per unique identity across targets and that its results
// live at document level.
func TestRun_ImpactEstimatesAreRunLevelAndDeduplicated(t *testing.T) {
	var asked []model.ResourceIdentity
	w := newWorkflow(
		fakeFactSource{},
		fakeCatalogSource{resources: []testResource{{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "stopped"}}}},
		fakeCompiler{resources: []testResource{{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "running"}}}},
	)
	w.ImpactQuerier = fakeQuerier{asked: &asked, status: model.ImpactStatusCompleted}

	enable := func(target *resolve.Target) { target.ImpactEstimate.Enabled = true }
	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{
		testTarget("web-01.example.test", enable),
		testTarget("web-02.example.test", enable),
	}})

	if len(asked) != 1 {
		t.Fatalf("queried %d identities, want 1 (deduplicated run-wide): %+v", len(asked), asked)
	}
	if len(result.ImpactEstimates) != 1 {
		t.Fatalf("ImpactEstimates = %+v", result.ImpactEstimates)
	}
	if result.Outcome != exitcode.OutcomeDifferencesAllowed {
		t.Errorf("Outcome = %q, want differences_allowed", result.Outcome)
	}
}

// TestRun_DisabledEstimateIssuesNoQuery covers requirements.md 9.1 and
// design.md section 8's "disabled estimates produce no request and no
// failure" — including with no querier configured at all.
func TestRun_DisabledEstimateIssuesNoQuery(t *testing.T) {
	var asked []model.ResourceIdentity
	w := newWorkflow(
		fakeFactSource{},
		fakeCatalogSource{resources: []testResource{{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "stopped"}}}},
		fakeCompiler{resources: []testResource{{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "running"}}}},
	)
	w.ImpactQuerier = fakeQuerier{asked: &asked, status: model.ImpactStatusCompleted}

	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{testTarget("web-01.example.test")}})

	if len(asked) != 0 {
		t.Errorf("queried %+v with estimation disabled", asked)
	}
	if len(result.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %+v, want none", result.Diagnostics)
	}
}

// TestRun_FailedEstimateIsOperational verifies design.md section 8: an
// enabled estimate's failure is reported both as a non-completed estimate
// and as a run-level diagnostic that reduces to an operational outcome
// after every target has finished.
func TestRun_FailedEstimateIsOperational(t *testing.T) {
	w := newWorkflow(
		fakeFactSource{},
		fakeCatalogSource{resources: []testResource{{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "stopped"}}}},
		fakeCompiler{resources: []testResource{{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "running"}}}},
	)
	w.ImpactQuerier = fakeQuerier{status: model.ImpactStatusTimeout}

	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{
		testTarget("web-01.example.test", func(target *resolve.Target) { target.ImpactEstimate.Enabled = true }),
	}})

	if result.Outcome != exitcode.OutcomeOperationalError || result.ExitCode != 30 {
		t.Fatalf("Outcome/ExitCode = %q/%d, want operational_error/30", result.Outcome, result.ExitCode)
	}
	if len(result.ImpactEstimates) != 1 || result.ImpactEstimates[0].Status != model.ImpactStatusTimeout {
		t.Errorf("ImpactEstimates = %+v", result.ImpactEstimates)
	}
	if len(result.Diagnostics) != 1 {
		t.Errorf("run Diagnostics = %+v, want one", result.Diagnostics)
	}
	// The target itself compared successfully; only the run failed.
	if findTarget(t, result, "web-01.example.test").Outcome != exitcode.OutcomeDifferencesAllowed {
		t.Errorf("target outcome = %q", findTarget(t, result, "web-01.example.test").Outcome)
	}
}

// TestRun_EnabledEstimateWithNoQuerierIsReported verifies requested
// analysis is never silently omitted (see doc.go).
func TestRun_EnabledEstimateWithNoQuerierIsReported(t *testing.T) {
	resources := []testResource{{Type: "Notify", Title: "hello"}}
	w := newWorkflow(fakeFactSource{}, fakeCatalogSource{resources: resources}, fakeCompiler{resources: resources})

	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{
		testTarget("web-01.example.test", func(target *resolve.Target) { target.ImpactEstimate.Enabled = true }),
	}})

	if result.Outcome != exitcode.OutcomeOperationalError {
		t.Fatalf("Outcome = %q, want operational_error", result.Outcome)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Operation != model.OperationEstimateImpact {
		t.Errorf("Diagnostics = %+v", result.Diagnostics)
	}
}

// TestRun_NormalizationFailureIsOperational verifies malformed catalog
// data is reported rather than compared as an empty catalog.
func TestRun_NormalizationFailureIsOperational(t *testing.T) {
	baseline := fakeCatalogSource{resources: []testResource{{Type: "", Title: "broken"}}}
	w := newWorkflow(fakeFactSource{}, baseline, fakeCompiler{resources: []testResource{{Type: "Notify", Title: "hello"}}})

	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{testTarget("web-01.example.test")}})

	if result.Outcome != exitcode.OutcomeOperationalError {
		t.Fatalf("Outcome = %q, want operational_error", result.Outcome)
	}
	target := findTarget(t, result, "web-01.example.test")
	if len(target.Diagnostics) != 1 || target.Diagnostics[0].Operation != model.OperationNormalize {
		t.Errorf("Diagnostics = %+v", target.Diagnostics)
	}
}

// TestRun_ExclusionsSuppressDifferencesAndAreReported covers
// requirements.md 6.3-6.5 reaching the shared result: an excluded
// resource change leaves the run clean while its suppression stays
// visible.
func TestRun_ExclusionsSuppressDifferencesAndAreReported(t *testing.T) {
	w := newWorkflow(
		fakeFactSource{},
		fakeCatalogSource{resources: []testResource{{Type: "Notify", Title: "noise", Parameters: map[string]any{"message": "a"}}}},
		fakeCompiler{resources: []testResource{{Type: "Notify", Title: "noise", Parameters: map[string]any{"message": "b"}}}},
	)

	result := w.Run(context.Background(), resolve.Config{Targets: []resolve.Target{
		testTarget("web-01.example.test", func(target *resolve.Target) {
			target.FailOnDiff = true
			target.Exclude = []config.ExclusionRule{{Type: "Notify", Title: "*"}}
		}),
	}})

	if result.Outcome != exitcode.OutcomeClean {
		t.Fatalf("Outcome = %q, want clean", result.Outcome)
	}
	target := findTarget(t, result, "web-01.example.test")
	if target.NodeDiff == nil || len(target.NodeDiff.Exclusions) != 1 {
		t.Fatalf("NodeDiff = %+v, want one reported exclusion", target.NodeDiff)
	}
	if len(result.Aggregate.Groups) != 0 {
		t.Errorf("Aggregate.Groups = %+v, want none: excluded changes never reach aggregation", result.Aggregate.Groups)
	}
}
