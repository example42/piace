# PIACE: Requirements

**Status:** Draft — requirements-stage input for Kiro spec-driven development.

**Product name:** Puppet Intelligent Analysis Catalog Explorer (PIACE)

## 1. Product summary

PIACE is a dependency-free Go command-line tool for CI/CD. It compares each
target node's selected baseline catalog—PuppetDB's latest catalog or a local
snapshot—with a catalog compiled by an existing compiler for the environment
deployed by CI. It reports individual node differences, cross-node aggregate
differences, and an optional PuppetDB-backed estimate of the wider
stored-catalog footprint of changed resources.

PIACE is a client of PuppetDB and an existing Puppet Server or OpenVox
compiler. It does not compile Puppet code locally, embed a Puppet runtime, or
modify catalogs, facts, reports, code, or PuppetDB data.

## 2. Goals

- Make proposed Puppet environment changes reviewable in CI before deployment.
- Compile against the actual environment already deployed to the configured
  compiler.
- Retrieve baseline catalogs and target facts from PuppetDB or reproducible
  local per-target snapshot files.
- Support multiple targets supplied in a file.
- Produce readable terminal output and a portable, static HTML artifact.
- Remain installable in an air-gapped environment without runtime dependency
  resolution.
- Support Puppet Server and OpenVox within a truthful compatibility contract.

## 3. Non-goals

- Running Puppet agents, applying a catalog, or changing node state.
- Reimplementing Puppet compilation, Facter, Hiera, PuppetDB, or a compiler.
- Claiming that PQL can prove which nodes would change without compiling them.
- AI analysis or integration; it is out of scope for this spec session.
- Replacing existing catalog-diff tools outside PIACE's CI use case.

## 4. Domain language

**Target** is a node identified by certname for which PIACE retrieves a
baseline catalog and requests a candidate catalog.

**Baseline catalog** is the catalog selected from PuppetDB or a local catalog
snapshot for a target certname and is the state against which a candidate
catalog is compared.

**Candidate catalog** is the catalog requested from the configured compiler for
a target certname in the CI environment under test.

**Catalog-reader certificate** is a dedicated Puppet TLS identity authorized by
the compiler to request catalogs for all targets.

**Fact source** is PuppetDB's latest factset or a local per-target factset
file.

**Catalog source** is PuppetDB's latest catalog or a local per-target catalog
snapshot.

**Snapshot** is a local capture of one target's factset or catalog, including
the source identity and environment required to reuse it as a PIACE input.

**Exclusion rule** is a configured resource selector that suppresses matching
resource differences from the displayed and evaluated result.

**Node diff** is the complete comparison result for one target.

**Aggregate diff** groups equivalent changes from node diffs and identifies the
targets sharing each change.

**Impact estimate** is an optional PQL result from PuppetDB's resources
endpoint that identifies nodes whose latest stored catalog contains a changed
exact resource type and title. It is an estimate, not proof of impact.

## 5. System boundary

```text
CI deploys candidate environment
             |
             v
        Existing compiler <--- mTLS --- PIACE --- mTLS ---> PuppetDB
             |                                      |
             | candidate catalog                    | latest catalog/facts,
             v                                      | impact estimate
          PIACE comparison/report engine <----------+
             |
             +-- local fact/catalog snapshots
             +-- text report
             +-- self-contained static HTML report
             +-- versioned JSON report
```

CI is responsible for deploying the candidate environment before PIACE runs.
The compiler and PuppetDB are pre-existing services. PIACE receives endpoint,
CA bundle, client certificate, and private-key locations through its
configuration; it must not obtain or mint credentials. PuppetDB retains only
the latest catalog and latest facts for a target. It is not a historical source
from which PIACE can recover an earlier production/default-environment state.

## 6. Functional requirements

### Requirement 1: CI catalog comparison

**User Story:** As a Puppet maintainer, I want CI to compare the catalog a
target most recently received with the catalog it would receive from the
candidate environment, so that I can review changes before release.

#### Acceptance Criteria

1. WHEN PIACE is invoked with targets and a candidate environment, THE CLI
   SHALL load a baseline catalog for every target from its configured catalog
   source: PuppetDB or a local snapshot file.
2. WHEN a baseline catalog is loaded, THE CLI SHALL record its source,
   certname, environment, producer timestamp, catalog identity/hash when
   available, and source producer in the result.
3. WHEN `baseline.source` is PuppetDB and the returned catalog environment
   differs from the target's configured baseline environment, THE CLI SHALL
   fail the target before diffing it.
