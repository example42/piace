# syntax=docker/dockerfile:1
#
# The PIACE container image: one statically linked binary on a base that
# carries nothing but a CA bundle, /etc/passwd and /tmp.
#
# This image is assembled from the artifacts `scripts/build-release.sh`
# already produced, not from a `golang` builder stage. The release job in
# .github/workflows/ci.yml publishes the bytes the build job verified
# rather than rebuilding, and the image holds to the same rule: what a
# `docker pull` runs is byte-for-byte what the GitHub Release publishes
# and what SHA256SUMS certifies. A builder stage would produce a second,
# unchecked binary that only looks identical: a different Go patch level
# in the base image is enough to make it differ.
#
# It also means nothing ever executes in the target architecture during
# the build: the arm64 image is a `COPY` of a cross-compiled binary, so
# multi-platform builds need buildx but no QEMU emulation.
#
# Build it by hand with:
#
#   scripts/build-release.sh 1.0.0
#   docker build --build-arg VERSION=1.0.0 -t piace:1.0.0 .

# distroless/static rather than scratch: `piace explain` reaches an
# OpenAI-compatible inference service over ordinary TLS (the compiler and
# PuppetDB transports carry their own CA bundle from the services file,
# but the inference client uses Go's default transport), so the image
# needs system root certificates or every `explain` run fails to verify.
# The `nonroot` variant runs as uid 65532; see docs/release.md for the
# `--user` flag that makes report output land in a bind mount.
FROM gcr.io/distroless/static-debian12:nonroot

# Both are consumed by the COPY below. TARGETARCH is a predefined build
# argument, but a stage sees it only after declaring it. Undeclared, it
# expands to the empty string and the COPY silently looks for the wrong
# file.
ARG VERSION
ARG TARGETARCH

# --chmod because the artifacts arrive in CI through actions/upload-artifact,
# which does not preserve the executable bit. Without it the image builds
# clean and fails at `docker run` with "permission denied".
COPY --chmod=0755 dist/piace-${VERSION}-linux-${TARGETARCH} /usr/local/bin/piace

# PIACE reads its targets, services and snapshot files from the working
# directory and writes its reports back to it, so the whole interface is
# one bind mount here.
WORKDIR /work

LABEL org.opencontainers.image.title="piace" \
      org.opencontainers.image.description="Puppet Impact Assessment & Change Explorer" \
      org.opencontainers.image.source="https://github.com/example42/piace" \
      org.opencontainers.image.documentation="https://github.com/example42/piace/blob/main/README.md" \
      org.opencontainers.image.vendor="example42" \
      org.opencontainers.image.version="${VERSION}"

ENTRYPOINT ["/usr/local/bin/piace"]
