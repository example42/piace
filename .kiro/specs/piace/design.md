# PIACE Design

## Overview

PIACE is a dependency-free, CGO-free Go CLI that compares a target's selected
**baseline catalog** against a **candidate catalog** requested from an existing
Puppet Server or OpenVox **compiler**. CI deploys the candidate environment
before PIACE runs. PIACE never embeds a Puppet runtime, runs agents, or issues
a write to PuppetDB. With `catalog_api: v4` the candidate compilation writes
nothing to PuppetDB either, because every v4 request disables fact and catalog
persistence; with `catalog_api: v3` the compiler stores the candidate facts and
catalog regardless, which is the constraint that shapes the v3 rules below.

This design fixes the contracts deliberately left open in the requirements:
configuration merge rules, snapshot integrity, compiler compatibility,
normalization, safe rendering, bounded impact estimates, and outcome
precedence. The implementation must use the terminology in `CONTEXT.md`.

### 1.1 Design goals

- Deterministic per-target node diffs, aggregate diffs, and text/JSON/HTML
  reports from the same internal result model.
- Strict source and target validation before a catalog is diffed.
- Independent least-privilege mTLS configuration for the compiler and
  PuppetDB; no secret material in output.
- An explicit degraded-compatibility path for compiler catalog API v3, bounded
  by what v3 cannot do: suppress persistence.
- Reusable, verifiable snapshots for environment-stable baselines.

### 1.2 Explicit non-goals

PIACE does not locally compile Puppet code; infer catalog changes for uncompiled
nodes; use PQL to expand target selection; retain history in PuppetDB; download
runtime dependencies; or render managed file content bytes.

## 2. Commands and runtime configuration

### 2.1 CLI surface

```text
piace compare --targets TARGETS.yaml --services SERVICES.yaml \
  [--text-out PATH] [--json-out PATH] [--html-out PATH]
piace capture facts --targets TARGETS.yaml --services SERVICES.yaml
piace capture catalog --targets TARGETS.yaml --services SERVICES.yaml \
  --environment ENVIRONMENT
```

`compare` produces all requested reports from one result object. Omitting an
artifact option writes text to stdout and suppresses that optional artifact;
CI can request all three explicitly. The capture commands select targets from
the same target file and write the configured local snapshot paths. They never
write to PuppetDB.

### 2.2 Service configuration

A separate `--services` YAML file keeps endpoint and mTLS settings out of the
reviewable target selection file. It has `version: 1`, a `compiler` section,
and a `puppetdb` section. Each section requires an HTTPS endpoint, CA bundle,
client certificate, and private-key file. The two sections are independently
loaded, so paths may deliberately be identical. Private-key values are file
paths only; inline keys, bearer tokens, and insecure TLS are rejected.


Runtime configuration is read once, validated without emitting its contents,
and retained only as a redacted provenance projection. The process accepts only
`https` endpoints, requires a non-empty `ServerName` derived from the endpoint
host, disables credential forwarding across redirects, and applies timeouts and
body limits at the transport boundary.

## 3. Target configuration resolution

### 3.1 Resolved target model

The target file remains the versioned selection and policy contract illustrated
in the requirements. A target resolves to this complete model before network
I/O:

```text
certname
candidate: environment, catalog_api, allow_v3_fallback
facts: source, file?
baseline: source, environment, file?
exclude: []ExclusionRule
redact: []RedactionSelector
impact_estimate: enabled, timeout, result_limit
fail_on_diff
```

`catalog_api` is `v3` or `v4`; `allow_v3_fallback` defaults to `false` and is
valid only with `v4`. This explicit opt-in prevents a server capability error
from silently degrading both trusted-fact semantics and PuppetDB integrity. A
v4 request may fall back only for a documented unsupported-endpoint or
unsupported-version response. It must not fall back after authentication,
authorization, timeout, malformed response, or candidate identity/environment
mismatch. Any fallback is recorded as a warning and is subject to every v3 rule
below, including the persistence consequences — an operator who enables the
fallback is accepting that a compiler without v4 will have the target's stored
factset and catalog overwritten.

