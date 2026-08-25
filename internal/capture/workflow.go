package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
	"github.com/example42/piace/internal/snapshot"
)

// Workflow implements `capture facts` and `capture catalog`, per
// design.md section 6 and requirements.md 11.1-11.3.
//
// It never issues a PuppetDB write/command request: every retrieval goes
// through PuppetDBFacts.Load or PuppetDBBaseline.LoadBaseline, both
// read-only per internal/puppetdb's doc.go, and the only local write I/O
// is snapshot.Write to the filesystem.
type Workflow struct {
	// PuppetDBFacts is the PuppetDB-backed FactSource (task 4's Adapter),
	// used when a target's Facts.Source resolves to puppetdb.
	PuppetDBFacts puppetdb.FactSource
	// FileFacts is the file-backed FactSource (puppetdb.FileSource), used
	// when a target's Facts.Source resolves to file — a target can select
	// a file-backed factset as the input to catalog capture just as it
	// can for comparison, per requirements.md 2.1/11.3's "configured fact
	// source" framing, which does not privilege one source for capture.
	FileFacts puppetdb.FactSource
	// Compiler requests the candidate catalog for `capture catalog`. See
	// compiler.go for the task 6 boundary; production wiring supplies
	// StubCompiler{} until task 6 lands.
	Compiler CompilerCatalogRequester
	// Replace, when true, allows overwriting an existing snapshot file
	// (--replace). When false, Write's overwrite refusal
	// (snapshot.ErrExists) becomes this target's reported diagnostic.
	Replace bool
	// Now supplies the current time for captured_at; nil uses time.Now.
	// Tests inject a fixed clock for deterministic assertions.
	Now func() time.Time
}

// factSourceFor selects PuppetDBFacts or FileFacts for target, per
// target.Facts.Source, using puppetdb.SelectFactSource.
func (w *Workflow) factSourceFor(target resolve.Target) puppetdb.FactSource {
	return puppetdb.SelectFactSource(target, w.PuppetDBFacts, w.FileFacts)
}

// candidateEnvironmentView returns a copy of target with the candidate
// environment replaced by environment.
//
// `capture catalog --environment ENV` names the environment to request,
// per design.md section 2.1's CLI surface and requirements.md 11.2 ("THE
// CLI SHALL provide arguments or subcommands to request each target's
// catalog from the configured compiler for a specified environment"). A
// target's own candidate.environment is the CI environment under test —
// a different thing, and typically a different value, since the whole
// point of requirement 11.7's workflow is capturing a *production*
// baseline for a *development-branch* comparison to run against.
//
// Without this view the request would carry the target's candidate
// environment while the envelope recorded --environment, producing a
// snapshot whose requested_environment metadata contradicted its own
// payload. Every later validation that trusts that metadata — the
// baseline-environment check a file-backed baseline runs (design.md
// section 6) most of all — would then be validating against a label
// rather than against the catalog. The compiler adapter's own
// environment verification (design.md section 5) checks the response
// against the requested environment, so overriding it here is also what
// makes a wrong-environment response a reported compilation failure
// instead of a silently mislabeled capture.
func candidateEnvironmentView(target resolve.Target, environment string) resolve.Target {
	target.Candidate.Environment = environment
	return target
}

// puppetDBView returns a copy of target with Facts.Source forced to
// config.FactSourcePuppetDB and Facts.File cleared, satisfying
// puppetdb.Adapter.Load's misuse guard (see doc.go) for a call that must
// query PuppetDB regardless of the target's own configured comparison-
// time fact source. See captureFactsForTarget's comment for why this is
// needed.
func puppetDBView(target resolve.Target) resolve.Target {
	target.Facts = resolve.Facts{Source: config.FactSourcePuppetDB}
	return target
}

