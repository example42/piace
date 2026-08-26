# PIACE — Puppet Impact Assessment & Change Explorer

A dependency-free, CGO-free Go CLI for CI. For each target node it compares
the **baseline catalog** (PuppetDB's latest, or a local snapshot) against a
**candidate catalog** compiled by an existing Puppet Server or OpenVox
**compiler** for the environment CI just deployed, then reports per-node
differences, a cross-node aggregate view, and an optional PuppetDB-backed
estimate of a changed resource's wider stored-catalog footprint.

PIACE is a client of PuppetDB and a compiler. It does not compile Puppet code
locally, embed a Puppet runtime, run agents, or write anything to PuppetDB.

Terminology used throughout the code and reports is fixed in
[CONTEXT.md](CONTEXT.md).

## Status

Spec-driven build against [`.kiro/specs/piace/`](.kiro/specs/piace/)
(requirements → design → tasks). Tasks 1–11 are complete; task 12 (acceptance
validation) is complete except for two confirmations that need real
infrastructure:

- **PuppetDB impact endpoints** — that design §8's PQL text is accepted at the
  root `/pdb/query/v4`, and that `limit`/`order_by` are honoured there. If
  `order_by` is not honoured, a *truncated* impact sample is not reproducible.
- **The Puppet `Sensitive` wire shape** — `{"__ptype":"Sensitive","__pvalue":…}`
  is derived from Puppet's Ruby serializer source, not a captured response. The
  test suite serves that shape, so it proves PIACE redacts what it *expects*; a
  compiler emitting a different encoding would pass the suite with the value
  unredacted.

Both are recorded as skipped tests carrying their confirmation procedures
(`cmd/piace/acceptance_assumptions_test.go`).

## Build

Go 1.22+; no other build or runtime dependency.

```sh
go build -o piace ./cmd/piace
go test ./...
```

Release artifacts and their checksum/signature workflow:
[docs/release.md](docs/release.md), `scripts/build-release.sh`.

### Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on every pull
request and on every push to `main`:

- **test** — `gofmt`, `go vet`, `go build`, and `go test -race -count=1` on
  Linux against the Go version `go.mod` declares and against current stable,
  and on macOS against current stable. Both platforms are covered because
  snapshot writes (atomic rename, `fsync`, `0600`) and the release script's
  `sha256sum`/`shasum` branch are where the two diverge.
- **build** — gated on `test`. Cross-compiles the full supported platform
  matrix, verifies the generated `SHA256SUMS` manifest the same way
  [docs/release.md](docs/release.md) tells a consumer to, and confirms the
  Linux binaries are statically linked. It runs on pull requests too, so a
  broken release script surfaces in review rather than at release time;
  artifacts are uploaded only for `main`.

## Usage

```
piace compare --targets TARGETS.yaml --services SERVICES.yaml \
  [--text-out PATH] [--json-out PATH] [--html-out PATH] [--impact-nodes]

piace capture facts   --targets TARGETS.yaml --services SERVICES.yaml [--replace]

piace capture catalog --targets TARGETS.yaml --services SERVICES.yaml \
  --environment ENVIRONMENT [--replace]
```

Omitting `--text-out` writes the text report to stdout; JSON and HTML are
produced only when explicitly requested. All three render from one redacted
result document, so they cannot disagree.

They do not all show the same amount of it. JSON and HTML are complete; only
the text report omits anything.

The HTML report keeps everything and collapses it. Resource changes are open;
dependency-graph edges, an estimate's PQL, request options and full node list,
the exclusion detail and the provenance block all sit in closed sections you
expand. Nothing is capped — a closed section already keeps a thousand certnames
out of the way without dropping a name — and the page embeds the canonical JSON
at the bottom as well. It is one self-contained file with a light background, no
webfonts and no images: `file://` is all it needs.

The text report is the one that summarizes, because a CI log is a linear read
with nothing to expand. It omits edge changes — a consequence of the resource
changes, and routinely more numerous than them — and each estimate's PQL and
request options, and it names an estimate's first few certnames and counts the
rest. `--impact-nodes` names all of them, up to the configured `result_limit`;
it does not affect the HTML report, which never capped them.

A target whose *only* differences are edges is still reported as changed: HTML
shows the edges, and the text report prints a count in place of the list.
Shortening a reading path must never make a run that exits non-zero read as if
nothing changed.

`capture catalog --environment ENV` requests the catalog for `ENV` — typically
the production/default environment, captured after merge, so development-branch
runs baseline against a frozen catalog rather than a later one from another
environment.

### Debugging a service request

Every subcommand accepts two options for inspecting what PIACE actually sent
and received. They are separate because they sit on opposite sides of the
redaction boundary in [Output and secrecy](#output-and-secrecy).

```
--debug                print one line per compiler/PuppetDB request to stderr
--debug-dump-dir DIR   additionally write raw request/response bodies to DIR
```

`--debug` prints metadata only — method, URL, status, duration, body sizes,
content type, and the response body's top-level JSON *member names*:

```
piace capture catalog: debug #002 POST https://compiler.example.test:8140/puppet/v4/catalog   -> 200 in 1.069s (request 24580 B, response 18362 B, content-type application/json,   body object, top-level keys: catalog)
```

Those top-level keys are the fastest way to spot a wire-shape mismatch between
PIACE and a compiler or PuppetDB version, and they contain no catalog values,
so the output is safe for a CI log.

`--debug-dump-dir` writes the verbatim request and response bodies to `0600`
files in a `0700` directory, never to stdout or stderr. Those bodies are
**unredacted**: they can contain Puppet `Sensitive` values and managed file
content. Use it on a workstation, not in CI, and delete the directory
afterwards.

## Configuration

Two files, deliberately separate: the reviewable selection/policy file, and the
endpoint/mTLS file that does not belong in a review diff.

### `targets.yaml`

```yaml
version: 1

defaults:
  candidate:
    environment: feature-123
    catalog_api: v4              # v3 | v4 — v4 unless the compiler lacks it
    allow_v3_fallback: false     # valid only with v4; opt-in, never implicit
  facts:
    source: puppetdb             # puppetdb | file
  baseline:
    source: file                 # puppetdb | file
    environment: production
    file: snapshots/catalogs/{certname}.json
  exclude:
    - type: File
      title: "/var/cache/*"      # exact type, case-sensitive path.Match glob
  redact:
    - type: File
      parameter: content
  impact_estimate:
    enabled: true
    timeout: 10s
    result_limit: 1000
  fail_on_diff: true

targets:
  - certname: web-01.example.test
    exclude:
      - type: File
        title: "/var/lib/app/cache/*"
```

Per-target scalars override the defaults. `exclude` and `redact` are
**append-only**: global rules are prepended to per-target ones, never replaced.
Relative snapshot paths resolve against the target file's directory;
`{certname}` may appear only as a whole path component.

### `services.yaml`

```yaml
version: 1
compiler:
  endpoint: https://compiler.example.test:8140
  ca_bundle:   /etc/piace/ca.pem
  client_cert: /etc/piace/catalog-reader.pem
  private_key: /etc/piace/catalog-reader.key
puppetdb:
  endpoint: https://puppetdb.example.test:8081
  ca_bundle:   /etc/piace/ca.pem
  client_cert: /etc/piace/catalog-reader.pem
  private_key: /etc/piace/catalog-reader.key
```

The two sections load independently, so using one identity for both is a
deliberate choice rather than a default. Only `https` is accepted; inline keys,
bearer tokens, and insecure TLS are rejected. **Use absolute paths** — TLS paths
resolve against the process working directory, not against `services.yaml`
(unlike snapshot paths, which resolve against the target file).

The compiler identity is a dedicated **catalog-reader certificate** whose
`auth.conf` rule grants it catalog reads for target certnames other than its
own.

### Authorizing the catalog-reader certificate

A stock compiler lets nobody use the v4 endpoint, so PIACE gets HTTP 403 until
one rule in `/etc/puppetlabs/puppetserver/conf.d/auth.conf` names the
catalog-reader certificate's **subject CN** — not the filename in
`services.yaml`. Edit the stock rule in place rather than appending a new one:
`name` and `sort-order` identify a rule, and a duplicate is a configuration
error.

```hocon
        {
            # Stock ships this rule as `deny: "*"`. Replace that deny with
            # an allow list; do not leave both in place.
            match-request: {
                path: "^/puppet/v4/catalog/?$"
                type: regex
                method: post
            }
            allow: [ "catalog-reader" ]
            sort-order: 500
            name: "puppetlabs v4 catalog for services"
        },
```

That is the whole requirement for a v4 setup. The v3 rule below is needed
**only** if you have opted into `catalog_api: v3` or `allow_v3_fallback: true`
— see [Use `catalog_api: v4`](#use-catalog_api-v4) for why that is a degraded
path:

```hocon
        {
            # Allow nodes to retrieve their own catalog, and the
            # catalog-reader certificate to retrieve anyone's.
            match-request: {
                path: "^/puppet/v3/catalog/([^/]+)$"
                type: regex
                method: [get, post]
            }
            allow: [ "$1", "catalog-reader" ]
            sort-order: 500
            name: "puppetlabs v3 catalog from agents"
        },
```

`$1` is the certname captured from the request path, and it keeps working
alongside a second entry — ordinary agents still fetch their own catalogs.
Adding a CN beside it grants that certificate *every* target's catalog, which
is the point of a dedicated identity and the reason it should be a certificate
used for nothing else. It is also exactly what makes `$trusted` in a v3 catalog
potentially reflect the reader rather than the target.

Reload the compiler after editing (`systemctl reload puppetserver`). No rule
change is needed for managed-File content evidence: the stock
`"puppetlabs file"` rule already covers `/puppet/v3/file_content/`, which a
catalog-reader certificate can therefore use for any target's files.

PuppetDB is authorized separately, by its own certificate allowlist or by
accepting any certificate signed by the CA, depending on how the installation
is configured.

## Exit codes

| Code | Outcome | Meaning |
| --- | --- | --- |
| `0` | `clean` / `differences_allowed` | Everything compared; no differences, or all allowed by policy |
| `10` | `policy_disallowed_difference` | A `fail_on_diff` target had a non-excluded difference |
| `20` | `compilation_failure` | A candidate request was rejected, or its identity/environment did not verify |
| `30` | `operational_error` | Config, TLS, retrieval, snapshot, normalization, content-verification, or enabled-impact-estimate failure |

Precedence is `30 > 20 > 10 > differences_allowed > clean`. A run is never
`clean` while any target has an unreported retrieval, compilation, or
normalization failure — an indeterminate File-content comparison included.

## Use `catalog_api: v4`

v4 is the supported path, and the reason is not trusted facts alone. Every v4
request PIACE makes carries `persistence: {facts: false, catalog: false}`: the
compiler returns the candidate catalog and writes nothing. The target's stored
factset and catalog stay exactly as its last real agent run left them.

The v3 catalog endpoint has no equivalent control, and the consequence is not
cosmetic. On every v3 request the compiler saves the facts you submitted —
rewriting the target's stored factset and its `facts_environment` to the
candidate environment — and stores the compiled catalog through its PuppetDB
catalog cache terminus, rewriting the target's stored catalog,
`catalog_environment`, and `transaction_uuid`. That is a property of the
endpoint. Nothing PIACE sends can turn it off.

So with `catalog_api: v3`:

- **`baseline.source: puppetdb` cannot work.** PIACE reads the baseline, then
  compiles the candidate, and the candidate compilation overwrites the baseline
  — for the next target in the same run, and for every later run. The symptom
  is a baseline-environment mismatch that names the candidate environment. A v3
  target needs `baseline.source: file`, captured while the baseline
  environment's catalog was the stored one.
- **A file baseline does not make v3 harmless.** It stops PIACE from destroying
  its own input. It does not stop the compiler from writing the candidate facts
  and catalog into PuppetDB, where anything reading PuppetDB state — reporting,
  exported resources, inventory, classification keyed on `facts_environment` —
  sees candidate values until the target's next agent run.

Puppet Server and OpenVox behave identically here: both serve v3 and v4, and
both honour the v4 `persistence` field. `catalog_api` is the only thing that
decides.

## Two things the reports say, and mean literally

**The v3 warning.** With `catalog_api: v3` — or any permitted v4→v3 fallback —
`$trusted` in the compiled catalog can reflect the catalog-reader certificate
rather than the target. The warning is non-suppressible and appears in all
three formats. It does not change the exit status; it makes the trust semantics
reviewable. v4 sends the target's own trusted facts, and fails compilation
rather than inventing them when neither a validated input nor a configured
compiler lookup is available.

**The impact estimate.** It reports only that a node's latest *stored* catalog
contains the exact `Type[title]`. It is not proof those nodes would change, and
PIACE never compiles them. Queries are bounded by `timeout` and `result_limit`;
an over-limit result is marked truncated and reported as *more than* the limit,
never as an exact population, with a sorted certname sample. Because an enabled
estimate is requested analysis, a failed one is an operational error.

The compact per-estimate line depends on the section header for its meaning:
`Service[nginx]: 9 nodes: …` is not a claim about those nodes, because the
fixed note above it says once, for the whole section, what a listed certname
does and does not mean.

## Output and secrecy

The JSON report is `schema_version`-tagged and canonically encoded, so identical
input catalogs and configuration produce byte-identical artifacts. The HTML
report is a single self-contained file: inline CSS, no JavaScript, no external
assets or URLs — it opens over `file://`, and it visibly marks retrieval
failure, compilation failure, the v3 warning, exclusions, and the final outcome.

Redaction happens after semantic comparison and exclusion but before
serialization, so masking never turns a real difference into a non-difference,
and two distinct sensitive values never merge into one aggregate group. Puppet
`Sensitive` wrappers are detected recursively; configured selectors redact by
exact type and parameter name. No report carries credentials, private key
material, managed file content bytes, or unredacted sensitive values — asserted
end to end over all three formats in `cmd/piace/acceptance_disclosure_test.go`.

That boundary holds for `--debug` too, which reports only request metadata and
response top-level member names. `--debug-dump-dir` is the one deliberate
exception: an operator-requested dump of verbatim bodies to `0600` files, never
to a console or a report.

## Snapshots

`piace capture` writes PIACE envelopes, not bare Puppet payloads: format
version, target identity, source, capture timestamp, SHA-256 payload checksum,
and — for catalogs — requested environment, compiler API version, and input
factset identity. Files are written atomically at `0600` and are never
overwritten without `--replace`. On reuse, version, kind, target, checksum,
required metadata, and baseline environment are all validated before the
catalog is diffed.

## Layout

```
cmd/piace/          CLI entry point; the acceptance suite (task 12)
internal/config/    Target and service file schemas
internal/config/resolve/  Defaults, overrides, validation, safe provenance
internal/transport/ Hardened, independent mTLS clients; redaction
internal/puppetdb/  Fact and baseline-catalog sources (PuppetDB and file)
internal/snapshot/  Envelopes, canonical JSON, checksums, atomic writes
internal/compiler/  v3/v4 candidate requests, trusted-fact and fallback policy
internal/normalize/ Catalogs into the deterministic semantic graph
internal/filecontent/ File-content evidence without content disclosure
internal/diff/      Node diffing, exclusions, redaction (fixed ordering)
internal/aggregate/ Cross-target grouping
internal/impact/    Bounded PQL estimates
internal/compare/   The compare pipeline
internal/report/    Text, JSON, and HTML renderers
internal/model/     Shared result document and the outcome reducer
```

Each package's `doc.go` records the decisions it owns and the assumptions it
still rests on.

## Further reading

- [CONTEXT.md](CONTEXT.md) — domain language
- [.kiro/specs/piace/](.kiro/specs/piace/) — requirements, design, tasks
- [docs/adr/0001-request-candidate-catalogs-from-an-existing-compiler.md](docs/adr/0001-request-candidate-catalogs-from-an-existing-compiler.md)
- [docs/research/trusted-facts-in-existing-catalog-diff-tools.md](docs/research/trusted-facts-in-existing-catalog-diff-tools.md)
- [docs/release.md](docs/release.md)
