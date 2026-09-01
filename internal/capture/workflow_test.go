package capture

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
	"github.com/example42/piace/internal/snapshot"
)

// fakeFactSource is a puppetdb.FactSource test double keyed by certname,
// so tests can control per-target success/failure without a real
// PuppetDB.
type fakeFactSource struct {
	factsets map[string]puppetdb.Factset
	fail     map[string]bool
}

func (f *fakeFactSource) Load(ctx context.Context, target resolve.Target) (puppetdb.Factset, model.SourceProvenance, *model.Diagnostic) {
	if f.fail[target.Certname] {
		diag := model.Diagnostic{Severity: model.SeverityError, Operation: model.OperationLoadFacts, Certname: target.Certname, Message: "simulated puppetdb failure"}
		return puppetdb.Factset{}, model.SourceProvenance{}, &diag
	}
	fs, ok := f.factsets[target.Certname]
	if !ok {
		diag := model.Diagnostic{Severity: model.SeverityError, Operation: model.OperationLoadFacts, Certname: target.Certname, Message: "no factset configured for target"}
		return puppetdb.Factset{}, model.SourceProvenance{}, &diag
	}
	return fs, model.SourceProvenance{Kind: model.SourceKindPuppetDB, Certname: fs.Certname}, nil
}

// fakeCompiler is a CompilerCatalogRequester test double keyed by
// certname.
type fakeCompiler struct {
	catalogs map[string]puppetdb.Catalog
	fail     map[string]bool
}

func (f *fakeCompiler) RequestCandidate(ctx context.Context, target resolve.Target, facts puppetdb.Factset) (puppetdb.Catalog, model.CandidateProvenance, []string, *model.Diagnostic) {
	if f.fail[target.Certname] {
		diag := model.Diagnostic{Severity: model.SeverityError, Operation: model.OperationRequestCandidate, Certname: target.Certname, Message: "simulated compiler failure"}
		return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, &diag
	}
	cat, ok := f.catalogs[target.Certname]
	if !ok {
		diag := model.Diagnostic{Severity: model.SeverityError, Operation: model.OperationRequestCandidate, Certname: target.Certname, Message: "no catalog configured for target"}
		return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, &diag
	}
	return cat, model.CandidateProvenance{}, nil, nil
}

func fixedClock() func() time.Time {
	t := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func factsTarget(certname, factsFile string) resolve.Target {
	return resolve.Target{
		Certname: certname,
		Facts:    resolve.Facts{Source: config.FactSourceFile, File: factsFile},
		Baseline: resolve.Baseline{Source: config.BaselineSourcePuppetDB, Environment: "production"},
	}
}

func catalogTarget(certname, baselineFile, catalogAPI, factsSource, factsFile string) resolve.Target {
	target := resolve.Target{
		Certname:  certname,
		Candidate: resolve.Candidate{Environment: "production", CatalogAPI: config.CatalogAPI(catalogAPI)},
		Baseline:  resolve.Baseline{Source: config.BaselineSourceFile, Environment: "production", File: baselineFile},
	}
	if factsSource == "file" {
		target.Facts = resolve.Facts{Source: config.FactSourceFile, File: factsFile}
	} else {
		target.Facts = resolve.Facts{Source: config.FactSourcePuppetDB}
	}
	return target
}

func TestWorkflow_CaptureFacts_WritesSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01.json")

	fake := &fakeFactSource{factsets: map[string]puppetdb.Factset{
		"web-01.example.test": {Certname: "web-01.example.test", Environment: "production", Producer: "puppetdb-01", Hash: "h", Facts: json.RawMessage(`{}`)},
	}}
	w := &Workflow{PuppetDBFacts: fake, FileFacts: puppetdb.NewFileSource(), Now: fixedClock()}

	outcomes := w.CaptureFacts(context.Background(), []resolve.Target{factsTarget("web-01.example.test", path)})
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if outcomes[0].Failed() {
		t.Fatalf("outcome failed: %+v", outcomes[0].Diagnostic)
	}
	if outcomes[0].Path != path {
		t.Errorf("Path = %q, want %q", outcomes[0].Path, path)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat: %v", err)
	}
	env, err := snapshot.Load(path)
	if err != nil {
		t.Fatalf("snapshot.Load: %v", err)
	}
	if env.Kind != snapshot.KindFactset || env.Target != "web-01.example.test" {
		t.Errorf("envelope = %+v", env)
	}
	if env.CapturedAt != "2026-08-24T00:00:00Z" {
		t.Errorf("CapturedAt = %q", env.CapturedAt)
	}
}

