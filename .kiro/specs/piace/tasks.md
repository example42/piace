# Implementation Plan: PIACE

## Overview

Tasks are ordered by dependency. A task is complete only when its stated
acceptance conditions are met and its changes preserve the design's security
and deterministic-output invariants. Requirement references point to
`requirements.md`.

## Tasks

- [x] 1. Establish the Go CLI and versioned public contracts
  - Create a CGO-free Go module with `compare`, `capture facts`, and `capture
    catalog` command entry points.
  - Define versioned Go schemas for target files, service configuration,
    snapshots, normalized catalogs, node and aggregate diffs, impact estimates,
    diagnostics, and the shared result document.
  - Implement the stable exit codes: `0` success (including allowed
    differences), `10` policy-disallowed difference, `20` compilation failure,
    and `30` operational error.
  - _Requirements: 4.1-4.4, 8.2, 10.1-10.4, 11.1-11.5, 12.1-12.2_

- [~] 2. Parse and validate target and service configuration before I/O
  - Decode `version: 1` target and service YAML with unknown-field rejection.
  - Resolve defaults, per-target scalar overrides, append-only exclusions and
    redactions, and relative snapshot paths as specified in design section 3.
  - Reject missing values, duplicate certnames, invalid certnames/templates,
    unsupported API/fallback combinations, malformed rules, bad durations and
    limits, unsafe endpoints, and invalid TLS paths before any service call.
  - Preserve resolved configuration provenance for reporting without secrets.
  - _Requirements: 3.3-3.5, 4.1-4.4, 6.1-6.2, 8.8, 9.1/9.5, 10.3_

- [~] 3. Implement hardened, independent mTLS HTTP clients
  - Build separate compiler and PuppetDB transports from the resolved service
    configuration, enforcing HTTPS, configured CA roots, client certificates,
    TLS 1.2+, no redirects to another authority, request deadlines, and bounded
    response sizes.
  - Centralize request/response metadata redaction so private key bytes,
    authorization headers, and raw sensitive catalog values cannot enter logs
    or diagnostics.
  - Map service failures into operational versus compiler failure classes.
  - _Requirements: 3.1-3.5, 10.1/10.5, 12.4_

- [~] 4. Implement PuppetDB fact and baseline-catalog source adapters
  - Retrieve the latest factset and baseline catalog for an explicit certname.
  - Record source, target, environment, producer timestamp, catalog identity or
    hash, and producer in source provenance.
  - Reject a PuppetDB baseline whose environment differs from the resolved
    baseline environment. Do not mutate PuppetDB.
  - _Requirements: 1.1-1.3/1.6, 2.1-2.2, 10.5_


- [~] 5. Add PIACE snapshot envelopes and capture workflows
  - Implement canonical JSON payload serialization and SHA-256 checksums using
    the envelope schema and atomic `0600` writes defined in design section 6.
  - Load and validate file-backed fact and catalog snapshots for version, kind,
    target, checksum, required metadata, and baseline environment.
  - Implement `capture facts` from PuppetDB and `capture catalog` from the
    compiler, including explicit overwrite protection and full capture
    provenance. Ensure captures never mutate PuppetDB.
  - _Requirements: 1.1-1.3, 2.1-2.2, 11.1-11.7_

- [~] 6. Implement the v3/v4 compiler adapter and trusted-fact policy
  - Request candidate catalogs only through the configured compiler, validate
    returned target and candidate environment, and collect compiler provenance.
  - Implement explicit v4 target trusted-fact handling and fail a v4 request
    when neither a validated input nor configured compiler lookup is available.
  - Permit v4-to-v3 fallback only when explicitly enabled and only for a
    verified unsupported-v4 response; emit the non-suppressible service-
    identity trusted-fact warning for every v3 catalog.
  - Reuse this path for catalog snapshot capture; do not claim v4 behavior for
    OpenVox without an operator-selected, documented compatibility contract.
  - _Requirements: 1.4-1.5, 2.3-2.5, 7 compatibility constraints, 11.2-11.5_

- [~] 7. Normalize Puppet catalogs into a deterministic semantic graph
  - Validate catalog resource and edge structures; construct exact
    `Type[title]` identities, sorted resources, canonical parameter values, and
    sorted edge endpoint keys.
  - Drop tags, source file/line fields, and other explicitly non-semantic
    catalog metadata. Treat unknown required shapes as reported normalization
    errors rather than silently discarding them.
  - Keep raw values only in short-lived comparison structures; make every
    serializable semantic representation redaction-ready.
  - _Requirements: 5.1-5.4/5.9, 8.6-8.8, 10.5_

