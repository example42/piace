# Puppet Impact Assessment & Change Explorer (PIACE)

PIACE is a CI command that compares node catalogs retained in PuppetDB with
catalogs compiled for an already deployed candidate environment.

This file holds the two things the code cannot state for itself: the words
this project uses for its own concepts, and the handful of decisions that
explain why it is shaped the way it is. Everything else lives with what it
describes: [README.md](README.md) for the tool, [docs/ci.md](docs/ci.md) for
pipelines, [docs/release.md](docs/release.md) for publishing and verifying a
release, [docs/development.md](docs/development.md) for working on it, and
[examples/](examples/) for configuration that loads.

## Language

### Comparison

**Baseline catalog**:
The catalog selected from PuppetDB or a local catalog snapshot for a target,
used as the state against which a candidate catalog is compared. A PuppetDB
baseline must match the target's configured baseline environment.
_Avoid_: old catalog, stored catalog, production catalog

**Candidate catalog**:
The catalog compiled by the configured compiler for a target certname in the CI
environment under test, using the selected v3 or v4 compiler catalog API. A v4
candidate compilation is not persisted; a v3 one is stored by the compiler in
PuppetDB and overwrites the target's stored catalog and factset.
_Avoid_: CI catalog, new catalog

**Compiler**:
The existing Puppet Server or OpenVox compiler endpoint that serves a deployed
Puppet environment and compiles candidate catalogs on request.
_Avoid_: local compiler, embedded compiler

**Catalog-reader certificate**:
A dedicated Puppet TLS identity whose compiler authorization grants it read
access to catalogs for every target certname.
_Avoid_: CI token, user certificate

**Fact source**:
The configured origin of target facts: PuppetDB's latest factset or a local
per-target factset file.
_Avoid_: fact history, current facts

**Catalog source**:
The configured origin of a baseline catalog: PuppetDB's latest catalog or a
local per-target catalog snapshot.
_Avoid_: catalog history, current catalog

**Snapshot**:
A local, versioned-in-workflow capture of one target's facts or catalog, with
its target identity, source, environment, capture metadata, and integrity
checksum needed to use it as a PIACE input.
_Avoid_: cache, archive

**Target**:
A node identified by its certname for which the viewer retrieves a baseline
catalog and requests a candidate catalog.
_Avoid_: hostname, host

**Exclusion rule**:
A user-configured resource identity selector that suppresses matching resource
differences and their connected edges from the presented and evaluated
comparison. Rules may be global or target-specific and use an exact type with a
case-sensitive glob title.
_Avoid_: ignore, filter

**File-content difference**:
A change to the effective managed content of a `File` resource, including its
inline content, content source, or compiled content checksum. PIACE reports
digest evidence rather than content bytes by default.
_Avoid_: File parameter difference

**Node diff**:
The complete catalog comparison result for one target, including its baseline,
candidate, and resource-graph differences.
_Avoid_: host diff

**Aggregate diff**:
A cross-target view that groups equivalent node-diff changes and reports the
targets affected by each group.
_Avoid_: general diff, global diff

**Impact estimate**:
An optional PQL result from PuppetDB's resources endpoint that identifies nodes
whose latest stored catalog contains a changed exact resource type and title.
It is bounded by configured limits and is a potential-impact estimate, not
proof that those nodes will change.
_Avoid_: affected nodes, blast radius

### Change assessment

**Change assessment**:
The advisory, model-generated document `piace explain` derives from one stored
result document and an optional change context. It is not deterministic, is not
part of the result document, and never affects a comparison outcome or exit
status.
_Avoid_: AI report, analysis, blast radius

**Risk indication**:
A change assessment's closed-enum judgement (`low`, `medium`, `high`,
`unknown`) for one aggregate group or for the run. It is a model's opinion
about a change, not a measurement of it.
_Avoid_: risk score, severity, danger level, safety rating

**Review focus**:
The ordered list of resource identities or targets a change assessment suggests
a reviewer look at first. It is a reading order, not a work list.
_Avoid_: recommendations, action items, findings

**Inference service**:
The configured external OpenAI-compatible endpoint a change assessment is
requested from. It is the only service PIACE contacts that is not the compiler
or PuppetDB, and `piace compare` never contacts it.
_Avoid_: AI provider, LLM, model backend

**Change context**:
The caller-supplied file describing the repository change under test: refs,
commit subjects, changed paths, and optional capped title and description.
PIACE reads it, never invokes git, and treats its free text as untrusted data.
_Avoid_: git diff, commit info, PR metadata

**Pseudonymized identity**:
A stable per-run substitute for a certname, used only in the two structured
identity fields of an inference request body: a target's node and a group's
node list. It never appears in a change assessment or any report, and it does
not reach free text, so a certname written into a resource title, a parameter
value or a change context travels as itself.
_Avoid_: anonymized, masked, redacted

## Design

### One path rule

