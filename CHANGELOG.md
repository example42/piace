# Changelog

All notable changes to PIACE are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.1] - 2026-08-30

### Added

- **A published container image**: cutting a `v*` tag now also pushes
  `example42/piace:<version>` to Docker Hub, as a `linux/amd64` +
  `linux/arm64` manifest list; `:latest` moves with every non-prerelease. The
  image is the release binary the workflow already verified, copied onto
  `distroless/static`, so what a `docker pull` runs is the bytes `SHA256SUMS`
  certifies. It runs as a non-root user out of `/work`: see the README's
  Install section for the mount and `--user` flags.
- **[docs/ci.md](docs/ci.md) and [examples/ci/](examples/ci/)**: copy-ready
  GitHub Actions and GitLab CI pipelines, where each configuration file belongs
  in a control repository, and what changes when the runner is one you do not
  control. Two jobs by design, so the catalog-reader identity and the inference
  token are never held by the same job.

## [0.2.0] - 2026-08-29

### Added

- **`piace explain`** — an optional, advisory **change assessment** of a stored
  result document. It is a second, independent step: it reads a JSON report
  `compare` already wrote, asks a configured **inference service** to judge the
  aggregate groups in it, and writes a separately versioned assessment artifact
  (`ai_schema_version: 1`) plus a re-rendered HTML report whose assessment
  section sits *below* the deterministic outcome.
- **`services.yaml` gains an `inference:` section** — endpoint (https only),
  model, `token_env` or `token_file` (never an inline token), `timeout`,
  `max_tokens`, `max_groups`, `pseudonymize`, `structured_output`, and
  `policy_notes_file`. It loads independently: a services file containing
  nothing but this section is valid for `explain`, so an assessment needs no
  Puppet infrastructure named at all.
- **`--change CHANGE.yaml`** — a caller-supplied **change context** describing
  the repository change under test: refs, commit subjects, changed paths, and a
  capped title and description. PIACE reads the file and never invokes git;
  `scripts/change-context.sh` generates one for the common CI case. Its free
  text is transmitted inside an explicit fence labelled as untrusted data.
- **Pseudonymized identities** — certnames in an outbound inference request are
  replaced by stable per-run substitutes, and the compiler and PuppetDB
  authorities are absent from it entirely. Resource identities pass through
  untouched: `File[/etc/sudoers]` is the signal. A pseudonym never appears in an
  assessment or any report. `pseudonymize: false` sends real certnames and is
  documented as the deliberate loosening it is.
- **`--fail-on-inference-error`** — exit 30 when the assessment could not be
  produced. Without it a failed assessment is recorded in the artifact with
  every risk indication `unknown`, and the command still exits 0.
- **`report.DecodeJSON`** — a result document can now be read back into the
  model it was rendered from, strictly: unknown fields and trailing content are
  refused, and numbers keep their exact decimal digits.

### Unchanged

- **`piace compare` is untouched by this feature.** Its result document stays
  `schema_version: 1`, its reports are byte-identical for identical inputs, its
  exit codes are the same, and it contacts no inference service. A report
  rendered without an assessment is byte-for-byte the artifact v0.1.0 wrote,
  asserted against a golden captured before the feature existed. `explain`
  contacts no compiler and no PuppetDB, asserted by failing the test if either
  configured endpoint is reached.

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

[Unreleased]: https://github.com/example42/piace/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/example42/piace/releases/tag/v0.2.1
[0.2.0]: https://github.com/example42/piace/releases/tag/v0.2.0
[0.1.0]: https://github.com/example42/piace/releases/tag/v0.1.0