// TestWorkflow_CaptureFacts_SkipsPuppetDBSourcedTargets verifies a target
// whose facts.source is puppetdb (no local destination configured) is
// skipped rather than failed or written to an arbitrary path.
func TestWorkflow_CaptureFacts_SkipsPuppetDBSourcedTargets(t *testing.T) {
	fake := &fakeFactSource{factsets: map[string]puppetdb.Factset{}}
	w := &Workflow{PuppetDBFacts: fake, FileFacts: puppetdb.NewFileSource(), Now: fixedClock()}

	target := resolve.Target{
		Certname: "web-02.example.test",
		Facts:    resolve.Facts{Source: config.FactSourcePuppetDB},
		Baseline: resolve.Baseline{Source: config.BaselineSourcePuppetDB, Environment: "production"},
	}
	outcomes := w.CaptureFacts(context.Background(), []resolve.Target{target})
	if len(outcomes) != 1 || !outcomes[0].Skipped || outcomes[0].Failed() {
		t.Errorf("outcomes = %+v, want a single skipped outcome", outcomes)
	}
}

// TestWorkflow_CaptureFacts_PerTargetErrorIsolation verifies one
// target's PuppetDB retrieval failure does not stop the remaining
// targets from being captured.
func TestWorkflow_CaptureFacts_PerTargetErrorIsolation(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.json")
	pathB := filepath.Join(dir, "b.json")

	fake := &fakeFactSource{
		factsets: map[string]puppetdb.Factset{
			"b.example.test": {Certname: "b.example.test", Environment: "production", Producer: "p", Hash: "h", Facts: json.RawMessage(`{}`)},
		},
		fail: map[string]bool{"a.example.test": true},
	}
	w := &Workflow{PuppetDBFacts: fake, FileFacts: puppetdb.NewFileSource(), Now: fixedClock()}

	outcomes := w.CaptureFacts(context.Background(), []resolve.Target{
		factsTarget("a.example.test", pathA),
		factsTarget("b.example.test", pathB),
	})
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if !outcomes[0].Failed() {
		t.Errorf("outcomes[0] should have failed: %+v", outcomes[0])
	}
	if outcomes[1].Failed() {
		t.Errorf("outcomes[1] should have succeeded: %+v", outcomes[1].Diagnostic)
	}
	if _, err := os.Stat(pathA); err == nil {
		t.Errorf("expected no snapshot written for the failed target")
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Errorf("expected a snapshot written for the succeeded target: %v", err)
	}
}

// TestWorkflow_CaptureFacts_RefusesOverwriteWithoutReplace verifies
// Workflow.Replace threads through to snapshot.Write's overwrite
// protection.
func TestWorkflow_CaptureFacts_RefusesOverwriteWithoutReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01.json")

	fake := &fakeFactSource{factsets: map[string]puppetdb.Factset{
		"web-01.example.test": {Certname: "web-01.example.test", Environment: "production", Producer: "p", Hash: "h", Facts: json.RawMessage(`{}`)},
	}}
	w := &Workflow{PuppetDBFacts: fake, FileFacts: puppetdb.NewFileSource(), Now: fixedClock()}

	target := factsTarget("web-01.example.test", path)
	first := w.CaptureFacts(context.Background(), []resolve.Target{target})
	if first[0].Failed() {
		t.Fatalf("first capture failed: %+v", first[0].Diagnostic)
	}

	second := w.CaptureFacts(context.Background(), []resolve.Target{target})
	if !second[0].Failed() {
		t.Fatal("expected second capture without --replace to fail")
	}

	w.Replace = true
	third := w.CaptureFacts(context.Background(), []resolve.Target{target})
	if third[0].Failed() {
		t.Fatalf("capture with --replace should have succeeded: %+v", third[0].Diagnostic)
	}
}

func TestWorkflow_CaptureCatalog_StubCompilerReportsNotImplemented(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01-catalog.json")

	fake := &fakeFactSource{factsets: map[string]puppetdb.Factset{
		"web-01.example.test": {Certname: "web-01.example.test", Environment: "production", Producer: "p", Hash: "h", Facts: json.RawMessage(`{}`)},
	}}
	w := &Workflow{PuppetDBFacts: fake, FileFacts: puppetdb.NewFileSource(), Compiler: StubCompiler{}, Now: fixedClock()}

	outcomes := w.CaptureCatalog(context.Background(), []resolve.Target{
		catalogTarget("web-01.example.test", path, "v4", "puppetdb", ""),
	}, "production")
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if !outcomes[0].Failed() {
		t.Fatal("expected the stub compiler to produce a failed outcome")
	}
	if outcomes[0].Diagnostic.Operation != model.OperationRequestCandidate {
		t.Errorf("Operation = %q, want %q", outcomes[0].Diagnostic.Operation, model.OperationRequestCandidate)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("expected no snapshot written when the compiler is not implemented")
	}
}