4. WHEN PIACE compiles a target, THE CLI SHALL request its candidate catalog
   from the configured existing compiler rather than compile locally.
5. WHEN a compiler returns a candidate catalog, THE CLI SHALL verify that its
   target identity and environment agree with the request, or report a
   compilation error.
6. THE CLI SHALL not persist candidate facts or candidate catalogs to PuppetDB.

### Requirement 2: Target facts and trusted identity

**User Story:** As a Puppet maintainer, I want candidate compilation to use
the correct target input, so that catalog differences do not result from an
unrelated client identity or stale data.

#### Acceptance Criteria

1. WHEN compiling a candidate catalog, THE CLI SHALL load the target's fact
   data from its configured fact source: PuppetDB's latest factset or a local
   per-target factset file.
2. THE CLI SHALL identify the fact source and factset identity used for each
   candidate result.
3. THE CLI SHALL allow CI configuration to select the compiler catalog API v3
   or v4 for candidate compilation.
4. WHEN v4 is selected and supports target trusted facts, THE CLI SHALL use
   that mechanism for the candidate request.
5. WHEN v3 is selected or used as a fallback, THE CLI SHALL emit a prominent
   trusted-fact compatibility warning in every output format.
6. THE v3 warning SHALL explain that `$trusted` can reflect the catalog-reader
   certificate rather than the target identity.

### Requirement 3: Service authentication and authorization

**User Story:** As a security owner, I want PIACE to use constrained mTLS
identities, so that CI does not need broadly shared interactive credentials.

#### Acceptance Criteria

1. THE CLI SHALL authenticate to the compiler using a dedicated
   catalog-reader certificate authorized by the compiler's `auth.conf` to
   request catalogs for target certnames other than its own.
2. THE CLI SHALL authenticate to PuppetDB using configured mTLS credentials.
3. THE CLI SHALL support independently configured compiler and PuppetDB TLS
   identities, including the option for an installation to deliberately use
   the same certificate for both services.
4. THE CLI SHALL load endpoint, CA certificate bundle, client certificate, and
   private key from CI-provided configuration or files.
5. THE CLI SHALL NOT log private keys, certificate private material, request
   authorization headers, or unredacted sensitive catalog parameter values.

### Requirement 4: Target selection

**User Story:** As a CI author, I want to supply a stable, reviewable list of
targets, so that the comparison scope is deterministic.

#### Acceptance Criteria

1. THE CLI SHALL accept a versioned YAML target file containing one or more
   target certnames.
2. THE YAML schema SHALL support global defaults and per-target overrides for
   candidate environment, candidate catalog API version, fact source, baseline
   catalog source, baseline environment, local factset path, and local catalog
   path.
3. WHEN a target omits a value, THE CLI SHALL apply the documented global
   default or fail before contacting external services.
4. THE v1 target-file format SHALL NOT rely on a PQL expression that expands
   the CI target set at runtime.

### Requirement 5: Semantic catalog diff

**User Story:** As a Puppet maintainer, I want PIACE to show configuration and
ordering changes rather than generated noise.

#### Acceptance Criteria

1. WHEN comparing catalogs, THE CLI SHALL identify added and removed
   resources by Puppet resource identity.
2. WHEN a resource exists in both catalogs, THE CLI SHALL identify changed
   parameters using a deterministic canonical value representation.
3. THE CLI SHALL identify added and removed dependency graph edges.
4. THE CLI SHALL produce the full node diff for each target independently.
5. THE CLI SHALL identify effective managed file-content changes, including
   changed inline content, content source, and compiled content checksum where
   available.
6. WHEN catalog data does not provide comparable content bytes or a checksum,
   THE CLI SHALL retrieve the managed content as necessary and compare a
   cryptographic digest.
7. THE CLI SHALL distinguish a verified file-content change from a changed
   content reference when bytes or a checksum are not available to compare.
8. THE CLI SHALL NOT render managed file-content bytes in text, JSON, or HTML
   output by default.
9. THE CLI SHALL exclude generated/noise-oriented fields from the semantic
   diff: tags, source file/line information, and catalog metadata unrelated to
   managed file content.

### Requirement 6: Resource exclusions

**User Story:** As a Puppet maintainer, I want to suppress known or irrelevant
resource changes, so that CI highlights actionable differences.

#### Acceptance Criteria

1. THE CLI SHALL support configurable exclusion rules for Puppet resources.
2. An exclusion rule SHALL use a `Type[title]` resource identity with an exact
   Puppet resource type and a case-sensitive title pattern with wildcard
   support. Its YAML representation SHALL use `type` and `title` keys.
