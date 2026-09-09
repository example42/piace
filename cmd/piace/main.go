// Command piace is the PIACE CLI entry point. It provides five
// subcommands: `compare`, `capture facts`, `capture catalog`, `explain`
// and `change-context`.
//
// This file wires argument parsing, transport/adapter construction, and
// stable exit codes. All domain behavior lives in internal packages:
// configuration resolution (internal/config/resolve), mTLS transports
// (internal/transport), source and compiler adapters (internal/puppetdb,
// internal/compiler), the compare pipeline (internal/compare), and the
// three renderers (internal/report).
//
// `explain` is a second, independent step over a result document
// `compare` already wrote. It is the only subcommand that contacts an
// inference service, and the only one that does not contact a compiler
// or PuppetDB: the two halves share nothing but a file on disk. See
// CONTEXT.md for why the assessment stays out of the result document.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"net/http"

	"github.com/example42/piace/internal/artifact"
	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/capture"
	"github.com/example42/piace/internal/compare"
	"github.com/example42/piace/internal/compiler"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/filecontent"
	"github.com/example42/piace/internal/impact"
	"github.com/example42/piace/internal/inference"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
	"github.com/example42/piace/internal/report"
	"github.com/example42/piace/internal/snapshot"
	"github.com/example42/piace/internal/transport"
)

// toolVersion is overridden at release build time via
// `-ldflags "-X main.toolVersion=..."`.
var toolVersion = "dev"

// clock supplies the invocation timestamp recorded in a report and in a
// snapshot envelope. It is a package variable so the acceptance suite
// can fix it: a report's timestamp is the one field that would otherwise
// make two runs over identical inputs differ, and they must not.
// Production never reassigns it.
var clock = time.Now

// stdin is the stream `explain --json-in -` reads a result document
// from. It is a package variable for the same reason clock is: the
// acceptance suite drives run() and has no other way to hand it one.
// Production never reassigns it.
var stdin = os.Stdin

// inferenceHTTPClient, when non-nil, replaces the HTTP client the
// inference client would build for itself.
//
// It exists so the acceptance suite can reach an in-process stub service
// over TLS with a generated certificate. That certificate is trusted by
// nothing outside the test process, and it must stay that way: this is
// deliberately a package variable production never assigns rather than an
// insecure_skip_verify or a ca_bundle in the services file, either of
// which would ship a way to weaken verification against a real inference
// service. Production never reassigns it.
var inferenceHTTPClient *http.Client

func main() {
	os.Exit(int(run(os.Args[1:], os.Stdout, os.Stderr)))
}

// run dispatches to a subcommand and returns the process exit code. It
// never calls os.Exit itself so it stays testable.
func run(args []string, stdout, stderr *os.File) exitcode.Code {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage())
		return exitcode.OperationalError
	}

	switch args[0] {
	case "compare":
		return runCompare(args[1:], stdout, stderr)
	case "capture":
		return runCapture(args[1:], stdout, stderr)
	case "explain":
		return runExplain(args[1:], stdout, stderr)
	case "change-context":
		return runChangeContext(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, usage())
		return exitcode.Success
	case "version", "--version":
		fmt.Fprintln(stdout, "piace "+toolVersion)
		return exitcode.Success
	default:
		fmt.Fprintf(stderr, "piace: unknown command %q\n\n%s\n", args[0], usage())
		return exitcode.OperationalError
	}
}

