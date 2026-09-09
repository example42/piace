# PIACE 0.5.0 implementation plan

Status: planned. Based on the codebase review of commit `e2b5090` on 2026-09-08.

PIACE is published but has no deployments. Treat 0.5.0 as the first deployment
target: choose the correct interfaces, configuration, and artifact formats
directly. Update producers, consumers, fixtures, and examples together.

The objective is a comparison whose identity, content evidence, disclosure,
provenance, and outcome can be trusted. Preserve the separate advisory
assessment command and the single comparison outcome reducer.

## Execution order

Complete the phases in this order. Within each phase, follow the numbered
steps. Each step includes the findings it closes, implementation scope, and
the evidence required for completion.

1. Fix sensitivity handling, URL credentials, and input identity validation.
2. Redesign file-content evidence and block hazardous v3 configurations.
3. Correct aggregation disclosure, evidence preservation, and capture provenance.
4. Repair command configuration, artifact publication, and resource limits.
5. Validate real wire formats, align documentation and CI, and prepare 0.5.0.

Before changing an existing module, read its implementation, callers, tests,
and package documentation. Add a regression assertion for each reproduced
failure at the narrowest interface that exercises the behavior. Update the
corresponding implementation documentation in the same step; phase 5 checks
the entire published contract for agreement.

Mark a step complete only when its acceptance criteria pass. Record relevant
commands and fixture provenance beside its checkbox. Live integration evidence
must be distinguished from synthetic test results.

## Phase 1: confidentiality and identity

### 1.1 Preserve sensitivity through comparison and publication

- [ ] Implement and verify.

**Finding: high severity, reproduced.** `internal/normalize/wire.go` drops
resource-level `sensitive_parameters`. `internal/diff/redact.go` recognizes
Pcore wrappers only. A `User` resource with `sensitive_parameters: [password]`
and a plaintext parameter value exposed the changed password in the JSON
report and the inference request.

**Changes**

1. Represent resource-level sensitivity in the normalized model and decode it
   from supported compiler and PuppetDB representations.
2. Preserve recursive Pcore wrapper detection. Define malformed sensitivity
   metadata as an explicit validation failure rather than silently discarding it.
3. Treat a parameter as sensitive on both sides when either side marks it
   sensitive. Apply the same rule to additions and removals when phase 3 adds
   their evidence.
4. Separate raw comparison evidence from the publishable projection. Make the
   transition explicit so serialization, renderers, and inference cannot receive
   the raw representation accidentally.
5. Keep grouping fingerprints internal. Redaction must preserve real differences
   without publishing secret-derived fingerprints.

**Acceptance**

- Plaintext values accompanied by `sensitive_parameters` remain absent from
  text, JSON, HTML, diagnostics, normal debug output, and inference requests.
- Sensitivity introduced or removed on one side still protects both values.
- Nested wrappers, configured selectors, and sensitive File content are covered.
- Different sensitive changes remain distinct when grouped.

### 1.2 Reject URL credentials and constrain observable metadata

- [ ] Implement and verify.

**Finding: high severity, reproduced.** HTTPS endpoint validation accepts
userinfo. Deleting `Authorization` before sending does not prevent Go from
constructing Basic authentication from `URL.User`. A local TLS probe received
that header and observed a plaintext password in the debug event URL.

**Scope:** `internal/config/resolve/validate.go`, `internal/transport/`,
`internal/inference/`, `cmd/piace/debug.go`.

**Changes**

1. Reject endpoint userinfo during configuration validation and client
   construction. Errors must identify the invalid field without echoing credentials.
2. Enforce the compiler/PuppetDB client's configured HTTPS authority on every
   request, including initial requests and redirects.
3. Define a safe URL projection for debug events and errors. Retain explicitly
   supported nonsecret query metadata, such as an inference API version, without
   assuming arbitrary query values are safe to print.
4. Treat response member names and content-type strings as untrusted metadata:
   bound their length and escape control characters before displaying them.
5. Preserve the dedicated inference bearer-token client and its redirect policy.

**Acceptance**

- Userinfo cannot produce an Authorization header or disclose a credential in
  configuration errors, transport errors, or debug output.
- Cross-authority requests and redirects are rejected before credentials leave.
- Legitimate configured inference requests retain their required authentication.
- Malicious metadata cannot inject extra log lines or unbounded debug output.

