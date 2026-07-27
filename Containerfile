# syntax=docker/dockerfile:1
#
# Single-binary Containerfile for the goAthena modular monolith.
#
# The image runs the one `goathena` binary, which dispatches to its subcommands:
#   - `serve`    (default, ENTRYPOINT CMD) the long-running server
#   - `migrate`  schema migrations (used by the one-shot compose service)
#   - `version`  build metadata
#
# Example:
#   docker build -t goathena:dev .
#   docker run --rm goathena:dev serve
#   docker run --rm goathena:dev migrate up
#
# Notes:
#   - Runtime base is `gcr.io/distroless/base-debian13:nonroot` — no shell, no
#     wget. A dedicated /healthcheck binary (cmd/healthcheck) is compiled into
#     the image so Docker healthchecks can probe /healthz without CMD-SHELL.
#   - Version metadata is injected via -ldflags into the binary's main package
#     (main.Version / main.CommitSHA / main.BuildTime), surfaced by
#     `goathena version`.

# -----------------------------------------------------------------------------
# Builder
# -----------------------------------------------------------------------------
FROM golang:1.26 AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags="-s -w \
  -X main.Version=${VERSION} \
  -X main.CommitSHA=${COMMIT_SHA} \
  -X main.BuildTime=${BUILD_TIME}" \
  -o /out/goathena ./cmd/goathena

RUN CGO_ENABLED=0 GOOS=linux go build -o /out/healthcheck ./cmd/healthcheck

# -----------------------------------------------------------------------------
# Runtime
# -----------------------------------------------------------------------------
FROM gcr.io/distroless/base-debian13:nonroot AS runtime

COPY --from=builder --chown=nonroot:nonroot /out/goathena /goathena
COPY --from=builder --chown=nonroot:nonroot /out/healthcheck /healthcheck
COPY --from=builder --chown=nonroot:nonroot /build/config.yaml /config.yaml

USER nonroot:nonroot

ENTRYPOINT ["/goathena"]
CMD ["serve"]
