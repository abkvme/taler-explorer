# syntax=docker/dockerfile:1.7

# --- build stage --------------------------------------------------------------
FROM golang:1.25-alpine AS build
WORKDIR /src

# Cache deps in a separate layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build \
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