### 1.3 Validate identities and required input structure consistently

- [ ] Implement and verify.

**Finding: high severity.** PuppetDB factsets and baselines are not checked
against the requested certname. Snapshot envelopes are checked, but payload
certnames are not. Trusted facts require a nonempty certname and a present
`authenticated` field without verifying their meaning or target agreement.

**Reproduced:** a valid, checksummed catalog envelope for `requested-node`
containing a payload for `different-node` was accepted.

**Scope:** `internal/puppetdb/`, `internal/compiler/wire.go`,
`internal/snapshot/store.go`, and workflow callers.

**Changes**

1. Require agreement between requested target, response certname, snapshot
   envelope target, payload certname, and supplied trusted-fact certname.
2. Validate trusted-fact field types and supported authentication values against
   the actual Puppet wire contract. Preserve the explicit compiler-lookup policy.
3. Validate factset containers and entries before flattening: distinguish an
   explicitly empty factset from missing or malformed data, reject duplicate
   fact names, and reject malformed entries.
4. Validate required snapshot metadata and compiler API values explicitly.
5. Apply consistent failure classification before compilation or comparison.

**Acceptance**

- Wrong-target responses and inconsistent envelopes fail before candidate
  compilation or diffing, even when checksums and environments match.
- Invalid trusted facts cannot be labeled as validated input.
- Missing fact collections and duplicate fact names cannot silently become an
  empty or overwritten factset.
- Valid file-backed and PuppetDB-backed inputs follow the same identity rules.

## Phase 2: content evidence and compiler side effects

### 2.1 Give each side of a File comparison its own evidence identity

- [ ] Implement and verify.

**Finding: high severity, reproduced.** The differ resolves file content only
when a content-bearing parameter changes. Identical `source` strings cause
zero retrievals and no difference. When retrieval does run, both sides use the
candidate environment and can incorrectly compare equal.

**Scope:** `internal/model/`, `internal/filecontent/`, `internal/diff/`,
`internal/compare/`, `internal/compiler/`, and `internal/snapshot/`.

**Changes**

1. Replace the single-environment content interface with explicit baseline and
   candidate evidence inputs. Each side identifies its source, environment,
   and any immutable content or capture identity available.
2. Define the evidence hierarchy for inline content, validated checksums,
   captured digest evidence, and live retrieval. Keep evidence source and
   verification status visible in the result.
3. Evaluate source-backed content even when the source URL is unchanged.
4. Distinguish current environment content from historical baseline content.
   Extend capture to retain digest evidence needed to compare source-backed
   historical snapshots. Define missing historical evidence as indeterminate;
   fetching today's baseline environment must not imply historical verification.
5. Inspect supported static-catalog metadata and retain the content evidence
   needed by this model instead of discarding it during compiler decoding.
6. Keep directory and recursive-source semantics explicit. Unsupported byte
   comparisons must state their limitation and cannot silently imply unchanged
   content. Preserve reference changes when that is the evidence available.
7. Specify how exclusions interact with retrieval and diagnostics. Keep requested
   failures visible, while ensuring the documented exclusion policy matches the
   implemented outcome rule.

**Acceptance**

- A module file edit with an unchanged `source` URL is detected when verified
  evidence exists, and is explicitly indeterminate otherwise.
- Baseline and candidate retrievals use their own evidence contexts.
- A historical snapshot cannot be declared equivalent using unrelated current
  bytes. Missing historical evidence cannot yield a clean result.
- Inline, source-backed, static-metadata, directory, recursive, and mixed
  representation cases have end-to-end coverage.

### 2.2 Implement source selection and checksum validation faithfully

- [ ] Implement and verify.

**Findings:** source arrays use the first element instead of the first existing
source; explicit source authorities are ignored; arbitrary nonempty strings
are accepted as compiled checksums. The checksum probe published
`audit-new-secret` as a SHA-256 digest without a diagnostic.

**Scope:** `internal/filecontent/evidence.go`, `resolver.go`, and interfaces.

**Changes**

1. Preserve ordered source lists through resolution. Try subsequent sources
   only for a verified absence, not for authentication or transport failures.
2. Handle recursive source-selection rules separately from single-file fallback.
3. Define supported source authorities. Reject unsupported explicit authorities
   with safe diagnostics rather than silently retrieving from a different server.