func usage() string {
	return `piace compare --targets TARGETS.yaml --services SERVICES.yaml \
  [--candidate-environment ENVIRONMENT] \
  [--text-out PATH] [--json-out PATH] [--html-out PATH] [--impact-nodes]
piace capture facts --targets TARGETS.yaml --services SERVICES.yaml
piace capture catalog --targets TARGETS.yaml --services SERVICES.yaml \
  --environment ENVIRONMENT
piace explain --json-in REPORT.json --services SERVICES.yaml \
  [--ai-out PATH] [--html-out PATH] [--change CHANGE.yaml] \
  [--fail-on-inference-error] [--debug] [--debug-dump-dir DIR]
piace change-context (--base-ref REF | --base-ref-env VAR) \
  [--head-ref REF | --head-ref-env VAR] \
  [--title-env VAR | --title-file PATH] \
  [--description-env VAR | --description-file PATH]

The text report summarizes for a CI log: it omits dependency-graph edge
changes and each impact estimate's PQL and request options, and names only
the first few certnames per estimate. The JSON and HTML reports are
complete -- HTML keeps everything, with the bulk behind expandable
sections.
  --impact-nodes         list every certname an impact estimate returned
                         instead of a capped sample; affects the text
                         report only (compare only)
  --candidate-environment ENVIRONMENT
                         compile every target's candidate catalog from
                         ENVIRONMENT, overriding candidate.environment in
                         the target file for every target (compare only).
                         The environment CI deployed is a per-pipeline
                         value; this is what lets the target file stay
                         reviewable policy instead of being rewritten by
                         the job that runs it. With it, the target file
                         may omit candidate.environment entirely

explain reads a result document compare wrote and asks a configured
inference service to assess the change it records. The assessment is
advisory: it is a separate, separately versioned artifact, it is never
part of the result document, and it cannot change an outcome or an exit
code. explain contacts no compiler and no PuppetDB, and compare contacts
no inference service.
  --json-in PATH         the stored result document; "-" reads stdin
  --change PATH          a change context file describing the repository
                         change under test; its free text is treated as
                         untrusted data, never as instruction
  --ai-out PATH          path to write the change assessment artifact
  --html-out PATH        path to write the report re-rendered with the
                         assessment below the deterministic outcome
  --fail-on-inference-error
                         exit 30 when the assessment could not be
                         produced; without it a failed assessment is
                         recorded in the artifact and the command still
                         exits 0

change-context writes a change context file to stdout for explain to
read. It is the one subcommand that invokes git, and it is optional:
explain --change reads a file the caller produced by any means, so a
repository under a different VCS still describes its change by hand.
Commit subjects are collected, never bodies.
  --base-ref REF         the ref the change branched from (required, or
                         --base-ref-env)
  --head-ref REF         the ref under test (default HEAD)
  --base-ref-env VAR, --head-ref-env VAR
                         read the ref from the named environment variable
                         instead
  --title-env VAR, --title-file PATH
                         the change title, by variable name or path
  --description-env VAR, --description-file PATH
                         the change description, by variable name or path

  There is no --title or --description flag on purpose. A pull request
  title is attacker-supplied text, and a CI system that substitutes it
  into script text before a shell runs (GitHub ${{ }}, Azure $( )) turns
  one into arbitrary code execution on the runner. Naming the variable
  keeps its value off the command line.

compare, capture and explain also accept:
  --debug                print one line per service request to stderr (method,
                         URL, status, duration, body sizes, response top-level
                         JSON keys); no body content is printed. For explain
                         this is what shows an inference endpoint's HTTP status
                         and the response's JSON shape without the body
  --debug-dump-dir DIR   additionally write raw request/response bodies to 0600
                         files in DIR. For compare and capture these may hold
                         sensitive catalog values; for explain the request body
                         is the catalog-derived payload sent to the inference
                         service and the response body of a 4xx is where the
                         provider names the field it rejected`
}

// compareFlags holds the parsed --compare flags. Kept as a struct so tests
// can exercise flag parsing without a real filesystem/network.
type compareFlags struct {
	targets  string
	services string
	// candidateEnvironment, when non-empty, replaces
	// candidate.environment for every target. Unlike impactNodes it does
	// reach resolve.Load, as an override applied to the decoded target
	// file before resolution: it is configuration, not display policy,
	// and the report's provenance must record what was actually
	// compiled. See resolve.Overrides.
	candidateEnvironment string
	textOut              string
	jsonOut              string
	htmlOut              string
	// impactNodes is display policy for the text report only; it never
	// reaches resolve.Config, because what PIACE queries and what PIACE
	// prints are separate concerns and an estimate's certname sample is
	// already bounded by the target's configured result_limit.
	impactNodes bool
	debug       debugFlags
}

