# --- Build Stage ---
FROM golang:1.26.7-alpine AS builder

# Install git (important for go mod download with some libs)
RUN apk add --no-cache git

# FIX for error 128: Allow Git to use the directory
RUN git config --global --add safe.directory '*'

WORKDIR /src

# Copy everything
COPY . .

# Arguments
ARG ENTRY_PATH
ARG BUILD_TAGS=""
# RELEASE_VERSION stamps the release this image is built from into
# main.releaseVersion, so a component can report its OWN position instead of
# Core assuming they all moved together. Left empty for components that carry no
# such variable; the linker ignores -X for a symbol that does not exist, so
# passing it everywhere is harmless.
#
# Empty is also the correct value when the repo has no releases yet: the
# component then reports nothing, which reads as "not reporting" rather than as
# a version it does not have.
ARG RELEASE_VERSION=""

# Build
# We explicitly specify the path and use sh expansion
# BUILD_TAGS allows excluding packages (e.g. "noxdp" to skip eBPF)
RUN echo "Building from: ${ENTRY_PATH} (tags: ${BUILD_TAGS:-none}, release: ${RELEASE_VERSION:-unstamped})" && \
    LD="-s -w"; \
    if [ -n "${RELEASE_VERSION}" ]; then LD="${LD} -X main.releaseVersion=${RELEASE_VERSION}"; fi && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags="${BUILD_TAGS}" -ldflags="${LD}" -o /app/binary ${ENTRY_PATH} && \
    chmod +x /app/binary

# --- Run Stage ---
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/binary .

ARG INSTALL_QUOTA=""
RUN chmod +x /app/binary && apk add --no-cache ca-certificates
RUN if [ "$INSTALL_QUOTA" = "1" ]; then apk add --no-cache quota-tools e2fsprogs-extra xfsprogs xfsprogs-extra && echo "Quota tools installed"; fi

# This image builds both Core and Node (ENTRY_PATH selects the binary); they
# need different runtime privileges, so both are threaded through as build
# args (ci.yml passes SERVICE=core/RUN_AS=dylaris for Core; Node's build
# leaves both at their root defaults below).
#   SERVICE - selects the HEALTHCHECK branch below (Core has an HTTP surface
#             to probe, Node does not).
#   RUN_AS  - the USER the container runs as. Core has no reason to run as
#             root. Node stays root: it mounts the host Docker socket
#             (/var/run/docker.sock) to manage MC server containers, which
#             needs root (or the socket's host GID, not portable across
#             hosts) - documented exception.
ARG SERVICE=node
ARG RUN_AS=root
ENV SERVICE=$SERVICE
# mkdir the data mount point BEFORE chown so a fresh named volume mounted here
# inherits uid-1000 ownership (Docker seeds a new volume's ownership from the
# image dir). Without this, non-root Core (RUN_AS=dylaris) cannot write the volume.
RUN adduser -D -u 1000 dylaris && mkdir -p /app/dylaris_data && chown -R dylaris:dylaris /app
USER ${RUN_AS}

# No-op for Node (SERVICE != core, the `if` is skipped and the shell exits 0).
# Core: pings its own /healthz (DB + Redis), added in core/handlers/health.go.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD if [ "$SERVICE" = "core" ]; then wget -q -O /dev/null "http://127.0.0.1:${API_PORT:-25500}/healthz" || exit 1; fi

CMD ["./binary"]
