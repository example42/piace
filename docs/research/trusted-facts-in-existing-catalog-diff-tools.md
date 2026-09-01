# Trusted facts in existing Puppet catalog-diff tools

## Question

How do established catalog comparison tools compile a target catalog when the
request is authenticated as a service identity rather than as that target?

## Findings

### `voxpupuli/puppet-catalog_diff`

This is the direct precedent for the proposed service-certificate model. Its
documented Puppet v3 `auth.conf` rule allows either the target certname or the
`catalog-diff` certificate to request `POST /puppet/v3/catalog/:certname`:

```hocon
allow: ["$1", "catalog-diff"]
```

The project explicitly documents the limitation: catalog compilation through
that path breaks when Puppet/Hiera code uses trusted facts, because the request
certificate belongs to `catalog-diff`, not to the target.

Its documented mitigation is Puppet's v4 catalog endpoint (`POST
/puppet/v4/catalog`), authorized for the `catalog-diff` certificate. The v4
`trusted_facts` request field can be supplied explicitly; when omitted, Puppet
Server attempts to obtain the target's trusted facts from PuppetDB (or from the
provided facts hash). The tool calls this the "certless API" because it does
not require the *target node's* certificate; its authorization example still
uses the service certificate.

Sources: [project README and auth.conf examples](https://github.com/voxpupuli/puppet-catalog_diff/blob/main/README.md),
[Puppet Server v4 catalog API](https://help.puppet.com/core/current/Content/PuppetCore/server/http_api/puppet-api/v4/catalog.htm).

### `github/octocatalog-diff`

`octocatalog-diff` supports compiling from local Puppet code, PuppetDB, a
Puppet master API, or precompiled JSON catalogs. Its original design compiles
with a local Puppet runtime using real node names and facts; it is not a
service-certificate/trusted-fact solution. Its public documentation describes
fact overrides but does not document an equivalent remote v4 trusted-fact
workflow.

Sources: [upstream project README](https://github.com/github/octocatalog-diff/blob/main/README.md),
[GitHub's design description](https://github.blog/news-insights/octocatalog-diff-githubs-puppet-development-and-testing-tool/).

### OpenVox compatibility

OpenVox's published HTTP API docs describe the v3 catalog endpoint and do not
describe a v4 catalog endpoint or v4 `trusted_facts` behaviour. Undocumented is
not the same as unimplemented, and here the distinction decides whether the
`puppet-catalog_diff` mitigation is available at all: measured against a
deployed OpenVox compiler on 2026-08-25, `POST /puppet/v4/catalog` returns 200
with a `{"catalog": ...}` envelope, accepts `trusted_facts`, and honours
`persistence: {facts: false, catalog: false}`: a request with persistence
disabled left no factset, no catalog, and no node in PuppetDB for a certname
that had none before. The v4 mitigation works against OpenVox.

Sources: [OpenVox catalog HTTP API](https://github.com/openvoxproject/openvox/blob/main/api/docs/http_catalog.md);
direct measurement against a deployed OpenVox compiler and PuppetDB.

### The v3 endpoint's second problem: persistence

The trusted-fact caveat is the one the existing tools document. Measurement
against the same deployed compiler surfaced a second, independent one: a v3
catalog request has no persistence control. One `POST
/puppet/v3/catalog/:certname` for a previously unknown certname created, in
PuppetDB, a factset and a catalog under the requested environment. The stored
catalog carrying the `transaction_uuid` the request had supplied. The compiler
saves the facts submitted with the request and stores the compiled catalog
through its PuppetDB catalog cache terminus. Neither is suppressible from the
client, and both apply to any tool that compiles a candidate catalog over v3,
not only to PIACE.

## Consequence for PIACE

PIACE allows the CI configuration to select the compiler's v3 or v4 catalog
API, and treats v4 as the supported one for two reasons rather than one.

v4 explicitly provides the target's trusted facts, or obtains them from
PuppetDB, and disables persistence so the candidate compilation leaves PuppetDB
untouched. It behaves this way on both Puppet Server and OpenVox.

A v3 candidate compilation made with the catalog-reader certificate must emit a
prominent, non-suppressible warning covering both consequences: any target code
that uses `$trusted` can observe the service identity instead of the target
identity, and the compilation overwrites the target's stored factset and
catalog under the candidate environment. The second consequence also makes a
PuppetDB baseline unusable with v3, since the candidate compilation destroys
the baseline the comparison reads, so a v3 target is constrained to
`baseline.source: file`.
