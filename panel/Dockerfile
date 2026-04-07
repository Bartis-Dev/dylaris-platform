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
COPY beam/app/frontend/package.json ./beam/app/frontend/package.json

RUN npm install --workspaces --include-workspace-root

# --- Stage 2: Build ---
FROM node:22-alpine AS builder
WORKDIR /app

ARG NEXT_PUBLIC_API_URL="https://api.dylaris.com/api"
ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL

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

ENV PORT=25510
ENV HOSTNAME="0.0.0.0"
EXPOSE 25510

CMD ["node", "server.js"]
