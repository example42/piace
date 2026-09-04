# Changelog

All notable changes to PIACE are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.4.0] - 2026-09-04

### Changed

- The impact-estimate section is labelled **Potential impact estimate** with a
  capital P in every output format.
- A `differences_allowed` run is coloured green in the HTML report. It shares
  exit `0` with `clean`, and yellow is reserved for the advisory medium-risk
  indication rather than for a run that succeeded.

### Fixed

- **The task prompt states the response shape unconditionally.** Anthropic's
  OpenAI-compatible endpoint documents itself as ignoring `response_format`
  rather than rejecting it, so a request relying on structured output alone
  asked for nothing. `Interpret` also unwraps a Markdown code fence around a
  whole reply, recording a warning, rather than discarding a complete
  assessment over its packaging.
- **An oversized inference response is reported as oversized** rather than as
  "not a chat completion", which sent an operator looking at the wrong thing.

### Security

- **A File `source` value can no longer steer the file-content request.** A
  `source` reached the compiler's `file_content` endpoint as an unescaped path
  concatenation, so `puppet:///../../pdb/query/v4/catalogs/<node>` was
  attacker-shaped input, compiled from the change under review, aiming an
  mTLS-authenticated GET at another path on a host PIACE is already authorized
  against. `parsePuppetSourceURI` now refuses an empty, `.` or `..` segment and
  a NUL byte.
- **The inference client has a redirect policy.** `net/http` keeps an
  `Authorization` header across a redirect to the same host, so an endpoint
  answering `302 Location: http://<same host>` received the bearer token in
  cleartext. Redirects that leave `https` or change authority are now refused,
  matching what `internal/transport` already enforced for the credential-free
  clients.
- **The text report escapes control characters** in every interpolated value.
  A resource title or diagnostic message carrying `ESC[2K\r` followed by a
  forged outcome line could make a run that exits `30` read as clean in a CI
  log. C0, DEL and C1 are replaced with a printable escape; the JSON and HTML
  reports are unchanged, since JSON escaping already makes a control character
  inert and `html/template` covers the HTML.
- **Refs reaching git are validated.** `change-context` refuses a ref beginning
  with `-` before any git command runs and passes `--end-of-options`, so a base
  or head ref chosen by whoever opened a fork pull request cannot become an
  option to `git diff`.
- **`--debug-dump-dir` no longer writes through a symlink** already sitting at
  a dump path, and applies `0600` to the file it writes rather than only to one
  it creates.

## [0.3.0] - 2026-09-01

### Added

- **One path rule.** Every relative path named in a config file now resolves
  against the directory of the file that names it. `facts.file` and
  `baseline.file` already did; the TLS paths, `token_file` and
  `policy_notes_file` now do too. Nothing resolves against the process working
  directory, so moving a config file takes its paths with it.
- **`ca_bundle_env`, `client_cert_env`, `private_key_env`**: each service
  section may name an environment variable holding a credential's absolute
  path, mirroring the `inference:` section's existing `token_env`. Naming both
  forms of one credential is an error rather than a precedence rule nobody
  remembers. Together with the path rule this removes the services *template*
  entirely: a committed services file is now read in place, unmodified, by a CI
  job whose credential directory did not exist when the file was written. No
  `sed`, no per-job render, and no tracked file a job rewrites.
- **`piace change-context`**: writes the change context file
  `explain --change` reads, by exec'ing git. Every untrusted input is taken by
  variable name (`--title-env`, `--base-ref-env`) or file path
  (`--title-file`), never on the command line, because every CI system
  substitutes into script text before a shell runs: GitHub's `${{ }}`, Azure's
  `$( ... )`. There is deliberately no `--title` or `--description` flag. It
  is optional, and `explain --change` still reads a file produced by any means,
  so a repository under a different VCS is unaffected.
- **Keyless signing and provenance**: the release job signs `SHA256SUMS` with
  cosign, using the workflow's own OIDC identity, and attaches
  `SHA256SUMS.sigstore.json`. The binaries carry a GitHub build provenance
  attestation, and both image manifests are signed by digest and attested.
  Verification is now actionable from the moment a release is published; the
  release notes carry the exact `cosign verify-blob` command. The OpenPGP
  signature remains available for sites that require one, as an extra rather
  than the verification path.
- **`ghcr.io/example42/piace`**: the image is mirrored to GHCR alongside Docker
  Hub, from the same build. An anonymous Docker Hub pull from a shared CI
  runner IP is exactly what Docker Hub rate-limits, and a pipeline failing for
  that reason fails for a reason unrelated to this project.