4. Validate checksum representation, algorithm, encoding, and length using real
   wire fixtures. Normalize supported equivalent forms before comparison.
5. Publish digest fields only after validation or local hashing. Retain the
   existing protection against source-path traversal.

**Acceptance**

- A missing first source and existing second source resolve correctly.
- Authentication and retrieval failures are not hidden by fallback.
- Unsupported authorities never produce purported evidence from the compiler.
- Invalid checksum strings cannot reach a report as verified digest evidence.

### 2.3 Block v3 configurations that invalidate the baseline

- [ ] Implement and verify.

**Finding: high severity, confirmed by code inspection and already acknowledged
for direct v3 in README.md.** Both direct v3 and permitted v4-to-v3 fallback
can compile against a PuppetDB baseline. The compiler can persist candidate
facts and catalogs, affecting later comparisons and other PuppetDB consumers.

**Scope:** target resolution, compiler fallback, comparison and capture reporting.

**Changes**

1. Reject a PuppetDB comparison baseline whenever the selected policy can invoke
   v3, whether directly or through fallback.
2. Keep this validation specific to comparison. Capture has a different purpose
   and must report the side effects of the API it actually invokes.
3. Report v3 persistence effects alongside trusted-fact semantics wherever v3
   executes. A file baseline protects the comparison input, not other consumers.
4. Revisit the claim that status 404 or 501 proves v4 is unsupported. Validate
   the supported fallback signals against server behavior and document any
   remaining ambiguity. Authentication and malformed-response failures must
   not trigger fallback.

**Acceptance**

- Invalid direct-v3 and fallback configurations fail before network activity.
- Every accepted v4 request disables both fact and catalog persistence.
- Every executed v3 path exposes effective API and persistence consequences.

## Phase 3: aggregation, assessment, and provenance

### 3.1 Make aggregation respect every member's disclosure policy

- [ ] Implement and verify.

**Finding: medium severity, reproduced.** Aggregation groups equal raw changes,
then publishes the alphabetically first target's projection. A group containing
targets `a` and `z` exposed plaintext although `z` redacted that parameter.

**Scope:** `internal/aggregate/build.go`, published model, and renderer consumers.

**Changes**

1. Define the group's projection as the most restrictive disclosure policy of
   its members. Preserve raw-evidence equality as a separate grouping concern.
2. Combine sensitivity and selector outcomes explicitly rather than choosing a
   representative whose value happens to be first.
3. Preserve links to individual target changes and deterministic group ordering.

**Acceptance**

- Reordering or renaming targets cannot change a group's disclosure policy.
- A group containing a redacted member cannot publish that member's protected
  evidence through another member's projection.
- Distinct sensitive changes remain separate, with correct target references.

### 3.2 Preserve useful evidence for resource and graph changes

- [ ] Implement and verify.

**Finding: medium severity.** Additions and removals retain identity only, so
different added configurations can group as equivalent. File-content evidence
does not reach the assessment projection. Edge groups are excluded from
assessment even though edge-only comparisons are supported.

**Scope:** resource differ, aggregate model, all report formats, and
`internal/assess/request.go`.

**Changes**

1. Preserve publishable before/after resource evidence for removals and additions.
   Apply phase 1 sensitivity rules and File-content disclosure rules throughout.
2. Include the actual resource evidence in equivalence decisions so materially
   different additions or removals do not collapse by identity alone.
3. Carry safe File-content state and evidence source into aggregate groups and
   inference payloads. Keep file bytes and content digests out of inference.
4. Include meaningful edge evidence in assessment planning, with explicit budget
   accounting. If evidence is omitted, report the omission and resulting
   assessment scope rather than implying a complete review.
5. Keep deterministic comparison outcomes independent of assessment output.

**Acceptance**

- Same-identity additions with different managed settings group separately.
- Added and removed sensitive values never reach publishable evidence.
- Assessment inputs distinguish verified changes, reference changes, and
  indeterminate File content without transmitting content or digests.
- Edge-only runs receive useful assessment evidence or an explicit scope limit.

### 3.3 Record actual capture provenance and define snapshot fidelity

- [ ] Implement and verify.

**Finding: medium severity.** Capture discards compiler provenance and warnings,
then stamps the requested API into the envelope. A v4-to-v3 capture can therefore
claim `compiler_api: v4`. Snapshot payloads are typed projections, although some
documentation describes them as unmodified service payloads.

