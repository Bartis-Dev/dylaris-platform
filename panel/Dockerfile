# --- Stage 1: Install dependencies ---
FROM node:22-alpine AS deps
WORKDIR /app

# Copy workspace structure (WITHOUT lockfile — lockfile was generated on Windows
# and misses linux-musl native binaries for lightningcss/swc. npm install
# resolves the correct platform-specific optional deps fresh.)
COPY package.json ./
COPY packages/ui-filebrowser/package.json ./packages/ui-filebrowser/package.json
COPY packages/ui-filebrowser/src/ ./packages/ui-filebrowser/src/
COPY packages/ui-filebrowser/tsconfig.json ./packages/ui-filebrowser/tsconfig.json
COPY panel/package.json ./panel/

RUN npm install --workspaces --include-workspace-root

# --- Stage 2: Build ---
FROM node:22-alpine AS builder
WORKDIR /app

ARG NEXT_PUBLIC_API_URL=""
ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL

# The release this bundle is built from. Baked in (not runtime) on purpose: it
# describes THIS build, so a runtime override would only ever be a lie about
# which panel the browser is running.
ARG NEXT_PUBLIC_RELEASE_VERSION=""
ENV NEXT_PUBLIC_RELEASE_VERSION=$NEXT_PUBLIC_RELEASE_VERSION

# Copy deps + workspace structure
COPY --from=deps /app/ ./

# Copy panel source code (overwrites package.json from deps, that's fine)
COPY panel/ ./panel/

# Delete panel's own lockfile to avoid Next.js workspace root confusion
RUN rm -f /app/panel/package-lock.json

WORKDIR /app/panel
RUN npx next build

# --- Stage 3: Runtime ---
FROM node:22-alpine
WORKDIR /app

COPY --from=builder /app/panel/.next/standalone ./
# Workspace standalone builds nest output under panel/ — flatten it
RUN if [ -f panel/server.js ] && [ ! -f server.js ]; then \
      cp -r panel/. . && rm -rf panel; \
    fi
COPY --from=builder /app/panel/.next/static ./.next/static
# public/ (incl. config.js, the runtime API-URL shim) is NOT part of the
# standalone output - Next.js only auto-copies it when building outside a
# workspace. Without this, /config.js 404s and the shim never runs.
COPY --from=builder /app/panel/public ./public

COPY panel/docker-entrypoint.sh ./docker-entrypoint.sh
RUN chmod +x ./docker-entrypoint.sh

# Non-root: the entrypoint only needs to (re)write ./public/config.js at
# container start; nothing else in this image writes to disk.
RUN addgroup -g 1001 -S nodejs && \
    adduser -S -u 1001 -G nodejs nextjs && \
    chown -R nextjs:nodejs /app
USER nextjs

ENV PORT=25510
ENV HOSTNAME="0.0.0.0"
EXPOSE 25510

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/" || exit 1

ENTRYPOINT ["./docker-entrypoint.sh"]