Any target that can compile over v3 requires `baseline.source: file`
(requirements.md 1.8) — `catalog_api: v3`, and equally `catalog_api: v4` with
`allow_v3_fallback: true`. A v3 compilation overwrites the target's stored
PuppetDB catalog and factset with the candidate's, so a `puppetdb` baseline is
not merely inaccurate here: the candidate request destroys the baseline the run
reads, and the failure surfaces on the *next* target or the *next* run as a
baseline-environment mismatch. Both combinations are configuration errors
rejected during resolution rather than runtime warnings, because a fallback
that has already fired cannot be un-fired. Pairing a file baseline with v3
still leaves the compilation itself mutating: see section 5.

### 3.2 Merge and validation rules

1. Resolve global defaults first, then replace each scalar or object field with
   a target override. A target must have a non-empty, unique certname.
2. Global exclusion and redaction lists are prepended to their per-target lists;
   they are never replaced. Duplicates are retained once in their first-seen
   order for provenance and evaluation.
3. `exclude.type` is an exact, case-sensitive Puppet resource type. Its `title`
   uses the Go `path.Match` glob dialect, case-sensitively. Invalid glob syntax
   is a validation error. `*` matches any title characters.
4. A redaction selector is `{type, parameter}`: both fields are exact,
   case-sensitive names. Redaction applies to every matching parameter,
   including values that did not change but appear in provenance or diagnostics.
5. A relative local file path is resolved against the target-file directory.
   `{certname}` may occur only as an entire path component. Certnames with `/`,
   `\\`, NUL, or `..` are invalid. Template expansion must remain beneath the
   target-file directory; an explicit absolute file path is allowed.
6. `facts.source: file` requires `facts.file`; `baseline.source: file` requires
   `baseline.file`. PuppetDB sources reject a file value. Every target needs a
   candidate environment, fact source, baseline source, baseline environment,
   API version, and `fail_on_diff` after resolution.
7. Impact limits must be positive; the effective network request deadline is
   the smaller of the service deadline and the target impact timeout.

Invalid configuration is one operational diagnostic and prevents every service
call. Resolved configuration provenance includes source choices, paths, API,
policy values, and matching rules, but never endpoint credentials or private
key paths.

## Architecture

```text
CLI/config -> resolver -> target work queue -------------------------------+
                               |                                           |
                 +-------------+-------------+                             |
                 v                           v                             v
        fact-source adapter          baseline-source adapter       compiler adapter
      (PuppetDB or envelope)       (PuppetDB or envelope)       (v3/v4 over mTLS)
                 \                           |                           /
                  +--------------------------+--------------------------+
                                             v
                              catalog normalizer / content verifier
                                             v
                         semantic differ -> exclusion evaluator
                                             v
                         aggregate builder -> impact estimator
                                             v
                   redaction boundary -> shared result -> text/JSON/HTML
```

Targets are independent and may run with bounded concurrency. The default is
one target at a time; a future explicit `--parallel` option may raise it, but
must preserve target-order result emission. A target error is captured in that
target's node result and processing continues for the other valid targets.
Global configuration failure is the sole fail-fast condition.

## Components and Interfaces

Interfaces separate remote wire formats from domain behavior:

- `FactSource.Load(target) -> Factset, Provenance`
- `CatalogSource.LoadBaseline(target) -> Catalog, Provenance`
- `Compiler.RequestCandidate(target, facts) -> Catalog, Provenance, Warnings`
- `ContentResolver.Digest(reference, context) -> DigestEvidence`
- `ImpactQuerier.Estimate(resourceIdentity, limits) -> ImpactEstimate`

Adapters preserve raw response bytes only transiently. They convert only
recognized, schema-validated responses into domain data. Unknown or malformed
catalog/fact data is an operational normalization failure, never an empty
catalog or factset.

## 5. Compiler request and compatibility policy

