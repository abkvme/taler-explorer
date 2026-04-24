# syntax=docker/dockerfile:1.7

# --- build stage --------------------------------------------------------------
# Debian-based Go image — broader tooling and fewer musl edge-cases than -alpine.
#
# Pinning --platform=$BUILDPLATFORM keeps the Go compiler running natively on the
# host architecture (no QEMU tax). The GOOS/GOARCH vars below drive Go's own
# cross-compile to TARGETPLATFORM. On a multi-arch buildx run this turns a
# ~150s-per-foreign-arch step into ~15s each.
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
WORKDIR /src

# Print versions into the build log so any surprise toolchain mismatch is
# immediately visible in CI.
RUN set -eux; \
    go version; \
    go env GOVERSION GOROOT GOPATH GOCACHE GOOS GOARCH CGO_ENABLED

# Resolve deps first so the COPY . . below doesn't invalidate the mod cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN set -eux; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/taler-explorer ./cmd/taler-explorer

# --- runtime stage ------------------------------------------------------------
# Distroless "static" has ca-certs + /etc/passwd pre-baked, nothing else.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/taler-explorer /usr/local/bin/taler-explorer

USER nonroot:nonroot
EXPOSE 37332

# Persisted SQLite lives in /data. Config is mounted at /etc/taler-explorer/.
VOLUME ["/data"]
ENV TALER_DB_PATH=/data/taler-explorer.db \
    TALER_LISTEN=0.0.0.0:37332

ENTRYPOINT ["/usr/local/bin/taler-explorer"]
CMD ["-config", "/etc/taler-explorer/config.toml"]