func runCompare(args []string, stdout, stderr *os.File) exitcode.Code {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f compareFlags
	fs.StringVar(&f.targets, "targets", "", "path to the target YAML file (required)")
	fs.StringVar(&f.services, "services", "", "path to the services YAML file (required)")
	fs.StringVar(&f.candidateEnvironment, "candidate-environment", "", "compile every target's candidate catalog from this environment, overriding candidate.environment in the target file")
	fs.StringVar(&f.textOut, "text-out", "", "path to write the text report (default: stdout)")
	fs.StringVar(&f.jsonOut, "json-out", "", "path to write the versioned JSON report")
	fs.StringVar(&f.htmlOut, "html-out", "", "path to write the static HTML report")
	fs.BoolVar(&f.impactNodes, "impact-nodes", false, "list every certname an impact estimate returned instead of a capped sample (text report only)")
	f.debug.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitcode.OperationalError
	}
	if f.targets == "" || f.services == "" {
		fmt.Fprintln(stderr, "piace compare: --targets and --services are required")
		return exitcode.OperationalError
	}

	cfg, err := resolve.Load(resolve.CommandCompare, f.targets, f.services, resolve.Overrides{
		CandidateEnvironment: f.candidateEnvironment,
	})
	if err != nil {
		fmt.Fprintf(stderr, "piace compare: %s\n", err)
		return exitcode.OperationalError
	}

	if err := resolve.ValidateComparisonTargets(cfg.Targets); err != nil {
		fmt.Fprintf(stderr, "piace compare: %s\n", err)
		return exitcode.OperationalError
	}
	if err := artifact.Validate(comparisonInputs(f, cfg), []artifact.File{
		{Role: "--json-out", Path: f.jsonOut},
		{Role: "--html-out", Path: f.htmlOut},
		{Role: "--text-out", Path: f.textOut},
	}); err != nil {
		fmt.Fprintf(stderr, "piace compare: %s\n", err)
		return exitcode.OperationalError
	}
	debugOpts, err := f.debug.transportOptions("compare", stderr)
	if err != nil {
		fmt.Fprintf(stderr, "piace compare: %s\n", err)
		return exitcode.OperationalError
	}

	workflow, err := newCompareWorkflow(cfg, debugOpts)
	if err != nil {
		fmt.Fprintf(stderr, "piace compare: %s\n", err)
		return exitcode.OperationalError
	}

	result := workflow.Run(context.Background(), cfg)

	if err := writeReports(f, result, stdout); err != nil {
		// A report the operator asked for and did not get must not be papered
		// over by the comparison's own outcome, however clean: the artifacts are
		// part of the requested work, so a write failure is an operational
		// error.
		fmt.Fprintf(stderr, "piace compare: %s\n", err)
		return exitcode.OperationalError
	}

	return exitcode.Code(result.ExitCode)
}

// comparisonInputs lists the files a comparison reads: its two
// configuration files and, for every file-backed target, the snapshots
// it loads. The snapshots are the ones that matter here: a report
// written over the baseline it was just compared against destroys the
// input of every later run, and the operator finds out at the next
// comparison rather than at this one.
//
// The list is built per command rather than from a shared helper,
// because the same path is legitimately an input to one command and an
// output of another: a target's facts.file is what `capture facts`
// writes and what a comparison reads.
func comparisonInputs(f compareFlags, cfg resolve.Config) []artifact.File {
	inputs := []artifact.File{
		{Role: "--targets", Path: f.targets},
		{Role: "--services", Path: f.services},
	}
	for _, target := range cfg.Targets {
		if target.Facts.File != "" {
			inputs = append(inputs, artifact.File{Role: "the factset snapshot of " + target.Certname, Path: target.Facts.File})
		}
		if target.Baseline.File != "" {
			inputs = append(inputs, artifact.File{Role: "the baseline snapshot of " + target.Certname, Path: target.Baseline.File})
		}
	}
	return inputs
}

// captureDestinations lists the snapshots a capture run writes, one per
// target that has a file-backed destination for the kind of snapshot
// being captured. Two targets resolving to one path would have the
// second silently overwrite the first, which a `{certname}`-free literal
// path in the defaults block produces.
func captureDestinations(targets []resolve.Target, catalog bool) []artifact.File {
	out := make([]artifact.File, 0, len(targets))
	for _, target := range targets {
		path := target.Facts.File
		role := "the factset snapshot of " + target.Certname
		if catalog {
			path, role = target.Baseline.File, "the catalog snapshot of "+target.Certname
		}
		if path != "" {
			out = append(out, artifact.File{Role: role, Path: path})
		}
	}
	return out
}

