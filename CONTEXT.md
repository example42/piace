# Puppet Impact Assessment & Change Explorer (PIACE)

This context defines a CI command that compares node catalogs retained in
PuppetDB with catalogs compiled for an already deployed candidate environment.

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
A stable per-run substitute for a certname or service authority used only in an
inference request body. It never appears in a change assessment or any report.
_Avoid_: anonymized, masked, redacted
