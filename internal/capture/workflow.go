package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/filecontent"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
	"github.com/example42/piace/internal/snapshot"
)

// Workflow implements `capture facts` and `capture catalog`.
//
// It never issues a PuppetDB write/command request: every retrieval goes
// through PuppetDBFacts.Load or PuppetDBBaseline.LoadBaseline, both
// read-only per internal/puppetdb's doc.go, and the only local write I/O
// is snapshot.Write to the filesystem.
type Workflow struct {
	// PuppetDBFacts is the PuppetDB-backed FactSource (internal/puppetdb's Adapter),
	// used when a target's Facts.Source resolves to puppetdb.
	PuppetDBFacts puppetdb.FactSource
	// FileFacts is the file-backed FactSource (puppetdb.FileSource), used
	// when a target's Facts.Source resolves to file. A target can select a
	// file-backed factset as the input to catalog capture just as it can for
	// comparison: both read the configured fact source, and neither
	// privileges one source for capture.
	FileFacts puppetdb.FactSource
	// Compiler requests the candidate catalog for `capture catalog`.
	// Production wiring supplies internal/compiler.Adapter, the same
	// adapter `compare` uses; see compiler.go for the boundary.
	Compiler         CompilerCatalogRequester
	ContentRetriever filecontent.ContentRetriever
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
// `capture catalog --environment ENV` names the environment to request.
// A target's own candidate.environment is the CI environment under test,
// a different thing and typically a different value, since the whole
// point of the snapshot workflow is capturing a *production* baseline
// for a *development-branch* comparison to run against.
//
// Without this view the request would carry the target's candidate
// environment while the envelope recorded --environment, producing a
// snapshot whose requested_environment metadata contradicted its own
// payload. Every later validation that trusts that metadata, the
// baseline-environment check a file-backed baseline runs most of all,
// would then be validating against a label rather than against the
// catalog. The compiler adapter's own environment verification checks
// the response against the requested environment, so overriding it here
// is also what makes a wrong-environment response a reported compilation
// failure instead of a silently mislabeled capture.
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
// target's latest factset from PuppetDB, wraps it in a factset Envelope,
// and writes it to Target.Facts.File. The destination is always a local
// file whatever source a comparison run would use; see doc.go for why
// this implementation nonetheless captures only for targets configured
// with facts.source: file, that being the only target-file-declared
// local destination path available.
//
// A target whose Facts.Source is puppetdb is skipped, not failed: it has
// no configured local factset destination for this run to write to. One
// target's retrieval, encoding, or write failure is recorded in that
// target's TargetOutcome and does not stop processing the remaining
// targets, the same rule comparison follows.
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

	// `capture facts` retrieves from PuppetDB unconditionally, independent
	// of the target's normal comparison-time facts.source. That source is
	// why this target has a local facts.file destination configured in the
	// first place: it is deliberately set to file so a later `compare` run
	// reads the frozen snapshot instead of live PuppetDB. Adapter.Load
	// validates target.Facts.Source == puppetdb as a misuse guard against
	// being invoked for a target that has selected a different source for
	// comparison; here that guard would misfire, since capture's whole point
	// is to query PuppetDB regardless of the target's selected
	// comparison-time source. puppetDBView presents the same certname to the
	// adapter with Facts.Source forced to puppetdb, satisfying that guard
	// without altering the adapter contract or the caller's own resolved
	// target.
	fs, _, diag := w.PuppetDBFacts.Load(ctx, puppetDBView(target))
	if diag != nil {
		return TargetOutcome{Certname: target.Certname, Diagnostic: diag}
	}

	env, err := buildFactsetEnvelope(target.Certname, fs, nowUTCRFC3339(w.Now))
	if err != nil {
		d := snapshotDiagnostic(target.Certname, err)
		return TargetOutcome{Certname: target.Certname, Diagnostic: &d}
	}

	if err := snapshot.Validate(env, snapshot.KindFactset, target.Certname); err != nil {
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
// target's facts from its configured fact source, requests a candidate
// catalog from the compiler for environment via w.Compiler, wraps the
// result in a catalog Envelope recording target, requested environment,
// compiler API version, fact source, and capture timestamp, and writes
// it to Target.Baseline.File.
//
// A w.Compiler that cannot serve a request, StubCompiler being the
// test-only one, returns a diagnostic here rather than a catalog. That is
// reported per-target exactly like any other capture failure, never a
// panic and never a silently succeeded capture.
func (w *Workflow) CaptureCatalog(ctx context.Context, targets []resolve.Target, environment string) []TargetOutcome {
	outcomes := make([]TargetOutcome, 0, len(targets))
	for _, target := range targets {
		outcomes = append(outcomes, w.captureCatalogForTarget(ctx, target, environment))
	}
	return outcomes
}

func (w *Workflow) captureCatalogForTarget(ctx context.Context, target resolve.Target, environment string) (outcome TargetOutcome) {
	if target.Baseline.Source != config.BaselineSourceFile {
		return TargetOutcome{Certname: target.Certname, Skipped: true}
	}

	fs, _, diag := w.factSourceFor(target).Load(ctx, target)
	if diag != nil {
		return TargetOutcome{Certname: target.Certname, Diagnostic: diag}
	}

	if _, err := puppetdb.ValidateFactset(fs, target.Certname); err != nil {
		d := model.Diagnostic{Certname: target.Certname, Operation: model.OperationLoadFacts, Severity: model.SeverityError, Message: err.Error()}
		return TargetOutcome{Certname: target.Certname, Diagnostic: &d}
	}
	cat, provenance, warnings, diag := w.Compiler.RequestCandidate(ctx, candidateEnvironmentView(target, environment), fs)
	defer func() {
		if provenance.EffectiveAPI != "" {
			outcome.Candidate = &provenance
		}
		outcome.Warnings = append(outcome.Warnings, warnings...)
	}()
	if diag != nil {
		return TargetOutcome{Certname: target.Certname, Diagnostic: diag}
	}
	contentDiagnostics := w.captureContent(ctx, &cat)
	for _, d := range contentDiagnostics {
		if d.Severity == model.SeverityError {
			return TargetOutcome{Certname: target.Certname, Diagnostic: &d, Candidate: &provenance}
		}
		warnings = append(warnings, d.Message)
	}

	env, err := buildCatalogEnvelope(target.Certname, cat, environment, provenance, nowUTCRFC3339(w.Now))
	if err != nil {
		d := snapshotDiagnostic(target.Certname, err)
		return TargetOutcome{Certname: target.Certname, Diagnostic: &d}
	}

	if err := snapshot.Validate(env, snapshot.KindCatalog, target.Certname); err != nil {
		d := snapshotDiagnostic(target.Certname, err)
		return TargetOutcome{Certname: target.Certname, Diagnostic: &d}
	}
	if err := snapshot.Write(target.Baseline.File, env, w.Replace); err != nil {
		d := snapshotDiagnostic(target.Certname, err)
		return TargetOutcome{Certname: target.Certname, Diagnostic: &d}
	}

	return TargetOutcome{Certname: target.Certname, Path: target.Baseline.File, Candidate: &provenance}
}

// buildFactsetEnvelope marshals fs as the envelope payload, computes its
// checksum, and constructs a factset Envelope. Source.Producer is fs's
// own reported producer, the PuppetDB-facing compiler or agent that last
// wrote this factset: the envelope records the adapter and producer
// identity whenever the service supplies one.
func buildFactsetEnvelope(certname string, fs puppetdb.Factset, capturedAt string) (snapshot.Envelope, error) {
	if _, err := puppetdb.ValidateFactset(fs, certname); err != nil {
		return snapshot.Envelope{}, err
	}
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
// requested_environment/capture/input_factset_identity fields populated.
//
// Every provenance value comes from provenance, the record the compiler
// adapter returned for the request it actually issued, and none from the
// target's configuration. Reading target.Candidate.CatalogAPI here is
// what let a v4-to-v3 fallback capture publish a snapshot claiming
// compiler_api v4: configuration says what was asked for, and only the
// adapter knows what answered.
func buildCatalogEnvelope(certname string, cat puppetdb.Catalog, environment string, provenance model.CandidateProvenance, capturedAt string) (snapshot.Envelope, error) {
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
		Target:               certname,
		Source:               snapshot.Source{Kind: "compiler", Producer: cat.Producer},
		CapturedAt:           capturedAt,
		RequestedEnvironment: environment,
		Capture:              captureProvenance(provenance),
		InputFactsetIdentity: provenance.FactsetIdentity,
		PayloadChecksum:      sum,
		Payload:              payload,
	}, nil
}

// captureProvenance projects the compiler adapter's
// model.CandidateProvenance onto the persisted snapshot contract. The
// two are deliberately separate types (see snapshot.CaptureProvenance):
// this projection drops the parts that describe the request rather than
// the payload, the candidate environment (the envelope records the
// requested environment itself) and the v3 warning text (derived from
// the effective API by every reader, from the single-sourced
// model.V3TrustedFactWarning constant).
func captureProvenance(p model.CandidateProvenance) *snapshot.CaptureProvenance {
	return &snapshot.CaptureProvenance{
		RequestedAPI:       snapshot.CompilerAPI(p.RequestedAPI),
		EffectiveAPI:       snapshot.CompilerAPI(p.EffectiveAPI),
		FellBackFromV4:     p.FellBackFromV4,
		TrustedFactsSource: snapshot.TrustedFactsSource(p.TrustedFactsSource),
		FactSource:         snapshot.FactSourceKind(p.FactSource),
	}
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
