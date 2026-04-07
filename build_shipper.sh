#!/usr/bin/env bash
set -e
cd "$(dirname "$0")/log-shipper"
echo "Building log-shipper for linux/amd64..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go build -trimpath -o ../docker/mc-server/log-shipper-linux .
echo "Done: docker/mc-server/log-shipper-linux"