// CaptureFacts implements `capture facts`: for every target whose
// Facts.Source resolves to config.FactSourceFile, it retrieves the
// target's latest factset from PuppetDB (per requirements.md 11.1: "THE
// CLI SHALL provide arguments or subcommands to retrieve each target's
// latest factset from PuppetDB and write a local per-target factset
// file" — the destination is always a *local* file regardless of which
// source a comparison run would use, but see doc.go for why this
// implementation still only captures for targets configured with
// facts.source: file, since that is the only target-file-declared local
// destination path available), wraps it in a factset Envelope, and
// writes it to Target.Facts.File.
//
// A target whose Facts.Source is puppetdb is skipped (not failed): it has
// no configured local factset destination for this run to write to.
// One target's retrieval, encoding, or write failure is recorded in that
// target's TargetOutcome and does not stop processing the remaining
// targets, per design.md's Architecture note ("A target error is
// captured in that target's node result and processing continues for the
// other valid targets").
func (w *Workflow) CaptureFacts(ctx context.Context, targets []resolve.Target) []TargetOutcome {
	outcomes := make([]TargetOutcome, 0, len(targets))
	for _, target := range targets {
		outcomes = append(outcomes, w.captureFactsForTarget(ctx, target))
	}
	return outcomes
}

func (w *Workflow) captureFactsForTarget(ctx context.Context, target resolve.Target) TargetOutcome {
	if target.Facts.Source != config.FactSourceFile {
		return TargetOutcome{Certname: target.Certname, Skipped: true}
	}

	// requirements.md 11.1 requires `capture facts` to retrieve from
	// PuppetDB unconditionally, independent of the target's normal
	// comparison-time facts.source (which is why this target even has a
	// local facts.file destination configured in the first place: it is
	// deliberately set to file so a later `compare` run reads the frozen
	// snapshot instead of live PuppetDB — see requirements.md Requirement
	// 11's user story). Adapter.Load (task 4) validates
	// target.Facts.Source == puppetdb as a misuse guard against being
	// invoked for a target that has selected a different source for
	// comparison; here that guard would misfire, since capture's whole
	// point is to query PuppetDB regardless of the target's selected
	// comparison-time source. puppetDBView presents the same certname to
	// the adapter with Facts.Source forced to puppetdb, satisfying that
	// guard without altering task 4's adapter contract or the caller's
	// own resolved target.
	fs, _, diag := w.PuppetDBFacts.Load(ctx, puppetDBView(target))
	if diag != nil {
		return TargetOutcome{Certname: target.Certname, Diagnostic: diag}
	}

	env, err := buildFactsetEnvelope(target.Certname, fs, nowUTCRFC3339(w.Now))
	if err != nil {
		d := snapshotDiagnostic(target.Certname, err)
		return TargetOutcome{Certname: target.Certname, Diagnostic: &d}
	}

	if err := snapshot.Write(target.Facts.File, env, w.Replace); err != nil {
		d := snapshotDiagnostic(target.Certname, err)
		return TargetOutcome{Certname: target.Certname, Diagnostic: &d}
	}

	return TargetOutcome{Certname: target.Certname, Path: target.Facts.File}
}

// CaptureCatalog implements `capture catalog`: for every target whose
// Baseline.Source resolves to config.BaselineSourceFile, it loads the
// target's facts from its configured fact source (requirements.md 11.3:
// "capturing a catalog snapshot... SHALL use the target facts from the
// configured fact source"), requests a candidate catalog from the
// compiler for environment via w.Compiler, wraps the result in a catalog
// Envelope recording target, requested environment, compiler API version,
// fact source, and capture timestamp (requirements.md 11.3/11.5), and
// writes it to Target.Baseline.File.
//
// Until task 6 supplies a working CompilerCatalogRequester, w.Compiler
// (StubCompiler in production wiring) always returns a diagnostic here;
// this is reported per-target exactly like any other capture failure —
// see compiler_stub.go — never a panic or a silently-succeeded capture.
func (w *Workflow) CaptureCatalog(ctx context.Context, targets []resolve.Target, environment string) []TargetOutcome {
	outcomes := make([]TargetOutcome, 0, len(targets))
	for _, target := range targets {
		outcomes = append(outcomes, w.captureCatalogForTarget(ctx, target, environment))
	}
	return outcomes
}