Every relative path named in a config file resolves against the directory of
the file that names it, never against the process working directory, so moving
a config file takes its paths with it. The [README](README.md#configuration)
lists which key resolves against which file.

A credential may be named rather than located: `ca_bundle_env`,
`client_cert_env`, `private_key_env` and `token_env` each name an environment
variable holding an absolute path. Naming both forms of one credential is an
error rather than a precedence rule nobody remembers. Together the two rules
are what let a services file be committed and read in place, unmodified, by a
CI job whose credential directory did not exist when the file was written.

### Candidate catalogs come from an existing compiler

PIACE is an HTTPS client, not a Puppet compiler. CI deploys the candidate
environment to an existing Puppet Server or OpenVox compiler, and PIACE
requests each candidate catalog through that compiler's v3 or v4 catalog API
using a dedicated catalog-reader certificate. That keeps the CLI
dependency-free and air-gap-installable while compiling with the deployed
environment's actual Puppet runtime and code.

Borrowing the deployed compiler means accepting its persistence behaviour. The
v4 API lets a request disable fact and catalog persistence, and PIACE sets
those fields on every v4 request, so a v4 candidate compilation leaves
PuppetDB untouched. The v3 API has no such control: the compiler saves the
submitted facts and stores the compiled catalog under the candidate
environment. v3 is therefore a degraded path constrained to a file-backed
baseline, not an equivalent one.

Local fact and catalog snapshots are PIACE envelopes rather than bare Puppet
payloads: they record source, target, environment where applicable, capture
metadata, input identity, and an integrity checksum. A PuppetDB baseline whose
environment differs from the configured baseline environment is rejected.

A snapshot payload is a validated projection of the service response, retaining
what a comparison needs (identity, environment, resources with their parameters
and sensitivity metadata, edges, content metadata and captured digests) and
dropping what PIACE does not consume. A catalog envelope also records how it
was obtained: the API requested, the API that answered, any permitted v4-to-v3
fallback, and the trusted-fact and fact sources. That distinction is the point.
A capture that fell back to v3 has v3's trust semantics regardless of what the
target file asked for, and a snapshot that recorded only the requested API
would misdescribe its own contents for the rest of its life.

### The change assessment stays out of the result document

The result document is canonically encoded and `schema_version`-tagged, so one
result always encodes to the same bytes and two runs over the same catalogs and
configuration reach the same comparison. Those are two claims, not one: a
production run stamps its own `invocation.timestamp_utc`, so what reproduces
across runs is the document with its invocation metadata removed, which
`report.SemanticProjection` defines and a reader can compute with
`jq -S 'del(.invocation)'`. The acceptance suite asserts both, the second by
running the pipeline twice under two different clocks.

A model-generated change assessment holds neither property: even with sampling
pinned, a provider-side model revision changes the prose.

Rather than weaken the invariant to accommodate an advisory feature, the
assessment is a separate artifact with its own `ai_schema_version`, carrying a
SHA-256 checksum of the canonical result document it was derived from.
Embedding it and bumping `schema_version` was rejected because it would turn a
guarantee a reader can state in one sentence into one with an exception list.

Because the assessment is a pure function of a stored result document, it is
produced by a second command, `piace explain`, rather than inside `compare`.
That leaves `compare`'s configuration surface, dependency surface, failure
modes and service reach unchanged, and it makes the feature runnable and
testable offline against a report some earlier run produced. The cost is one
extra step in a pipeline.

### The inference service is the one bearer-token exception

PIACE authenticates to the compiler and PuppetDB exclusively via mTLS, and
`internal/transport` enforces it beyond configuration: `Client` deletes any
`Authorization` header from every request it sends, so a stolen services file
yields nothing usable. Every practical OpenAI-compatible inference service
authenticates with a bearer token, so there is one scoped exception.

The scope is structural rather than a matter of discipline: the inference
client is its own package, `internal/inference`, and is the only code in PIACE
that sets an `Authorization` header. The token is never written in the
services file; `token_env` and `token_file` reference it, and `https` is the
only accepted scheme.

The `inference:` section lives in the same services file as the compiler and
puppetdb sections, and the three load independently: a file carrying only
`inference:` is valid for `explain`, which needs no mTLS identity and builds no
compiler or PuppetDB client. What separates a comparison job from an
assessment job is which credentials each is granted, not which file it reads.

### `change-context` is the only subcommand that invokes git

`compare` and `explain` contact nothing but the compiler, PuppetDB and the
inference service, and neither runs git. `explain --change` reads a file the
caller produced by any means, so a repository under a different VCS, or a CI
system with no checkout, describes its change by hand.

`piace change-context` produces that file by exec'ing git, and it is optional.
It exists because the alternative is every adopter hand-writing the same YAML
in shell, and the free-text part of that is the dangerous part: a pull request
title is attacker-supplied, and a CI system that substitutes one into script
text before a shell runs turns it into a command. Every untrusted input is
taken by variable name or file path, never by value, and there is deliberately
no `--title` or `--description` flag.
