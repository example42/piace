package main

import (
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
)

// This file is the acceptance suite's acceptance suite: fixture-driven validation of
// the behavior the internal packages implement, exercised through the CLI entry
// point against two in-process mTLS services.
//
// It deliberately drives run() rather than compare.Workflow. The
// pipeline-with-fakes level is already covered by
// internal/compare/workflow_test.go; what only this level can exercise
// is everything between the CLI boundary and the socket: PEM loading,
// real mTLS handshakes, HTTP status and JSON decoding inside the
// adapters, artifact writing, and the process exit code.
//
// Two of the acceptance suite's acceptance conditions cannot be discharged here and
// remain outstanding; see acceptance_assumptions_test.go, which states
// each one at the exact place a green test could otherwise be mistaken
// for confirmation.

// targetsYAML builds a target file with one target per certname, sharing
// the defaults block given.
func targetsYAML(defaults string, targets ...string) string {
	var b strings.Builder
	b.WriteString("version: 1\ndefaults:\n")
	b.WriteString(defaults)
	b.WriteString("targets:\n")
	for _, t := range targets {
		b.WriteString(t)
	}
	return b.String()
}

// defaultDefaults is the defaults block most acceptance cases use: v4
// against PuppetDB for both sources, impact estimation off, differences
// allowed.
const defaultDefaults = `  candidate:
    environment: feature-123
    catalog_api: v4
  facts:
    source: puppetdb
  baseline:
    source: puppetdb
    environment: production
  impact_estimate:
    enabled: false
    timeout: 5s
    result_limit: 2
  fail_on_diff: false
`

func target(certname string) string { return "  - certname: " + certname + "\n" }

// baseResources is a small catalog shared by several cases.
func baseResources() []resourceSpec {
	return []resourceSpec{
		{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}},
		{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "running", "enable": true}},
	}
}

func baseEdges() []edgeSpec {
	return []edgeSpec{{SourceType: "Notify", SourceTitle: "hello", TargetType: "Service", TargetTitle: "nginx"}}
}

// seedTarget registers a factset, a PuppetDB baseline catalog, and a
// compiler candidate catalog for certname.
func (h *harness) seedTarget(certname string, baseline, candidate []resourceSpec, edges []edgeSpec) {
	h.pdb.factsets[certname] = pdbFactset(certname, true)
	h.pdb.catalogs[certname] = pdbCatalog(certname, "production", baseline, edges)
	h.compiler.catalogs[certname] = compilerCatalog(certname, "feature-123", candidate, edges)
}

// TestAcceptance_CleanRunOverPuppetDBSources is the baseline case: v4
// with provided trusted facts, identical baseline and candidate, exit 0
// clean, and all three artifacts produced.
func TestAcceptance_CleanRunOverPuppetDBSources(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))

	textOut := h.path("report.txt")
	got := h.compare(t, "--text-out", textOut)

	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.code, got.stdout, got.stderr)
	}
	for name, artifact := range got.all() {
		if name == "stdout" {
			// --text-out was given, so the text report went to the file.
			continue
		}
		if artifact == "" {
			t.Errorf("%s artifact is empty", name)
		}
	}
	if !strings.Contains(got.text, "outcome: clean (exit 0)") {
		t.Errorf("text report does not report a clean outcome:\n%s", got.text)
	}
	if !strings.Contains(got.json, `"outcome":"clean"`) {
		t.Errorf("JSON report does not report a clean outcome")
	}

	// A candidate request must never ask the compiler to persist facts or
	// the catalog.
	if len(h.compiler.v4Bodies) != 1 {
		t.Fatalf("compiler received %d v4 requests, want 1", len(h.compiler.v4Bodies))
	}
	persistence, _ := h.compiler.v4Bodies[0]["persistence"].(map[string]any)
	if persistence["facts"] != false || persistence["catalog"] != false {
		t.Errorf("v4 request persistence = %+v, want both false", persistence)
	}
	// v4 uses the target trusted-fact mechanism when the factset supplies
	// one.
	if _, ok := h.compiler.v4Bodies[0]["trusted_facts"]; !ok {
		t.Error("v4 request omitted trusted_facts despite a valid trusted fact in the factset")
	}
	if !strings.Contains(got.json, `"trusted_facts_source":"provided"`) {
		t.Error("candidate provenance does not record trusted_facts_source=provided")
	}
}

