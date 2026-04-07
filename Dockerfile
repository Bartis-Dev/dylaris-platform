# --- Build Stage ---
FROM golang:1.26.1-alpine AS builder

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

# Build
# We explicitly specify the path and use sh expansion
# BUILD_TAGS allows excluding packages (e.g. "noxdp" to skip eBPF)
RUN echo "Building from: ${ENTRY_PATH} (tags: ${BUILD_TAGS:-none})" && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags="${BUILD_TAGS}" -ldflags="-s -w" -o /app/binary ${ENTRY_PATH} && \
    chmod +x /app/binary

# --- Run Stage ---
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/binary .

ARG INSTALL_QUOTA=""
RUN chmod +x /app/binary && apk add --no-cache ca-certificates
RUN if [ "$INSTALL_QUOTA" = "1" ]; then apk add --no-cache quota-tools e2fsprogs-extra xfsprogs xfsprogs-extra && echo "Quota tools installed"; fi

CMD ["./binary"]
