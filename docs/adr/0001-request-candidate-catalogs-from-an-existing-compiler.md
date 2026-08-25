# Request candidate catalogs from an existing compiler

PIACE is a Go HTTPS client, not a Puppet compiler. CI deploys the candidate
environment to an existing Puppet Server or OpenVox compiler, and PIACE
requests each candidate catalog through that compiler's configured v3 or v4
catalog API using a dedicated catalog-reader certificate. This retains a
dependency-free, air-gap-installable CLI while compiling with the deployed
environment's actual Puppet runtime and code. PIACE can instead use local
catalog snapshots as baselines, allowing CI to compare a development candidate
with an intentional capture from each target's default environment rather than
with PuppetDB's latest catalog regardless of environment.

Borrowing the deployed compiler means accepting its persistence behaviour. The
v4 catalog API lets a request disable fact and catalog persistence, so a v4
candidate compilation leaves PuppetDB untouched; PIACE sets those fields on
every v4 request. The v3 catalog API has no such control: the compiler saves
the submitted facts and stores the compiled catalog under the candidate
environment. v3 therefore remains available as a degraded path, constrained to
a file-backed baseline, rather than an equivalent one.

Local fact and catalog snapshots are PIACE envelopes rather than bare Puppet
payloads: they record source, target, environment where applicable, capture
metadata, input identity, and an integrity checksum. A PuppetDB baseline whose
environment differs from the configured baseline environment is rejected.
