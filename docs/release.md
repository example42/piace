# PIACE release artifacts: checksums and signatures

What the release process produces, how it is produced, and what a consumer
runs to verify it before installing into an air-gapped environment.

## What a release contains

| File | Contents |
| --- | --- |
| `piace-<version>-<os>-<arch>` | One statically linked binary per supported platform |
| `SHA256SUMS` | One SHA-256 line per binary, sorted by filename |
| `SHA256SUMS.sigstore.json` | Keyless cosign signature bundle over `SHA256SUMS` |
| `SHA256SUMS.asc` | Optional detached OpenPGP signature over the same manifest |

Published alongside them, from the same artifacts:

| Image | Contents |
| --- | --- |
| `example42/piace:<version>` | A `linux/amd64` + `linux/arm64` manifest list; `:latest` moves with every non-prerelease |
| `ghcr.io/example42/piace:<version>` | The same manifest, mirrored |

A signature covers the **manifest**, not each binary individually. One
signature then transitively covers every artifact, and a consumer verifies one
thing rather than one signature per platform.

The binaries also carry a GitHub build provenance attestation, and both image
manifests are signed by digest and attested. Provenance answers a different
question from a signature: which workflow, at which commit, produced these
bytes.

The supported OS and architecture matrix is release metadata: it is published
with the release and edited deliberately in `scripts/build-release.sh`, never
discovered or downloaded at run time.

## Cutting a release

Pushing a `v*` tag runs the whole thing:

```sh
git tag v1.0.0
git push origin v1.0.0
```

A tagged run uses the workflow **as it exists at the tagged commit**, so
tag a commit that already carries `.github/workflows/ci.yml`. Tagging a
branch the workflow has not reached yet does nothing at all: no run, no
error, nothing in the Actions log, which is a confusing way to spend a
version number.

`.github/workflows/ci.yml` then runs the test matrix, builds every
platform, verifies the manifest, confirms the Linux binaries are
statically linked and that each reports the version it was stamped with,
and publishes a GitHub Release with the binaries and `SHA256SUMS`
attached. The release job publishes the artifacts the build job produced
rather than rebuilding, so what a consumer downloads is what CI checked.
A tag that is not `vMAJOR.MINOR.PATCH[-prerelease]` fails before anything
is built; a tag whose version carries a `-suffix` is published as a
prerelease.

**The signature is part of that.** The release job signs `SHA256SUMS` with
keyless cosign: the certificate is issued against the workflow's own OIDC
identity and lives for minutes, so there is no signing key to store, rotate or
lose, and the signature is attached from the moment the release exists. The
release notes carry the exact `cosign verify-blob` command, including the
certificate identity for the tag being published.

An OpenPGP signature over the same manifest remains available for sites whose
policy requires one. It is an extra, not the verification path, and CI holds no
key for it:

```sh
gh release download v1.0.0 --pattern SHA256SUMS
gpg --armor --detach-sign --local-user <signing-key-id> SHA256SUMS
gh release upload v1.0.0 SHA256SUMS.asc
```

### The container image

Once the release exists, a fourth job packages those same binaries and pushes
one manifest to both Docker Hub and GHCR. It waits on the release rather than
running beside it, so the GitHub Release stays the primary artifact: if the
push fails, the release is already out and re-running the `publish container
image` job on its own finishes the work. A prerelease publishes its version tag
but does not move `latest`.

Docker Hub is the name the documentation uses. GHCR exists because an anonymous
pull from a shared CI runner IP is exactly what Docker Hub rate-limits, and a
pipeline failing for that reason is failing for a reason that has nothing to do
with this project. GHCR needs no stored credential: the job pushes with the
workflow's own token.

Docker Hub needs two repository secrets, and the job fails visibly on the first
tag pushed without them:

| Secret | Value |
| --- | --- |
| `DOCKERHUB_USERNAME` | A Docker Hub account with push access to `example42/piace` |
| `DOCKERHUB_TOKEN` | A Docker Hub **access token** for that account with Read & Write scope, not the account password |