The compiler adapter owns protocol-specific paths, request encoding, response
shape validation, content retrieval authorization, and error classification.
Its contract requires that the returned catalog identify the requested certname
and candidate environment exactly. A non-2xx compiler response, semantic
request rejection, identity mismatch, or environment mismatch is a
**compilation failure**.

For API v4, PIACE uses the compiler's target trusted-fact mechanism. When the
selected factset exposes a valid trusted-fact structure, the adapter sends it
as `trusted_facts` with the selected target facts. When it does not, PIACE
uses the documented v4 omitted-field behavior only when the compiler is
configured to obtain target trusted facts from PuppetDB; the result records
`trusted_facts_source: compiler_lookup`. If neither source is available,
PIACE fails compilation rather than inventing trusted facts. The response
provenance records `provided` or `compiler_lookup` but never trusted-fact
values.

Every v4 request carries `persistence: {facts: false, catalog: false}`. This is
not configurable. It is what allows requirement 1.6 to hold and what keeps a
PuppetDB baseline meaningful: the compiler returns the candidate catalog and
writes nothing, so the target's stored factset and catalog remain those of its
last real agent run.

For API v3, and every permitted v4-to-v3 fallback, PIACE attaches a prominent,
non-suppressible warning to the target, covering both v3 consequences: the
catalog-reader certificate can make `$trusted` reflect the service identity
rather than the target, and the compilation writes the candidate facts and
candidate catalog into PuppetDB under the candidate environment. The same
warning appears in the shared result, text, JSON, and HTML.

The v3 endpoint offers no persistence control, so neither consequence is
avoidable from the client: the compiler saves the facts submitted with the
request, and stores the compiled catalog through its PuppetDB catalog cache
terminus. PIACE contains v3's blast radius through configuration
(`baseline.source: file` is mandatory, section 3.1) and reports it through the
warning; it cannot prevent it.

Puppet Server and OpenVox are treated identically. Both serve v3 and v4, both
authorize a catalog-reader certificate through `auth.conf`, and both honour the
v4 `persistence` field, so `catalog_api` alone determines PIACE's guarantees.
There is no implementation-specific branch anywhere in the adapter.

PIACE does not probe alternate API versions speculatively. Capture catalog uses
the exact same adapter and policy as comparison and records the requested API,
effective API, environment, factset identity, and trusted-fact source.

## Data Models

### Snapshot envelopes and capture

Snapshot files are UTF-8 JSON PIACE envelopes, not bare Puppet payloads:

```json
{
  "format_version": 1,
  "kind": "factset",
  "target": "web-01.example.test",
  "source": {"kind": "puppetdb", "producer": "..."},
  "captured_at": "2026-08-24T00:00:00Z",
  "requested_environment": "production",
  "compiler_api": "v4",
  "input_factset_identity": "sha256:...",
  "payload_checksum": "sha256:...",
  "payload": {}
}
```

`kind` is `factset` or `catalog`. Fields inapplicable to a factset are omitted;
`requested_environment`, `compiler_api`, and `input_factset_identity` are
mandatory for catalog snapshots. `source` records the adapter and producer
identity when supplied by the service. The payload retains the original
validated service document, not a lossy normalized form.

`payload_checksum` is SHA-256 over the compact canonical JSON encoding of only
`payload`: map keys sort lexicographically by UTF-8 bytes, arrays retain order,
strings use JSON escaping, and parsed numeric tokens normalize to their exact
base-10 numeric value. The same in-tree canonical encoder is used for writing
and validation. Envelope metadata is excluded from the checksum to allow a
future migration tool to add non-semantic metadata without rewriting payload
integrity; `format_version` gates such migrations.

Writes use a same-directory temporary file, mode `0600`, `fsync`, atomic rename,
and a directory sync where supported. Capture refuses to overwrite a snapshot
unless `--replace` is supplied. Reuse validates version, kind, target,
checksum, required metadata, and file decoding before it is accepted. A catalog
snapshot selected as a baseline must also match the resolved baseline
environment; a fact snapshot supplies its recorded identity to candidate
provenance. Any violation is an operational error for that target.