**Scope:** `internal/capture/workflow.go`, snapshot envelope, compiler carriers,
and capture output.

**Changes**

1. Build capture metadata from the returned compiler provenance: requested API,
   effective API, fallback, trust semantics, and factset identity.
2. Surface capture warnings and preserve the metadata needed to audit the snapshot.
3. Define snapshots as a validated PIACE projection. Retain every field needed
   for identity, sensitivity, comparison, and phase 2 content evidence; document
   which service fields are intentionally outside that projection.
4. Define checksum scope explicitly for payload and captured evidence. Validate
   relationships among metadata and payload when loading a snapshot.

**Acceptance**

- A fallback capture records effective v3 and exposes its warnings.
- Capture and reuse preserve all evidence required by the comparison contract.
- Snapshot integrity checks and attribution checks are distinct and both tested.

## Phase 4: command behavior, artifacts, and bounded work

### 4.1 Resolve requirements per command and capability

- [ ] Implement and verify.

**Finding: medium severity.** Service resolution always requires compiler and
PuppetDB configuration. Comparison constructs four clients even when PuppetDB
is unused. Capture requires a comparison candidate environment despite its
own `--environment` flag, or despite capturing facts only.

**Changes**

1. Share strict decoding and common field validation, then resolve the requirements
   of compare, capture facts, capture catalog, and explain separately.
2. Require and instantiate only services used by the selected sources and enabled
   analysis. Reuse each configured service's hardened client where appropriate.
3. Let catalog capture use its requested environment directly. Fact capture must
   not require comparison-only fields.
4. Keep compiler and PuppetDB credentials isolated, and keep inference independent.
5. Add configurable request timeouts with explicit precedence. Fix the implicit
   30-second ceiling that currently caps longer impact-estimate deadlines.
6. Verify candidate-environment selection against supported classifiers. Decide
   explicitly whether v4 requests require `prefer_requested_environment`, and
   keep returned-environment validation unconditional.

**Acceptance**

- File facts plus file baseline with impact disabled need no PuppetDB identity.
- Fact capture requires only its actual retrieval and destination inputs.
- Catalog capture accepts its environment without an unrelated candidate value.
- An unused inference section causes no inference request from comparison.
- Configured longer deadlines work; shorter caller deadlines still win.

### 4.2 Publish artifacts atomically and validate destination relationships

- [ ] Implement and verify.

**Finding: medium severity.** Snapshot no-clobber behavior is a check followed
by an overwriting rename. Reports and assessments use direct writes, and output
paths can collide with each other or their inputs.

**Changes**

1. Implement atomic no-clobber snapshot publication when replacement is disabled.
2. Use same-directory temporary files and atomic replacement for generated
   reports and assessments, with explicit permissions and durability behavior.
3. Reject conflicting output destinations and input/output aliases before service
   requests. Account for normalized paths, existing symlinks, and hard links.
4. Specify multi-artifact failure behavior. Prepare requested artifacts before
   publication where possible; do not claim an atomic transaction across files.
5. Preserve nonzero outcomes on requested-output failure and keep logs honest
   about what was produced.

**Acceptance**

- Concurrent no-replace snapshot writers cannot overwrite each other.
- Readers never observe partially written individual artifacts.
- `explain --json-in report.json --ai-out report.json` fails before inference.
- Colliding text, JSON, HTML, assessment, and snapshot destinations are rejected.
- Filesystem behavior is verified on Linux and macOS.

### 4.3 Validate stored reports before trusting or transmitting them

- [ ] Implement and verify.

**Finding: reproduced hardening gap.** `DecodeJSON` accepts
`{"schema_version":1}`. Syntactic decoding and a version tag do not establish
that a stored document is a complete, internally consistent comparison.

**Changes**

1. Add semantic validation for required fields, enums, target uniqueness, identity
   agreement, aggregate references, content evidence, and outcome consistency.
2. Recognize legitimate partial comparisons without confusing missing structure
   with recorded retrieval or compilation failures.
3. Validate that publishable evidence obeys disclosure rules before inference.
   Reject contradictory documents rather than silently repairing their outcome.
4. Apply bounded decoding and reject duplicate or trailing document content where
   it creates ambiguous interpretation. Give configuration documents equivalent
   single-document guarantees.

**Acceptance**

- Sparse, contradictory, invalid-reference, and malformed documents fail before
  inference. Valid partial reports remain usable and explicitly partial.
