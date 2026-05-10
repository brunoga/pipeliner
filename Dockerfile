# ── Build stage ───────────────────────────────────────────────────────────────
# Run the builder on the native host platform for speed (no emulation).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

# Docker injects these automatically for multi-platform builds.
ARG TARGETOS=linux
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev

WORKDIR /build

# Download dependencies first so this layer is cached separately.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Cross-compile for the target platform.
# GOARM is derived from TARGETVARIANT (e.g. "v7" → "7") for linux/arm/v7.
RUN GOARM="${TARGETVARIANT#v}" \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w -X main.version=${VERSION}" -o pipeliner ./cmd/pipeliner

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:latest

# ca-certificates: needed for HTTPS calls to external APIs (TVDB, TMDB, etc.)
# tzdata: needed for correct time zone handling in scheduled tasks
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /build/pipeliner .
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# /config holds config.star and the pipeliner.db state database.
# Mount a volume here to persist configuration and state across restarts.
VOLUME ["/config"]

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