// TestAcceptance_EndpointsRestrictedToConfiguredServices checks the
// network-egress claim structurally: the exact set of paths contacted on
// each configured authority is asserted, and a third mTLS service that
// fails the test on contact witnesses the absence of any other traffic.
func TestAcceptance_EndpointsRestrictedToConfiguredServices(t *testing.T) {
	h := newHarness(t)
	// The candidate differs from the baseline so impact estimation
	// actually issues its query: an endpoint that is never contacted
	// cannot demonstrate that contacting it is permitted.
	h.seedTarget("web-01.example.test", baseResources(), changedResources(), baseEdges())
	h.pdb.impactCertnames = []string{"db-01.example.test"}
	h.writeConfigs(t, targetsYAML(impactDefaults, target("web-01.example.test")))

	got := h.compare(t)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}

	// Estimation is enabled for this run, so the root query endpoint is
	// part of the expected set and is listed rather than filtered out: a
	// test that proves endpoint restriction must not exclude an endpoint
	// from its own exact-set claim.
	wantPDB := []string{
		"/pdb/query/v4",
		"/pdb/query/v4/catalogs/web-01.example.test",
		"/pdb/query/v4/factsets/web-01.example.test",
	}
	if diff := comparePaths(h.pdb.sortedPaths(), wantPDB); diff != "" {
		t.Errorf("puppetdb paths: %s", diff)
	}
	wantCompiler := []string{"/puppet/v4/catalog"}
	if diff := comparePaths(h.compiler.sortedPaths(), wantCompiler); diff != "" {
		t.Errorf("compiler paths: %s", diff)
	}
	// The forbidden service asserts on contact from its own handler; a
	// clean run here means it was never reached.
}

// comparePaths reports a human-readable difference between an observed
// and an expected path set. It filters nothing: every path a service
// received participates in the comparison.
func comparePaths(got, want []string) string {
	if strings.Join(got, ",") == strings.Join(want, ",") {
		return ""
	}
	return "got " + strings.Join(got, ",") + ", want " + strings.Join(want, ",")
}

// TestAcceptance_OutcomePrecedence exercises the full precedence chain
// through combinations rather than single-outcome runs: only a
// combination can show that the reducer picks the most severe outcome
// instead of the last or first one.
func TestAcceptance_OutcomePrecedence(t *testing.T) {
	// Each target below is seeded to produce exactly one outcome class.
	// The cases then select which of them participate.
	cases := []struct {
		name     string
		targets  []string
		wantCode exitcode.Code
		wantWord string
	}{
		{"clean only", []string{"clean.example.test"}, exitcode.Success, "clean"},
		{"clean + allowed", []string{"clean.example.test", "allowed.example.test"}, exitcode.Success, "differences_allowed"},
		{"allowed + policy", []string{"allowed.example.test", "policy.example.test"}, exitcode.PolicyDisallowedDifference, "policy_disallowed_difference"},
		{"policy + compile failure", []string{"policy.example.test", "compilefail.example.test"}, exitcode.CompilationFailure, "compilation_failure"},
		{"every class", []string{"clean.example.test", "allowed.example.test", "policy.example.test", "compilefail.example.test", "operational.example.test"}, exitcode.OperationalError, "operational_error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			changed := []resourceSpec{
				{Type: "Notify", Title: "hello", Parameters: map[string]any{"message": "hi"}},
				{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "stopped", "enable": true}},
			}

			// clean: identical catalogs, fail_on_diff irrelevant.
			h.seedTarget("clean.example.test", baseResources(), baseResources(), baseEdges())
			// allowed: a difference with fail_on_diff false.
			h.seedTarget("allowed.example.test", baseResources(), changed, baseEdges())
			// policy: the same difference with fail_on_diff true.
			h.seedTarget("policy.example.test", baseResources(), changed, baseEdges())
			// compilefail: the compiler rejects the request (500 is not a
			// verified-unsupported status, so it is never a fallback).
			h.pdb.factsets["compilefail.example.test"] = pdbFactset("compilefail.example.test", true)
			h.pdb.catalogs["compilefail.example.test"] = pdbCatalog("compilefail.example.test", "production", baseResources(), baseEdges())
			// operational: PuppetDB has no baseline catalog for it.
			h.pdb.factsets["operational.example.test"] = pdbFactset("operational.example.test", true)

			var entries []string
			for _, certname := range tc.targets {
				entry := target(certname)
				if certname == "policy.example.test" {
					entry += "    fail_on_diff: true\n"
				}
				entries = append(entries, entry)
			}
			h.writeConfigs(t, targetsYAML(defaultDefaults, entries...))

			// compilefail participates only when selected; when it does, its
			// candidate request must fail rather than 404 into a fallback, so no
			// catalog is registered for it and the fake compiler answers 404, which
			// IS a verified-unsupported status. Force a 500 instead by registering a
			// status override for the whole compiler when that target is in play.
			for _, certname := range tc.targets {
				if certname == "compilefail.example.test" {
					// A semantic rejection is served as the whole
					// response body, with no catalog envelope around it:
					// the adapter's error probe reads the outer body.
					h.compiler.rawBodies["compilefail.example.test"] = map[string]any{
						"error": "Evaluation Error: Unknown class site::missing",
					}
				}
			}

			got := h.compare(t)
			if got.code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", got.code, tc.wantCode, got.stdout, got.stderr)
			}
			if !strings.Contains(got.stdout, "outcome: "+tc.wantWord) {
				t.Errorf("text report outcome is not %q:\n%s", tc.wantWord, got.stdout)
			}
			// A target's diagnostic remains in every output regardless of global
			// precedence.
			for _, certname := range tc.targets {
				if !strings.Contains(got.json, certname) {
					t.Errorf("JSON report dropped target %s", certname)
				}
			}
		})
	}
}

