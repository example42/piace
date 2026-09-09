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
// CatalogSource: the envelope adapter, selected by a target whose
// Facts.Source or Baseline.Source resolves to config.FactSourceFile or
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
// FileSource never performs any network I/O and never mutates PuppetDB:
// it only reads local snapshot files written by the capture workflow.
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
// Provenance mapping: Kind is model.SourceKindFile, and Certname,
// Environment, ProducerTimestamp, Producer and CatalogIdentity are taken
// from the decoded Factset payload itself (fs.Certname, fs.Environment,
// fs.ProducerTimestamp, fs.Producer, fs.Hash) rather than from envelope
// metadata. A file-backed provenance therefore carries the exact same
// shape and meaning as the PuppetDB-backed provenance in adapter.go: the
// only thing that differs between the two sources is Kind and where the
// bytes came from, not what the fields mean. The envelope's own
// CapturedAt and Source fields, for a captured payload that does not
// repeat that information, stay available on the Envelope for a caller
// that wants capture provenance specifically (see LoadEnvelope), and are
// not folded into SourceProvenance, which is a fact or catalog *content*
// provenance record.
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
			"malformed or unparseable factset payload in snapshot")
		return Factset{}, model.SourceProvenance{}, &diag
	}

	if _, err := ValidateFactset(fs, target.Certname); err != nil {
		diag := snapshotDiagnostic(model.OperationLoadFacts, target.Certname, err.Error())
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
// Unlike Adapter.LoadBaseline's PuppetDB environment check (see doc.go),
// this file-backed check involves no judgment call. A snapshot selected
// as a fact or baseline source has its recorded target identity and
// integrity checksum validated before comparison, and a catalog snapshot
// selected as a baseline must also match the resolved baseline
// environment. There is no "regardless of its environment" carve-out for
// a file source: that carve-out is about a live PuppetDB retrieval's
// semantics, and a file snapshot's whole purpose is to pin one captured
// environment on purpose.
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
			"malformed or unparseable catalog payload in snapshot")
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	if target.Certname == "" || cat.Certname != target.Certname {
		diag := snapshotDiagnostic(model.OperationLoadBaseline, target.Certname, "catalog certname does not match the requested target")
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	if cat.Environment != target.Baseline.Environment {
		diag := snapshotDiagnostic(model.OperationLoadBaseline, target.Certname,
			fmt.Sprintf("baseline catalog snapshot %s environment %q does not match the configured baseline environment %q",
				target.Baseline.File, cat.Environment, target.Baseline.Environment))
		return Catalog{}, model.SourceProvenance{}, &diag
	}
	// The envelope itself also records requested_environment for a catalog
	// snapshot (mandatory metadata); a mismatch between the payload's own recorded
	// environment and the envelope's requested_environment would indicate a
	// corrupted or hand-edited snapshot rather than a normal operator error,
	// so it is checked too, defensively, with the same diagnostic operation.
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
		CatalogIdentity:   env.PayloadChecksum,
		Producer:          cat.Producer,
	}
	return cat, prov, nil
}

// snapshotDiagnostic builds a model.Diagnostic for a local snapshot load
// or validation failure. Unlike transport.Diagnostic, used by the
// PuppetDB adapter for a remote service failure, there is no host or
// status metadata to record here, and message is already a safe, locally
// constructed string that never carries raw file content, so it is used
// as-is.
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
