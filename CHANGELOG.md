# Changelog

All notable changes to PIACE are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-08-28

First release: the whole tool, so this entry describes what it does rather
than what changed.

### Comparison

- `piace compare` compares each target's **baseline catalog** (PuppetDB's
  latest, or a local snapshot) against a **candidate catalog** compiled by an
  existing Puppet Server or OpenVox compiler, and reports a per-node diff, a
  cross-node aggregate view, and an optional PuppetDB-backed impact estimate.
- Deterministic semantic normalization: exact `Type[title]` identities with no
  case folding, a canonical value domain with exact-decimal numbers, and
  resources and edges sorted before comparison and serialization.
- Generated catalog noise is excluded before comparison — tags, source
  file/line, `exported`, `aliases`, and the `alias` parameter the PuppetDB
  terminus injects into a stored catalog.
- Four change kinds: resource added, resource removed, parameter changed, and
  dependency-graph edge added/removed. A target whose only differences are
  edges is still reported as changed.
- Managed `File` content evidence without content disclosure: inline `content`
  digests, an authoritative compiled checksum, compiler retrieval of a
  `source` reference, and the explicit `reference_changed` /
  `content_indeterminate` states when bytes cannot be compared. No managed
  file bytes reach any output, log, PQL query, or aggregate state.
- Configurable per-target exclusions (`Type[title]`, case-sensitive glob) and
  redaction selectors, applied after equality so redaction cannot alter a
  comparison.
- Impact estimates report only that a node's latest *stored* catalog contains
  the exact `Type[title]`, bounded by a configured timeout and result limit,
  and labelled as such in every format.

### Catalog APIs

- `catalog_api: v4` is the supported path. Every request carries
  `persistence: {facts: false, catalog: false}`, so a comparison leaves the
  target's stored factset and catalog untouched, and sends the target's own
  trusted facts.
- `catalog_api: v3` is a degraded path, with a non-suppressible trusted-fact
  warning in every output format, and an opt-in, never implicit, v4→v3
  fallback.

### Snapshots

- `piace capture facts` and `piace capture catalog` write PIACE envelopes —
  format version, target identity, source, capture timestamp, SHA-256 payload
  checksum, and a catalog's requested environment, compiler API version and
  input factset identity — atomically, at `0600`, never overwriting without
  `--replace`. Every field is validated on reuse.

### Reports

- Three formats from one redacted result document, so they cannot disagree:
  **text** for a CI log (the only format that omits anything), **JSON** as the
  complete versioned record, and **HTML** as a complete, self-contained page
  with no JavaScript, webfonts, or images.
- The HTML report is an index of the run: every list of rows sits in a closed
  disclosure whose heading counts what it holds. Failures, the v3 warning and
  the outcome badges never collapse.

### Outcomes

- Exit codes `0` (clean / differences allowed), `10`
  (policy-disallowed difference), `20` (compilation failure) and `30`
  (operational error), with documented precedence. A run is never `clean`
  while any target has an unreported retrieval, compilation, normalization or
  content-verification failure.

### Transport and secrecy

- Independent hardened mTLS clients per service, with bounded timeouts,
  response size limits, and no redirect following.
- Private keys, certificate material and authorization headers never reach a
  diagnostic, report or log.
- `--debug` prints request metadata only (safe for CI); `--debug-dump-dir`
  writes verbatim, unredacted bodies to `0600` files in a `0700` directory,
  never to stdout or stderr.

### Build and release

- Dependency-free and CGO-free; Go 1.22+ is the only build requirement.
- CI runs `gofmt`, `go vet`, `go build` and `go test -race` on Linux and
  macOS, cross-compiles the full platform matrix on every pull request, and
  publishes a GitHub Release with `SHA256SUMS` from a `v*` tag. The detached
  signature over the manifest is attached by hand afterwards — CI holds no
  signing key, and the release notes say so.

### Known limitations

- **`catalog_api: v3` with `baseline.source: puppetdb` is not rejected.**
  requirements.md 1.8 requires a v3 target to use a file baseline, but config
  validation does not yet enforce it. The configuration loads and the run
  overwrites the baseline it just read; the symptom on the next run is a
  baseline-environment mismatch naming the candidate environment. Set
  `baseline.source: file` yourself. See the README's
  "If you must use v3, compare against a captured file".
- **Two PuppetDB impact-endpoint behaviours are unconfirmed against a
  deployment**: that design §8's PQL text is accepted at the root
  `/pdb/query/v4`, and that `limit`/`order_by` are honoured there. If
  `order_by` is not honoured, a *truncated* impact sample is not reproducible.
- **The Puppet `Sensitive` wire shape** (`{"__ptype":"Sensitive","__pvalue":…}`)
  is derived from Puppet's Ruby serializer source rather than a captured
  response. A compiler emitting a different encoding would pass the acceptance
  suite with the value unredacted.

The last two are recorded as skipped tests carrying their confirmation
procedures in `cmd/piace/acceptance_assumptions_test.go`.

[Unreleased]: https://github.com/example42/piace/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/example42/piace/releases/tag/v0.1.0
