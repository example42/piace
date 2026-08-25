package puppetdb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/snapshot"
)

// FileSource is the file-backed implementation of FactSource and
// CatalogSource: the "envelope" adapter named in design.md's Architecture
// diagram ("fact-source adapter (PuppetDB or envelope)", "baseline-source
// adapter (PuppetDB or envelope)"), selected by a target whose
// Facts.Source/Baseline.Source resolves to config.FactSourceFile /
// config.BaselineSourceFile.
//
// It lives in this package (rather than in internal/snapshot) so it can
// implement this package's FactSource/CatalogSource interfaces directly
// without those interfaces having to move, and without internal/snapshot
// importing this package (it does not: snapshot only encodes/checksums/
// writes/loads a generic Envelope, and knows nothing about
// resolve.Target or the Factset/Catalog raw carrier types). This package
// already imports internal/snapshot with no cycle risk, since
// internal/snapshot imports nothing from this codebase's tree.
//
// FileSource never performs any network I/O and never mutates PuppetDB —
// it only reads local snapshot files written by the capture workflow (see
// capture.go).
type FileSource struct{}

// NewFileSource builds a FileSource. It takes no arguments: unlike
// Adapter, a file-backed source needs no transport client or endpoint,
// only the resolve.Target passed to each call, which carries the already-
// resolved snapshot file path.
func NewFileSource() *FileSource { return &FileSource{} }

// Load implements FactSource for a file-backed fact source. It loads,
// checksum-verifies, and shape-validates the factset envelope at
// target.Facts.File, then decodes its payload into a Factset.
//
// Provenance mapping (documented, since design.md does not spell out the
// exact field-by-field mapping for a file-backed source): Kind is
// model.SourceKindFile; Certname/Environment/ProducerTimestamp/Producer/
// CatalogIdentity are taken from the decoded Factset payload itself
// (fs.Certname, fs.Environment, fs.ProducerTimestamp, fs.Producer,
// fs.Hash) rather than from envelope metadata, so a file-backed
// provenance carries the exact same shape and meaning as the PuppetDB-
// backed provenance in adapter.go — the only thing that differs between
// the two sources is Kind and where the bytes came from, not what the
// fields mean. The envelope's own CapturedAt/Source fields (when the
// captured factset payload doesn't repeat that information) are
// available on the Envelope itself for a caller that wants capture
// provenance specifically (see LoadEnvelope), but are not folded into
// SourceProvenance, which is a fact/catalog *content* provenance record.
func (f *FileSource) Load(ctx context.Context, target resolve.Target) (Factset, model.SourceProvenance, *model.Diagnostic) {
	if target.Facts.Source != config.FactSourceFile {
		diag := snapshotDiagnostic(model.OperationLoadFacts, target.Certname,
			fmt.Sprintf("file-backed fact-source adapter invoked for a target whose facts.source is %q, not %q",
				target.Facts.Source, config.FactSourceFile))
		return Factset{}, model.SourceProvenance{}, &diag
	}

	env, err := snapshot.Load(target.Facts.File)
	if err != nil {
		diag := snapshotDiagnostic(model.OperationLoadFacts, target.Certname, err.Error())
		return Factset{}, model.SourceProvenance{}, &diag
	}
	if err := snapshot.Validate(env, snapshot.KindFactset, target.Certname); err != nil {
		diag := snapshotDiagnostic(model.OperationLoadFacts, target.Certname, err.Error())
		return Factset{}, model.SourceProvenance{}, &diag
	}

	var fs Factset
	if err := json.Unmarshal(env.Payload, &fs); err != nil {
		diag := snapshotDiagnostic(model.OperationLoadFacts, target.Certname,
			fmt.Sprintf("malformed or unparseable factset payload in snapshot %s: %s", target.Facts.File, err))
		return Factset{}, model.SourceProvenance{}, &diag
	}

	prov := model.SourceProvenance{
		Kind:              model.SourceKindFile,
		Certname:          fs.Certname,
		Environment:       fs.Environment,
		ProducerTimestamp: fs.ProducerTimestamp,
		CatalogIdentity:   fs.Hash,
		Producer:          fs.Producer,
	}
	return fs, prov, nil
}

