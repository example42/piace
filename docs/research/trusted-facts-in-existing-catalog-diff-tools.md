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

OpenVox currently documents the v3 catalog endpoint only. It does not document
Puppet Server's v4 catalog endpoint or v4 `trusted_facts` behaviour. Therefore
the `puppet-catalog_diff` mitigation cannot be assumed to work against OpenVox.

Source: [OpenVox catalog HTTP API](https://github.com/openvoxproject/openvox/blob/main/api/docs/http_catalog.md).

## Consequence for PIACE

PIACE allows the CI configuration to select the compiler's v3 or v4 catalog
API. A v3 candidate compilation made with the catalog-reader certificate must
emit a prominent trusted-fact compatibility warning: any target code that uses
`$trusted` can observe that service identity instead of the target identity.
Puppet Server v4 is the documented path that can explicitly provide, or obtain
from PuppetDB, target trusted facts. OpenVox's currently documented v3 API does
not establish an equivalent behaviour.
