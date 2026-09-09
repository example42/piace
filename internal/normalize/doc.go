// Package normalize implements PIACE's catalog normalizer: it turns a
// raw Puppet catalog into a deterministic semantic graph.
//
// # Scope
//
// Catalog converts a raw puppetdb.Catalog carrier (see
// internal/puppetdb/types.go) into a model.NormalizedCatalog: a resource
// map keyed by the exact `Type[title]` identity with no case folding,
// and a sorted edge set keyed by the ordered pair (source identity,
// target identity). It does not fetch, request, or cache a catalog: it
// is a pure function of the bytes already retrieved by the baseline
// source (PuppetDB or file) or the candidate source (the compiler), and
// it sits after both source adapters and before the semantic differ.
//
// This package is reused, unmodified, for both a baseline catalog
// (PuppetDB or a local snapshot) and a candidate catalog (the compiler
// adapter). It has to accept whichever raw shape any of those sources
// hands it, not just one.
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
//     *reference string*. Puppet::Relationship#to_data_hash serializes
//     `source.to_s`/`target.to_s`, and Puppet::Resource#to_s is its ref.
//     PIACE splits it with a Go port of the PuppetDB terminus's own
//     resource_ref_to_hash regex, which is the same function that
//     produced the source_type/source_title of the PuppetDB baseline
//     being compared against, so the two sides line up by construction.
//
//     PuppetDB's documented catalog wire format v8 defines the vertex as
//     a `<resource-spec>` *object*, `{"type", "title"}`
//     (https://puppet.com/docs/puppetdb/8/catalog_format_v8.html); that
//     is what the terminus submits, and the terminus itself converts
//     reference strings into it (munge_edges). A plain-array catalog can
//     therefore legitimately carry either form.
//
// Catalog auto-detects which shape it was given, an object or an array
// at the top level of each field, rather than requiring the caller to
// say which source produced the bytes. Adapters preserve raw response
// bytes only transiently and convert only recognized, schema-validated
// responses into domain data; normalization is exactly that conversion
// step, and it has to recognize either of the two contracts documented
// above. A raw value that is neither of those two shapes, or that
// decodes but is missing a required field, is a reported
// model.OperationNormalize diagnostic, never a silently empty or
// partially populated NormalizedCatalog: unknown or malformed catalog
// and fact data is an operational normalization failure, never an empty
// catalog or factset.
//
// # What is dropped, and why that is safe
//
// Tags, source file and line, and metadata unrelated to managed content
// are discarded before comparison, so this package never copies a
// resource's `tags`, `file`, `line`, `exported`, or
// `aliases`/`certname`/`resource` fields, whichever the input shape
// happens to carry, into the returned model.Resource: only `type`,
// `title`, and `parameters` participate in comparison. Resource-level
// `sensitive_parameters` is retained for disclosure policy. This is a one-way, lossy
// conversion by design. The normalized model is the semantic graph the
// differ and the content verifier operate on, not a lossless mirror of
// the wire response, and a caller that still needs the discarded fields
// has to keep its own reference to the raw puppetdb.Catalog rather than
// recovering it from a NormalizedCatalog.
//
// One *parameter* is dropped for the same reason, and it is the only
// one: Puppet's `alias` metaparameter. The PuppetDB terminus injects it
// into a stored catalog's `parameters` object for every resource whose
// namevar differs from its title, recording the catalog-internal alias
// index as if it were a declared attribute; a compiler's own catalog
// response carries no such parameter. Measured against a deployed
// OpenVox installation on 2026-08-28 for one node: the PuppetDB-stored
// catalog carried `alias` on 9 of 53 resources (`Stage[main]`,
// `Class[main]`, `File[info scripts]`, and so on), while the same node's
// freshly compiled catalog carried it on 0 of 40 through both the v3 and
// the v4 endpoint, and `alias` was the *only* parameter present on one
// side and absent on the other. Comparing a PuppetDB baseline against a
// compiled candidate therefore reported a spurious `alias: [...] ->
// null` parameter change for roughly a quarter of the shared resources:
// exactly the generated noise that dropping catalog metadata unrelated
// to managed file content exists to avoid.
//
// Dropping it cannot hide a real difference. `alias` only registers
// additional keys in the compiler's own resource index so that
// `File['/etc/tp/run_info']` resolves to `File['info scripts']` during
// compilation and relationship resolution; it is never enforced on a
// node, and a change to it cannot alter anything an agent does to a
// system. The drop is symmetric, applied to whichever wire shape is
// being normalized rather than conditionally to the PuppetDB one,
// because a file baseline captured from PuppetDB carries `alias` too and
// a shape-conditional filter would let the same asymmetry back in
// through a snapshot. See value.go's generatedMetadataParameters, which
// is that list and is deliberately not generalized beyond the one
// parameter actually measured to cause this.
//
// # Relationship order, and why it is not a difference
//
// Seven parameters have their array values sorted on both sides, by this
// package's own total order: `audit`, `before`, `check`, `notify`,
// `require`, `subscribe` and `tag`. The list is Puppet's, not an
// invention here. The PuppetDB terminus declares them as
// UnorderedMetaparams, "metaparams that may contain arrays, but whose
// semantics are fundamentally unordered", and sorts each one before
// storing a catalog. A compiler's catalog response is not sorted.
//
// Measured against a deployed OpenVox 8.15.2 installation on 2026-09-09,
// comparing one node's production environment with itself: the stored
// baseline held Service[pabawi]'s `require` as
// ["Concat[pabawi_env_file]", "Exec[docker_pull_pabawi]",
// "Exec[systemd_reload_pabawi]", "File[/etc/systemd/system/pabawi.service]"]
// and the freshly compiled candidate held the same four entries in
// declaration order. Two of the twelve remaining differences in that
// comparison were this and nothing else. Compiling the same catalog
// twice returned byte-identical parameters, so the reordering is the
// terminus's, not compilation nondeterminism.
//
// `alias` is on Puppet's list too; it is dropped entirely above, so it
// never reaches the sort.
//
// The order applied is not Ruby's `sort_by {|x| x.to_s}`. It does not
// need to be: the same order is imposed on both sides, and any
// permutation of one multiset sorts to the same sequence. Nothing
// outside this codebase has to agree with PIACE about the sequence.
//
// This is deliberately not generalized to every array-valued parameter.
// A File `source` array is ordered, and its order decides which source
// is retrieved first; sorting it would silently undo phase 2's source
// selection.
//
// # Relationship edges, and why only containment is compared
//
// An edge whose relationship is present and is not "contains" is dropped,
// in both wire shapes. Puppet's PuppetDB terminus synthesizes one such
// edge per `require`, `before`, `notify` or `subscribe` metaparameter on
// its way to storage: `synthesize_edges`, whose relationship names are its
// Relationships table, "before", "required-by", "notifies" and
// "subscription-of". A compiler's catalog response carries containment
// edges only, because relationship edges are resolved by the agent at
// apply time.
//
// Measured against a deployed OpenVox 8.15.2 installation on 2026-09-09,
// comparing one node's production environment with itself: 296 stored
// edges against 239 compiled ones, the 239 containment edges an identical
// multiset, and every one of the 57 extras synthesized. Each was reported
// as an edge removal. The text report hides edge groups by design, so they
// were invisible there while reaching the JSON document, the aggregate and
// the inference request.
//
// Dropping them loses nothing a reader had. Each is derived from a
// metaparameter that is compared as a parameter in its own right, so a
// real relationship change is still reported, and reported once rather
// than twice. An edge with no relationship field is a compiler-shaped
// edge, which is containment.
//
// # Canonical parameter values and Property 1
//
// Each parameter value is converted into the model.Value domain (nil,
// bool, string, model.Number, []model.Value, map[string]model.Value) by
// canonicalizeValue, decoding numeric JSON tokens with
// json.Decoder.UseNumber() and normalizing them with
// snapshot.CanonicalNumberString, the exact same exact-decimal algorithm
// internal/snapshot's canonical JSON encoder already uses for snapshot
// payload checksums. This package deliberately does not reimplement a
// second numeric-canonicalization algorithm: determinism requires
// exactly one canonicalization behavior across the codebase. Object keys
// are not pre-sorted into a separate representation, because
// encoding/json's own map marshaling already sorts string keys
// alphabetically, so any serialization of the returned model.Value tree
// is deterministic without a second sorting step here. Object keys sort
// recursively at serialization time, not in this package's in-memory
// representation.
//
// A parameter value that is not JSON-decodable at all (malformed
// "parameters" JSON) is a reported normalization error. Every other JSON
// value is necessarily one of the domain's cases, since JSON has no
// value outside {null, bool, number, string, array, object}, so
// canonicalizeValue's "unsupported value type" branch is unreachable
// from any successfully decoded JSON and exists only as defense in
// depth. Catalog data outside this JSON-compatible value domain is
// rejected as a normalization error.
//
// # Sensitivity and publication
//
// NormalizedCatalog carries raw canonical values, never a report projection.
// Resource sensitivity metadata is retained from either supported container
// shape. If present, sensitive_parameters must be an array of unique nonempty
// strings; null, malformed types and duplicate names are validation failures.
// Recursive Pcore Sensitive wrappers require __pvalue, which may be null.
// Parameter validation errors omit value trees and nested keys.
//
// internal/diff owns the explicit transition to model.ResourceChange after
// comparison and exclusions. Only that publishable projection reaches reports
// and inference. Sensitivity declared by either side protects both values.
// Static catalog metadata is retained per File title for content evidence;
// recursive metadata marks an unsupported tree comparison. Captured digests
// survive normalization separately from parameters and sensitivity metadata.
package normalize
