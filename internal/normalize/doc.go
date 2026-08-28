// Package normalize implements PIACE's catalog normalizer: task 7
// ("Normalize Puppet catalogs into a deterministic semantic graph"),
// design.md section 7.1 ("Normalized catalog model"), and requirements.md
// 5.1-5.4/5.9, 8.6-8.8, 10.5.
//
// # Scope
//
// Catalog converts a raw puppetdb.Catalog carrier (see
// internal/puppetdb/types.go) into a model.NormalizedCatalog: a resource
// map keyed by the exact `Type[title]` identity (no case folding), and a
// sorted edge set keyed by the ordered pair (source identity, target
// identity). It does not fetch, request, or cache a catalog — it is a
// pure function of the bytes already retrieved by the baseline (PuppetDB
// or file) or candidate (compiler) source, per design.md's Architecture
// diagram ("catalog normalizer / content verifier" sits after both
// source adapters and before the semantic differ).
//
// This package is reused, unmodified, for both a baseline catalog
// (PuppetDB or a local snapshot) and a candidate catalog (the compiler
// adapter), because task 7 depends on all three of tasks 4, 5, and 6's
// "normalized source contracts" (tasks.md's task dependency graph) — it
// must accept whichever raw shape any of those sources hands it, not just
// one.
//
// # Two documented wire shapes for the same carrier type
//
// puppetdb.Catalog.Resources and .Edges are json.RawMessage precisely
// because two different, both-legitimate call sites populate them with
// two different documented shapes (see internal/puppetdb/doc.go and
// internal/compiler/doc.go, and this package's own doc comment on
// detectShape):
//
//   - A PuppetDB query-API response (baseline catalogs, and any snapshot
//     captured from PuppetDB) uses the documented `{href, data}`
//     expansion: `resources: {"href": <url>, "data": [{"certname",
//     "resource", "type", "title", "exported", "tags", "file", "line",
//     "parameters"}, ...]}` and `edges: {"href": <url>, "data":
//     [{"relationship", "source_title", "source_type", "target_title",
//     "target_type"}, ...]}` (PuppetDB catalogs endpoint documentation,
//     https://puppet.com/docs/puppetdb/8/catalogs.html).
//
//   - The compiler's v3/v4 catalog response (candidate catalogs, and any
//     snapshot captured via `capture catalog`) uses the plain-array
//     catalog interchange format: `resources: [{"type", "title",
//     "aliases", "exported", "file", "line", "tags", "parameters"}, ...]`
//     and `edges: [{"source", "target", "relationship"}, ...]`.
//
//     An edge vertex takes either of two forms there, and both are
//     accepted (see resourceSpecWire in wire.go for the full rationale
//     and the primary sources):
//
//     A compiler's own response carries each vertex as a `Type[title]`
//     *reference string* — Puppet::Relationship#to_data_hash serializes
//     `source.to_s`/`target.to_s`, and Puppet::Resource#to_s is its ref.
//     PIACE splits it with a Go port of the PuppetDB terminus's own
//     resource_ref_to_hash regex, which is the same function that
//     produced the source_type/source_title of the PuppetDB baseline
//     being compared against — so the two sides line up by construction.
//
//     PuppetDB's documented catalog wire format v8 defines the vertex as
//     a `<resource-spec>` *object*, `{"type", "title"}`
//     (https://puppet.com/docs/puppetdb/8/catalog_format_v8.html); that
//     is what the terminus submits, and the terminus itself converts
//     reference strings into it (munge_edges). A plain-array catalog can
//     therefore legitimately carry either form.
//
// Catalog auto-detects which shape it was given (an object vs. an array
// at the top level of each field) rather than requiring the caller to say
// which source produced the bytes, since design.md's Components and
// Interfaces section states "Adapters preserve raw response bytes only
// transiently. They convert only recognized, schema-validated responses
// into domain data" — normalization is exactly that conversion step, and
// it must recognize either of the two contracts documented above. A raw
// value that is neither of those two shapes (or that decodes but is
// missing a required field) is a reported model.OperationNormalize
// diagnostic, never a silently empty or partially-populated
// NormalizedCatalog: design.md's Components and Interfaces section is
// explicit that "Unknown or malformed catalog/fact data is an operational
// normalization failure, never an empty catalog or factset."
//
// # What is dropped, and why that is safe
//
// Per requirements.md 5.9 and design.md section 7.1's closing sentence
// ("Tags, source file/line, and metadata unrelated to managed content are
// discarded before comparison"), this package never copies a resource's
// `tags`, `file`, `line`, `exported`, `aliases`/`certname`/`resource`
// fields (whichever the input shape happens to carry) into the returned
// model.Resource — only `type`, `title`, and `parameters` participate.
// This is a one-way, lossy conversion by design: the normalized model is
// the semantic graph the differ (task 9) and content verifier (task 8)
// operate on, not a lossless mirror of the wire response. A caller that
// still needs the discarded fields (e.g. a future capture inspection tool)
// must keep its own reference to the raw puppetdb.Catalog rather than
// recovering it from a NormalizedCatalog.
//
// One *parameter* is dropped for the same reason, and it is the only one:
// Puppet's `alias` metaparameter. The PuppetDB terminus injects it into a
// stored catalog's `parameters` object for every resource whose namevar
// differs from its title, recording the catalog-internal alias index as
// if it were a declared attribute; a compiler's own catalog response
// carries no such parameter. Measured against a deployed OpenVox
// installation on 2026-08-28 for one node: the PuppetDB-stored catalog
// carried `alias` on 9 of 53 resources (`Stage[main]`, `Class[main]`,
// `File[info scripts]`, ...), while the same node's freshly compiled
// catalog carried it on 0 of 40 through both the v3 and the v4 endpoint —
// and `alias` was the *only* parameter present on one side and absent on
// the other. Comparing a PuppetDB baseline against a compiled candidate
// therefore reported a spurious `alias: [...] -> null` parameter change
// for roughly a quarter of the shared resources: exactly the "generated
// noise" requirements.md 5.9 excludes ("catalog metadata unrelated to
// managed file content").
//
// Dropping it cannot hide a real difference. `alias` only registers
// additional keys in the compiler's own resource index so that
// `File['/etc/tp/run_info']` resolves to `File['info scripts']` during
// compilation and relationship resolution; it is never enforced on a
// node, and a change to it cannot alter anything an agent does to a
// system. The drop is symmetric — applied to whichever wire shape is
// being normalized, not conditionally to the PuppetDB one — because a
// file baseline captured from PuppetDB carries `alias` too, and a
// shape-conditional filter would let the same asymmetry back in through a
// snapshot. See value.go's generatedMetadataParameters, which is that
// list and is deliberately not generalized beyond the one parameter
// actually measured to cause this.
//
// # Canonical parameter values and Property 1
//
// Each parameter value is converted into the model.Value domain (nil,
// bool, string, model.Number, []model.Value, map[string]model.Value) by
// canonicalizeValue, decoding numeric JSON tokens with
// json.Decoder.UseNumber() and normalizing them with
// snapshot.CanonicalNumberString — the exact same exact-decimal algorithm
// internal/snapshot's canonical JSON encoder already uses for snapshot
// payload checksums (task 5). This package deliberately does not
// reimplement a second numeric-canonicalization algorithm: design.md's
// Property 1 (Deterministic results) and this task's brief both require
// exactly one canonicalization behavior across the codebase. Object keys
// are not pre-sorted into a separate representation — encoding/json's own
// map marshaling already sorts string keys alphabetically, so any
// serialization of the returned model.Value tree is deterministic without
// this package needing a second sorting step; design.md section 7.1's
// "object keys sort recursively" is satisfied at serialization time, not
// by this package's in-memory representation.
//
// A parameter value that is not JSON-decodable at all (malformed
// "parameters" JSON) is a reported normalization error. Every other JSON
// value IS necessarily one of the domain's cases (JSON has no value
// outside {null, bool, number, string, array, object}), so
// canonicalizeValue's "unsupported value type" branch is unreachable from
// any successfully-decoded JSON and exists only as defense in depth,
// matching design.md section 7.1's "Catalog data outside this
// JSON-compatible value domain is rejected as a normalization error."
//
// # Redaction-readiness (Property 5)
//
// NormalizedCatalog, model.Resource, and model.Edge are the same
// serializable types the shared result document, JSON/text/HTML
// renderers, and aggregate builder consume (see model/catalog.go and
// model/diff.go). This package holds no separate "raw" representation
// once Catalog returns: the only transient, non-serializable
// intermediate state (the decoded-but-not-yet-canonicalized `any` tree
// from json.Decoder.UseNumber()) exists only inside canonicalizeValue's
// call stack and is never retained, matching this task's brief ("Keep
// raw values only in short-lived comparison structures; make every
// serializable semantic representation redaction-ready"). Detecting and
// redacting a Puppet `Sensitive` wrapper or a configured redaction
// selector is deliberately NOT this package's job — design.md section
// 7.3 places that at the result boundary, after equality and exclusion
// evaluation (task 9), specifically so redaction cannot corrupt the
// comparison itself. This package's canonical output is a necessary
// input to that later step, not a redacted value in itself.
package normalize
