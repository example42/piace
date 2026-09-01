// Package filecontent implements PIACE's managed File-content evidence
// resolver: it establishes whether a File resource's effective content
// changed, without ever putting the content itself into a result.
//
// # Scope
//
// ResolveFileContentEvidence is a pure decision function over two
// normalized Puppet `File` resources' parameter maps (see
// internal/normalize): it does not diff resources, does not decide
// *whether* a File resource's content-bearing parameter changed, and does
// not walk a NormalizedCatalog. internal/diff is its only caller: when it
// detects that a File
// resource's content-bearing parameter (content, source, checksum, or
// checksum_value) differs between a target's baseline and candidate
// catalogs, it calls ResolveFileContentEvidence once for that resource
// and attaches the returned model.FileContentEvidence to the
// corresponding model.ResourceChange.FileContent field. This package is
// deliberately independent of internal/diff so it can be tested and
// reviewed on its own: it is a standalone, reusable File-content-evidence
// resolver.
//
// # The exact priority order
//
// ResolveFileContentEvidence works through four resolution steps in
// order, falling through to the next only when the current one cannot
// produce comparable evidence:
//
//  1. Inline content: if both sides expose Puppet's `content` parameter
//     as a literal string value, hash both with SHA-256 and compare the
//     digests directly. No network call. EvidenceSource:
//     FileContentEvidenceInline.
//  2. Compiled checksum: if inline content is unavailable/incomparable
//     but both sides expose a "recognized compatible checksum" (see
//     below for exactly what that means and why), compare the checksum
//     values directly. EvidenceSource: FileContentEvidenceCompiledChecksum.
//  3. Compiler retrieval: if neither of the above applies (typically
//     because one or both sides only carry a `source` reference, e.g. a
//     puppet:/// module path, with no directly comparable checksum),
//     retrieve the referenced bytes through a ContentResolver for
//     whichever side needs it, hash the retrieved bytes locally, and
//     compare digests. A side that already has literal `content`, even
//     when the other side does not, is hashed locally rather than
//     retrieved. Retrieval only happens for a side that has a `source`
//     reference and no literal content. EvidenceSource:
//     FileContentEvidenceCompilerRetrieval.
//  4. If step 3 cannot establish comparable bytes for both sides, with no
//     ContentResolver was supplied at all, a side has neither literal
//     content nor a resolvable reference, or retrieval itself failed
//     (network, timeout, not-found, unsupported source scheme), this
//     package reports FileContentReferenceChanged or
//     FileContentIndeterminate rather than State: changed/unchanged, and
//     always returns a non-nil *model.Diagnostic
//     (model.OperationVerifyContent) alongside it. See "Step 4:
//     reference_changed vs. content_indeterminate" below for the exact
//     rule distinguishing the two, and evidence.go's ResolveFileContentEvidence
//     doc comment for the precise decision tree.
//
// Resolution stops between step 2 and step 3 for a directory or
// recursive File resource, whose `source` names a directory tree the
// compiler's file_content endpoint cannot serve at all; see "Sources
// that are not byte-comparable" below.
//
// Every returned model.FileContentEvidence carries only an algorithm
// name, digest hex strings, the evidence-source enum, and the comparison
// state, never managed file bytes. Hashing always happens locally over
// already-retrieved bytes (evidence.go's sideDigest and resolver.go's
// CompilerContentResolver.Digest); only the resulting digest crosses
// back into ResolveFileContentEvidence's return value or into any
// diagnostic message this package builds. See evidence_test.go for the
// explicit assertion that no test's sample content bytes ever appear in
// any value or diagnostic message this package produces.
//
// # Step 4: reference_changed vs. content_indeterminate
//
// Two failure states are distinguished. If retrieval cannot establish
// comparable bytes, the result is `reference_changed` or
// `content_indeterminate` rather than a claimed verified content change.
// A source or reference change is always reported without rendering its
// bytes, and a retrieval failure carries a target diagnostic and makes
// any unresolved content comparison non-clean. This package draws the
// boundary between the two states as follows:
//
//   - FileContentReferenceChanged (step 4a): no ContentResolver was
//     supplied at all (retrieval capability itself is unavailable to this
//     comparison, for example no compiler adapter being wired up for this call
//     site) AND the two sides' `source` reference strings differ (or one
//     side has a reference and the other does not). This is the
//     "we can see the reference changed, but nothing attempted or could
//     attempt a byte-level comparison" case: PIACE still reports the
//     visible fact (the reference changed) without ever claiming a
//     verified content change.
//   - FileContentIndeterminate (step 4b, and the residual case of 4a):
//     either an actual retrieval attempt failed (the ContentResolver
//     returned an error: network, timeout, not-found or unsupported source
//     scheme), or no resolver was supplied and the references do not
//     visibly differ (so there is not even a reference-level fact to
//     report) or a side has neither literal content nor a resolvable
//     reference at all. This is the "we cannot tell what happened" case.
//
// Both states always carry a non-nil diagnostic
// (model.OperationVerifyContent): verify_content is one of the named
// operations in the operational-error class. The outcome reducer treats
// any FileContentReferenceChanged or FileContentIndeterminate state as
// non-clean; this package does not implement that reducer, but its State
// value is exactly what makes the distinction determinable, and the
// accompanying diagnostic guarantees the retrieval failure is never
// silently dropped even if a caller ignored the State value.
//
// # Sources that are not byte-comparable
//
// A File resource with `ensure => directory`, or with any `recurse`
// value other than an explicit false, copies a whole directory tree:
// its `source` (e.g. `puppet:///modules/tp/run_info/`) names a
// directory, not a file. The compiler's file_content endpoint serves one
// file's bytes and nothing else -- a directory reference is rejected
// outright (verified against a deployed OpenVox compiler on 2026-08-28:
// GET /puppet/v3/file_content/modules/tp/run_info/?environment=<env>
// returns HTTP 500 with an empty body, for both the trailing-slash and
// the bare form). Attempting step 3 for such a resource therefore cannot
// succeed, and reporting its inevitable failure as content_indeterminate
// would state that content evidence was *lost* when in truth
// byte-level content evidence was never the right evidence for this
// resource.
//
// ResolveFileContentEvidence therefore tests isDirectoryOrRecursive on
// both sides after step 2 and before step 3, and resolves the resource
// without any retrieval:
//
//   - The `source` references differ (or one side has one and the other
//     does not): FileContentReferenceChanged, with a *warning*-severity
//     verify_content diagnostic. Source and reference changes are always
//     reported without rendering their bytes, and this is that rule
//     applied to the one case where rendering them is not merely
//     undesirable but impossible. The severity is what
//     distinguishes it from step 4: nothing failed, so
//     model.OutcomeForDiagnostic must not turn the comparison into an
//     operational error, while the diagnostic still records why no
//     digest accompanies the change.
//   - The references do not visibly differ, so some other
//     content-bearing parameter did: FileContentIndeterminate with an
//     error-severity diagnostic, exactly as step 4 would. There is
//     neither a reference-level fact to report nor bytes to compare, and
//     model.TargetResult.ClassifyOutcome's indeterminate check keeps that
//     from collapsing into a clean run.
//
// This widens the reference_changed state beyond the "no ContentResolver
// was supplied" rule stated below: reference_changed now also covers a
// reference that changed and *cannot* be byte-compared by any resolver,
// not only one that changed with no resolver available to try. Both
// readings share the same meaning -- "the reference changed and no
// byte-level comparison stands behind that statement" -- and neither
// ever claims a verified content change.
//
// # Identifying "a recognized compatible checksum" (step 2)
//
// Puppet's `File` resource type documents two related but distinct
// checksum-related parameters (references.md puppetlabs/puppet, "File"
// resource type reference, `checksum` and `checksum_value` attributes):
//
//   - `checksum`: "The checksum type to use when determining whether to
//     replace a file's contents." Allowed values: sha256, sha256lite,
//     md5, md5lite, sha1, sha1lite, sha512, sha384, sha224, mtime, ctime,
//     none. The default is sha256.
//   - `checksum_value`: "The checksum of the source contents. Only md5,
//     sha256, sha224, sha384 and sha512 are supported when specifying
//     this parameter. If this parameter is set, source_permissions will
//     be assumed to be false..."
//
// A normalized File resource's parameter map (internal/normalize)
// carries these as ordinary string-valued entries under the keys
// "checksum" and "checksum_value" when a manifest sets them explicitly,
// or when the compiler inlines them into a static catalog for a
// `source`-based File resource. That inlining is documented behaviour:
// PUP-5117 "Inline file checksums" says the compiler should inline the
// desired file `checksum` and `checksum_value` for `file` resources
// provided the resource has a `source` parameter with URI scheme
// `puppet`. Either origin produces the same two parameter keys in the
// normalized model, so this package does not need to distinguish an
// explicit declaration from a compiler-inlined one.
//
// "A recognized compatible checksum" is therefore judged as: both sides
// carry a non-empty `checksum_value`, both sides carry the same
// `checksum` algorithm name, and that algorithm name is one of the five
// checksum_value-compatible types `checksum_value`'s own documentation
// names explicitly: md5, sha256, sha224, sha384, sha512 (see
// recognizedChecksumAlgorithms in evidence.go). `mtime`, `ctime` and
// `none` are excluded even if both sides happen to agree on the
// algorithm name, because they are not cryptographic content digests at
// all: `mtime` and `ctime` reflect filesystem timestamps rather than
// file bytes, and `none` disables content comparison entirely, so
// treating either as compatible-checksum evidence would misrepresent a
// timestamp or an intentionally skipped comparison as a verified content
// comparison. A checksum-*lite* variant, evaluated over only a file's
// first and last blocks rather than its full contents, is excluded by
// the same documented restriction: checksum_value.md explicitly says the
// lite variants are not among the supported set.
//
// A mismatched `checksum` algorithm name between before/after (e.g. one
// side sha256, the other md5) is deliberately never treated as step 2
// evidence, even though the checksum_value strings could technically
// still be compared byte-for-byte as opaque strings: two different-length
// digest algorithms are not "authoritative" evidence of anything when
// interpreted as if they were comparable, so this case falls through to
// step 3/4 rather than risking a false changed/unchanged classification.
//
// # ContentResolver and its documented, unverified endpoint assumption
//
// The interface this package must implement is
// "ContentResolver.Digest(reference, context) -> DigestEvidence".
// resolver.go's CompilerContentResolver is the compiler-backed
// implementation, built against *transport.Client exactly as the
// PuppetDB and compiler adapters are: no separate unauthenticated HTTP
// path is introduced.
//
// Protocol adapters are the compatibility boundary, and their exact
// requests and responses have to be demonstrated with fixtures from the
// deployed service versions before a combination is declared supported,
// so what follows separates the two. Verified against a deployed OpenVox
// compiler on 2026-08-25: the request path and query shape, the 200
// response with Content-Type application/octet-stream and raw bytes, and
// the whole Accept contract (400 without the header, 200 with
// application/octet-stream, 406 with application/json), exercised over
// one `puppet:///modules/<MODULE>/<file>` reference. Still documented
// only, and marked as such below: the 404 response body for a missing
// file, and the treatment of non-`puppet:` source schemes.
//
//   - GET /puppet/v3/file_content/<mount-point>/<name>?environment=<env>
//     returns the raw bytes of the referenced file with Content-Type
//     application/octet-stream and HTTP 200, per Puppet's documented v3
//     file_content endpoint (puppetlabs/puppet, api/docs/http_file_content.md):
//     "The file_content endpoint returns the contents of the specified
//     file." A missing file returns HTTP 404 with the documented body
//     ({"message":"Not Found: Could not find file_content <path>",
//     "issue_kind":"RESOURCE_NOT_FOUND"}), and a reference naming a
//     directory returns HTTP 500 with an empty body -- both verified
//     against a deployed OpenVox compiler on 2026-08-28. Nothing depends
//     on either body, because
//     this package treats any non-2xx response as a retrieval failure
//     (step 4b), never
//     inspecting the response body for meaning, matching this package's
//     "never render bytes, never trust echoed content" posture.
//   - The request carries `Accept: application/octet-stream`, and the
//     header is mandatory. Every /puppet/v3/ route is served by the
//     compiler's embedded Ruby Puppet request handler, whose
//     Puppet::Network::HTTP::Request#response_formatters_for raises
//     "Missing required Accept header" when no Accept header is present
//     the request is rejected before any file is served. Verified
//     against a deployed OpenVox server (2026-08-25) on this exact
//     endpoint: no Accept header returns HTTP 400
//     "Bad Request: Missing required Accept header", and
//     `Accept: application/octet-stream` returns HTTP 200 with the
//     file's raw bytes. The value is endpoint-specific and cannot be
//     shared with the catalog endpoint's: the file-content indirection
//     serves only the binary format, and the same verified request with
//     `Accept: application/json` returns HTTP 406
//     "Not Acceptable: No supported formats are acceptable". Reusing the
//     catalog's Accept value here would trade one rejected request for
//     another.
//   - A Puppet File resource's `source` value in the form
//     `puppet:///<mount-point>/<name>` (the documented form for the
//     `modules/<MODULE>` and other file-serving mount points; see
//     Puppet's File type `source` attribute documentation) maps directly
//     onto the file_content endpoint's path: the URI's path component,
//     with its leading slash trimmed, is exactly the endpoint's
//     `<mount-point>/<name>` path segment; see parsePuppetSourceURI in
//     resolver.go.
//   - Documented-only, not exercised: a `source` value using any other
//     URI scheme (a bare local filesystem path, a `file:` URI, or an
//     `http(s):` URI) is not retrievable through this endpoint at all,
//     Puppet's own File type
//     documentation describes those as resolved directly by the agent,
//     not proxied through the compiler's file-serving API. This package
//     reports that case as a retrieval failure (step 4b:
//     content_indeterminate), not step 4a, since there is no
//     compiler-mediated way to establish whether such a reference
//     changed either. Retrieving content for those source schemes,
//     including via any other request path, is out of scope here and is
//     not implemented.
//
// # Redaction boundary: deferred to internal/diff
//
// model.FileContentEvidence.Redacted already exists on the struct as a
// plain bool field. This package's ResolveFileContentEvidence never sets
// it: whether a given File resource's content-bearing parameter is
// subject to a configured config.RedactionSelector is a property of a
// target's *resolved configuration*, not of the two catalogs being
// compared, and this package has no configuration dependency at all and
// must not gain one just to answer that question.
//
// Every redaction determination happens after semantic equality and
// exclusions but before result serialization, which is the result
// boundary internal/diff owns. That boundary's job, not this package's,
// is to take the model.FileContentEvidence this package produced and
// construct a *redacted projection* of it when a resolved redaction
// selector matches the resource and parameter: a redacted content
// selector emits a stable REDACTED value while preserving the change
// classification and no digest. So a redacted projection keeps State
// exactly as this package computed it, clears Algorithm, BeforeDigest
// and AfterDigest, puts a stable REDACTED marker in their place, and
// sets Redacted: true. This package produces only the unredacted,
// real-digest FileContentEvidence, and nothing in its shape (a plain
// serializable struct with an already-present Redacted bool) stops that
// projection being built as a separate, later transformation.
package filecontent
