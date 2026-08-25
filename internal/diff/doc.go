// Package diff implements PIACE's node differ: task 9 ("Build node
// diffing, exclusions, and redaction boundaries"), design.md sections 7.1
// ("Normalized catalog model"), 7.2 ("File-content evidence"), and 7.3
// ("Exclusions and redaction ordering"), and requirements.md 5.1-5.9,
// 6.1-6.6, 8.7-8.8, 10.4.
//
// # Scope
//
// Diff is the single entry point: given a resolved resolve.Target and two
// already-normalized catalogs (internal/normalize, task 7) for that
// target's certname — before (baseline) and after (candidate) — it
// produces exactly one model.NodeDiff plus any diagnostics discovered
// while resolving File-content evidence (internal/filecontent, task 8).
// It does not fetch, normalize, or aggregate anything itself: those are
// tasks 4-8 (source retrieval/normalization) and task 10 (cross-target
// aggregation), which is deliberately not implemented here.
//
// # Ordering (design.md section 7.3)
//
// Diff performs exactly three sequential passes, matching design.md
// section 7.3's fixed order:
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
//     this step, from the remaining (non-excluded) differences only —
//     an excluded difference never counts as "a difference" for
//     HasDifference, policy evaluation, or aggregation. Exclusion
//     suppresses differences only, never diagnostics: File-content
//     evidence is resolved in pass 1, before pass 2 knows an identity
//     is excluded, so a verify_content failure on an excluded File is
//     still returned. That is deliberate — design.md section 10 forbids
//     any result with an unreported content-verification failure from
//     being clean, and an exclusion rule states which differences are
//     interesting, not that a failure to look may go unreported.
//  3. Redaction: applied last, strictly after HasDifference is already
//     fixed, so a redacted value can never remove a change from being
//     counted as a difference — it only masks the value in place.
//     Redaction has two independent sources, both described below.
//
// # Redaction source 1: Puppet `Sensitive` wrapper detection
//
// design.md section 7.3 states "Puppet `Sensitive` wrappers are detected
// recursively; their payload is never copied to the serializable
// result," without specifying the wire shape a Sensitive value takes
// inside a normalized catalog's canonical model.Value tree. This package
// resolves that as follows (moderate confidence: derived from Puppet's
// own Ruby serialization source, not yet verified against a live
// rich-data-enabled compiler response — the same category of documented-
// but-unverified assumption internal/filecontent/doc.go already carries
// for its own wire-shape assumptions):
//
// Puppet's Pcore "generic data" representation (the format used when a
// catalog is compiled with rich data enabled — see
// https://github.com/puppetlabs/puppet-specifications/blob/master/language/data-types/pcore-data-representation.md
// and pcore-generic-data.md) represents any value outside the plain
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
// isSensitiveWrapper (redact.go) recognizes this shape — a
// map[string]model.Value whose "__ptype" entry is the exact string
// "Sensitive" — at any depth within a parameter's canonical value tree
// (redactSensitiveValue walks maps and arrays recursively, matching
// "detected recursively"). Wherever the shape is found, the entire
// matched subtree — never just some substring of it — is replaced with
// model.RedactedValue in the *returned* ResourceChange's Before/After
// projection. The full wrapped-but-unredacted canonical value is still
// used for equality comparison during pass 1 (a Sensitive value that
// changed is still reported as a difference; only its displayed value is
// masked), matching design.md's "their payload is never copied to the
// serializable result" — the payload is compared structurally via
// reflect.DeepEqual on the still-wrapped map, never unwrapped or
// interpreted.
//
// A catalog compiled without rich data enabled never produces this
// shape at all (Sensitive values either fail to serialize or are
// converted to a plain "Sensitive [value redacted]" string by Puppet
// itself before the wire response is built); this package's Sensitive
// detection is therefore a defense-in-depth complement to, not a
// replacement for, whatever the compiler itself already does — it costs
// nothing when the shape never appears.
//
// # Redaction source 2: configured selectors
//
// resolve.Target.Redact ({Type, Parameter} exact match) redacts a named
// parameter's value on a parameter-changed entry, exactly as design.md
// section 3.2 rule 4 and requirements.md 8.8 describe.
//
// A resource-added/removed entry carries no value projection at all to
// redact: diffResources emits Kind and Identity only. That is
// deliberate, and it is what requirements.md 5.1 asks for ("identify
// added and removed resources by Puppet resource identity" — identity,
// not parameters); design.md section 7.1 likewise attaches canonical
// before/after values to the parameter-changed kind alone. The security
// argument settles it independently: an added `File` resource's
// parameter map would carry its literal `content` bytes, which is
// precisely the managed-content disclosure internal/filecontent exists
// to prevent, and it would carry them on a code path with no
// File-content collapsing and no evidence-only projection to route them
// through.
//
// For a File resource's synthesized content-bearing change (see
// "File-content-bearing parameter handling" below),
// this package follows the existing test-fixture convention already
// established in internal/config/target_test.go and
// internal/config/resolve/target_test.go — a File-content redaction
// selector is always written as {Type: "File", Parameter: "content"}
// regardless of which of the four raw content-bearing parameters
// (content/source/checksum/checksum_value) actually produced the
// difference. redactChange (redact.go) is triggered by exactly that
// selector shape and clears the evidence's Algorithm field, replacing
// both digests with model.RedactedValue and setting Redacted: true —
// preserving State (the change classification) exactly as
// internal/filecontent computed it,
// per design.md section 7.2's closing sentence ("a redacted content
// selector emits a stable REDACTED value while preserving the change
// classification and no digest in reports").
//
// # File-content-bearing parameter handling
//
// For a File resource present (unchanged identity) in both catalogs,
// diffParameters (resources.go) never reports content, source,
// checksum, or checksum_value as ordinary independent parameter-changed
// entries. If any of those four raw parameter values differ between the
// two catalogs, it calls filecontent.ResolveFileContentEvidence exactly
// once and, only if the resulting State is not
// model.FileContentUnchanged (an evidence-verified non-difference is not
// reported at all, matching requirements.md 5's "show changes, not
// noise" framing), emits a single synthesized ResourceChange with
// Kind: model.ChangeParameterChanged, Parameter: "content" (the stable
// label these four raw parameters collapse into), and FileContent set —
// Before/After are deliberately left unset on this entry: every piece of
// safe evidence already lives in FileContent, and leaving Before/After
// empty means the literal `content` parameter's managed bytes (which,
// unlike a `source` reference string, are exactly the disclosure this
// whole subsystem exists to prevent) can never be copied into the
// result through this path.
//
// # Aggregate grouping across the redaction boundary
//
// Redaction inside Diff's return value creates an apparent conflict
// with the aggregate builder (task 10). design.md section 7.1 states
// that "equivalent aggregate keys include kind, identity, parameter
// name when relevant, and the unredacted canonical comparison
// evidence," which reads as though task 10 needs each node diff's
// unredacted Before/After. But design.md section 7.3 lists exactly what
// redaction must precede — "result serialization, template data,
// diagnostic composition, and rendering" — and requires that the
// ordering avoid "merging distinct sensitive changes in aggregate
// groups," while task 9's own brief requires "retaining no secret
// material in logs or aggregate keys." Handing task 10 the unredacted
// values would satisfy the first constraint by violating the third; a
// naive redact-then-aggregate would satisfy the third by violating the
// second, since every distinct sensitive value collapses to the same
// model.RedactedValue and merges into one group.
//
// All three constraints hold at once because task 10 needs to decide
// *equality* of the unredacted evidence, not to read it. Pass 1
// therefore computes model.ResourceChange.Fingerprint (fingerprint.go):
// a SHA-256 over the canonical JSON of the change's kind, identity,
// parameter name, and unredacted before/after evidence — or, for a
// File-content entry, its unredacted FileContentEvidence — using
// snapshot.CanonicalJSON, the same single canonicalization algorithm
// internal/normalize and internal/snapshot already share (design.md's
// Property 1). Two changes with identical unredacted evidence share a
// Fingerprint; two distinct sensitive values do not, so they cannot
// merge. The field is `json:"-"` and never reaches a report, template,
// log line, or persistent aggregate state, satisfying section 7.1's
// "raw values never enter logs, PQL, serialized reports, templates, or
// persistent aggregate state."
//
// Diff therefore returns an already-redacted model.NodeDiff — matching
// design.md section 9's "HTML, text, and JSON derive from the same
// redacted projection" — carrying an opaque grouping token that task 10
// groups on alongside kind/identity/parameter.
package diff