- **`piace compare --candidate-environment ENVIRONMENT`**: compiles every
  target's candidate catalog from ENVIRONMENT, overriding
  `candidate.environment` in both the `defaults:` block and any per-target
  `candidate:` block. The environment CI deployed is the one per-pipeline
  value in an otherwise static policy file; passing it at the invocation is
  what lets a pipeline stop rewriting its own committed target file between
  checkout and run. The override is applied to the target file before
  resolution, so it is validated and recorded in a report's provenance
  exactly as a file-supplied value, and with it the target file may omit
  `candidate.environment` entirely. `capture catalog --environment` is
  unchanged and unrelated: it names the environment to snapshot, not the
  candidate environment under test.
- **`piace explain --debug` / `--debug-dump-dir DIR`**: the two observation
  options `compare` and `capture` already accept now work on `explain` too, so
  a rejected inference request can be diagnosed without guessing. `--debug`
  prints one stderr line for the inference round trip: method, URL, HTTP
  status, duration, request and response body sizes, and the response body's
  top-level JSON member names. `--debug-dump-dir` additionally writes the raw
  request and response bodies to `0600` files in DIR: the request-body dump is
  the exact catalog-derived payload PIACE sent, and the response-body dump of a
  4xx is where a provider names the field it rejected. Neither the returned
  error nor any log line ever carries a response-body value, and the bearer
  token is a header so it reaches no dump file.
- **`services.inference.token_limit_param`**: selects the request field that
  carries the output-token bound, `max_tokens` (the default) or
  `max_completion_tokens`. OpenAI's GPT-5 family rejects `max_tokens` outright
  and requires `max_completion_tokens`; OpenAI-compatible servers other than
  current OpenAI (Ollama, vLLM, llama.cpp) only understand `max_tokens`. The
  value in `max_tokens` is unchanged; only the wire field name differs.
- **`services.inference.temperature`**: optional sampling temperature, sent
  only when set.

### Changed

- **One services file.** The `compiler:`, `puppetdb:` and `inference:` sections
  already loaded independently, so the two-file split was policy rather than
  necessity. The examples and the CI documentation now show one committed
  `services.yaml` for the whole pipeline. What separates a comparison job from
  an assessment job is which credentials each is granted, not which file it
  reads.
- **Documentation rewritten to describe the current tool only.** The README is
  a third shorter, with the `explain` reference moved to
  [docs/change-assessment.md](docs/change-assessment.md); `docs/ci.md` loses
  the section explaining why the services file had to be a template, because it
  no longer does.