// newCompareWorkflow builds the compare pipeline from resolved
// configuration, using the same hardened transports and the same
// compiler adapter `capture` uses. Capture catalog and comparison share
// one adapter and one policy.
//
// One client per service, not one per use. The compiler client serves
// both the candidate-catalog adapter and file-content retrieval, and the
// PuppetDB client both the fact/baseline adapter and the impact
// estimate: they are the same endpoint, the same credentials, and the
// same hardened transport, so a second client to the same service buys
// nothing but a second connection pool. The two services still get
// wholly separate clients, which is the isolation that matters: neither
// service's credentials can reach the other.
//
// The PuppetDB client is built only when cfg says this run needs one.
// A comparison of file-backed facts against a file-backed baseline with
// the impact estimate disabled contacts PuppetDB not at all, and must
// not require a PuppetDB identity to start.
func newCompareWorkflow(cfg resolve.Config, debugOpts []transport.Option) (*compare.Workflow, error) {
	compilerClient, err := transport.NewClient(cfg.Services.Compiler, debugOpts...)
	if err != nil {
		return nil, fmt.Errorf("building compiler client: %w", err)
	}

	fileSource := puppetdb.NewFileSource()
	w := &compare.Workflow{
		FileFacts:        fileSource,
		FileBaseline:     fileSource,
		Compiler:         compiler.NewAdapter(compilerClient, cfg.Services.Compiler.URL),
		ContentRetriever: filecontent.NewCompilerContentResolver(compilerClient, cfg.Services.Compiler.URL),
		ToolVersion:      toolVersion,
		Now:              clock,
	}

	if cfg.Required.PuppetDB {
		puppetDBClient, err := transport.NewClient(cfg.Services.PuppetDB, debugOpts...)
		if err != nil {
			return nil, fmt.Errorf("building puppetdb client: %w", err)
		}
		adapter := puppetdb.NewAdapter(puppetDBClient, cfg.Services.PuppetDB.URL)
		w.PuppetDBFacts = adapter
		w.PuppetDBBaseline = adapter
		w.ImpactQuerier = impact.NewQuerier(puppetDBClient, cfg.Services.PuppetDB.URL)
	}
	return w, nil
}

// writeReports emits the requested artifacts. Text goes to stdout when
// --text-out is omitted; JSON and HTML are written only when explicitly
// requested.
//
// The file artifacts are written before the text report, and the stdout
// text report last of all. A failed artifact write is an operational
// error (exit 30), and the text report's stated outcome is load-bearing,
// so emitting `outcome: clean (exit 0)` to a CI log and then exiting 30
// because an artifact could not be written would put the log's most-read
// line in direct contradiction with the process result. Ordering the
// writes this way means the contradiction cannot occur: whatever reaches
// stdout is the outcome the process exits with.
//
// Artifacts are written 0644: unlike a snapshot envelope (0600), a report
// is a review artifact meant to be read by CI and by humans, and it
// contains no credentials, private material, managed content bytes, or
// unredacted sensitive values by construction.
func writeReports(f compareFlags, result model.Result, stdout *os.File) error {
	// Display policy applies to the text report alone. report.JSON takes no
	// options by design: it is the complete machine-readable record, and a
	// flag that changed what it contained would make one run's artifact
	// incomparable with another's. report.HTML takes none because it shows
	// everything too, using disclosure rather than omission to stay
	// readable.
	//
	// The nil passed to both renderers is the change assessment. `compare`
	// never has one: it does not contact an inference service, and an
	// assessment reaches a report only through `explain`. A nil renders
	// nothing at all, so these are the artifacts v0.1.0 wrote.
	opts := report.Options{ImpactNodes: f.impactNodes}

	var pending []pendingArtifact
	if f.jsonOut != "" {
		data, err := report.JSON(result)
		if err != nil {
			return err
		}
		pending = append(pending, pendingArtifact{role: "JSON report", path: f.jsonOut, data: data})
	}
	if f.htmlOut != "" {
		data, err := report.HTML(result, nil)
		if err != nil {
			return err
		}
		pending = append(pending, pendingArtifact{role: "HTML report", path: f.htmlOut, data: data})
	}
	text, err := report.Text(result, nil, opts)
	if err != nil {
		return err
	}
	if f.textOut != "" {
		pending = append(pending, pendingArtifact{role: "text report", path: f.textOut, data: text})
	}

	if err := publish(pending); err != nil {
		return err
	}
	if f.textOut == "" {
		if _, err := stdout.Write(text); err != nil {
			return fmt.Errorf("writing text report to stdout: %w", err)
		}
	}
	return nil
}

// pendingArtifact is one rendered artifact waiting to be published.
type pendingArtifact struct {
	role string
	path string
	data []byte
}

