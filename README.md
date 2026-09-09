# PIACE: Puppet Impact Assessment & Change Explorer

A single-binary Go CLI that answers one question in CI: **what would this Puppet
change actually do to my nodes?**

For each target node PIACE compares the **baseline catalog** (PuppetDB's latest,
or a local snapshot) against a **candidate catalog** compiled by your existing
Puppet Server or OpenVox compiler for the environment CI just deployed. It
reports per-node differences, a cross-node aggregate view, and an optional
estimate of how many other nodes' stored catalogs contain a changed resource.

It does not compile Puppet code locally, embed a Puppet runtime, or run agents.
Every PuppetDB request it makes is a read.

> **Server-side side effects depend on one setting.** With `catalog_api: v4`
> (the supported path) nothing is stored: each request carries
> `persistence: {facts: false, catalog: false}`. With `catalog_api: v3` the
> *compiler* rewrites the target's stored factset and catalog as a side effect
> of compiling. Read [Choosing the catalog API](#choosing-the-catalog-api)
> before selecting v3.

---

## Install

Download a release binary, or build it:

```sh
go build -o piace ./cmd/piace
```

Go 1.22+, one dependency. Release artifacts, checksums and signature
verification: [docs/release.md](docs/release.md). Getting it onto a CI runner:
[docs/ci.md](docs/ci.md#getting-the-binary-onto-the-runner).

Or run the published image, which is the same release binary on a distroless
base, from Docker Hub or GHCR. It runs as a non-root user out of `/work`, so
mount your workspace there and pass your own uid; without both, writing a
report into the mount fails with a permission error:

```sh
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --volume "$PWD:/work" \
  ghcr.io/example42/piace:latest \
  compare --targets targets.yaml --services services.yaml --html-out report.html
```

Every path resolves inside the container, so keep them under the mount.

## Quick start

1. **Write `services.yaml`**: where your compiler and PuppetDB are, and the
   mTLS identity to reach them with. Start from
   [`examples/services.yaml`](examples/services.yaml).
2. **Authorize that identity on the compiler**: one `auth.conf` rule, or you
   get HTTP 403. See [Authorizing the catalog-reader certificate](#authorizing-the-catalog-reader-certificate).
3. **Write `targets.yaml`**: which nodes, which environments, what to exclude.
   See [`targets.yaml`](#targetsyaml) and
   [`examples/targets-puppetdb-baseline.yaml`](examples/targets-puppetdb-baseline.yaml).
4. **Run it:**

```sh
piace compare --targets targets.yaml --services services.yaml \
  --html-out report.html
```

The text report goes to stdout; the exit code tells CI what happened. See
[Exit codes](#exit-codes), and [docs/ci.md](docs/ci.md) for the pipeline around
it: file layout, credential handling, and copy-ready GitHub Actions, GitLab CI
and Azure Pipelines jobs.

**Freeze the baseline instead**, when PuppetDB's latest catalog moves under you,
and always with `catalog_api: v3`: set `baseline.source: file`, run
`piace capture catalog --environment production --replace` once after each
promotion, then compare as often as you like. `capture` takes no destination
flag: it writes to `baseline.file` or `facts.file` and skips, with a warning,
any target whose matching `source` is not `file`, so configure the file source
before the capture that populates it.
[`examples/targets-snapshot-baseline.yaml`](examples/targets-snapshot-baseline.yaml)
is that shape.

---

## Commands

```
piace compare --targets TARGETS.yaml --services SERVICES.yaml \
  [--candidate-environment ENVIRONMENT] \
  [--text-out PATH] [--json-out PATH] [--html-out PATH] [--impact-nodes]

piace capture facts   --targets TARGETS.yaml --services SERVICES.yaml [--replace]

piace capture catalog --targets TARGETS.yaml --services SERVICES.yaml \
  --environment ENVIRONMENT [--replace]

piace explain --json-in REPORT.json --services SERVICES.yaml \
  [--ai-out PATH] [--html-out PATH] [--change CHANGE.yaml] \
  [--fail-on-inference-error]

piace change-context (--base-ref REF | --base-ref-env VAR) \
  [--head-ref REF | --head-ref-env VAR] \
  [--title-env VAR | --title-file PATH] \
  [--description-env VAR | --description-file PATH]
```

| Flag | Command | Meaning |
| --- | --- | --- |
| `--targets` | compare, capture | Target and policy file (required) |
| `--services` | compare, capture, explain | Endpoint, TLS and inference file (required) |
| `--candidate-environment` | compare | Compile every target's candidate catalog from this environment, overriding `candidate.environment` in the target file |
| `--text-out` | compare | Text report path; default stdout |
| `--json-out` | compare | Versioned, canonically encoded JSON report |
| `--html-out` | compare, explain | Self-contained static HTML report |
| `--impact-nodes` | compare | Name every certname an impact estimate returned, not a capped sample (text report only) |
| `--environment` | capture catalog | Environment to compile the snapshot from (required) |
| `--replace` | capture | Overwrite an existing snapshot |
| `--json-in` | explain | Stored result document; `-` reads stdin (required) |
| `--change` | explain | Change context file (see [docs/change-assessment.md](docs/change-assessment.md#change-context)) |
| `--ai-out` | explain | Change assessment artifact path |
| `--fail-on-inference-error` | explain | Exit 30 when the assessment could not be produced |
| `--base-ref`, `--head-ref` | change-context | The refs to describe the change between; `--head-ref` defaults to `HEAD` |
| `--*-env`, `--*-file` | change-context | Take a value by variable name or path rather than on the command line |
| `--debug` | compare, capture, explain | One metadata line per service request to stderr |
| `--debug-dump-dir` | compare, capture, explain | Also write raw bodies to `0600` files in DIR |

The two environment flags are not the same flag.
`compare --candidate-environment` names the environment CI deployed and
overrides `candidate.environment` for every target, so the target file may omit
the field entirely; see
[docs/ci.md](docs/ci.md#why-the-targets-file-is-not-a-template).
`capture catalog --environment` names the environment to *snapshot*, typically
the production baseline, and never overrides `candidate.environment`.

### Reports

All three formats render from one redacted result document, so they cannot
disagree. Only the text report omits anything.

- **Text** (stdout by default): summarizes for a linear CI log. Omits
  dependency-graph edge changes and each impact estimate's PQL and request
  options, and names only the first few certnames per estimate.
  `--impact-nodes` names all of them, up to the configured `result_limit`.
- **JSON** (`--json-out`): complete, `schema_version`-tagged, canonically
  encoded. Identical inputs produce byte-identical bytes.
- **HTML** (`--html-out`): complete. One self-contained file with inline CSS,
  no JavaScript, no webfonts and no external assets, so `file://` is all it
  needs. You land on an index of the run; every list of rows is a `<details>`
  section whose heading counts what it holds. Failures, the v3 warning and
  outcome badges never collapse. Printing expands everything.

A target whose *only* differences are edges is still reported as changed: the
text report prints a count in place of the list. A run that exits non-zero never
reads as if nothing changed.

### Debugging a request

`--debug` prints one metadata line per request: method, URL, status, duration,
body sizes, content type, and the response body's top-level JSON *member
names*. URLs omit credentials, fragments and arbitrary query values. Only a
dated `api-version` value is retained; File-content source paths are masked.
Member names and content types have
control characters escaped and are limited to 256 bytes each; at most 64 member
names are printed.

```
piace capture catalog: debug #002 POST https://compiler.example.test:8140/puppet/v4/catalog -> 200 in 1.069s (request 24580 B, response 18362 B, content-type application/json, body object, top-level keys: catalog)
```

Those key names are the fastest way to spot a wire-shape mismatch against a
compiler or PuppetDB version, and they contain no catalog values, so the output
is safe for a CI log. On `explain` the same flag instruments the one outbound
call to the inference service, which is usually enough to place a `400` from a
provider.

> **`--debug-dump-dir` writes unredacted bodies.** They can contain Puppet
> `Sensitive` values and managed file content. Files are `0600` in a `0700`
> directory and never go to a console, but use it on a workstation, not in CI,
> and delete the directory afterwards. On `explain` the response dump of a 4xx
> is where a provider names the field it rejected; the bearer token is an HTTP
> header, so it is in no dump file.

---

## Configuration

Two files, deliberately separate: the reviewable selection and policy file, and
the file naming endpoints and where credentials come from. **Unknown keys are
rejected** in both: a typo is a load error, not a silently ignored setting.

Complete, loadable, heavily commented samples are in
[`examples/`](examples/). This section is the reference for what the keys mean.

**Each command reads what it needs.** One target file and one services file
serve every command, but they do not read the same fields, and a field a command
never reads is not required to be present for it. `capture facts` needs a
certname and a `facts.file` destination; `capture catalog` needs a fact source,
a catalog API and a `baseline.file` destination, taking its environment from
`--environment`; `compare` needs all of it. Whatever *is* present is validated
identically for every command: only presence is scoped, never validity.

**One path rule.** Every relative path in a config file resolves against the
directory of the file that names it. `facts.file` and `baseline.file` resolve
against the target file; the TLS paths, `token_file` and `policy_notes_file`
resolve against the services file. Nothing resolves against the working
directory.

### `targets.yaml`

```yaml
version: 1

defaults:
  candidate:
    environment: feature-123
    catalog_api: v4
  facts:
    source: puppetdb
  baseline:
    source: puppetdb
    environment: production
  exclude:
    - type: File
      title: "/var/cache/*"
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
  - certname: db-01.example.test
```

Everything under `defaults` may also be set per target. Per-target scalars
override defaults; `exclude` and `redact` are **append-only**, with global rules
prepended to per-target ones rather than replaced.

| Key | Required | Values | Notes |
| --- | --- | --- | --- |
| `version` | yes | `1` | |
| `candidate.environment` | yes, unless `--candidate-environment` is passed | string | The deployed environment to compile against. The flag overrides it for every target |
| `candidate.catalog_api` | yes | `v4` \| `v3` | No default. See [Choosing the catalog API](#choosing-the-catalog-api) |
| `candidate.allow_v3_fallback` | no | bool (`false`) | v4 only. Permits v3 only after an empty-body or literal `Not Found` v4 HTTP 404. This ambiguous signal is opt-in, not proof that v4 is unsupported |
| `candidate.trusted_facts_compiler_lookup` | no | bool (`false`) | v4 only. Asserts the compiler is configured to fetch the target's trusted facts from PuppetDB when the request omits them. PIACE never assumes this |
| `facts.source` | yes | `puppetdb` \| `file` | Where the factset submitted for compilation comes from |
| `facts.file` | with `source: file` | path | Must be unset with `source: puppetdb` |
| `baseline.source` | yes | `puppetdb` \| `file` | Comparison requires `file` with direct v3 or `allow_v3_fallback: true`; enforced before network activity |
| `baseline.environment` | yes | string | A PuppetDB baseline in a different environment fails the target before diffing |
| `baseline.file` | with `source: file` | path | Must be unset with `source: puppetdb` |
| `exclude[].type` | | string | Exact, case-sensitive Puppet resource type |
| `exclude[].title` | | glob | Case-sensitive `path.Match` glob. Suppresses matching resource differences and their connected edges |
| `redact[].type` / `.parameter` | | string | Exact, case-sensitive names. Replaces the value with a stable marker in every format |
| `impact_estimate.enabled` | no | bool (`false`) | |
| `impact_estimate.timeout` | with `enabled: true` | duration | For example `10s`. Required, positive |
| `impact_estimate.result_limit` | with `enabled: true` | int > 0 | Required. Bounds the certnames retained per estimate |
| `fail_on_diff` | no | bool (`false`) | A non-excluded difference on such a target exits `10` |

`{certname}` may appear in a snapshot path only as a whole path component.

### `services.yaml`

One file for every subcommand. The `compiler:`, `puppetdb:` and `inference:`
sections load independently, and each is required only by the commands that
actually contact it. `explain` reads `inference:` alone, so a file carrying only
`version:` and `inference:` is valid for it. `capture facts` retrieves from
PuppetDB and compiles nothing, so it needs no `compiler:`. A comparison of
file-backed facts against a file-backed baseline with the impact estimate
disabled contacts PuppetDB nowhere, so it needs no `puppetdb:`. PIACE does not
ask you to provision an identity for a service the run will never speak to; the
requirement returns the moment a target selects that service.

```yaml
version: 1
compiler:
  endpoint: https://compiler.example.test:8140
  ca_bundle:   /etc/piace/ca.pem
  client_cert: /etc/piace/catalog-reader.pem
  private_key: /etc/piace/catalog-reader.key
puppetdb:
  endpoint: https://puppetdb.example.test:8081
  ca_bundle_env:   PIACE_CA_BUNDLE
  client_cert_env: PIACE_CLIENT_CERT
  private_key_env: PIACE_PRIVATE_KEY
```

Each credential is named exactly once, either as a path or, with the `_env`
suffix, as the name of an environment variable holding an absolute path.
Naming both forms of one credential is an error. The `_env` form is what lets a
committed services file be read in place by a CI job whose credential directory
did not exist when the file was written; see
[docs/ci.md](docs/ci.md#why-nothing-is-rendered).

**Request deadlines.** Each service section takes an optional `timeout` (a Go
duration such as `45s`), the default deadline for requests to that service; 30
seconds when unset. A request that names its own deadline uses that instead, in
both directions: a target's `impact_estimate.timeout` of `90s` gets 90 seconds
even where the service default is shorter, and a caller asking for less than the
service default gets less. There is no way to configure "no deadline".

Using one identity for both services is a deliberate choice rather than a
default. Only `https` endpoints without URL userinfo are accepted. Compiler
and PuppetDB clients enforce their configured authority on initial requests
and redirects. Inline keys, bearer tokens and insecure TLS are rejected. [`examples/services.yaml`](examples/services.yaml) documents every
key, the `inference:` section included.

### Authorizing the catalog-reader certificate

The compiler identity should be a **dedicated certificate** used for nothing
else, because the rule below grants it *every* target's catalog.

A stock compiler lets nobody use the v4 endpoint, so PIACE gets HTTP 403 until
one rule in `/etc/puppetlabs/puppetserver/conf.d/auth.conf` names the
catalog-reader certificate's **subject CN**, not the filename in
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

That is the whole requirement for a v4 setup. Add the v3 rule **only** if you
opted into `catalog_api: v3` or `allow_v3_fallback: true`:

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

`$1` is the certname captured from the request path, so ordinary agents keep
fetching their own catalogs alongside the added CN. It is also exactly what
makes `$trusted` in a v3 catalog potentially reflect the reader rather than the
target.

Reload the compiler afterwards (`systemctl reload puppetserver`). No rule change
is needed for managed-`File` content evidence: the stock `"puppetlabs file"`
rule already covers `/puppet/v3/file_content/`.

PuppetDB is authorized separately, by its own certificate allowlist or by
accepting any certificate signed by the CA, depending on the installation.

---

## Exit codes

| Code | Outcome | Meaning |
| --- | --- | --- |
| `0` | `clean` / `differences_allowed` | Everything compared; no differences, or all allowed by policy |
| `10` | `policy_disallowed_difference` | A `fail_on_diff` target had a non-excluded difference |
| `20` | `compilation_failure` | A candidate request was rejected, or its identity or environment did not verify |
| `30` | `operational_error` | Config, TLS, retrieval, snapshot, normalization, content-verification, or enabled-impact-estimate failure |

Precedence is `30 > 20 > 10 > differences_allowed > clean`. A run is never
`clean` while any target has an unreported retrieval, compilation, or
normalization failure.

A File whose content could not be verified may have changed, so it is
reported as a difference and the target's `fail_on_diff` decides the exit
code, exactly as any other difference does. It is never folded into a clean
result. A retrieval that was attempted and failed is a separate thing and
still exits `30`: the distinction is between evidence PIACE could not have
had, such as a historical baseline that retains no digest or a directory
that has no single set of bytes, and an attempt that went wrong.

`piace explain` and `piace change-context` exit `0` or `30` only.

---

## Choosing the catalog API

**Use `catalog_api: v4`.** Every v4 request carries
`persistence: {facts: false, catalog: false}`: the compiler returns the
candidate catalog and writes nothing. The target's stored factset and catalog
stay exactly as its last real agent run left them.

The v3 catalog endpoint has no equivalent control. On every v3 request the
compiler saves the facts you submitted, rewriting the target's stored factset
and its `facts_environment` to the candidate environment, and stores the
compiled catalog through its PuppetDB catalog cache terminus, rewriting the
target's stored catalog, `catalog_environment` and `transaction_uuid`. That is a
property of the endpoint; nothing PIACE sends can turn it off.

So with `catalog_api: v3`:

- **`baseline.source: puppetdb` cannot work.** PIACE reads the baseline, then
  compiles the candidate, and the candidate compilation overwrites the baseline,
  for the next target in the same run and for every later run.
- **A file baseline does not make v3 harmless.** It stops PIACE from destroying
  its own input. It does not stop the compiler from writing candidate facts and
  catalog into PuppetDB, where anything reading PuppetDB state (reporting,
  exported resources, inventory, classification keyed on `facts_environment`)
  sees candidate values until the target's next agent run.

Puppet Server and OpenVox behave identically here: both serve v3 and v4, and
both honour the v4 `persistence` field.

**Every v4 request sets `options.prefer_requested_environment: true.`** Without
it, a site whose node classifier assigns environments would have its candidate
compiled in the classified environment rather than the one the target file
names, and PIACE would reject the response, since the returned environment is
validated against the requested one unconditionally. The consequence is worth
being explicit about: the candidate is compiled in the environment you named,
which is not necessarily the environment that node would receive on its next
run. That is the right catalog for reviewing a branch and is not a prediction of
what the classifier will hand the node.

### If you must use v3, compare against a captured file

> Comparison rejects a PuppetDB baseline before any network activity whenever
> direct v3 or permitted fallback could execute. A generic v4 HTTP 404 is
> ambiguous: only an empty body or literal `Not Found` is fallback-eligible,
> and only with explicit opt-in. Structured errors, HTTP 501, authentication,
> malformed successful responses, and transport failures never trigger fallback.

```yaml
defaults:
  candidate:
    environment: feature-123
    catalog_api: v3
  baseline:
    source: file                 # required, not optional, with v3
    environment: production
    file: snapshots/catalogs/{certname}.json
```

`capture catalog` uses the configured API and reports the requested and
effective API and warnings, including failed v3 attempts. Its snapshot records
both, plus the fallback, so a v4 request served by v3 cannot be read later as a
v4 capture.
Capture is allowed to use v3, but its trusted identity is still the reader's,
not an agent's. v3 can persist submitted facts and catalogs even if the request
ultimately fails. Prefer v4 capture to disable both persistence operations.

---

## What the reports mean literally

File-content evidence is resolved independently for each catalog, even when
`source` is unchanged. Reports identify each side's environment, evidence source,
historical status, and verification status. Inline content, validated compiled
checksums, static-catalog metadata, and captured digests can establish historical
evidence. Neither a PuppetDB baseline nor a file snapshot is verified by fetching
today's environment bytes. Missing historical evidence is an operational error,
not a clean comparison.

Catalog capture retains source-content digests inside the checksummed payload.
Keep the environment stable during capture: compilation and file retrieval are
not an atomic observation. Static metadata is retained without mutable retrieval.
Snapshots are validated PIACE projections, not complete original service responses.

Live single-file retrieval supports authority-free `puppet:///` references only.
Ordered source lists advance only after a recognized missing-file response, never
after authentication, transport, or generic HTTP errors. Digests require full
hexadecimal MD5, SHA-224, SHA-256, SHA-384, or SHA-512 values; matching Puppet
`{algorithm}` prefixes are accepted. Unsupported or malformed checksums cannot
become verified evidence. Different digest algorithms remain incomparable.

Directory and recursive-source byte comparison is unsupported, including recursive
`sourceselect` behavior. Changed references remain explicit reference-only evidence;
unchanged references are indeterminate. Exclusions suppress differences but do not
cancel content resolution or hide its diagnostics. Capture warns about unsupported
trees, but retrieval or checksum errors prevent publishing the snapshot.

**The v3 warning.** With `catalog_api: v3`, or any permitted v4-to-v3 fallback,
`$trusted` in the compiled catalog can reflect the catalog-reader certificate
rather than the target. The warning is non-suppressible, appears in all three
formats, and does not change the exit status; it makes the trust semantics
reviewable. It also describes v3 persistence consequences, including on failed
requests. A file baseline protects input, not other PuppetDB consumers. v4 sends the target's own trusted facts, and fails compilation
rather than inventing them when neither a validated input nor a configured
compiler lookup is available. Supplied factset and trusted certnames must match
the requested target. Trusted `authenticated` must be `"remote"` or `"local"`;
Puppet's unauthenticated `false` context cannot establish that identity.
Malformed supplied trusted facts fail even when lookup is enabled. These checks
validate structure and identity agreement, not the authenticity of file contents.

**The impact estimate.** It reports only that a node's latest *stored* catalog
contains the exact `Type[title]`. It is **not** proof those nodes would change,
and PIACE never compiles them. Queries are bounded by `timeout` and
`result_limit`; an over-limit result is marked truncated and reported as *more
than* the limit, never as an exact population, with a sorted certname sample.
Because an enabled estimate is requested analysis, a failed one is an
operational error.

## Bounded work

Every input bounds the work it can ask for, because a byte limit alone does not:
eight bytes of JSON (`1e-10000`) expand into ten thousand bytes of exact decimal.
Numeric tokens are bounded by significant digits and exponent magnitude before
they are parsed, encoded values by nesting depth, local files (snapshots, stored
reports, configuration, policy notes, change contexts) by size before they are
read into memory, `change-context`'s git invocations by both output size and
running time, and the inference request by total encoded bytes. The budgets and
the reasoning behind each are in `internal/limits`.

Exceeding one is an operational error with a diagnostic naming the limit, never
a silent truncation and never an unbounded wait. The exception is the inference
request, which sheds evidence in a documented order and reports exactly what it
shed; see [docs/change-assessment.md](docs/change-assessment.md).

## Writing artifacts

Every file PIACE writes is published through a same-directory temporary file
and one atomic replacement: a reader sees the previous file or the complete new
one, never a half-written artifact, and a run that dies mid-write leaves no
truncated file behind. Reports and assessments are `0644`, snapshots `0600`, and
the mode is set before any content reaches the file. A destination that is a
pipe or a device (`--json-out /dev/stdout`) is written in place, since it has no
directory entry to replace; a destination that is a symlink to a regular file is
replaced by the artifact, leaving the file it pointed at alone.

Publication across *several* files is not a transaction and is not described as
one. Every requested artifact is rendered before any is written, so the usual
failure costs nothing; if a write then fails, the run exits `30` and says which
artifacts exist and which do not.

Destinations are checked before any service request. Two artifacts of one run
may not name the same file, and no artifact may be written over a file the same
run reads: its configuration, or a snapshot it loads. Paths are compared after
normalization, and where both already exist, by file identity, so a symlink or a
hard link to the input is caught too. Two destinations that do not exist yet and
would become hard links to each other are not detectable and are not claimed to
be.

```sh
# Rejected before an inference service is contacted:
piace explain --json-in report.json --ai-out report.json
# Rejected before a compiler request:
piace compare --targets t.yaml --services s.yaml \
  --json-out out.txt --text-out out.txt
```

## Output and secrecy

Redaction happens after semantic comparison and exclusions through an explicit
conversion from private comparison evidence to the report model. Grouping
fingerprints remain internal; they never enter JSON or inference requests.

Resource-level `sensitive_parameters` lists and recursive Pcore
`{"__ptype":"Sensitive","__pvalue":...}` wrappers are retained and validated.
A parameter marked sensitive by either catalog is protected on both sides.
Configured `redact` selectors match exact type and parameter names. Sensitive
File content also suppresses derived digests while retaining the comparison
state. Additions and removals publish the existing side's redacted parameter
map. File content-bearing parameters are replaced by markers, with separate
one-sided content evidence. Missing or unsupported File evidence remains
indeterminate and produces a diagnostic; see the exit-code table above for
which severity that carries. A File the catalog asks Puppet to remove, or to
manage as a symlink, manages no bytes: that is `not_managed`, reported without
evidence and without a diagnostic.

Aggregate groups combine the most restrictive member disclosure policy.
File summaries retain state, sources and verification without digests or
target-specific provenance. Individual target changes retain that provenance.
The result document uses `schema_version: 3`; `explain` requires this version.

Malformed sensitivity lists and wrappers fail normalization. Synthetic tests
cover compiler arrays and PuppetDB expanded containers, reports, normal debug
output and inference. These tests do not establish which sensitivity metadata
each deployed PuppetDB version retains. Other encodings need wire conformance
verification; see [docs/development.md](docs/development.md#project-status).

`--debug-dump-dir` remains an explicit raw-body disclosure outside normal debug
output. Cross-target aggregation disclosure policy is tracked separately in
phase 3 of [the 0.5.0 plan](docs/plan-0.5.0.md).

## Snapshots

`piace capture` writes PIACE envelopes, not bare Puppet payloads: format
version, target identity, source, capture timestamp, SHA-256 payload checksum,
and, for catalogs, requested environment, input factset identity, and a
`capture` block recording the requested API, the API that actually answered,
whether a permitted v4-to-v3 fallback executed, the trusted-fact source of a v4
request, and the fact source of the input factset. Configuration is never
stamped into that block: it describes the request the compiler reported.

The checksum covers the payload alone, captured content evidence included,
since capture stores that evidence inside the payload. Envelope metadata sits
outside the checksum and is checked a different way: its claims must agree with
the payload they describe, and its capture provenance must describe a request
sequence that can actually occur (a fallback means a v4 request compiled
through v3; a v3 capture carries no trusted-fact source). Files are written
atomically at `0600`, and without `--replace` the refusal to overwrite is the
publication step itself, so two capture runs racing for one destination cannot
both believe they wrote it.

Reuse applies two separate checks. Integrity: a supported format version and a
payload matching its checksum. Attribution: kind, target, source kind,
envelope/payload certname and environment agreement, required metadata,
consistent capture provenance, usable captured content evidence, and the
configured baseline environment. An intact snapshot of the wrong node fails the
second while passing the first, which is why they are not one check.

A snapshot payload is a validated PIACE projection, not the original service
response. It retains what the comparison contract needs: identity, environment,
resources with parameters and sensitivity metadata, edges, static and recursive
content metadata, captured digests, and catalog identity fields. A compiler
catalog's `tags`, `classes`, and `catalog_format` are outside the projection, as
is any service field PIACE does not consume. A captured catalog is a compiler
document, so PuppetDB's `hash`, `producer`, and `producer_timestamp` appear in
captured factsets, not in captured catalogs.

Fact inputs require a `facts` object with a `data` array. An explicit empty
array is valid. Missing or null collections, malformed entries, missing values,
and duplicate fact names fail before candidate compilation. File and PuppetDB
inputs follow the same identity rules.

---

## Change assessment (`piace explain`)

Optional and advisory. It reads a JSON report `compare` already wrote, sends
**one** request to a configured **inference service**, and writes a separately
versioned assessment artifact plus a re-rendered HTML report, never rewriting
the result document. Nothing it says can change an outcome or an exit code.

```sh
piace change-context --base-ref origin/main \
  --title-env PR_TITLE --description-env PR_BODY > change.yaml
piace explain --json-in report.json --services services.yaml \
  --change change.yaml --ai-out assessment.json --html-out report.html
```

The stored report is validated before anything is sent. Decoding is bounded and
strict, and the document must describe one coherent comparison: an outcome that
matches what its own targets reduce to, aggregate groups referring to changes
that exist, failures that are actually recorded, and no published value carrying
a sensitivity wrapper or a File content-bearing parameter. A partial comparison
stays valid and explainable; a contradictory one is refused rather than repaired,
because repairing it would mean choosing which half to believe. Configuration
files get the equivalent guarantee: bounded, strict, and exactly one YAML
document, so a file whose real content sits after a `---` cannot be read as
whatever preceded it.

It is the only part of PIACE that talks to something other than your compiler
and PuppetDB. Read [docs/change-assessment.md](docs/change-assessment.md)
before enabling it: what leaves the building, what a risk indication is and is
not, and how a change context is written and bounded.

---

## Further reading

- [docs/ci.md](docs/ci.md): running PIACE in CI, pipeline shape, where each
  file belongs, and credentials on a runner you do not control
- [docs/change-assessment.md](docs/change-assessment.md): the optional
  `explain` step, and exactly what it sends where
- [examples/](examples/): loadable, commented sample configuration
- [CONTEXT.md](CONTEXT.md): the domain language used throughout code and
  reports, and the decisions behind the tool's shape
- [docs/release.md](docs/release.md): release artifacts, signing and verification
- [docs/development.md](docs/development.md): building, testing, package layout,
  project status
- [CHANGELOG.md](CHANGELOG.md): what each release contains, and known limitations