## 7. Semantic graph comparison

### 7.1 Normalized catalog model

A catalog normalizes into a resource map and an edge set. A resource key is the
exact Puppet identity `Type[title]`; type and title are strings with no case
folding. A graph edge key is the ordered pair `(source identity, target
identity)`; direction is significant. Resources and edges are sorted by those
keys before comparison and serialization.

Each parameter becomes a typed canonical value. Strings, booleans, null/undef,
and numbers retain semantic type; number comparisons use exact normalized
decimal values rather than machine floating point; arrays retain order; object
keys sort recursively. Catalog data outside this JSON-compatible value domain
is rejected as a normalization error unless the protocol adapter has a defined,
lossless translation. Tags, source file/line, and metadata unrelated to managed
content are discarded before comparison.

A node diff contains four distinct kinds: resource added, resource removed,
parameter changed, and edge added/removed. Parameter changed includes canonical
before/after values internally and a redaction-safe projection externally.
Equivalent aggregate keys include kind, identity, parameter name when relevant,
and the unredacted canonical comparison evidence. Raw values never enter logs,
PQL, serialized reports, templates, or persistent aggregate state.

### 7.2 File-content evidence

`File` is handled in addition to normal parameter comparison. PIACE determines
effective-content evidence in this priority order:

1. hash inline `content` and compare the digest;
2. compare an authoritative compiled content checksum when both catalogs expose
   a recognized compatible checksum;
3. retrieve the referenced content through the compiler adapter when a source
   or reference must be resolved, then compare cryptographic digests;
4. if retrieval cannot establish comparable bytes, report `reference_changed`
   or `content_indeterminate` rather than claiming a verified content change.

Source/reference changes are always reported without rendering their bytes. A
retrieval failure carries a target diagnostic and makes any unresolved content
comparison non-clean; it cannot silently collapse into an unchanged file. The
result exposes only checksum algorithm, digest, evidence source, and comparison
state. Digest values are not a substitute for parameter redaction: a redacted
content selector emits a stable `REDACTED` value while preserving the change
classification and no digest in reports.

### 7.3 Exclusions and redaction ordering

Diffing first establishes complete graph semantics. Exclusion evaluation then
matches resource identities and removes matching resource differences. It also
suppresses any edge difference attached to an excluded identity. The result
retains deterministic counts by rule and by suppressed kind, not hidden raw
parameter values. Policy evaluation and aggregate building consume only the
remaining differences.

Redaction applies after semantic equality and exclusions but before result
serialization, template data, diagnostic composition, and rendering. Puppet
`Sensitive` wrappers are detected recursively; their payload is never copied to
the serializable result. Configured selectors replace matched values with the
constant `"<redacted>"`. Errors quote identities and parameter names only, never
parameter value excerpts. This ordering preserves correct comparisons without
leaking values or merging distinct sensitive changes in aggregate groups.

## 8. Impact estimation

Impact estimation runs only for non-excluded resource additions, removals, and
parameter changes; it does not run for edge-only differences. For each unique
exact `Type[title]`, the PuppetDB adapter emits the PQL projection:

```text
resources[certname] { type = <quoted-type> and title = <quoted-title> }
```

The adapter uses one PQL string-literal encoder, sends `limit = result_limit +
1`, requests certname ordering when the PuppetDB API supports it, and sorts the
returned certnames locally in all cases. It preserves the exact generated PQL
and request options in the result. Receiving more than `result_limit` sets
`truncated: true` and retains the first `result_limit` sorted certnames as the
deterministic sample. A per-query deadline is the resolved impact timeout;
queries are bounded and may be sequential in v1 to limit PuppetDB load.

Every estimate is visibly labeled **potential impact estimate**. It says only
that the latest stored catalog contains the resource; it never states that a
node will change and never schedules more compiler calls. Timeout, transport,
PQL, or response errors become a separately reported failed estimate. Because
an enabled estimate is requested analysis, an estimate failure contributes an
operational outcome after all other targets finish; disabled estimates produce
no request and no failure.

