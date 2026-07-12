#!/bin/sh
# Writes the runtime API-base-URL shim consumed by panel/src/lib/api/core.ts via
# window.__DYLARIS_CONFIG__. Runs on every container start (not build time), so
# a self-hoster's PANEL_API_URL takes effect without rebuilding the image.
# Empty (default) -> apiUrl:"" -> the panel resolves same-origin /api.
set -e

cat > /app/public/config.js <<CONFIGJS
window.__DYLARIS_CONFIG__ = {
  apiUrl: "${PANEL_API_URL:-}"
};
CONFIGJS

exec node server.js
