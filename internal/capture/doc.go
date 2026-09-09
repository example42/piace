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
// and v3 effects, including after a failed request.
//
// # Recorded provenance
//
// A catalog envelope's provenance describes the request that actually
// happened, not the one configuration asked for. Every value in
// snapshot.CaptureProvenance comes from the model.CandidateProvenance the
// compiler adapter returned: requested API, effective API, whether a permitted
// v4-to-v3 fallback executed, the trusted-fact source of a v4 request, and
// which fact source supplied the input factset, whose identity is recorded
// alongside it as input_factset_identity. Reading the target's configured
// catalog_api instead is what let a fallback capture publish a snapshot
// claiming v4: only the adapter knows what answered.
//
// The v3 trusted-fact and persistence warning is not copied into the file.
// model.V3TrustedFactWarning is single-sourced, and a reader derives the
// warning from an effective API of v3, so the wording can change without
// contradicting snapshots already on disk.
//
// # Snapshot fidelity
//
// A snapshot payload is a validated PIACE projection of the service response,
// not the response itself. It retains everything the comparison contract
// needs: target identity, environment, resources with their parameters and
// sensitivity metadata, edges, static and recursive content metadata, captured
// content digests, and the catalog identity fields (version, code_id,
// catalog_uuid, transaction_uuid). Fields PIACE does not consume are dropped
// by the typed carriers rather than stored: a compiler catalog's tags,
// classes, and catalog_format, and any field a supported service adds that
// internal/puppetdb's Factset and Catalog carriers do not promote. A captured
// catalog is a compiler document, so the PuppetDB query-API fields hash,
// producer, and producer_timestamp are absent from it; they are present in a
// captured factset, which is a PuppetDB document.
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