// TestAcceptance_BaselineEnvironmentRejection: a PuppetDB baseline whose
// environment differs from the configured baseline environment fails the
// target before it is diffed.
func TestAcceptance_BaselineEnvironmentRejection(t *testing.T) {
	h := newHarness(t)
	h.pdb.factsets["web-01.example.test"] = pdbFactset("web-01.example.test", true)
	h.pdb.catalogs["web-01.example.test"] = pdbCatalog("web-01.example.test", "some-other-env", baseResources(), baseEdges())
	h.compiler.catalogs["web-01.example.test"] = compilerCatalog("web-01.example.test", "feature-123", baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))

	got := h.compare(t)
	if got.code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30\nstdout:\n%s", got.code, got.stdout)
	}
	if !strings.Contains(got.stdout, "does not match the configured baseline environment") {
		t.Errorf("text report does not explain the rejection:\n%s", got.stdout)
	}
	// The candidate must never have been requested for a target whose
	// baseline was rejected.
	if h.compiler.count() != 0 {
		t.Errorf("compiler was contacted %d times for a target with a rejected baseline", h.compiler.count())
	}
}

// TestAcceptance_ExclusionsSuppressDifferencesAndAreReported covers
// exclusion handling end to end, including the edge-suppression rule and
// the visible suppression counts.
func TestAcceptance_ExclusionsSuppressDifferencesAndAreReported(t *testing.T) {
	h := newHarness(t)
	baseline := []resourceSpec{
		{Type: "Notify", Title: "noise", Parameters: map[string]any{"message": "a"}},
		{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "running"}},
	}
	candidate := []resourceSpec{
		{Type: "Notify", Title: "noise", Parameters: map[string]any{"message": "b"}},
		{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "running"}},
	}
	edges := []edgeSpec{{SourceType: "Notify", SourceTitle: "noise", TargetType: "Service", TargetTitle: "nginx"}}
	// The candidate drops the edge, so an edge difference exists and must
	// also be suppressed because one endpoint is excluded.
	h.pdb.factsets["web-01.example.test"] = pdbFactset("web-01.example.test", true)
	h.pdb.catalogs["web-01.example.test"] = pdbCatalog("web-01.example.test", "production", baseline, edges)
	h.compiler.catalogs["web-01.example.test"] = compilerCatalog("web-01.example.test", "feature-123", candidate, nil)

	defaults := defaultDefaults + `  exclude:
    - type: Notify
      title: "noi*"
`
	h.writeConfigs(t, targetsYAML(defaults, target("web-01.example.test")+"    fail_on_diff: true\n"))

	got := h.compare(t)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0 (every difference is excluded)\nstdout:\n%s", got.code, got.stdout)
	}
	if !strings.Contains(got.stdout, "excluded: Notify[noi*]") {
		t.Errorf("text report does not report the applied exclusion rule:\n%s", got.stdout)
	}
	// The edge attached to the excluded resource is suppressed and counted.
	// The count is asserted against the JSON report because the text and
	// HTML formats omit edge information entirely (see internal/report's
	// doc.go). The counts are owed in machine-readable and human-readable
	// output alike, and the human-readable half is the rule identity and its
	// resource/parameter counts above.
	if !strings.Contains(got.json, `"suppressed_edges":1`) {
		t.Errorf("the edge attached to an excluded resource was not suppressed and counted:\n%s", got.json)
	}
	if strings.Contains(got.stdout, "edge(s) suppressed") {
		t.Errorf("the text report still prints the suppressed-edge count:\n%s", got.stdout)
	}
	if !strings.Contains(got.html, "Excluded differences") {
		t.Error("the HTML report does not visibly mark excluded differences")
	}
}

