# syntax=docker/dockerfile:1.7

# --- Build Stage ---
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates git tzdata

# Cache go modules layer
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source tree
COPY . .

# Multi-arch arguments injected automatically by Docker Buildx
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

# Compile static binaries for target OS and architecture
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/engram ./cmd/engram && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/engramd ./cmd/engramd

# --- Final Runtime Image ---
FROM alpine:3.21 AS runtime

LABEL org.opencontainers.image.title="engram" \
      org.opencontainers.image.description="High-performance agentic memory service, state plane, and MCP daemon" \
      org.opencontainers.image.licenses="Apache-2.0"

# Install certificates for TLS, tzdata, and wget for healthchecks
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -g 10001 -S engram \
    && adduser -u 10001 -S engram -G engram

WORKDIR /app

# Copy binaries and entrypoint script
COPY --from=builder /out/engram /usr/local/bin/engram
COPY --from=builder /out/engramd /usr/local/bin/engramd
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Embed database schema migrations and Antigravity plugin (includes bundled skill)
COPY migrations /app/migrations
COPY plugins /app/plugins

# Secure directory ownership
RUN chown -R engram:engram /app

USER engram:engram

EXPOSE 8085

ENV ENGRAM_PORT=8085 \
    ENGRAM_MIGRATIONS_DIR=/app/migrations

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:${ENGRAM_PORT}/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["engramd"]
