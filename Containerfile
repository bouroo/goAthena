# syntax=docker/dockerfile:1
#
# Multi-service Containerfile for goAthena.
#
# Build a specific service binary with --build-arg BINARY=<gateway|identity|zone|migrate>.
# Defaults to `identity` when BINARY is unset.
#
# Example:
#   docker build --build-arg BINARY=zone -t goathena/zone:dev .
#
# Notes:
#   - Runtime base is `distroless/base-debian12:nonroot` — no shell, no wget.
#     A dedicated /healthcheck binary (cmd/healthcheck) is compiled into the
#     image so Docker healthchecks work without CMD-SHELL.
#   - Version metadata is injected into the binary via -ldflags at
#     github.com/bouroo/goAthena/internal/app/common (Version/CommitSHA/BuildTime).

# -----------------------------------------------------------------------------
# Builder
# -----------------------------------------------------------------------------
FROM golang:1.26 AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG BINARY=identity
ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags="-s -w \
  -X github.com/bouroo/goAthena/internal/app/common.Version=${VERSION} \
  -X github.com/bouroo/goAthena/internal/app/common.CommitSHA=${COMMIT_SHA} \
  -X github.com/bouroo/goAthena/internal/app/common.BuildTime=${BUILD_TIME}" \
  -o /out/service ./cmd/${BINARY}

RUN CGO_ENABLED=0 GOOS=linux go build -o /out/healthcheck ./cmd/healthcheck

# -----------------------------------------------------------------------------
# Runtime
# -----------------------------------------------------------------------------
FROM gcr.io/distroless/base-debian13:nonroot AS runtime

ARG BINARY=identity

COPY --from=builder --chown=nonroot:nonroot /out/service /service
COPY --from=builder --chown=nonroot:nonroot /out/healthcheck /healthcheck
COPY --from=builder --chown=nonroot:nonroot /build/config.yaml /config.yaml

USER nonroot:nonroot

ENTRYPOINT ["/service"]
