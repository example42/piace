// Package diff implements PIACE's node differ: semantic resource and
// edge comparison, exclusion evaluation, and the redaction boundary, in
// that fixed order.
//
// # Scope
//
// Diff is the single entry point: given a resolved resolve.Target and
// two already-normalized catalogs (internal/normalize) for that target's
// certname, before being the baseline and after the candidate, it
// produces exactly one model.NodeDiff plus any diagnostics discovered
// while resolving File-content evidence (internal/filecontent). It does
// not fetch, normalize, or aggregate anything itself: retrieval and
// normalization happen upstream, and cross-target aggregation in
// internal/aggregate.
//
// # Ordering
//
// Diff performs exactly three sequential passes:
//
//  1. Full graph diff: resource added/removed, parameter changed (with
//     File-content evidence attached where relevant), and edge
//     added/removed, computed with no knowledge of exclusion or
//     redaction configuration at all. This establishes "complete graph
//     semantics" before either later step can influence it.
//  2. Exclusion evaluation: resolve.Target.Exclude rules are matched
//     against every resource identity appearing in either catalog (not
//     only those with a change), and every resource/parameter/edge
//     difference touching an excluded identity is removed from the
//     result. model.NodeDiff.HasDifference is computed immediately after
//     this step, from the remaining (non-excluded) differences only:
//     an excluded difference never counts as "a difference" for
//     HasDifference, policy evaluation, or aggregation. Exclusion
//     suppresses differences only, never diagnostics: File-content
//     evidence is resolved in pass 1, before pass 2 knows an identity
//     is excluded, so a verify_content failure on an excluded File is
//     still returned. That is deliberate: no result with an unreported
//     content-verification failure may be clean, and an exclusion rule
//     states which differences are
//     interesting, not that a failure to look may go unreported.
//  3. Redaction: applied last, strictly after HasDifference is already
//     fixed, so a redacted value can never remove a change from being
//     counted as a difference: it only masks the value in place.
//     Redaction has two independent sources, both described below.
//
// # Redaction source 1: Puppet `Sensitive` wrapper detection
//
// Puppet `Sensitive` wrappers are detected recursively and their payload
// is never copied to the serializable result. What that leaves open is
// the wire shape a Sensitive value takes inside a normalized catalog's
// canonical model.Value tree. This package resolves it as follows, with
// moderate confidence: derived from Puppet's own Ruby serialization
// source, not yet verified against a live rich-data-enabled compiler
// response, the same category of documented-but-unverified assumption
// internal/filecontent/doc.go already carries for its own wire shapes.
//
// Puppet's Pcore "generic data" representation, the format used when a
// catalog is compiled with rich data enabled (see
// https://github.com/puppetlabs/puppet-specifications/blob/master/language/data-types/pcore-data-representation.md
// and pcore-generic-data.md), represents any value outside the plain
// JSON-compatible subset as a JSON object carrying a reserved `__ptype`
// key naming the Pcore type, with the wrapped payload usually under a
// `__pvalue` key. Puppet's Ruby serializer
// (lib/puppet/pops/serialization.rb defines PCORE_TYPE_KEY = '__ptype',
// PCORE_VALUE_KEY = '__pvalue', PCORE_TYPE_SENSITIVE = 'Sensitive'; see
// lib/puppet/pops/serialization/to_data_converter.rb's
// `Types::PSensitiveType::Sensitive` branch) encodes a Sensitive-wrapped
// value as exactly:
//
//	{"__ptype": "Sensitive", "__pvalue": <converted unwrapped value>}
//
// isSensitiveWrapper (redact.go) recognizes this shape, a
// map[string]model.Value whose "__ptype" entry is the exact string
// "Sensitive", at any depth within a parameter's canonical value tree,
// since redactSensitiveValue walks maps and arrays recursively. Wherever
// the shape is found, the entire matched subtree, never just some
// substring of it, is replaced with model.RedactedValue in the
// *returned* ResourceChange's Before/After projection. The full
// wrapped-but-unredacted canonical value is still used for equality
// comparison during pass 1: a Sensitive value that changed is still
// reported as a difference, and only its displayed value is masked. The
// payload is compared structurally via reflect.DeepEqual on the
// still-wrapped map, never unwrapped or interpreted, so it is never
// copied into the serializable result.
//
// A catalog compiled without rich data enabled never produces this shape
// at all: Sensitive values either fail to serialize or are converted to
// a plain "Sensitive [value redacted]" string by Puppet itself before
// the wire response is built. This package's Sensitive detection is
// therefore a defense-in-depth complement to whatever the compiler
// already does rather than a replacement for it, and it costs nothing
// when the shape never appears.
//
// # Redaction source 2: configured selectors
//
// resolve.Target.Redact ({Type, Parameter} exact match) redacts a named
// parameter's value on a parameter-changed entry.
//
// A resource-added or resource-removed entry carries no value projection
// at all to redact: diffResources emits Kind and Identity only. That is
// deliberate, and it is what identifying added and removed resources by
// Puppet resource identity asks for: identity, not parameters. The
// normalized catalog model likewise attaches canonical before and after
// values to the parameter-changed kind alone. The security argument
// settles it independently: an added `File` resource's parameter map
// would carry its literal `content` bytes, which is precisely the
// managed-content disclosure internal/filecontent exists to prevent, and
// it would carry them on a code path with no File-content collapsing and
// no evidence-only projection to route them through.
//
// For a File resource's synthesized content-bearing change (see
// "File-content-bearing parameter handling" below), a File-content
// redaction selector is always written as {Type: "File", Parameter:
// "content"} regardless of which of the four raw content-bearing
// parameters (content, source, checksum, checksum_value) actually
// produced the difference. redactChange (redact.go) is triggered by
// exactly that selector shape and clears the evidence's Algorithm field,
// replacing both digests with model.RedactedValue and setting Redacted:
// true, preserving State exactly as internal/filecontent computed it. A
// redacted content selector emits a stable REDACTED value while
// preserving the change classification, and no digest reaches a report.
//
// # File-content-bearing parameter handling
//
// For a File resource present, with unchanged identity, in both
// catalogs, diffParameters (resources.go) never reports content, source,
// checksum, or checksum_value as ordinary independent parameter-changed
// entries. If any of those four raw parameter values differ between the
// two catalogs, it calls filecontent.ResolveFileContentEvidence exactly
// once and, only if the resulting State is not
// model.FileContentUnchanged, emits a single synthesized ResourceChange
// with Kind: model.ChangeParameterChanged, Parameter: "content" (the
// stable label those four raw parameters collapse into), and FileContent
// set. An evidence-verified non-difference is not reported at all.
// Before/After are deliberately left unset on this entry: every piece of
// safe evidence already lives in FileContent, and leaving Before/After
// empty means the literal `content` parameter's managed bytes, which
// unlike a `source` reference string are exactly the disclosure this
// whole subsystem exists to prevent, can never be copied into the result
// through this path.
//
// # Aggregate grouping across the redaction boundary
//
// Redaction inside Diff's return value creates an apparent conflict with
// the aggregate builder. Equivalent aggregate keys include kind,
// identity, parameter name when relevant, and the unredacted canonical
// comparison evidence, which reads as though the builder needs each node
// diff's unredacted Before/After. But redaction must precede result
// serialization, template data, diagnostic composition, and rendering,
// the ordering must avoid merging distinct sensitive changes in
// aggregate groups, and no secret material may be retained in logs or
// aggregate keys. Handing the builder the unredacted values would
// satisfy the first constraint by violating the third; a naive
// redact-then-aggregate would satisfy the third by violating the second,
// since every distinct sensitive value collapses to the same
// model.RedactedValue and merges into one group.
//
// All three constraints hold at once because the aggregate builder needs
// to decide *equality* of the unredacted evidence, not to read it. Pass
// 1 therefore computes model.ResourceChange.Fingerprint
// (fingerprint.go): a SHA-256 over the canonical JSON of the change's
// kind, identity, parameter name, and unredacted before and after
// evidence, or for a File-content entry its unredacted
// FileContentEvidence, using snapshot.CanonicalJSON, the same single
// canonicalization algorithm internal/normalize and internal/snapshot
// already share. Two changes with identical unredacted evidence share a
// Fingerprint; two distinct sensitive values do not, so they cannot
// merge. The field is `json:"-"` and never reaches a report, template,
// log line, or persistent aggregate state, so raw values never enter
// logs, PQL, serialized reports, templates, or persistent aggregate
// state.
//
// Diff therefore returns an already-redacted model.NodeDiff, so that
// HTML, text, and JSON all derive from the same redacted projection,
// carrying an opaque grouping token the aggregate builder groups on
// alongside kind, identity and parameter.
package diff