// publish writes rendered artifacts, in order, and reports what actually
// happened when one of them cannot be written.
//
// Every artifact is rendered before any is published (see the callers),
// so the common failure, a renderer that cannot produce one of them, costs
// nothing and leaves nothing behind. Publication itself is not a
// transaction across files and is not described as one: the filesystem
// offers atomicity per file, which internal/artifact uses, and there is
// no way to make three separate destinations appear together. What this
// does guarantee is that each individual artifact is complete or absent,
// and that a partial run says which of the requested artifacts exist, so
// a CI job archiving them is not left guessing.
func publish(artifacts []pendingArtifact) error {
	for i, a := range artifacts {
		// Reports are 0644: unlike a snapshot envelope (0600), a report is a
		// review artifact meant to be read by CI and by humans, and it
		// contains no credentials, private material, managed content bytes,
		// or unredacted sensitive values by construction.
		if err := artifact.Write(a.path, a.data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w%s", a.role, err, publishedSoFar(artifacts[:i], artifacts[i+1:]))
		}
	}
	return nil
}

// publishedSoFar renders the "what exists now" clause of a publication
// failure. A message that says only which artifact failed leaves the
// operator to work out whether the others were written.
func publishedSoFar(done, remaining []pendingArtifact) string {
	describe := func(as []pendingArtifact) string {
		names := make([]string, 0, len(as))
		for _, a := range as {
			names = append(names, a.role+" ("+a.path+")")
		}
		return strings.Join(names, ", ")
	}
	switch {
	case len(done) == 0 && len(remaining) == 0:
		return ""
	case len(done) == 0:
		return "; not written: " + describe(remaining)
	case len(remaining) == 0:
		return "; already written: " + describe(done)
	default:
		return "; already written: " + describe(done) + "; not written: " + describe(remaining)
	}
}

// captureFlags holds the parsed `capture facts`/`capture catalog` flags.
type captureFlags struct {
	targets     string
	services    string
	environment string
	replace     bool
	debug       debugFlags
}

func runCapture(args []string, stdout, stderr *os.File) exitcode.Code {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "piace capture: expected \"facts\" or \"catalog\"")
		return exitcode.OperationalError
	}

	switch args[0] {
	case "facts":
		return runCaptureFacts(args[1:], stdout, stderr)
	case "catalog":
		return runCaptureCatalog(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "piace capture: unknown subcommand %q\n", args[0])
		return exitcode.OperationalError
	}
}

func runCaptureFacts(args []string, stdout, stderr *os.File) exitcode.Code {
	fs := flag.NewFlagSet("capture facts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f captureFlags
	fs.StringVar(&f.targets, "targets", "", "path to the target YAML file (required)")
	fs.StringVar(&f.services, "services", "", "path to the services YAML file (required)")
	fs.BoolVar(&f.replace, "replace", false, "overwrite an existing snapshot")
	f.debug.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitcode.OperationalError
	}
	if f.targets == "" || f.services == "" {
		fmt.Fprintln(stderr, "piace capture facts: --targets and --services are required")
		return exitcode.OperationalError
	}

	cfg, err := resolve.Load(resolve.CommandCaptureFacts, f.targets, f.services, resolve.Overrides{})
	if err != nil {
		fmt.Fprintf(stderr, "piace capture facts: %s\n", err)
		return exitcode.OperationalError
	}

	debugOpts, err := f.debug.transportOptions("capture facts", stderr)
	if err != nil {
		fmt.Fprintf(stderr, "piace capture facts: %s\n", err)
		return exitcode.OperationalError
	}

	if err := artifact.Validate([]artifact.File{
		{Role: "--targets", Path: f.targets},
		{Role: "--services", Path: f.services},
	}, captureDestinations(cfg.Targets, false)); err != nil {
		fmt.Fprintf(stderr, "piace capture facts: %s\n", err)
		return exitcode.OperationalError
	}

	puppetDBFacts, err := newPuppetDBAdapter(cfg, debugOpts)
	if err != nil {
		fmt.Fprintf(stderr, "piace capture facts: %s\n", err)
		return exitcode.OperationalError
	}

	w := &capture.Workflow{
		PuppetDBFacts: puppetDBFacts,
		FileFacts:     puppetdb.NewFileSource(),
		Replace:       f.replace,
		Now:           clock,
	}
	outcomes := w.CaptureFacts(context.Background(), cfg.Targets)
	return reportCaptureOutcomes(stdout, stderr, "capture facts", outcomes)
}