- [~] 8. Implement managed File content evidence without content disclosure
  - Classify `File` differences using inline-content digests, source changes,
    and available compiled checksums.
  - Add the compiler-backed content resolver for cases lacking comparable
    inline content or checksums; retain only digest evidence and reference
    metadata, never the managed bytes in a report or log.
  - Emit a reported indeterminate-content diagnostic when content retrieval or
    comparison cannot establish the required evidence; ensure it cannot be
    mistaken for a clean verified comparison.
  - _Requirements: 5.5-5.8, 8.7, 10.5_

- [~] 9. Build node diffing, exclusions, and redaction boundaries
  - Produce resource additions/removals, canonical parameter changes, edge
    additions/removals, and File-content difference classifications per target.
  - Apply the resolved exact-type/case-sensitive-glob exclusion rules before
    policy evaluation; suppress matching resources and every connected edge;
    record rule identities and suppressed counts.
  - Evaluate Puppet `Sensitive` values and configured redaction selectors at
    the result boundary, replacing values with stable redaction markers in all
    formats while retaining no secret material in logs or aggregate keys.
  - _Requirements: 5.1-5.9, 6.1-6.6, 8.7-8.8, 10.4_

- [~] 10. Build deterministic aggregate diffs and optional impact estimates
  - Group equivalent non-excluded node changes by kind, identity, and raw
    canonical before/after evidence; output each group with sorted certnames
    and links to its node changes.
  - Generate safely escaped exact type/title PQL resource queries, request no
    more than `result_limit + 1`, apply time limits, sort certnames, and retain
    only the deterministic sample and truncation state.
  - Label every result as a potential impact estimate, record the PQL and query
    outcome separately, and never compile returned nodes.
  - _Requirements: 7.1-7.4, 9.1-9.8_

- [~] 11. Implement the shared result model, renderers, and outcome reducer
  - Populate a versioned JSON document with node results, aggregate diff,
    source/configuration provenance, diagnostics, exclusions, redactions, and
    optional impact-estimate states.
  - Render the same data deterministically to concise CI text and a single
    self-contained, safely escaped `file://` HTML artifact with no external
    assets or network requests.
  - Apply outcome precedence: operational error, compilation failure,
    policy-disallowed difference, allowed differences, then clean. Include
    outcome and reason in every format and preserve all target failures.
  - _Requirements: 7.1-7.4, 8.1-8.8, 9.3/9.7, 10.1-10.5_

- [~] 12. Validate release and operational behavior against the accepted matrix
  - Exercise the defined behavior with fixture-driven checks covering PuppetDB
    and snapshot sources; valid and invalid envelopes; v3, v4, and allowed
    fallback; trusted-fact warnings; baseline-environment rejection; exclusions;
    sensitive/redacted values; File evidence states; impact time/limit states;
    partial target failures; all outcome precedences; and byte-identical report
    ordering for identical inputs.
  - Verify a `CGO_ENABLED=0` build has no Puppet/Ruby/Facter or runtime package
    dependency, and document checksum/signature generation and verification for
    the supported release artifacts.
  - Confirm runtime endpoints are restricted to the configured compiler and
    PuppetDB services, and that no report contains credentials, private material,
    managed content bytes, or unredacted sensitive values.
  - _Requirements: 1-12_


## Notes

- The task sequence deliberately validates configuration and source integrity
  before implementation reaches compiler requests or graph diffing.
- Protocol adapters remain the compatibility boundary. Their exact requests and
  responses must be demonstrated with fixtures from the deployed service
  versions before declaring a compiler/PuppetDB combination supported.
- No task authorizes candidate facts or catalogs to be persisted to PuppetDB.

## Task Dependency Graph

```json
{
  "waves": [
    {"wave": 1, "tasks": [1]},
    {"wave": 2, "tasks": [2]},
    {"wave": 3, "tasks": [3]},
    {"wave": 4, "tasks": [4, 5, 6]},
    {"wave": 5, "tasks": [7]},
    {"wave": 6, "tasks": [8]},
    {"wave": 7, "tasks": [9]},
    {"wave": 8, "tasks": [10]},
    {"wave": 9, "tasks": [11]},
    {"wave": 10, "tasks": [12]}
  ]
}
```

```text
1 -> 2 -> 3 -> 4 -> 5
                 \-> 6
4 + 5 + 6 -> 7 -> 8 -> 9 -> 10 -> 11 -> 12
```

Tasks 4, 5, and 6 may proceed in parallel after task 3. Task 7 depends on their
normalized source contracts; later tasks remain ordered because they consume
the shared diff and result models.