3. WHEN a resource matches an exclusion rule, THE CLI SHALL suppress its
   added, removed, and parameter differences.
4. WHEN an edge has an excluded resource as either endpoint, THE CLI SHALL
   suppress that edge difference.
5. THE CLI SHALL include applied exclusion-rule identities and suppressed
   difference counts in machine-readable and human-readable output.
6. THE YAML target file SHALL support global exclusion rules and per-target
   exclusion-rule overrides.

### Requirement 7: Aggregate diff

**User Story:** As a reviewer, I want an aggregate view across the selected
targets, so that I can see common changes without manually correlating node
reports.

#### Acceptance Criteria

1. AFTER node diffs are produced, THE CLI SHALL group equivalent changes into
   an aggregate diff.
2. FOR every aggregate change group, THE CLI SHALL report the number and
   certnames of targets that exhibit it.
3. THE aggregate diff SHALL link or otherwise identify the underlying node
   diffs.
4. THE aggregate diff SHALL retain resource additions, removals, parameter
   changes, and edge changes as distinct change kinds.

### Requirement 8: Text, HTML, and result data

**User Story:** As a CI user, I want concise console output and a downloadable
review artifact, so that both automated jobs and humans can consume results.

#### Acceptance Criteria

1. THE CLI SHALL emit a text report suitable for CI logs.
2. THE CLI SHALL emit a versioned JSON report that contains the complete node
   diffs, aggregate diff, configuration provenance, warnings, exclusions,
   errors, and optional impact estimates.
3. THE CLI SHALL generate a static HTML report that can be opened using
   `file://` without an HTTP server, a CDN, network access, or sibling assets.
4. THE HTML report SHALL include per-target node diffs and the aggregate diff.
5. THE HTML report SHALL visibly mark catalog retrieval failure, compilation
   failure, the v3 trusted-fact compatibility warning, and excluded
   differences.
6. THE result formats SHALL remain deterministic for identical input catalogs
   and configuration.
7. THE CLI SHALL redact Puppet `Sensitive` values from text, JSON, and HTML
   output.
8. THE CLI SHALL support configured parameter selectors that redact additional
   values from text, JSON, and HTML output.

### Requirement 9: Stored-catalog footprint estimate

**User Story:** As a maintainer, I want PIACE to estimate the wider relevance
of changed resources, so that I can decide whether to expand validation.

#### Acceptance Criteria

1. THE CLI SHALL allow impact estimation to be enabled or disabled by
   configuration.
2. WHEN impact estimation is enabled and a relevant resource change is present,
   THE CLI SHALL query `/pdb/query/v4/resources` using PQL for nodes whose
   latest stored catalog has the changed exact resource type and title.
3. THE CLI SHALL label the result **potential impact estimate**, and SHALL NOT
   state that selected nodes will change.
4. THE CLI SHALL report the exact generated PQL query used for
   an estimate.
5. THE CLI SHALL enforce configurable time and result limits on each impact
   query.
6. WHEN an impact query reaches a configured result limit, THE CLI SHALL mark
   the estimate as truncated and report a deterministic certname sample.
7. THE CLI SHALL report query scope, result count, truncation, timeout, and
   query failures separately from catalog differences.
8. THE CLI SHALL not automatically compile impact-estimate nodes in v1.

### Requirement 10: CI outcomes

**User Story:** As a CI author, I want a machine-actionable result, so that
the pipeline can distinguish an acceptable diff from invalid analysis.

#### Acceptance Criteria

1. THE CLI SHALL return distinct outcomes for a clean comparison, a policy-
   disallowed difference, catalog compilation failure, and operational error.
2. THE CLI SHALL report its outcome and its reason in text and HTML output.
3. THE YAML target file SHALL support a global `fail_on_diff` setting and a
   per-target override.
4. WHEN `fail_on_diff` is enabled and a non-excluded semantic difference is
   present, THE CLI SHALL return the policy-disallowed-difference outcome.
5. THE CLI SHALL never report a clean outcome when one or more targets have an
   unreported retrieval, compilation, or normalization failure.

### Requirement 11: Snapshot capture and reuse

**User Story:** As a CI author, I want PIACE to capture stable target inputs
after a production/default-environment deployment, so that development-branch
comparisons do not accidentally baseline against a later catalog from another
environment.

#### Acceptance Criteria

1. THE CLI SHALL provide arguments or subcommands to retrieve each target's
   latest factset from PuppetDB and write a local per-target factset file.