func runCaptureCatalog(args []string, stdout, stderr *os.File) exitcode.Code {
	fs := flag.NewFlagSet("capture catalog", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f captureFlags
	fs.StringVar(&f.targets, "targets", "", "path to the target YAML file (required)")
	fs.StringVar(&f.services, "services", "", "path to the services YAML file (required)")
	fs.StringVar(&f.environment, "environment", "", "candidate environment to request the catalog from (required)")
	fs.BoolVar(&f.replace, "replace", false, "overwrite an existing snapshot")
	f.debug.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitcode.OperationalError
	}
	if f.targets == "" || f.services == "" || f.environment == "" {
		fmt.Fprintln(stderr, "piace capture catalog: --targets, --services, and --environment are required")
		return exitcode.OperationalError
	}

	// `capture catalog --environment` is deliberately not a candidate
	// override: it names the environment to snapshot, which is a
	// different thing from the candidate environment under test and
	// typically the opposite one. See capture.candidateEnvironmentView.
	cfg, err := resolve.Load(resolve.CommandCaptureCatalog, f.targets, f.services, resolve.Overrides{})
	if err != nil {
		fmt.Fprintf(stderr, "piace capture catalog: %s\n", err)
		return exitcode.OperationalError
	}
	debugOpts, err := f.debug.transportOptions("capture catalog", stderr)
	if err != nil {
		fmt.Fprintf(stderr, "piace capture catalog: %s\n", err)
		return exitcode.OperationalError
	}
	// A catalog capture reads each target's file-backed factset and writes
	// its baseline snapshot, so the two cannot name one file.
	captureInputs := []artifact.File{
		{Role: "--targets", Path: f.targets},
		{Role: "--services", Path: f.services},
	}
	for _, target := range cfg.Targets {
		if target.Facts.File != "" {
			captureInputs = append(captureInputs,
				artifact.File{Role: "the factset snapshot of " + target.Certname, Path: target.Facts.File})
		}
	}
	if err := artifact.Validate(captureInputs, captureDestinations(cfg.Targets, true)); err != nil {
		fmt.Fprintf(stderr, "piace capture catalog: %s\n", err)
		return exitcode.OperationalError
	}

	// One compiler client for both the catalog request and the file-content
	// retrieval capture performs while the environment is live; see
	// newCompareWorkflow for why a service gets one client rather than one
	// per use.
	compilerClient, err := transport.NewClient(cfg.Services.Compiler, debugOpts...)
	if err != nil {
		fmt.Fprintf(stderr, "piace capture catalog: %s\n", err)
		return exitcode.OperationalError
	}

	w := &capture.Workflow{
		FileFacts:        puppetdb.NewFileSource(),
		Compiler:         compiler.NewAdapter(compilerClient, cfg.Services.Compiler.URL),
		ContentRetriever: filecontent.NewCompilerContentResolver(compilerClient, cfg.Services.Compiler.URL),
		Replace:          f.replace,
		Now:              clock,
	}
	// Only targets whose facts come from PuppetDB need the fact adapter;
	// a run capturing catalogs entirely from file-backed factsets needs
	// no PuppetDB identity at all.
	if cfg.Required.PuppetDB {
		puppetDBFacts, err := newPuppetDBAdapter(cfg, debugOpts)
		if err != nil {
			fmt.Fprintf(stderr, "piace capture catalog: %s\n", err)
			return exitcode.OperationalError
		}
		w.PuppetDBFacts = puppetDBFacts
	}
	outcomes := w.CaptureCatalog(context.Background(), cfg.Targets, f.environment)
	return reportCaptureOutcomes(stdout, stderr, "capture catalog", outcomes)
}

// newPuppetDBAdapter builds the PuppetDB-backed fact/baseline source
// adapter (internal/puppetdb) from cfg's resolved PuppetDB service endpoint, per
// internal/transport's hardened mTLS transport construction.
func newPuppetDBAdapter(cfg resolve.Config, debugOpts []transport.Option) (*puppetdb.Adapter, error) {
	client, err := transport.NewClient(cfg.Services.PuppetDB, debugOpts...)
	if err != nil {
		return nil, fmt.Errorf("building puppetdb client: %w", err)
	}
	return puppetdb.NewAdapter(client, cfg.Services.PuppetDB.URL), nil
}