// LoadBaseline implements CatalogSource for a file-backed baseline
// source. It loads, checksum-verifies, and shape-validates the catalog
// envelope at target.Baseline.File, then decodes its payload into a
// Catalog.
//
// Unlike Adapter.LoadBaseline's PuppetDB environment check (which applies
// unconditionally to a *live* retrieval, per doc.go's discussion of
// requirements.md 1.3 vs. section 8), this file-backed check is not a
// judgment call about ambiguous requirement text: requirements.md 11.6 is
// unconditional ("WHEN a local snapshot is selected as a fact or baseline
// catalog source, THE CLI SHALL validate its recorded target identity and
// integrity checksum before comparison") and design.md section 6 states
// plainly, "A catalog snapshot selected as a baseline must also match the
// resolved baseline environment." There is no analogous "regardless of
// its environment" carve-out for a file source anywhere in
// requirements.md or design.md — section 8's carve-out is stated
// specifically about baseline.source: puppetdb's retrieval semantics (see
// adapter.go's doc.go), and a file snapshot's whole purpose per
// requirements.md Requirement 11's user story is to pin a specific
// captured environment intentionally. So this check is at least as
// strict as the PuppetDB adapter's, and arguably has stronger textual
// grounding: it is requirements.md 11.6 applied literally, not resolved
// from a perceived tension between two sections.
func (f *FileSource) LoadBaseline(ctx context.Context, target resolve.Target) (Catalog, model.SourceProvenance, *model.Diagnostic) {
	if target.Baseline.Source != config.BaselineSourceFile {
		diag := snapshotDiagnostic(model.OperationLoadBaseline, target.Certname,
			fmt.Sprintf("file-backed baseline-source adapter invoked for a target whose baseline.source is %q, not %q",
				target.Baseline.Source, config.BaselineSourceFile))
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	env, err := snapshot.Load(target.Baseline.File)
	if err != nil {
		diag := snapshotDiagnostic(model.OperationLoadBaseline, target.Certname, err.Error())
		return Catalog{}, model.SourceProvenance{}, &diag
	}
	if err := snapshot.Validate(env, snapshot.KindCatalog, target.Certname); err != nil {
		diag := snapshotDiagnostic(model.OperationLoadBaseline, target.Certname, err.Error())
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	var cat Catalog
	if err := json.Unmarshal(env.Payload, &cat); err != nil {
		diag := snapshotDiagnostic(model.OperationLoadBaseline, target.Certname,
			fmt.Sprintf("malformed or unparseable catalog payload in snapshot %s: %s", target.Baseline.File, err))
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	if cat.Environment != target.Baseline.Environment {
		diag := snapshotDiagnostic(model.OperationLoadBaseline, target.Certname,
			fmt.Sprintf("baseline catalog snapshot %s environment %q does not match the configured baseline environment %q",
				target.Baseline.File, cat.Environment, target.Baseline.Environment))
		return Catalog{}, model.SourceProvenance{}, &diag
	}
	// The envelope itself also records requested_environment for a
	// catalog snapshot (mandatory per requirements.md 11.5); a mismatch
	// between the payload's own recorded environment and the envelope's
	// requested_environment would indicate a corrupted or hand-edited
	// snapshot rather than a normal operator error, so it is checked too,
	// defensively, with the same diagnostic operation.
	if env.RequestedEnvironment != "" && env.RequestedEnvironment != cat.Environment {
		diag := snapshotDiagnostic(model.OperationLoadBaseline, target.Certname,
			fmt.Sprintf("baseline catalog snapshot %s is inconsistent: envelope requested_environment %q does not match payload environment %q",
				target.Baseline.File, env.RequestedEnvironment, cat.Environment))
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	prov := model.SourceProvenance{
		Kind:              model.SourceKindFile,
		Certname:          cat.Certname,
		Environment:       cat.Environment,
		ProducerTimestamp: cat.ProducerTimestamp,
		CatalogIdentity:   cat.Hash,
		Producer:          cat.Producer,
	}
	return cat, prov, nil
}

// snapshotDiagnostic builds a model.Diagnostic for a local snapshot
// load/validation failure. Unlike transport.Diagnostic (used by the
// PuppetDB adapter for a remote service failure), there is no host/status
// metadata to record here — message is already a safe, locally
// constructed string (never raw file content), so it is used as-is.
func snapshotDiagnostic(op model.DiagnosticOperation, certname, message string) model.Diagnostic {
	return model.Diagnostic{
		Severity:  model.SeverityError,
		Operation: op,
		Certname:  certname,
		Message:   message,
	}
}

// Ensure FileSource satisfies both adapter interfaces at compile time.
var (
	_ FactSource    = (*FileSource)(nil)
	_ CatalogSource = (*FileSource)(nil)
)