2. THE CLI SHALL provide arguments or subcommands to request each target's
   catalog from the configured compiler for a specified environment and write
   a local per-target catalog snapshot.
3. WHEN capturing a catalog snapshot, THE CLI SHALL use the target facts from
   the configured fact source and SHALL record the target, requested
   environment, compiler API version, fact source, and capture timestamp.
4. THE CLI SHALL store fact and catalog snapshots in PIACE envelopes rather
   than bare Puppet JSON payloads.
5. A PIACE snapshot envelope SHALL contain its format version, target identity,
   source, capture timestamp, integrity checksum, and—where applicable—the
   requested environment, compiler API version, and input factset identity.
6. WHEN a local snapshot is selected as a fact or baseline catalog source, THE
   CLI SHALL validate its recorded target identity and integrity checksum before
   comparison.
7. THE CLI SHALL support a workflow in which CI refreshes catalog snapshots
   from each target's default environment after merge to the main/production
   branch, then uses those snapshots as development-branch baselines.

### Requirement 12: Air-gapped distribution

**User Story:** As an infrastructure operator, I want PIACE to work in an
air-gapped CI environment, so that the tool can be installed without external
package resolution.

#### Acceptance Criteria

1. PIACE SHALL be distributed as a CGO-free Go binary for supported target
   platforms.
2. THE binary SHALL require no Ruby, Puppet agent, Facter, package manager, or
   runtime dependency resolution.
3. THE release process SHALL provide a checksum and signature suitable for an
   internal artifact repository.
4. THE CLI SHALL require network access only to the installation's configured
   compiler and PuppetDB endpoints during execution.

## 7. Compatibility and known constraints

| Capability | Puppet Server | OpenVox |
| --- | --- | --- |
| Shared catalog API | `POST /puppet/v3/catalog/:certname` | `POST /puppet/v3/catalog/:certname` |
| Dedicated catalog-reader certificate | Supported through `auth.conf` | Expected through `auth.conf` |
| v4 catalog API with explicit trusted facts | Documented | Not documented |
| v3 trusted-fact caveat with service identity | Warning required | Warning required |

Puppet's v3 catalog request uses the TLS client identity. When the
catalog-reader certificate is not the target's certificate, `$trusted` use in
manifests or Hiera can yield a non-equivalent catalog. `puppet-catalog_diff`
documents this exact limitation; PIACE therefore allows an explicit v3/v4
selection and emits the v3 compatibility warning. See
[trusted-facts research](../../../docs/research/trusted-facts-in-existing-catalog-diff-tools.md).

## 8. Target-file shape

The following schema illustrates the required global defaults and per-target
overrides. Exact option names are implementation details, but the represented
configuration is part of the product contract.

```yaml
version: 1

defaults:
  candidate:
    environment: feature-123
    catalog_api: v4
  facts:
    source: puppetdb # puppetdb | file
  baseline:
    source: file # puppetdb | file
    environment: production
    file: snapshots/catalogs/{certname}.json
  exclude:
    - type: File
      title: "/var/cache/*"
    - type: Notify
      title: "*"
  impact_estimate:
    enabled: true
    timeout: 10s
    result_limit: 1000
  fail_on_diff: true
  redact:
    - type: File
      parameter: content

targets:
  - certname: web-01.example.test
    candidate:
      environment: feature-123
      catalog_api: v4
    facts:
      source: file
      file: snapshots/facts/web-01.example.test.json
    baseline:
      source: file
      environment: production
      file: snapshots/catalogs/web-01.example.test.json
    exclude:
      - type: File
        title: "/var/lib/app/cache/*"
```

For a development branch, `baseline.source: file` points to a snapshot captured
after the target's main/production environment was deployed. A
`baseline.source: puppetdb` request intentionally means PuppetDB's current
latest catalog, regardless of its environment.

## 9. Source material

- [Domain language](../../../CONTEXT.md)
- [ADR 0001: Existing compiler boundary](../../../docs/adr/0001-request-candidate-catalogs-from-an-existing-compiler.md)
- [Trusted-facts research](../../../docs/research/trusted-facts-in-existing-catalog-diff-tools.md)
- [Puppet Server v4 catalog API](https://help.puppet.com/core/current/Content/PuppetCore/server/http_api/puppet-api/v4/catalog.htm)
- [OpenVox v3 catalog API](https://github.com/openvoxproject/openvox/blob/main/api/docs/http_catalog.md)
- [PuppetDB resources query API](https://github.com/puppetlabs/puppetdb/blob/main/documentation/api/query/v4/resources.markdown)