// reportCaptureOutcomes prints one line per target outcome and returns
// the process exit code: OperationalError if any target failed, Success
// otherwise. A capture run that reports a failure for even one target
// must never exit 0, mirroring the rule that no result with an
// unreported failure can be clean. A skipped target (no file-backed
// destination configured) is reported but does not affect the exit code.
func reportCaptureOutcomes(stdout, stderr *os.File, label string, outcomes []capture.TargetOutcome) exitcode.Code {
	failed := false
	for _, o := range outcomes {
		for _, warning := range o.Warnings {
			fmt.Fprintf(stderr, "piace %s: %s: warning: %s\n", label, o.Certname, warning)
		}
		if o.Candidate != nil {
			fmt.Fprintf(stdout, "piace %s: %s: requested catalog API %s, effective catalog API %s%s\n",
				label, o.Certname, o.Candidate.RequestedAPI, o.Candidate.EffectiveAPI, fallbackNote(*o.Candidate))
			if o.Candidate.TrustedFactsSource != "" {
				fmt.Fprintf(stdout, "piace %s: %s: trusted facts from %s\n", label, o.Certname, o.Candidate.TrustedFactsSource)
			}
			if o.Candidate.V3Warning != "" {
				fmt.Fprintln(stderr, o.Candidate.V3Warning)
			}
		}
		switch {
		case o.Failed():
			failed = true
			fmt.Fprintf(stderr, "piace %s: %s: %s\n", label, o.Certname, o.Diagnostic.Message)
		case o.Skipped:
			fmt.Fprintf(stdout, "piace %s: %s: skipped (no file-backed destination configured)\n", label, o.Certname)
		default:
			fmt.Fprintf(stdout, "piace %s: %s: wrote %s\n", label, o.Certname, o.Path)
		}
	}
	if failed {
		return exitcode.OperationalError
	}
	return exitcode.Success
}

// fallbackNote annotates the effective API line when a permitted
// v4-to-v3 fallback executed. Requested and effective API are printed
// unconditionally, since an operator reading a capture log has no other
// way to tell a configured v3 capture from a v4 capture that ended up on
// v3, and both wrote a snapshot with v3's trust and persistence
// consequences.
func fallbackNote(p model.CandidateProvenance) string {
	if !p.FellBackFromV4 {
		return ""
	}
	return " (fell back from v4)"
}

// explainFlags holds the parsed `explain` flags.
type explainFlags struct {
	jsonIn   string
	services string
	change   string
	aiOut    string
	htmlOut  string
	// failOnInferenceError turns a failed assessment into exit 30. It is
	// off by default and documented as a deliberate loosening in the
	// other direction: a change assessment is advisory, so a CI job that
	// fails because an inference service was briefly unavailable is
	// failing for a reason that has nothing to do with the change under
	// test. An operator who would rather know may ask for it.
	failOnInferenceError bool
	debug                debugFlags
}