## 9. Result schema and renderers

The shared document begins with `schema_version: 1` and includes:

- invocation metadata (tool version, UTC timestamp, resolved safe provenance);
- a deterministic, target-sorted node result for every selected target;
- baseline, facts, and candidate provenance, warnings, and diagnostics;
- complete non-excluded node diffs and exclusion summaries;
- aggregate groups sorted by kind and canonical identity;
- impact estimates, including exact PQL, limits, sample, truncation, timeout,
  and failure state; and
- final outcome, exit code, and ordered reason list.

JSON uses the in-tree canonical encoder. Text renders final outcome first, then
per-target status, node changes, warnings/errors, aggregate summary, and impact
summary. HTML embeds the redacted canonical result as escaped data and uses
inlined CSS/JavaScript only; it makes target failure, v3 warning, exclusions,
and final outcome visible without network access. HTML, text, and JSON derive
from the same redacted projection, preventing format drift or secret exposure.

### 9.1 Display policy

The three formats show the same document at three levels of detail, decided
through shared helpers so they cannot drift apart in what a value says. JSON
encodes the complete document and takes no options. HTML is complete too and
uses disclosure rather than omission: edge changes, aggregate edge groups, and
each estimate's PQL, request options and full certname list are on the page
inside closed `<details>`; a target's resource changes open by default. Only
the text report omits, because a CI log is a linear read with nothing to
expand — it drops edge changes and each estimate's query mechanics, and caps an
estimate's certname sample unless `--impact-nodes` is passed. Requirements 5.3,
6.5, 7.4, 8.2, and 9.4 are discharged by JSON, and visibly by HTML as well.

Two invariants keep this safe rather than lossy. Every section header counts
what it actually displays, not what the document holds. And a target whose only
differences are edges — `has_difference` true, no resource change — is never
rendered as unchanged: HTML shows the edges, and the text report prints an
explicit note, so no report reads as "nothing changed" on a run that exits
non-zero (requirements 10.2, 10.5).

Requirement 8.3's "no HTTP server, a CDN, network access, or sibling assets"
also rules out webfonts and image files, so the HTML report uses system font
stacks with declared fallbacks and draws its disclosure markers in CSS. It
commits to one light palette rather than following the reader's system theme:
a review artifact is shared, printed, and pasted into tickets, and a single
appearance is a single thing to verify.

## 10. Error taxonomy and outcomes

PIACE records all target-local problems with an operation, safe reason, and
source context. Classes are:

- **operational error**: configuration, TLS, baseline/fact retrieval, snapshot
  validation, response decoding/normalization, content verification, or enabled
  impact-estimate failure;
- **compilation failure**: a compiler request is rejected/fails, candidate
  identity or environment does not match, or v4 trusted-fact requirements are
  unmet;
- **policy-disallowed difference**: a target with `fail_on_diff: true` has a
  non-excluded semantic difference; and
- **success**: all requested work completed, with either no differences
  (`clean`) or differences allowed by every affected target policy
  (`differences_allowed`).

The reducer uses the following precedence across all targets:

```text
operational error (exit 30)
  > compilation failure (exit 20)
  > policy-disallowed difference (exit 10)
  > differences_allowed (exit 0)
  > clean (exit 0)
```

A target's diagnostic remains in every output regardless of global precedence.
No result with an unreported retrieval, compilation, or normalization failure
can be clean. A reported v3 compatibility warning alone does not change exit
status; it makes trust semantics explicitly reviewable.

## 11. Security and distribution

The release uses standard-library Go where practical, builds with CGO disabled,
and ships static supported-platform binaries with a SHA-256 checksum manifest
and detached signature from the release process. The supported OS/architecture
matrix and signature verification key are release metadata, not implicit
runtime downloads. No Ruby, Puppet agent, Facter, package manager, or package
resolution is permitted at execution time.

