# syntax=docker/dockerfile:1.7
# Multi-stage build → a single static goathena binary on distroless.
# `serve` is the default; override command: ["migrate","up"] for the init
# container once migrations land (persistence phase).

# ---- builder ----
FROM golang:1.27 AS build
WORKDIR /src
# Cache deps first.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
# Build with trimmed paths + version metadata (overridable via build args).
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags "-s -w \
        -X github.com/bouroo/goAthena/internal/app.Version=${VERSION} \
        -X github.com/bouroo/goAthena/internal/app.Commit=${COMMIT} \
        -X github.com/bouroo/goAthena/internal/app.BuildTime=${BUILD_TIME}" \
      -o /out/goathena ./cmd/goathena && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" \
      -o /out/healthcheck ./cmd/healthcheck

# ---- runtime ----
FROM gcr.io/distroless/static-debian13:nonroot AS runtime
COPY --from=build /out/goathena /goathena
COPY --from=build /out/healthcheck /healthcheck
# No config file is baked into the image: secrets (DB_PASSWORD, VALKEY_PASSWORD,
# ...) must never ship in the layer. The binary runs on config defaults +
# environment-variable overrides (12-factor). Operators mount a config at
# /config.yaml to override defaults; when absent, config.Load falls back to
# defaults + env without error.
EXPOSE 6900 6121 5121 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/healthcheck"]
ENTRYPOINT ["/goathena"]
CMD ["serve", "-config", "/config.yaml"]