// runExplain produces a change assessment from a stored result document.
//
// It contacts exactly one service, the configured inference service, and
// constructs no compiler client and no PuppetDB client, whatever a
// services file happens to name. That is not an optimisation: `explain`
// sends catalog-derived data outside the building, and the set of hosts
// it can reach while doing so has to be short enough to state in one
// sentence.
//
// Nothing here can fail a comparison. The result document is read, never
// rewritten; its outcome and exit code are the run's, not this command's.
func runExplain(args []string, stdout, stderr *os.File) exitcode.Code {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f explainFlags
	fs.StringVar(&f.jsonIn, "json-in", "", "path to the stored JSON result document, or - for stdin (required)")
	fs.StringVar(&f.services, "services", "", "path to the services YAML file (required)")
	fs.StringVar(&f.change, "change", "", "path to a change context file describing the repository change under test")
	fs.StringVar(&f.aiOut, "ai-out", "", "path to write the change assessment artifact")
	fs.StringVar(&f.htmlOut, "html-out", "", "path to write the report re-rendered with the assessment")
	fs.BoolVar(&f.failOnInferenceError, "fail-on-inference-error", false, "exit 30 when the change assessment could not be produced")
	f.debug.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitcode.OperationalError
	}
	if f.jsonIn == "" || f.services == "" {
		fmt.Fprintln(stderr, "piace explain: --json-in and --services are required")
		return exitcode.OperationalError
	}
	// An explain run with no output flag would contact an inference
	// service, disclose a comparison to it, and discard the answer. It is
	// a usage error rather than a no-op for that reason.
	if f.aiOut == "" && f.htmlOut == "" {
		fmt.Fprintln(stderr, "piace explain: at least one of --ai-out and --html-out is required")
		return exitcode.OperationalError
	}

	if err := artifact.Validate([]artifact.File{
		{Role: "--json-in", Path: f.jsonIn},
		{Role: "--services", Path: f.services},
	}, []artifact.File{
		{Role: "--ai-out", Path: f.aiOut},
		{Role: "--html-out", Path: f.htmlOut},
	}); err != nil {
		fmt.Fprintf(stderr, "piace explain: %s\n", err)
		return exitcode.OperationalError
	}

	raw, err := readResultDocument(f.jsonIn)
	if err != nil {
		fmt.Fprintf(stderr, "piace explain: %s\n", err)
		return exitcode.OperationalError
	}
	result, err := report.DecodeJSON(raw)
	if err != nil {
		fmt.Fprintf(stderr, "piace explain: %s\n", err)
		return exitcode.OperationalError
	}
	if result.SchemaVersion != model.ResultSchemaVersion {
		fmt.Fprintf(stderr, "piace explain: result document schema_version %d is not supported by piace %s, which reads version %d\n",
			result.SchemaVersion, toolVersion, model.ResultSchemaVersion)
		return exitcode.OperationalError
	}
	// The checksum is over the document's *canonical* form, not its
	// literal bytes: snapshot.Checksum canonicalizes before hashing. So
	// it ties an assessment to the comparison the document records rather
	// than to one file's whitespace, and it will not match a plain
	// `sha256sum report.json`.
	checksum, err := snapshot.Checksum(raw)
	if err != nil {
		fmt.Fprintf(stderr, "piace explain: checksumming the result document: %s\n", err)
		return exitcode.OperationalError
	}

	in, err := resolve.LoadInferenceFile(f.services)
	if err != nil {
		fmt.Fprintf(stderr, "piace explain: %s\n", err)
		return exitcode.OperationalError
	}
	changeContext, err := assess.LoadChangeContext(f.change)
	if err != nil {
		fmt.Fprintf(stderr, "piace explain: %s\n", err)
		return exitcode.OperationalError
	}

	inferenceOpts, err := f.debug.inferenceOptions("explain", stderr)
	if err != nil {
		fmt.Fprintf(stderr, "piace explain: %s\n", err)
		return exitcode.OperationalError
	}
	client, err := inference.New(in.URL, in.Token, in.Timeout, inferenceOpts...)
	if err != nil {
		fmt.Fprintf(stderr, "piace explain: %s\n", err)
		return exitcode.OperationalError
	}
	client.SetHTTPClient(inferenceHTTPClient)

	assessment, diagnostics := assess.Produce(context.Background(), client, result, changeContext, in.Assess, assess.Meta{
		GeneratedAt:          clock().UTC().Format(time.RFC3339),
		ModelID:              in.Assess.Model,
		EndpointAuthority:    in.Authority,
		SourceReportChecksum: checksum,
	})
	// The diagnostics are attached once, here, before anything renders or
	// writes: an artifact that records why every group came back unknown
	// and a report that does not would be two accounts of the same run.
	assessment.Diagnostics = diagnostics

	if err := writeAssessment(f, result, assessment); err != nil {
		fmt.Fprintf(stderr, "piace explain: %s\n", err)
		return exitcode.OperationalError
	}

	for _, d := range diagnostics {
		fmt.Fprintf(stderr, "piace explain: %s: %s\n", d.Severity, d.Message)
	}
	if f.failOnInferenceError && assess.HasErrorDiagnostic(diagnostics) {
		return exitcode.OperationalError
	}
	return exitcode.Success
}

// readResultDocument reads the stored result document from a path, or
// from stdin when the path is `-`. Reading stdin is what lets a CI job
// pipe `compare --json-out /dev/stdout` straight into `explain` without
// an intermediate file.
func readResultDocument(path string) ([]byte, error) {
	if path == "-" {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading the result document from stdin: %w", err)
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the result document: %w", err)
	}
	return raw, nil
}

// writeAssessment writes the artifacts `explain` was asked for, 0644 for
// the same reason a report is: a change assessment carries no credential
// and no managed content, and CI has to be able to publish it.
func writeAssessment(f explainFlags, result model.Result, a assess.Assessment) error {
	var pending []pendingArtifact
	if f.aiOut != "" {
		data, err := assess.JSON(a)
		if err != nil {
			return err
		}
		pending = append(pending, pendingArtifact{role: "change assessment", path: f.aiOut, data: data})
	}
	if f.htmlOut != "" {
		data, err := report.HTML(result, &a)
		if err != nil {
			return err
		}
		pending = append(pending, pendingArtifact{role: "HTML report", path: f.htmlOut, data: data})
	}
	return publish(pending)
}