- A stored report round-trips without loss of numeric precision or safe evidence.
- Forged sensitivity wrappers or raw File content cannot bypass request disclosure
  policy merely by arriving through a stored result.

### 4.4 Bound computation and inference payload size

- [ ] Implement and verify.

**Findings:** response-byte limits do not bound canonicalization cost, local
file/stdin reads, or inference request size. The eight-byte numeric input
`1e-10000` expanded into 10,002 output bytes. Group-count limits do not constrain
large values, all target records, or all impact estimates.

**Changes**

1. Define numeric digit/exponent, nesting, total input, and canonical output
   budgets. Preserve exact supported decimal semantics without repeated string
   prepending or unbounded rational expansion.
2. Apply limits to snapshots, result files/stdin, policy notes, and change-context
   acquisition before expensive allocation or parsing.
3. Bound git output collection and execution time for change-context generation.
4. Introduce a total inference request budget covering values, targets, groups,
   impact entries, context, and policy notes. Account for the retry request too.
5. Make omitted evidence and truncation explicit. Preserve valid UTF-8 and valid
   structured values; never truncate raw JSON bytes into malformed payloads.

**Acceptance**

- Small exponent inputs cannot cause unbounded memory or CPU work.
- Oversized local and remote inputs fail with controlled diagnostics.
- Large catalogs produce a bounded, valid inference request with truthful scope
  and truncation accounting.
- Benchmarks cover realistic large catalogs and adversarial numeric shapes.

## Phase 5: real evidence, documentation, CI, and release readiness

### 5.1 Verify the wire contracts using synthetic infrastructure data

- [ ] Implement and verify.

The existing suite passed race tests, vet, build, and formatting during review.
That does not establish real-service conformance: several fixtures encode the
same assumptions as the code. Acquire real wire evidence as early as a preceding
step needs it; this step is the final integration gate.

**Changes**

1. Generate catalogs and facts from dedicated test manifests containing synthetic
   values. Exercise supported Puppet and OpenVox versions and PuppetDB baselines.
2. Record fixture producer/version, request shape, generation procedure, and
   expected semantic result. Keep credentials and real infrastructure data out.
3. Cover sensitivity metadata and nested wrappers, trusted facts, v3/v4 envelopes,
   environment classification, static file metadata, source arrays, directories,
   recursive sources, and graph relationships.
4. Verify PuppetDB impact query syntax, ordering, and limits. Reject `null` as a
   successful empty row collection and test truthful truncation accounting.
5. Verify inference structured-output behavior for the supported provider matrix,
   retaining unconditional local reply validation and the bounded retry policy.
6. Replace relevant assumption-only tests with recorded evidence and executable
   conformance assertions. Keep genuinely unverified claims explicitly scoped.

**Acceptance**

- Every load-bearing wire assumption has a reproducible fixture or a clearly
  identified integration gate still preventing completion.
- The initial disclosure and false-clean failures are permanent regression tests.
- Model-generated prose never changes deterministic outcomes or exit codes.

### 5.2 Define determinism and align every documented guarantee

- [ ] Implement and verify.

**Finding:** documentation promises identical artifact bytes for identical
catalogs and configuration, while production includes a changing invocation
timestamp and the acceptance harness freezes the clock.

**Changes**

1. Define a canonical semantic comparison projection separately from invocation
   metadata. Preserve real timestamps while making semantic reproducibility
   independently testable. Keep assessment linkage to its source report explicit.
2. Test semantic equality with different clocks and input ordering, and exact
   byte equality when the complete result, including metadata, is identical.
3. Update README.md, CONTEXT.md, docs/development.md, docs/change-assessment.md,
   docs/ci.md, docs/release.md, examples, CLI help, and package comments using
   the following alignment checklist.

