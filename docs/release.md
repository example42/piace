# PIACE release artifacts: checksums and signatures

This document is task 12's "document checksum/signature generation and
verification for the supported release artifacts". It covers what the
release process produces, how it is produced, and what a consumer runs to
verify it before installing into an air-gapped environment.

Requirements: 12.1 (CGO-free binary), 12.2 (no runtime dependency
resolution), 12.3 (checksum and signature suitable for an internal
artifact repository). Design reference: section 11, "Security and
distribution".

## What a release contains

| File | Contents |
| --- | --- |
| `piace-<version>-<os>-<arch>` | One statically linked binary per supported platform |
| `SHA256SUMS` | One SHA-256 line per binary, sorted by filename |
| `SHA256SUMS.asc` | Detached OpenPGP signature over `SHA256SUMS` |

The signature covers the **manifest**, not each binary individually. One
signature then transitively covers every artifact, and a consumer needs
exactly one trusted public key rather than one signature per platform.

The supported OS/architecture matrix and the signing key fingerprint are
release metadata (design.md section 11): they are published with the
release and edited deliberately in `scripts/build-release.sh`, never
discovered or downloaded at run time.

## Generating the artifacts

```sh
scripts/build-release.sh 1.0.0
```

The script builds each platform with:

```sh
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> \
  go build -trimpath -ldflags "-s -w -X main.toolVersion=<version>" \
  -o dist/piace-<version>-<os>-<arch> ./cmd/piace
```

- `CGO_ENABLED=0` is requirement 12.1. `cmd/piace`'s
  `TestRelease_BuildsWithCGODisabled` asserts the build succeeds without
  cgo, and `TestRelease_NoNonStandardDependenciesBeyondYAML` asserts the
  transitive dependency set is the standard library plus
  `gopkg.in/yaml.v3` and nothing else — which is how requirement 12.2's
  "no Ruby, Puppet agent, Facter, package manager, or runtime dependency
  resolution" is kept true as the code changes.
- `-trimpath` removes local filesystem paths from the binary, so the same
  commit built on two machines produces the same bytes and a mismatched
  checksum means a real difference rather than a different build
  directory.
- `-X main.toolVersion=<version>` stamps the version the tool reports and
  records in every result document's invocation metadata.

## Signing the manifest

```sh
gpg --armor --detach-sign --local-user <signing-key-id> dist/SHA256SUMS
```

This writes `dist/SHA256SUMS.asc`. Publish `SHA256SUMS`,
`SHA256SUMS.asc`, and the binaries together, and publish the signing
key's fingerprint through a channel independent of the artifact
repository — a signature verified against a key fetched from the same
place as the artifact proves nothing about the artifact's origin.

## Verifying a downloaded release

Verify in this order. Checking the checksum first would confirm only that
the binary matches a manifest that may itself have been substituted.

**1. Verify the manifest signature.**

```sh
gpg --verify SHA256SUMS.asc SHA256SUMS
```

Confirm the reported key fingerprint matches the published one. `gpg`
reports a valid signature from any key in the local keyring, so an
unchecked "Good signature" line is not on its own evidence of origin.

**2. Verify the binary against the now-trusted manifest.**

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

On macOS, where `sha256sum` is absent:

```sh
shasum -a 256 --check --ignore-missing SHA256SUMS
```

`--ignore-missing` lets a consumer verify only the platform they
downloaded without the check failing over absent files. Both commands
exit non-zero on any mismatch, so they are usable directly as a CI gate.

**3. Confirm the binary is the one you verified.**

```sh
./piace-<version>-<os>-<arch> version
```

The reported version comes from the `-X main.toolVersion` stamp above, so
it ties the running binary back to the manifest entry.

## Air-gapped installation

All three files transfer as ordinary artifacts; verification is entirely
local and needs no network beyond the trusted key already being present.
The binary itself opens network connections only to the compiler and
PuppetDB endpoints named in its own `--services` file (requirement 12.4,
asserted by `TestAcceptance_EndpointsRestrictedToConfiguredServices`), so
an installed PIACE reaches nothing a release process introduced.