Endpoint allowlisting is configuration-derived: runtime requests go only to the
validated compiler and PuppetDB authorities and local snapshot paths. Logs use
structured safe fields and must be reviewed with the same redaction projection
as reports. TLS private keys and raw sensitive data have no `String`/marshal
paths. HTTP redirect following, arbitrary URL content retrieval, external HTML
assets, and user-controlled template execution are excluded.

## 12. Key decisions and traceability

| Decision | Rationale | Requirements |
| --- | --- | --- |
| Separate target and service files | Keeps reviewable scope/policy distinct from mTLS locations. | 3, 4 |
| Explicit v3 fallback opt-in | Prevents silent loss of v4 trusted-fact behavior and silent PuppetDB mutation. | 2, 7 |
| v4 persistence always disabled | The only client-side control that keeps a candidate compilation out of PuppetDB. | 1, 7 |
| v3 requires a file baseline | A v3 candidate compilation overwrites a PuppetDB baseline, including its own run's. | 1, 7 |
| SHA-256 canonical payload envelope | Detects snapshot corruption without a Puppet runtime. | 11 |
| Continue valid targets after local errors | Produces actionable CI evidence without hiding failures. | 8, 10 |
| Redact after equality, before results | Maintains correct diff semantics and prevents disclosure. | 3, 8 |
| Enabled impact failure is operational | A requested bounded analysis must not be silently omitted. | 9, 10 |

The external protocol details are isolated behind the compiler and PuppetDB
adapters. The v3/v4 request shapes, the v3 `Accept` requirement, the v4
response envelope, and the v3/v4 persistence behaviour were verified against a
deployed OpenVox compiler and PuppetDB on 2026-08-25 (requirements.md section
7). For any other compiler or PuppetDB version, fixture captures must verify
request fields, response shapes, checksum semantics, file-content endpoints,
and PQL options before that combination is declared supported; adapter support
is not enabled merely because another implementation accepts a similar
endpoint.


## Correctness Properties

### Property 1: Deterministic results

**Validates: Requirements 8.6**

For the same validated service/snapshot inputs and resolved configuration,
PIACE emits byte-identical canonical JSON and equivalently ordered text and
HTML data.

### Property 2: Verified snapshot acceptance

**Validates: Requirements 11.6**

Every accepted baseline and fact snapshot has a matching kind, target, format
version, and SHA-256 payload checksum; no invalid envelope reaches
normalization.

### Property 3: Candidate identity integrity

**Validates: Requirements 1.5**

Every candidate catalog compared belongs to its requested target and candidate
environment; a response mismatch is never diffed.

### Property 4: Complete exclusion suppression

**Validates: Requirements 6.3**

Excluded resources and every edge touching one are absent from policy and
aggregate inputs, while their safe suppression counts remain visible.

### Property 5: Redaction containment

**Validates: Requirements 8.7**

Sensitive and selector-redacted values never occur in rendered output,
persisted result data, aggregate keys, or diagnostic excerpts.

### Property 6: Clean-outcome completeness

**Validates: Requirements 10.5**

A final `clean` outcome implies every target was fully retrieved, compiled,
normalized, compared, and reported without unreported failure.

## Error Handling

Adapter errors preserve the operation (`load_facts`, `load_baseline`,
`request_candidate`, `verify_content`, or `estimate_impact`), target, source,
and safe status/context. They do not preserve raw body text by default, because
service errors can echo values. The outcome reducer in section 10 maps these
structured diagnostics after all valid targets have completed. Invalid global
configuration is reported once and prevents service traffic.

## Testing Strategy

Verification is fixture- and contract-driven. Unit-level coverage should lock
the canonical encoder, configuration resolution, envelope checks, graph
normalization, exclusions, redaction, aggregate equivalence, PQL quoting, and
outcome precedence. Adapter contract fixtures must represent every supported
PuppetDB, Puppet Server, and OpenVox response variant. Integration validation
uses an mTLS test service to prove authority isolation, no secret disclosure,
v3 warning behavior, v4 handling including the always-disabled persistence
fields, fallback limits, and deterministic self-contained report generation. Release validation proves the CGO-free
artifact and checksum/signature workflow.
