// Package capture implements PIACE's `capture facts` and `capture
// catalog` workflows: retrieving a target's latest factset from PuppetDB
// (facts) or a candidate catalog from the compiler (catalog), wrapping
// the result in a snapshot.Envelope, and writing it to the target's
// resolved local snapshot path.
//
// # Which targets each command captures for
//
// There is no destination-path CLI flag for `capture facts` or `capture
// catalog`: the CLI surface takes only --targets and --services, plus
// --environment for catalog. The only per-target destination path the
// resolved configuration model (internal/config/resolve) exposes is
// Target.Facts.File / Target.Baseline.File, and resolve.go's own
// validation makes each of those fields present if and only if the
// corresponding Source resolves to "file": a target configured with
// facts.source: puppetdb has Facts.File == "" by construction (see
// resolveOneTarget's "facts.file must not be set when facts.source is
// puppetdb" rule).
//
// This package therefore implements the only coherent reading of that
// CLI surface: CaptureFacts writes a snapshot only for targets whose
// Facts.Source is config.FactSourceFile (using Target.Facts.File as the
// destination), and CaptureCatalog writes a snapshot only for targets
// whose Baseline.Source is config.BaselineSourceFile (using
// Target.Baseline.File as the destination). That matches the workflow
// snapshots exist for exactly: CI refreshes catalog snapshots from each
// target's default environment after a merge to the baseline branch,
// then uses those snapshots as development-branch baselines. The
// refreshed snapshot and its later consumption are the same configured
// file, which only exists in the resolved model when the corresponding
// source is file. A target without a matching file destination is
// skipped with a reported warning diagnostic rather than silently doing
// nothing or failing the whole run.
//
// # Compiler side effects and retained content
//
// PuppetDB input requests are reads. Compiler v4 requests disable persistence;
// v3 requests can persist facts and catalogs. Capture reports the effective API
// and v3 effects, including after a failed request. Snapshot compiler_api records
// the effective API, including fallback.
//
// Catalog capture resolves single-file sources while the requested environment
// is live and puts their locally computed digests inside the checksummed
// payload. Inline content and compiler static metadata already retain evidence.
// Unsupported directory/recursive sources are retained with a warning; later
// comparison must report their byte comparison as unsupported. Retrieval and
// checksum failures prevent snapshot publication. Capture evaluates all files,
// including those a comparison would exclude, since the snapshot must support
// reuse under a different comparison policy.
//
// Factsets are validated before capture or compilation, and built envelopes are
// checked for required metadata and payload identity before publication.
package capture
