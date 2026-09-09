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
// # Read-only guarantee
//
// No function in this package issues, or can issue, any PuppetDB
// write/command request: CaptureFacts and CaptureCatalog only ever call
// FactSource.Load / CatalogSource.LoadBaseline (both read-only, see
// internal/puppetdb's doc.go) to retrieve input, and use
// CompilerCatalogRequester.RequestCandidate (also read-only against the
// compiler) to obtain a candidate catalog. The only write I/O anywhere in
// this package is internal/snapshot.Write, which writes to the local
// filesystem, never to PuppetDB.
//
// Factsets are validated before capture or candidate compilation. Every built
// envelope is checked for required metadata and payload identity agreement
// before writing, using the same snapshot.Validate contract as file reuse.
package capture
