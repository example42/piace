# Static catalog fixture

`static-catalog.json` is a reduced, syntactically corrected projection of the
[Puppet Server static catalog response example](https://help.puppet.com/core/current/Content/PuppetCore/server/http_api/http_catalog.htm),
retrieved through Context7 on 2026-09-09. The checksum strings and metadata
shapes are from that published example. Node, environment and code identity
were replaced for the acceptance harness; unrelated resources were removed.
This is a documentation-derived wire fixture, not a live deployment capture.

The title-keyed `metadata` and title/source-keyed `recursive_metadata` forms
also follow `Puppet::Resource::Catalog#to_data_hash` in
[Puppet's catalog serializer](https://github.com/puppetlabs/puppet/blob/main/lib/puppet/resource/catalog.rb).