| Existing mismatch | Required documented contract |
| --- | --- |
| Identical catalogs imply identical full artifact bytes | Separate semantic determinism from invocation metadata |
| Explain sends exactly one request | One attempt, plus at most one retry for an unusable reply |
| Sensitive values are always absent under wrapper-only detection | State the verified sensitivity representations and disclosure scope |
| Redacted parameters are absent from inference | Distinguish omitted data from entries carrying redaction markers |
| Pseudonymization implies certnames cannot appear elsewhere | State which structured identity fields are substituted and which free-text values remain |
| Changing pseudonymization leaves assessment output identical | Assessment prose is nondeterministic; identity restoration is the local guarantee |
| Snapshots retain an unmodified service payload | Describe the validated PIACE projection and retained evidence |
| Capture records compiler API correctly | Document requested and effective API plus fallback provenance |
| All relative paths have only one resolution rule | State snapshot containment restrictions as well as the config-relative base |
| `{certname}` is a whole component only | State the accepted extension-suffix grammar |
| Reports never rewrite inputs or overwrite snapshots unexpectedly | State and test the implemented publication and destination rules |
| Every edge is a consequence of a resource change | Describe edge-only changes and assessment scope accurately |

4. Remove stale references to missing `design.md` and `requirements.md` files.
   Keep architectural decisions with the module that owns them. Remove routine
   procedural commentary and unsupported absolutes.

**Acceptance**

- Every guarantee has a matching test or an explicit evidence limitation.
- Examples load under command-specific validation and CLI help agrees with them.
- Documentation distinguishes result completeness, verification, redaction,
  pseudonymization, and advisory assessment without conflating them.

### 5.3 Make CI examples and release tooling exercise the supported path

- [ ] Implement and verify.

**Findings:** CI installation examples verify checksums but leave signature
verification inactive. Change-context examples rely on branch names that may
not exist locally in detached checkouts. Release compilation selects the Go
version from go.mod, coupling the release compiler to the minimum language version.

**Changes**

1. Make signature verification an executed prerequisite to running downloaded
   binaries in each CI example. Fetch the signature bundle and constrain signer
   identity and issuer. Keep checksum verification as the integrity check.
2. Use explicitly fetched refs or commit identities for change-context. Test
   detached merge checkouts and branches represented only by remote refs.
3. Verify job-specific credential scope in each example, especially GitLab
   protected-variable availability and environment scoping. State setup needed
   to enforce the claimed separation between compare and explain jobs.
4. Check candidate-environment mapping against the actual deploy step. Avoid
   implying a dash substitution implements every site's branch mapping.
5. Select and record a supported release Go toolchain explicitly, while retaining
   separate minimum-version testing. Review action references and container base
   identity so published artifacts have traceable build inputs.
6. Keep binary, container, checksum, signature, and attestation verification
   consistent across the release workflow and consumer instructions.

**Acceptance**

- Each example's install and change-context sequence works in its intended
  checkout shape and refuses invalid signatures.
- Credential separation is enforced by CI configuration, not just comments.
- Release artifacts identify their actual toolchain and pass the documented
  consumer verification process.

### 5.4 Complete the 0.5.0 release candidate

- [ ] Implement and verify.

1. Run formatting, vet, build, the full race-enabled suite, and the supported
   Linux/macOS matrix. Run the focused integration and resource-budget checks
   introduced above.
2. Inspect representative text, JSON, and HTML artifacts for clean, allowed
   difference, policy failure, compilation failure, operational failure,
   redacted, source-content, edge-only, and partial-assessment runs.
3. Update CHANGELOG.md and release examples for 0.5.0, describing resulting
   behavior and verified limitations. Update artifact schema identifiers and
   fixtures to match the final formats.
4. Build and verify the release candidate locally or in the authorized CI flow.
   Record the commit, toolchain, test results, and conformance evidence here.
5. Obtain confirmation before pushing, creating a PR, publishing a release or
   image, or performing any other externally visible release action.

**Acceptance:** all phase checkboxes are complete, release evidence is recorded,
and no unresolved finding is hidden behind a passing synthetic fixture.

## Primary references

- [Puppet parser sensitivity handling](https://github.com/puppetlabs/puppet/blob/main/lib/puppet/parser/resource.rb)
- [Puppet resource serialization](https://github.com/puppetlabs/puppet/blob/main/lib/puppet/resource.rb)
- [Puppet File source selection and checksums](https://help.puppet.com/core/current/Content/PuppetCore/Markdown/file.htm)
- [Puppet file-content endpoint](https://help.puppet.com/core/current/Content/PuppetCore/server/http_api/http_file_content.htm)
- [Puppet v4 catalog request contract](https://help.puppet.com/core/current/Content/PuppetCore/server/http_api/puppet-api/v4/catalog.htm)

These references establish upstream behavior, not conformance of a particular
deployment. Pin source revisions when producing the phase 5 fixture records.
