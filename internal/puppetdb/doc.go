// Package puppetdb implements PIACE's PuppetDB-backed fact-source and
// baseline-catalog-source adapters: `FactSource.Load(target) -> Factset,
// Provenance` and `CatalogSource.LoadBaseline(target) -> Catalog,
// Provenance`.
//
// # Scope
//
// This package retrieves the latest factset and the latest catalog for
// one explicit certname from PuppetDB's query API, and nothing else. It
// never issues a request to a PuppetDB command or write endpoint: there
// is no code path in this package capable of doing so, since it
// implements only the two read-only adapter interfaces below. PIACE
// never mutates PuppetDB.
//
// Normalizing a raw catalog into the semantic graph
// (`model.NormalizedCatalog`) is internal/normalize's job, not this
// package's. This package's Catalog and Factset types are raw carriers:
// they promote the handful of top-level fields this package commits to
// (certname, environment, producer_timestamp, an identity or hash field,
// producer) to named fields, and keep the resources, edges and facts
// payloads as json.RawMessage for a later stage to parse.
//
// File-backed snapshot loading (target.Facts.Source == file or
// target.Baseline.Source == file) is internal/snapshot's job. This package defines
// the FactSource and CatalogSource interfaces so internal/snapshot's file-backed
// implementation is a drop-in alternative selected by
// target.Facts.Source/target.Baseline.Source, but it does not implement
// that branch itself: Load and LoadBaseline in this package assume they
// are only ever invoked for a resolve.Target whose respective Source is
// puppetdb, and return a diagnostic (rather than guessing) if that
// invariant is violated by a caller.
//
// # Endpoint shape: documented assumption, unverified against a live
// PuppetDB
//
// Protocol adapters are the compatibility boundary, and their exact
// requests and responses have to be demonstrated with fixtures from the
// deployed service versions before a compiler and PuppetDB combination
// is declared supported. Fixture verification against a real deployed
// PuppetDB is explicitly deferred. This package's request and response
// handling is built from PuppetDB's publicly documented v4 query API
// (documentation/api/query/v4/factsets.markdown and
// documentation/api/query/v4/catalogs.markdown in the
// puppetlabs/puppetdb repository), specifically:
//
//   - GET /pdb/query/v4/factsets/<NODE> returns "a single map of the
//     factset structure ... or a JSON error message if the factset is not
//     found", with the success shape:
//     {certname, environment, timestamp, producer_timestamp, producer,
//     facts, hash}. hash is documented as always present ("a hash of the
//     factset's certname, environment, timestamp, facts, and
//     producer_timestamp").
//   - GET /pdb/query/v4/catalogs/<NODE> returns "a single map of the
//     catalog structure ... or a JSON error message if the catalog is not
//     found", with the success shape:
//     {certname, version, environment, hash, transaction_uuid,
//     catalog_uuid, code_id, producer_timestamp, producer, resources,
//     edges}. hash is documented as always present ("SHA-1 sum of catalog
//     resources"); catalog_uuid can be null.
//
// The documented examples show the not-found response as a JSON object
// with only an "error" string field (e.g. {"error": "Could not find
// catalog for my_fake_hostname"}), but the documentation does not state
// which HTTP status code accompanies it. This is an unverified assumption:
// this package treats ANY of the following as "not found/error", so it is
// robust regardless of which convention a given PuppetDB version actually
// uses:
//
//   - a non-200 HTTP status code, or
//   - a 200 response whose decoded body has a non-empty "error" field, or
//   - a 200 response whose decoded body has an empty "certname" field
//     (defensive: catches an unrecognized success-shaped-but-empty body).
//
// In every case, the raw response body text is never placed into a
// Diagnostic.Message (: "They do not preserve raw body text by default,
// because service errors can echo values"); only a fixed, safe
// classification string plus transport.Summary metadata (host, status
// code) is used.
//
// # Baseline-environment-mismatch resolution
//
// The baseline-environment rule is, verbatim:
//
//	"WHEN `baseline.source` is PuppetDB and the returned catalog
//	environment differs from the target's configured baseline
//	environment, THE CLI SHALL fail the target before diffing it."
//
// The target-file shape is specified as:
//
//	"For a development branch, `baseline.source: file` points to a
//	snapshot captured after the target's main/production environment was
//	deployed. A `baseline.source: puppetdb` request intentionally means
//	PuppetDB's current latest catalog, regardless of its environment."
//
// These do not conflict, and this package implements 1.3's rejection
// unconditionally for baseline.source == puppetdb. The resolution:
//
//   - 1.3's own conditional clause is "WHEN baseline.source is PuppetDB
//     ...", i.e. it is explicitly scoped to (and only makes sense for) the
//     puppetdb source. There is no reading of 1.3 under which it applies
//     to baseline.source == file instead — a file snapshot's environment
//     is a property recorded at capture time and checked when the
//     snapshot is loaded, not "returned" by a live retrieval.
//   - Section 8's sentence describes PuppetDB's *retrieval* semantics: you
//     cannot ask PuppetDB's query API for "the catalog last compiled for
//     environment X"; PuppetDB retains and returns only the single latest
//     catalog for a certname, whatever environment it happens to record.
//     That is a statement about what a puppetdb-sourced *request* can
//     express (no environment selection is possible), not an instruction
//     to skip validating the environment PuppetDB actually returns against
//     the operator's configured expectation.
//   - Reading section 8 as silently disabling 1.3 would make 1.3
//     vacuous — 1.3 has no other subject than baseline.source == puppetdb
//     to apply to. It would also remove the one safety net that catches
//     exactly the failure mode section 8 itself warns about: an operator
//     who deliberately points a development-branch comparison's baseline
//     at PuppetDB and gets an unexpected catalog because main/production
//     was redeployed after the branch was cut. 1.3 is that guardrail, not
//     a contradiction of it.
//   - This reading is not a novel interpretation invented here:
//     internal/config/target.go's BaselineConfig doc comment already
//     states that a PuppetDB baseline whose returned environment differs
//     from Environment fails the target before diffing, and every target
//     resolves a baseline environment unconditionally (not only when
//     baseline.source == file), which would be pointless if a puppetdb
//     source never consulted it.
//
// LoadBaseline therefore always compares the returned catalog's
// environment against resolve.Target.Baseline.Environment and returns an
// operational diagnostic (model.OperationLoadBaseline) when they differ,
// before the catalog is usable for diffing.
package puppetdb