func (w *Workflow) captureCatalogForTarget(ctx context.Context, target resolve.Target, environment string) TargetOutcome {
	if target.Baseline.Source != config.BaselineSourceFile {
		return TargetOutcome{Certname: target.Certname, Skipped: true}
	}

	fs, _, diag := w.factSourceFor(target).Load(ctx, target)
	if diag != nil {
		return TargetOutcome{Certname: target.Certname, Diagnostic: diag}
	}

	factsetIdentity, err := puppetdb.FactsetIdentity(fs)
	if err != nil {
		d := snapshotDiagnostic(target.Certname, err)
		return TargetOutcome{Certname: target.Certname, Diagnostic: &d}
	}

	cat, _, _, diag := w.Compiler.RequestCandidate(ctx, candidateEnvironmentView(target, environment), fs)
	if diag != nil {
		return TargetOutcome{Certname: target.Certname, Diagnostic: diag}
	}

	env, err := buildCatalogEnvelope(target, cat, environment, factsetIdentity, nowUTCRFC3339(w.Now))
	if err != nil {
		d := snapshotDiagnostic(target.Certname, err)
		return TargetOutcome{Certname: target.Certname, Diagnostic: &d}
	}

	if err := snapshot.Write(target.Baseline.File, env, w.Replace); err != nil {
		d := snapshotDiagnostic(target.Certname, err)
		return TargetOutcome{Certname: target.Certname, Diagnostic: &d}
	}

	return TargetOutcome{Certname: target.Certname, Path: target.Baseline.File}
}

// buildFactsetEnvelope marshals fs as the envelope payload, computes its
// checksum, and constructs a factset Envelope. Source.Producer is fs's
// own reported producer (the PuppetDB-facing compiler/agent that last
// wrote this factset), per design.md section 6's "source records the
// adapter and producer identity ... when supplied by the service".
func buildFactsetEnvelope(certname string, fs puppetdb.Factset, capturedAt string) (snapshot.Envelope, error) {
	payload, err := json.Marshal(fs)
	if err != nil {
		return snapshot.Envelope{}, fmt.Errorf("encoding factset payload: %w", err)
	}
	sum, err := snapshot.Checksum(payload)
	if err != nil {
		return snapshot.Envelope{}, fmt.Errorf("computing factset checksum: %w", err)
	}
	return snapshot.Envelope{
		FormatVersion:   snapshot.FormatVersion,
		Kind:            snapshot.KindFactset,
		Target:          certname,
		Source:          snapshot.Source{Kind: string(model.SourceKindPuppetDB), Producer: fs.Producer},
		CapturedAt:      capturedAt,
		PayloadChecksum: sum,
		Payload:         payload,
	}, nil
}

// buildCatalogEnvelope marshals cat as the envelope payload and
// constructs a catalog Envelope with its mandatory
// requested_environment/compiler_api/input_factset_identity fields
// populated, per requirements.md 11.5.
func buildCatalogEnvelope(target resolve.Target, cat puppetdb.Catalog, environment, factsetIdentity, capturedAt string) (snapshot.Envelope, error) {
	payload, err := json.Marshal(cat)
	if err != nil {
		return snapshot.Envelope{}, fmt.Errorf("encoding catalog payload: %w", err)
	}
	sum, err := snapshot.Checksum(payload)
	if err != nil {
		return snapshot.Envelope{}, fmt.Errorf("computing catalog checksum: %w", err)
	}
	return snapshot.Envelope{
		FormatVersion:        snapshot.FormatVersion,
		Kind:                 snapshot.KindCatalog,
		Target:               target.Certname,
		Source:               snapshot.Source{Kind: "compiler", Producer: cat.Producer},
		CapturedAt:           capturedAt,
		RequestedEnvironment: environment,
		CompilerAPIVersion:   snapshot.CompilerAPI(target.Candidate.CatalogAPI),
		InputFactsetIdentity: factsetIdentity,
		PayloadChecksum:      sum,
		Payload:              payload,
	}, nil
}

// snapshotDiagnostic wraps a local envelope-construction or write failure
// as a model.Diagnostic with model.OperationSnapshot, per that constant's
// doc comment.
func snapshotDiagnostic(certname string, err error) model.Diagnostic {
	return model.Diagnostic{
		Severity:  model.SeverityError,
		Operation: model.OperationSnapshot,
		Certname:  certname,
		Message:   err.Error(),
	}
}