func TestWorkflow_CaptureCatalog_WritesSnapshotWithWorkingCompiler(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01-catalog.json")

	fakeFacts := &fakeFactSource{factsets: map[string]puppetdb.Factset{
		"web-01.example.test": {Certname: "web-01.example.test", Environment: "production", Producer: "p", Hash: "h", Facts: json.RawMessage(`{}`)},
	}}
	fakeCompilerImpl := &fakeCompiler{catalogs: map[string]puppetdb.Catalog{
		"web-01.example.test": {Certname: "web-01.example.test", Environment: "production", Producer: "compiler-01", Hash: "c", Resources: json.RawMessage(`[]`), Edges: json.RawMessage(`[]`)},
	}}
	w := &Workflow{PuppetDBFacts: fakeFacts, FileFacts: puppetdb.NewFileSource(), Compiler: fakeCompilerImpl, Now: fixedClock()}

	outcomes := w.CaptureCatalog(context.Background(), []resolve.Target{
		catalogTarget("web-01.example.test", path, "v4", "puppetdb", ""),
	}, "production")
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if outcomes[0].Failed() {
		t.Fatalf("outcome failed: %+v", outcomes[0].Diagnostic)
	}

	env, err := snapshot.Load(path)
	if err != nil {
		t.Fatalf("snapshot.Load: %v", err)
	}
	if env.Kind != snapshot.KindCatalog {
		t.Errorf("Kind = %q", env.Kind)
	}
	if env.RequestedEnvironment != "production" {
		t.Errorf("RequestedEnvironment = %q", env.RequestedEnvironment)
	}
	if env.CompilerAPIVersion != snapshot.CompilerAPIv4 {
		t.Errorf("CompilerAPIVersion = %q", env.CompilerAPIVersion)
	}
	if env.InputFactsetIdentity == "" {
		t.Error("InputFactsetIdentity is empty, want a computed factset identity")
	}
	if err := snapshot.Validate(env, snapshot.KindCatalog, "web-01.example.test"); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// TestWorkflow_CaptureCatalog_SkipsPuppetDBSourcedBaselines verifies a
// target whose baseline.source is puppetdb (no local destination
// configured) is skipped.
func TestWorkflow_CaptureCatalog_SkipsPuppetDBSourcedBaselines(t *testing.T) {
	w := &Workflow{Compiler: StubCompiler{}, Now: fixedClock()}
	target := resolve.Target{
		Certname:  "web-03.example.test",
		Candidate: resolve.Candidate{Environment: "production", CatalogAPI: config.CatalogAPIv4},
		Facts:     resolve.Facts{Source: config.FactSourcePuppetDB},
		Baseline:  resolve.Baseline{Source: config.BaselineSourcePuppetDB, Environment: "production"},
	}
	outcomes := w.CaptureCatalog(context.Background(), []resolve.Target{target}, "production")
	if len(outcomes) != 1 || !outcomes[0].Skipped || outcomes[0].Failed() {
		t.Errorf("outcomes = %+v, want a single skipped outcome", outcomes)
	}
}

// TestWorkflow_CaptureCatalog_PerTargetErrorIsolation verifies one
// target's compiler failure does not stop the remaining targets.
func TestWorkflow_CaptureCatalog_PerTargetErrorIsolation(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a-catalog.json")
	pathB := filepath.Join(dir, "b-catalog.json")

	fakeFacts := &fakeFactSource{factsets: map[string]puppetdb.Factset{
		"a.example.test": {Certname: "a.example.test", Environment: "production", Producer: "p", Hash: "h", Facts: json.RawMessage(`{}`)},
		"b.example.test": {Certname: "b.example.test", Environment: "production", Producer: "p", Hash: "h", Facts: json.RawMessage(`{}`)},
	}}
	fakeCompilerImpl := &fakeCompiler{
		catalogs: map[string]puppetdb.Catalog{
			"b.example.test": {Certname: "b.example.test", Environment: "production", Producer: "compiler-01", Hash: "c", Resources: json.RawMessage(`[]`), Edges: json.RawMessage(`[]`)},
		},
		fail: map[string]bool{"a.example.test": true},
	}
	w := &Workflow{PuppetDBFacts: fakeFacts, FileFacts: puppetdb.NewFileSource(), Compiler: fakeCompilerImpl, Now: fixedClock()}

	outcomes := w.CaptureCatalog(context.Background(), []resolve.Target{
		catalogTarget("a.example.test", pathA, "v4", "puppetdb", ""),
		catalogTarget("b.example.test", pathB, "v4", "puppetdb", ""),
	}, "production")
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if !outcomes[0].Failed() {
		t.Errorf("outcomes[0] should have failed: %+v", outcomes[0])
	}
	if outcomes[1].Failed() {
		t.Errorf("outcomes[1] should have succeeded: %+v", outcomes[1].Diagnostic)
	}
	if _, err := os.Stat(pathA); err == nil {
		t.Error("expected no snapshot written for the failed target")
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Errorf("expected a snapshot written for the succeeded target: %v", err)
	}
}