- **Code comments say what the code does** instead of citing a build-time
  specification. 578 references to `.kiro/specs/piace/` across 109 files are
  gone, along with the specification itself and the ADR directory whose
  rationale now lives in [CONTEXT.md](CONTEXT.md#design).


- **`piace explain`**: no sampling parameter is sent unless
  `services.inference.temperature` is configured. PIACE previously hard-coded
  `temperature: 0` and `seed: 0` into every request; Claude 4+ and OpenAI's
  GPT-5 family reject any non-default `temperature` with a 400, and `seed`
  never left a mark (Anthropic's compat endpoint ignores it, OpenAI deprecated
  it, reasoning models reject it), so the `seed` field is gone. Pinning them
  never made a model-generated assessment reproducible in the first place: a
  provider-side model revision still moves the bytes.
- **`piace explain`**: dependency-graph edge groups are no longer sent to the
  inference service. An edge change is a consequence of the resource changes
  around it, carries no before/after pair to reason about, and a run's edges
  routinely outnumber its resource changes, so sending them spent the group
  budget and returned a wall of `unknown` risk indications. The deterministic
  report still lists every edge group in its own section; only the change
  assessment skips them, and `groups_total` now counts what was eligible for
  assessment.

### Removed

- **`scripts/change-context.sh`**, replaced by `piace change-context`. Its job
  was to emit YAML with `printf` and leave the caller to append a title and
  description by hand, which is the step that has to be got right on three CI
  platforms and is arbitrary code execution on the runner when it is not.
- **`examples/ci/services.yaml.tmpl`**, replaced by
  [`examples/ci/services.yaml`](examples/ci/services.yaml). Nothing renders it.
- **The three `-docker` CI examples.** They demonstrated socket-mount
  gymnastics for a path the same document recommends against; `docs/ci.md` now
  covers `docker run` in one section, for a Kubernetes Job or a workstation.

### Fixed

- **HTML report**: risk-indication rows in the change assessment's "Group risk
  indications" list put a full risk badge in a grid track sized for a
  one-character change sign, so the badge overlapped the group identity and was
  stretched to the row height. The row now has its own track width.
- **[docs/ci.md](docs/ci.md) and [examples/ci/](examples/ci/)**: the shipped
  pipelines never bound `candidate.environment` to the environment CI had just
  deployed, and never added the merge request title and description to the
  change context, so a copied pipeline compared against a hardcoded environment
  and assessed a change without the sentence that says what it is for. Both are
  now rendered by the pipelines, the first through `--candidate-environment`.
- **[examples/ci/github-actions.yml](examples/ci/github-actions.yml)**: the
  pull request base ref, title and body now reach the job through `env:`
  instead of `${{ }}` inside a `run:` block, where they were substituted into
  the script before a shell saw them.

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

- **`piace explain`**: an optional, advisory **change assessment** of a stored
  result document. It is a second, independent step: it reads a JSON report
  `compare` already wrote, asks a configured **inference service** to judge the
  aggregate groups in it, and writes a separately versioned assessment artifact
  (`ai_schema_version: 1`) plus a re-rendered HTML report whose assessment
  section sits *below* the deterministic outcome.
- **`services.yaml` gains an `inference:` section**: endpoint (https only),
  model, `token_env` or `token_file` (never an inline token), `timeout`,
  `max_tokens`, `max_groups`, `pseudonymize`, `structured_output`, and
  `policy_notes_file`. It loads independently: a services file containing
  nothing but this section is valid for `explain`, so an assessment needs no
  Puppet infrastructure named at all.
- **`--change CHANGE.yaml`**: a caller-supplied **change context** describing
  the repository change under test: refs, commit subjects, changed paths, and a
  capped title and description. PIACE reads the file and never invokes git;
  `scripts/change-context.sh` generates one for the common CI case. Its free
  text is transmitted inside an explicit fence labelled as untrusted data.
- **Pseudonymized identities**: certnames in an outbound inference request are
  replaced by stable per-run substitutes, and the compiler and PuppetDB
  authorities are absent from it entirely. Resource identities pass through
  untouched: `File[/etc/sudoers]` is the signal. A pseudonym never appears in an
  assessment or any report. `pseudonymize: false` sends real certnames and is
  documented as the deliberate loosening it is.
- **`--fail-on-inference-error`**: exit 30 when the assessment could not be
  produced. Without it a failed assessment is recorded in the artifact with
  every risk indication `unknown`, and the command still exits 0.
- **`report.DecodeJSON`**: a result document can now be read back into the
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
- Generated catalog noise is excluded before comparison: tags, source
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

- `piace capture facts` and `piace capture catalog` write PIACE envelopes:
  format version, target identity, source, capture timestamp, SHA-256 payload
  checksum, and a catalog's requested environment, compiler API version and
  input factset identity, written atomically at `0600` and never overwritten without
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
  signature over the manifest is attached by hand afterwards, since CI holds no
  signing key, and the release notes say so.

### Known limitations

- **`catalog_api: v3` with `baseline.source: puppetdb` is not rejected.**
  A v3 target is required to use a file baseline, but config validation does
  not yet enforce it. The configuration loads and the run overwrites the
  baseline it just read; the symptom on the next run is a
  baseline-environment mismatch naming the candidate environment. Set
  `baseline.source: file` yourself. See the README's
  "If you must use v3, compare against a captured file".
- **Two PuppetDB impact-endpoint behaviours are unconfirmed against a
  deployment**: that the impact PQL text is accepted at the root
  `/pdb/query/v4`, and that `limit`/`order_by` are honoured there. If
  `order_by` is not honoured, a *truncated* impact sample is not reproducible.
- **The Puppet `Sensitive` wire shape** (`{"__ptype":"Sensitive","__pvalue":…}`)
  is derived from Puppet's Ruby serializer source rather than a captured
  response. A compiler emitting a different encoding would pass the acceptance
  suite with the value unredacted.

The last two are recorded as skipped tests carrying their confirmation
procedures in `cmd/piace/acceptance_assumptions_test.go`.

[0.4.0]: https://github.com/example42/piace/releases/tag/v0.4.0
[0.3.0]: https://github.com/example42/piace/releases/tag/v0.3.0
[0.2.1]: https://github.com/example42/piace/releases/tag/v0.2.1
[0.2.0]: https://github.com/example42/piace/releases/tag/v0.2.0
[0.1.0]: https://github.com/example42/piace/releases/tag/v0.1.0
