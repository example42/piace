// Package filecontent compares desired File bytes using independent evidence
// inputs for baseline and candidate. It never returns or publishes file bytes.
//
// ResolveSide uses inline content, validated checksum_value, static metadata,
// captured digests, then live compiler retrieval, in that order. Each side
// records its environment, catalog source/identity, evidence source and whether
// it was verified. Different digest algorithms are indeterminate, with a
// warning: opaque digest strings are never compared as if they used the same
// algorithm, and neither side failed to produce evidence.
//
// Both PuppetDB baselines and snapshot baselines are historical. A historical
// source reference without retained evidence is indeterminate and is never
// fetched from today's environment. Capture can hash a live source and retain
// that digest inside the snapshot's checksummed payload. The captured digest
// describes bytes observed at capture time, not an atomic compiler/fileserver
// transaction. Keep the environment stable during capture and comparison.
// Candidate retrieval likewise describes current environment bytes, not an
// immutable compilation identity merely because a catalog carries code_id.
//
// Static metadata is Puppet's title-keyed metadata map with checksum.type and
// checksum.value. Recursive metadata is retained in the snapshot but is not a
// verified digest for a whole tree. Inline content is hashed with SHA-256.
// Supported full checksums are md5, sha224, sha256, sha384 and sha512: exact
// hexadecimal lengths are required and case is normalized. The matching
// {algorithm} prefix in metadata is accepted. Timestamps, lite algorithms and
// arbitrary strings never become digest evidence. Invalid supplied checksums
// fail validation and are not hidden by live retrieval.
//
// Source lists preserve order and use the first existing source. Only a 404
// carrying Puppet's RESOURCE_NOT_FOUND issue kind or its exact file_content
// not-found message permits trying the next source. Generic router errors,
// authorization failures and transport failures stop selection.
//
// Retrieval supports authority-free puppet:/// references only. Explicit
// authorities, other schemes, query/fragment components and traversal paths are
// refused with diagnostics that do not echo catalog values. Each retrieval uses
// its own side's environment at GET /puppet/v3/file_content/<mount-path>, with
// Accept: application/octet-stream. Only HTTP 200 yields locally hashed bytes.
//
// A File whose ensure is absent or link manages no bytes: Puppet ignores
// content, source and checksum_value there. That is not_managed on either
// side, reported without evidence and without a diagnostic, because it is a
// determinate answer rather than a failed comparison. The ensure value itself
// is not a content-bearing parameter, so a change to it is reported by the
// ordinary parameter diff.
//
// Directory, recursive and non-file sources are not byte-comparable. Their
// source changes are reference_changed with a visible limitation warning;
// unchanged references are content_indeterminate with a warning (not a
// resource difference): byte equality is never claimed, and fail_on_diff
// does not treat the unverified comparison as a change. Recursive
// sourceselect first/all is not approximated by single-file fallback.
// Capture may retain these catalogs with a limitation warning; later
// comparison still reports the missing byte evidence as a notice.
//
// Diff evaluates content even when the source URL is unchanged. Its exclusions
// run after evidence resolution: they suppress matching changes and connected
// edges, but requested retrieval failures remain diagnostics and affect outcome.
// Disclosure policy remains in diff's publication pass: sensitivity or a
// content selector suppresses both digests without changing comparison state.
// Added and removed resources resolve only the existing catalog side. Verified
// evidence uses resource_added/resource_removed states; these describe catalog
// membership, not filesystem creation or deletion. Missing historical digests,
// failed retrievals and unsupported byte evidence all remain indeterminate,
// including when an exclusion later suppresses the resource change.
//
// The diagnostic severity separates evidence that was never obtainable from an
// attempt that failed, and classifyEvidenceError is the single place that
// decides it for both the differ and internal/capture. A historical baseline
// retaining no digest, a directory or recursive source, a bare local path the
// compiler's file server does not serve, a resource naming neither content nor
// source, and two sides carrying digests of different algorithms are warnings:
// they describe the catalog, not the run, and they are ordinary. An invalid
// checksum, a refused traversal path, a failed retrieval and a missing
// environment are errors. Either way the state stays indeterminate, which is
// what keeps a clean result off the table; see model.ClassifyOutcome for how
// that reaches the exit code. In capture the same split decides whether a
// snapshot is published at all.
//
// Wire provenance: compiler/testdata/static-catalog.json is reduced from
// Puppet's published static catalog example, not a live capture. Source-array
// behavior follows lib/puppet/type/file/source.rb. Existing repository notes
// record live OpenVox file_content HTTP 200/404 and directory HTTP 500 checks on
// 2026-08-25/28; Phase 2 tests themselves use local TLS fixtures. Deployment
// conformance remains a separately recorded Phase 5 validation.
package filecontent