// TestAcceptance_CandidateEnvironmentOverride covers `compare
// --candidate-environment`: the environment CI deployed is a per-pipeline
// value, so a pipeline must be able to name it on the command line
// instead of rewriting the committed target file between checkout and
// run.
//
// The target file here names no candidate environment at all (neither in
// its defaults nor in the target's own block), which is only valid
// because the override is applied before resolution. Both halves are
// asserted at the socket and in the report: the request the compiler
// actually received carries the overridden environment, and the
// provenance the result document records is the environment compiled, not
// the one the file asked for.
func TestAcceptance_CandidateEnvironmentOverride(t *testing.T) {
	const certname = "web-01.example.test"
	const overridden = "pr-441"

	h := newHarness(t)
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	// seedTarget serves a catalog labelled feature-123; the adapter
	// verifies a response against the environment it requested, so the
	// override has to reach the fixture too or this fails as a
	// compilation failure rather than passing on a mislabelled catalog.
	h.compiler.catalogs[certname] = compilerCatalog(certname, overridden, baseResources(), baseEdges())

	const noEnvironmentDefaults = `  candidate:
    catalog_api: v4
  facts:
    source: puppetdb
  baseline:
    source: puppetdb
    environment: production
  impact_estimate:
    enabled: false
    timeout: 5s
    result_limit: 2
  fail_on_diff: false
`
	targets := targetsYAML(noEnvironmentDefaults, target(certname))
	h.writeConfigs(t, targets)

	got := h.compare(t, "--candidate-environment", overridden)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.code, got.stdout, got.stderr)
	}
	if len(h.compiler.v4Bodies) != 1 {
		t.Fatalf("compiler received %d v4 requests, want 1", len(h.compiler.v4Bodies))
	}
	if env := h.compiler.v4Bodies[0]["environment"]; env != overridden {
		t.Errorf("v4 request environment = %v, want %q", env, overridden)
	}
	if !strings.Contains(got.json, `"environment":"`+overridden+`"`) {
		t.Errorf("result document does not record the overridden environment:\n%s", got.json)
	}

	// Without the override the same file is a configuration error, which
	// is what makes the flag the only thing supplying the value above.
	h.writeConfigs(t, targets)
	if again := h.compare(t); again.code != exitcode.OperationalError {
		t.Errorf("exit without --candidate-environment = %d, want %d", again.code, exitcode.OperationalError)
	}
}

// TestAcceptance_CandidateEnvironmentOverrideBeatsTheTargetFile pins the
// precedence: a flag that lost to a per-target `candidate:` block would
// leave a pipeline silently comparing against whatever the file happened
// to name, which is the failure this flag exists to prevent.
func TestAcceptance_CandidateEnvironmentOverrideBeatsTheTargetFile(t *testing.T) {
	const certname = "web-01.example.test"
	const overridden = "pr-441"

	h := newHarness(t)
	h.seedTarget(certname, baseResources(), baseResources(), baseEdges())
	h.compiler.catalogs[certname] = compilerCatalog(certname, overridden, baseResources(), baseEdges())

	perTarget := "  - certname: " + certname + "\n    candidate:\n" +
		"      environment: stale-from-the-file\n      catalog_api: v4\n"
	h.writeConfigs(t, targetsYAML(defaultDefaults, perTarget))

	got := h.compare(t, "--candidate-environment", overridden)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.code, got.stdout, got.stderr)
	}
	if env := h.compiler.v4Bodies[0]["environment"]; env != overridden {
		t.Errorf("v4 request environment = %v, want %q", env, overridden)
	}
	if strings.Contains(got.json, "stale-from-the-file") {
		t.Errorf("result document still carries the target file's environment:\n%s", got.json)
	}
}