Use an access token: it is scoped, revocable on its own, and does not
carry the account's Hub session.

## Generating the artifacts by hand

CI runs exactly this, and it stays usable directly for an air-gapped or
out-of-band build:

```sh
scripts/build-release.sh 1.0.0
```

The script builds each platform with:

```sh
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> \
  go build -trimpath -ldflags "-s -w -X main.toolVersion=<version>" \
  -o dist/piace-<version>-<os>-<arch> ./cmd/piace
```

- `CGO_ENABLED=0` keeps the artifact a static binary. `cmd/piace`'s
  `TestRelease_BuildsWithCGODisabled` asserts the build succeeds without
  cgo, and `TestRelease_NoNonStandardDependenciesBeyondYAML` asserts the
  transitive dependency set is the standard library plus
  `gopkg.in/yaml.v3` and nothing else, which is how the no-runtime-dependency
  "no Ruby, Puppet agent, Facter, package manager, or runtime dependency
  resolution" is kept true as the code changes.
- `-trimpath` removes local filesystem paths from the binary, so the same
  commit built on two machines produces the same bytes and a mismatched
  checksum means a real difference rather than a different build
  directory.
- `-X main.toolVersion=<version>` stamps the version the tool reports and
  records in every result document's invocation metadata.

## Building the image by hand

The `Dockerfile` copies release artifacts; it does not compile. Build
them first, then hand the same version in as a build argument:

```sh
scripts/build-release.sh 1.0.0
docker build --build-arg VERSION=1.0.0 -t piace:1.0.0 .
```

The image is `gcr.io/distroless/static-debian12:nonroot` plus the one
binary: no shell, no package manager, and a CA bundle only because
`piace explain` verifies an inference service against the system roots
(the compiler and PuppetDB transports carry their own CA bundle from the
services file, and trust nothing else). Assembling it from the built
artifacts rather than from a `golang` builder stage is what makes the
binary inside the image the same bytes `SHA256SUMS` certifies.

Running `piace:1.0.0` is the same as running the published image: mount your
workspace at `/work` and pass your own uid, since the image runs as uid 65532
and cannot otherwise write a report into the mount. See the
[README](../README.md#install).

The image deliberately carries the binary and nothing else, which decides
where it fits. `piace change-context` runs there like any other subcommand but
execs git, which the image does not carry, so change context is produced on the
runner. For the same reason the image cannot serve as a GitLab or GitHub CI job
image, which must provide a shell: in CI, install the verified binary instead.
See [ci.md](ci.md).

## Signing the manifest by hand

CI signs with keyless cosign, so nothing here is needed for an ordinary
release. For an out-of-band build, or a site that requires OpenPGP:

```sh
gpg --armor --detach-sign --local-user <signing-key-id> dist/SHA256SUMS
```

This writes `dist/SHA256SUMS.asc`. Publish `SHA256SUMS`, `SHA256SUMS.asc` and
the binaries together, and publish the signing key's fingerprint through a
channel independent of the artifact repository: a signature verified against a
key fetched from the same place as the artifact proves nothing about the
artifact's origin.

## Verifying a downloaded release

Verify in this order. Checking the checksum first would confirm only that
the binary matches a manifest that may itself have been substituted.

**1. Verify the manifest signature.**

```sh
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity "https://github.com/example42/piace/.github/workflows/ci.yml@refs/tags/v<version>" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The certificate identity is the workflow that published the tag, so it names
both the repository and the release. Substitute the tag you downloaded.

Where policy requires OpenPGP instead, and the release carries `SHA256SUMS.asc`:

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
PuppetDB endpoints named in its own `--services` file, asserted by
`TestAcceptance_EndpointsRestrictedToConfiguredServices`, so
an installed PIACE reaches nothing a release process introduced.
