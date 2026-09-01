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

`compare --candidate-environment ENV` compiles every target against `ENV`,
overriding `candidate.environment` in both the `defaults:` block and any
per-target `candidate:` block. The environment CI deployed is a per-pipeline
value, so passing it at the invocation keeps the target file reviewable policy
that no job has to rewrite; with the flag, the file may omit
`candidate.environment` entirely.

`capture catalog --environment ENV` is a different flag with a different
meaning: it requests the catalog for `ENV`, typically the production or default
environment, captured after merge so development-branch runs baseline against a
frozen catalog rather than a later one from another environment. It never
overrides `candidate.environment`.

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
names*.

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
| `candidate.allow_v3_fallback` | no | bool (`false`) | v4 only. Permits falling back to v3 when the compiler lacks v4. Opt-in, never implicit |
| `candidate.trusted_facts_compiler_lookup` | no | bool (`false`) | v4 only. Asserts the compiler is configured to fetch the target's trusted facts from PuppetDB when the request omits them. PIACE never assumes this |
| `facts.source` | yes | `puppetdb` \| `file` | Where the factset submitted for compilation comes from |
| `facts.file` | with `source: file` | path | Must be unset with `source: puppetdb` |
| `baseline.source` | yes | `puppetdb` \| `file` | Must be `file` with `catalog_api: v3`, and [not enforced](#if-you-must-use-v3-compare-against-a-captured-file) |
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
sections load independently: `compare` and `capture` read the first two and
never look at `inference:`, and `explain` reads `inference:` and builds no
compiler or PuppetDB client. A file carrying only `version:` and `inference:`
is valid for `explain`, so an assessment needs no Puppet infrastructure named
at all.

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

Using one identity for both services is a deliberate choice rather than a
default. Only `https` is accepted; inline keys, bearer tokens and insecure TLS
are rejected. [`examples/services.yaml`](examples/services.yaml) documents every
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
normalization failure, an indeterminate File-content comparison included.

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

### If you must use v3, compare against a captured file

> **PIACE does not currently refuse `catalog_api: v3` with
> `baseline.source: puppetdb`.** It should, and config validation does not yet
> enforce it. The configuration loads, the first comparison looks normal, and
> the run corrupts the baseline it just read; the symptom on the next run is an
> operational error naming a baseline-environment mismatch against the candidate
> environment. **Set `baseline.source: file` yourself; nothing will do it for
> you.**

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

`capture catalog` compiles through the target's own `catalog_api`, so a v3
capture stores what it compiled, but it compiled the *baseline* environment,
which is what an agent run would have stored anyway. Capturing with
`catalog_api: v4` avoids even that.

---

## What the reports mean literally

**The v3 warning.** With `catalog_api: v3`, or any permitted v4-to-v3 fallback,
`$trusted` in the compiled catalog can reflect the catalog-reader certificate
rather than the target. The warning is non-suppressible, appears in all three
formats, and does not change the exit status; it makes the trust semantics
reviewable. v4 sends the target's own trusted facts, and fails compilation
rather than inventing them when neither a validated input nor a configured
compiler lookup is available.

**The impact estimate.** It reports only that a node's latest *stored* catalog
contains the exact `Type[title]`. It is **not** proof those nodes would change,
and PIACE never compiles them. Queries are bounded by `timeout` and
`result_limit`; an over-limit result is marked truncated and reported as *more
than* the limit, never as an exact population, with a sorted certname sample.
Because an enabled estimate is requested analysis, a failed one is an
operational error.

## Output and secrecy

Redaction happens after semantic comparison and exclusion but before
serialization, so masking never turns a real difference into a non-difference,
and two distinct sensitive values never merge into one aggregate group. Puppet
`Sensitive` wrappers are detected recursively; configured `redact` selectors
mask by exact type and parameter name. No report carries credentials, private
key material, managed file content bytes, or unredacted sensitive values.

That boundary holds for `--debug` too, which reports only request metadata and
response top-level member names. `--debug-dump-dir` is the one deliberate
exception.

> **One assumption to be aware of.** `Sensitive` detection matches Puppet's
> documented wire shape (`{"__ptype":"Sensitive","__pvalue":…}`). A compiler
> emitting a different encoding would leave such a value unredacted. Confirm
> against your compiler before treating redaction as a hard guarantee; see
> [docs/development.md](docs/development.md#project-status).

## Snapshots

`piace capture` writes PIACE envelopes, not bare Puppet payloads: format
version, target identity, source, capture timestamp, SHA-256 payload checksum,
and, for catalogs, requested environment, compiler API version, and input
factset identity. Files are written atomically at `0600` and are never
overwritten without `--replace`. On reuse, version, kind, target, checksum,
required metadata, and baseline environment are all validated before the catalog
is diffed.

---

## Change assessment (`piace explain`)

Optional and advisory. It reads a JSON report `compare` already wrote, sends
**one** request to a configured **inference service**, and writes a separately
versioned assessment artifact plus a re-rendered HTML report. It never
re-compiles anything, never contacts a compiler or PuppetDB, and never rewrites
the result document. `compare`, for its part, never contacts an inference
service, and nothing the assessment says can change an outcome or an exit code.

```sh
piace change-context --base-ref origin/main \
  --title-env PR_TITLE --description-env PR_BODY > change.yaml
piace explain --json-in report.json --services services.yaml \
  --change change.yaml --ai-out assessment.json --html-out report.html
```

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
