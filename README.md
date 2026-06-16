<div align="center">

# DYLARIS

**Self-hostable, multi-node Minecraft server hosting platform & control panel.**

Provision, run, route, and scale Minecraft (vanilla, modded, and modpack) servers across one host or a whole fleet — from a single web panel.

</div>

---

## Table of Contents

- [What is DYLARIS?](#what-is-dylaris)
- [Features](#features)
- [Architecture](#architecture)
- [How it works](#how-it-works)
- [Quick start (single host)](#quick-start-single-host)
- [Deployment](#deployment)
  - [Single-host (`docker-compose.yml`)](#single-host-docker-composeyml)
  - [Docker Swarm (`docker-stack.yml`)](#docker-swarm-docker-stackyml)
  - [The compose files explained](#the-compose-files-explained)
- [Configuration reference](#configuration-reference)
- [Ports](#ports)
- [Scalability](#scalability)
- [Operations](#operations)
- [Development](#development)
- [Tech stack](#tech-stack)
- [Anonymous usage stats](#anonymous-usage-stats)

---

## What is DYLARIS?

DYLARIS is a service-oriented platform for hosting Minecraft servers at any scale. A web **Panel** (Next.js) drives a Go **Core** API, which orchestrates one or more Go **Node** agents. Each Node talks to its local Docker daemon to create, start, stop, and manage Minecraft server containers — so a "server" in DYLARIS is just a managed Docker container with persistent data, networking, and lifecycle handled for you. State lives in **PostgreSQL/TimescaleDB**; **Redis/Valkey** is the coordination bus (queues, pub/sub, discovery, settings).

It runs anywhere from a **single VPS** (everything on one host) up to a **Docker Swarm fleet** spanning multiple data-centre hosts and even NAT'd home/off-site machines (via the optional WireGuard bridge, *Warp*). Public domain-without-`ip:port` ingress is provided by a separate, optional **Gateway** repo (`dylaris-gateway`); without it, DYLARIS runs standalone in `ip_port` routing mode.

Everything is self-hosted: your servers, your data, your infrastructure.

## Features

- **Server lifecycle** — create, start/stop/kill, restart, delete; multiple *sub-servers* per slot with switching.
- **Modpack-as-a-server** — deploy a `.mrpack` modpack directly as a runnable server.
- **Modrinth integration** — in-panel mod browser, install mods into a server, plus a modpack **builder + publisher** (publish your own modpacks to Modrinth).
- **RCON & player management** — gRPC-backed RCON console, player list, ban/kick/op, plus an external API-key surface for automation.
- **File access** — in-panel file browser, plus pluggable file transport: **Beam** (overlay gRPC, no exposed IP) or **SFTP**.
- **Scheduled tasks** — cron-style restarts and console commands per server.
- **Spark profiler** — start/stop profiling and capture results from the panel.
- **Custom tabs** — attach iframe/popout tools to a server's UI.
- **Library** — shared storage for JARs, modpacks and assets (local multi-path or S3).
- **Live events** — Server-Sent Events over Redis Pub/Sub for real-time status (no polling).
- **Multi-tenant user management** — UUID users, username history + cooldowns, admin controls, API keys.
- **First-run setup wizard** — browser-driven first-admin creation, plus lost-admin recovery.
- **Warp** — pull external/home nodes behind NAT into the swarm over an encrypted WireGuard tunnel, and run servers on them as if they were in your DC. See [`docs/superpowers/warp-deploy.md`](../docs/superpowers/warp-deploy.md).
- **Optional Gateway stack** — public ingress/proxy (edge), hub and link services for routing player traffic without exposing node IPs. Lives in a separate repo (`dylaris-gateway`).

## Architecture

DYLARIS is a small set of independently deployable services that coordinate through **Redis/Valkey** (queues, pub/sub, discovery, settings) and a **gRPC mesh** (Core ↔ Node). State lives in **TimescaleDB** (PostgreSQL 16 + time-series for stats).

```
                                  ┌──────────────────────────────┐
              Browser  ─────────▶ │            PANEL             │  Next.js web UI (:25510)
                                  └───────────────┬──────────────┘
                                                  │ REST (browser → Core API)
                                                  ▼
   TimescaleDB ◀───────── SQL ─────────┐  ┌──────────────────────┐
   (Postgres 16)                       └──│         CORE         │  Go API (:25500) + gRPC (:25501)
                                          │  leader election,    │
   Redis / Valkey ◀── queues ────────────│  scheduler, auth,    │
   (queues / pub-sub / discovery)        │  RCON, SSE, library  │
        ▲   ▲                            └───────┬──────────────┘
        │   │                                    │ Redis queue: dylaris:node:{id}:queue
        │   │                                    │ + inbound gRPC mesh (node dials core)
        │   │                                    ▼
        │   └──────────────────────────┐  ┌──────────────────────┐
        └──────────────────────────────│──│         NODE         │  Go agent — drives local Docker
                                        │  │  creates/manages MC  │
                                        │  │  server containers   │
                                        │  └───────┬──────────────┘
                                        │          │ /var/run/docker.sock
                                        │          ▼
                                        │   ┌──────────────┐ ┌──────────────┐
                                        │   │ mc-server A  │ │ mc-server B  │  (one container per server,
                                        │   └──────────────┘ └──────────────┘   log-shipper as PID 1)
                                        │
              (optional) Gateway repo ──┘  edge (ingress) · hub · link · warp leader
```

### Services

| Service | Folder | Role |
|---|---|---|
| **Core** | `core/` | REST API (`:25500`) + gRPC mesh endpoint (`:25501`). Auth/JWT, scheduler, RCON, SSE, library, Beam-ticket signing, DB migrations, Redis-based leader election. |
| **Node** | `node/` | Per-host agent. Mounts `/var/run/docker.sock` to create/manage MC server containers; reads commands from its Redis queue and dials Core over gRPC; persists data to the `dylaris_data` volume. Hosts SFTP (`:25520`) and the Beam gRPC server (`:25521`). |
| **Panel** | `panel/` | Next.js (App Router) web UI (`:25510`). |
| **Log Shipper** | `log-shipper/` | Tiny wrapper binary that runs as **PID 1 inside each MC container**, wrapping the Java process and shipping stdout/stderr to a Redis stream. Built into the MC container image, not deployed as a standalone service. |
| **Agent** | `agent/` | A Go **library** (not a service) imported by Node for host CPU/RAM/network stats collection. No `main.go`. |
| TimescaleDB | (image) | `timescale/timescaledb:latest-pg16` — PostgreSQL 16 + TimescaleDB for relational data and time-series stats. The **source of truth**. |
| Redis/Valkey | (image) | `valkey/valkey:8-alpine` — in-memory coordination bus (queues, pub/sub, discovery, settings, stats streams). |

### How the services talk

- **Core ↔ Node — Redis queues + gRPC mesh.** Core pushes commands onto `dylaris:node:{id}:queue`. The Node dials *out* to Core over gRPC (`NodeService.NodeConnect`, a bidi stream) for auth and file operations — it never needs inbound reachability, which is what makes NAT'd / home nodes (Warp) possible.
- **Node ↔ Docker.** The Node mounts the host Docker socket and manages real containers (RAM limits, port allocation, persistent volumes).
- **File access.** `beam` uses an overlay gRPC transport (`:25521`, JWT-ticket gated, signed by Core, validated by Node) that never exposes the node IP. `sftp` exposes a classic SFTP server (`:25520`). External/Warp nodes force `beam`.
- **Live data.** MC console logs, stats, and player data stream back through Redis streams; the browser receives them as Server-Sent Events.
- **Optional Gateway (separate repo).** Cross-repo communication is exclusively through shared Redis keys/queues (e.g. `dylaris:hub:queue`, `route:{domain}`) — no direct HTTP/gRPC across the repo boundary. The Beam data plane (Beam client → relay → link → node) is the one separate data path.

### Data stores

- **TimescaleDB (Postgres 16)** — source of truth: users, servers, settings, audit, plus time-series stats. Back this up.
- **Redis/Valkey** — in-memory by design (`--save "" --appendonly no`). Losing it loses transient queue/discovery state, not your servers.

## How it works

1. **Provision** — an admin creates a server via the Panel → Core writes it to Postgres and pushes a `create` command onto the target Node's Redis queue.
2. **Setup** — the user picks a Java/loader/software (or a modpack), Core pushes a `setup` command, the Node installs it and the server goes `stopped`.
3. **Run** — start/stop/restart commands flow Core → Redis → Node → Docker. Console, stats, and player data stream back over Redis streams + SSE.
4. **Reach the server** — depending on the platform **routing mode**:
   - `ip_port` — the Node binds a host port (`PORT_RANGE_START`–`PORT_RANGE_END`); players connect to `node-ip:port`. This is the default and works without the Gateway.
   - `gateway` — the Node binds **no** host port; player traffic is routed through the optional Gateway **edge** via a reverse tunnel (no node IP exposed). Required for home/external (Warp) nodes.
   - `both` — direct ports *and* gateway routes.
5. **Files** — `sftp` exposes SFTP (`:25520`), `beam` uses an overlay gRPC transport (`:25521`) that never exposes the node IP. (External/Warp nodes force `gateway`+`beam` automatically via `NODE_EXTERNAL`.)

## Quick start (single host)

Everything on one machine (Docker + Docker Compose v2 required).

```bash
git clone https://github.com/Bartis-Dev/dylaris-platform.git
cd dylaris-platform

# 1. Configure secrets — at minimum set JWT_SECRET, CLUSTER_SECRET, DB_PASSWORD.
#    Core refuses to boot if JWT_SECRET or CLUSTER_SECRET is empty or left at
#    its placeholder default — set strong random values, e.g.:
cat > .env <<EOF
JWT_SECRET=$(openssl rand -hex 32)
CLUSTER_SECRET=$(openssl rand -hex 32)
DB_USER=dylaris
DB_PASSWORD=$(openssl rand -hex 24)
DB_NAME=dylaris
PANEL_API_URL=http://localhost:25500
EOF

# 2. Start the stack
docker compose up -d

# 3. Open the panel and run the first-run setup wizard
#    http://localhost:25510   →   /setup  (creates the first admin)
```

That's it — Core, a Node, the Panel, TimescaleDB and Valkey are now running. Create your first server from the Panel.

> **Production note:** put the Panel/Core behind a reverse proxy with TLS and set `PANEL_API_URL` to the **public** URL the browser will use to reach the Core API (e.g. `https://api.example.com`), because the browser — not the container — calls the API. Set `DB_SSLMODE=require` if the database is remote.

## Deployment

Two compose files are provided. They run the **same images** and differ only in topology.

### Single-host (`docker-compose.yml`)

All services on one host, a local **bridge** network, one Node. Best for a single VPS / homelab box.

```yaml
# ─────────────────────────────────────────────────────────────────────────────
# DYLARIS — SINGLE-HOST deployment
#
# Everything runs on ONE machine over a local bridge network.
# Best for a single VPS / homelab box.
#
#   cp .env.example .env   # then edit JWT_SECRET, CLUSTER_SECRET, DB_PASSWORD
#   docker compose up -d
#   open http://localhost:25510  →  /setup
#
# For a multi-host fleet, use docker-stack.yml (Docker Swarm) instead.
# ─────────────────────────────────────────────────────────────────────────────

services:
  core:
    image: ghcr.io/bartis-dev/dylaris-platform-core:latest
    restart: unless-stopped
    depends_on:
      timescaledb:
        condition: service_healthy
      redis:
        condition: service_started
    environment:
      API_PORT: "25500"
      FRONTEND_URL: "http://panel:25510"
      JWT_SECRET: "${JWT_SECRET}"
      CLUSTER_SECRET: "${CLUSTER_SECRET}"
      DYLARIS_REGION: "${DYLARIS_REGION:-default}"
      DB_HOST: timescaledb
      DB_PORT: "5432"
      DB_USER: "${DB_USER}"
      DB_PASSWORD: "${DB_PASSWORD}"
      DB_NAME: "${DB_NAME}"
      REDIS_ADDR: "redis:6379"
    ports:
      - "25500:25500"   # REST API
      - "25501:25501"   # gRPC node mesh (Cluster-Sync)
    networks:
      - dylaris_net

  node:
    image: ghcr.io/bartis-dev/dylaris-platform-node:latest
    restart: unless-stopped
    depends_on:
      - redis
    environment:
      NODE_ID: "${NODE_ID:-node-01}"
      CLUSTER_SECRET: "${CLUSTER_SECRET}"
      NODE_TAGS: "${NODE_TAGS:-}"
      NODE_REGION: "${NODE_REGION:-}"
      REDIS_ADDR: "redis:6379"
      PORT_RANGE_START: "${PORT_RANGE_START:-25600}"
      PORT_RANGE_END: "${PORT_RANGE_END:-30000}"
    volumes:
      # The Node drives the host Docker daemon to launch MC server containers.
      - /var/run/docker.sock:/var/run/docker.sock
      - dylaris_data:/app/dylaris_data
    # MC server host ports (ip_port / both routing modes) are published from the
    # container by the Node. Expose the configured range on the host:
    ports:
      - "25600-30000:25600-30000"
      - "25520:25520"   # SFTP (when file access = sftp/both)
    networks:
      - dylaris_net

  panel:
    image: ghcr.io/bartis-dev/dylaris-platform-panel:latest
    restart: unless-stopped
    environment:
      # Browser-reachable Core API URL (NOT the internal service name).
      NEXT_PUBLIC_API_URL: "${PANEL_API_URL:-http://localhost:25500}"
    ports:
      - "25510:25510"
    networks:
      - dylaris_net

  timescaledb:
    image: timescale/timescaledb:latest-pg16
    restart: unless-stopped
    environment:
      POSTGRES_USER: "${DB_USER}"
      POSTGRES_PASSWORD: "${DB_PASSWORD}"
      POSTGRES_DB: "${DB_NAME}"
    volumes:
      - timescaledb_data:/var/lib/postgresql/data
    healthcheck:
      # $$ escapes compose interpolation so the container shell expands
      # POSTGRES_USER/DB from its own environment.
      test: ["CMD-SHELL", "pg_isready -U $${POSTGRES_USER} -d $${POSTGRES_DB}"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 30s
    networks:
      - dylaris_net

  redis:
    # Valkey — a drop-in, Redis-compatible fork. Service name stays "redis"
    # so REDIS_ADDR=redis:6379 works everywhere. In-memory only (coordination
    # bus, not the source of truth — Postgres is).
    image: valkey/valkey:8-alpine
    restart: unless-stopped
    command: valkey-server --save "" --appendonly no
    networks:
      - dylaris_net

volumes:
  timescaledb_data:
  dylaris_data:

networks:
  dylaris_net:
    driver: bridge
```

```bash
docker compose up -d          # start
docker compose logs -f core   # tail
docker compose pull && docker compose up -d   # update
docker compose down           # stop (keeps volumes)
```

### Docker Swarm (`docker-stack.yml`)

Multi-host fleet on an **overlay** network with `deploy:` blocks (replicas, placement, restart policy, resource limits). Best for scaling Core/Panel and running Nodes across many machines.

> **Portainer:** paste `docker-stack.yml` into a new Stack and set the variables in the stack's **environment** editor (and secrets via the `*_FILE` pattern above) — the CLI `set -a; . ./.env` step below is only for `docker stack deploy` from a shell.

```yaml
# ─────────────────────────────────────────────────────────────────────────────
# DYLARIS — DOCKER SWARM deployment
#
# A multi-host fleet on an overlay network, with `deploy:` blocks
# (replicas, placement, restart policy, resources).
#
#   docker swarm init                                            # once, on the manager
#   docker node update --label-add dylaris.db=true <node-id>     # pin the DB host
#   set -a; . ./.env; set +a                                     # load env into the shell
#   docker stack deploy -c docker-stack.yml dylaris
#
#   docker stack services dylaris
#   docker service logs -f dylaris_core
#
# Join more hosts as Nodes:  `docker swarm join-token worker`  → run the printed
# command on each host. The `node` service is GLOBAL, so every host becomes a
# Node automatically (NODE_ID templated from the hostname).
#
# For a single machine, use docker-compose.yml instead.
# ─────────────────────────────────────────────────────────────────────────────

services:
  core:
    image: ghcr.io/bartis-dev/dylaris-platform-core:latest
    environment:
      API_PORT: "25500"
      FRONTEND_URL: "http://panel:25510"
      JWT_SECRET: "${JWT_SECRET}"
      CLUSTER_SECRET: "${CLUSTER_SECRET}"
      DYLARIS_REGION: "${DYLARIS_REGION:-default}"
      DB_HOST: timescaledb
      DB_PORT: "5432"
      DB_USER: "${DB_USER}"
      DB_PASSWORD: "${DB_PASSWORD}"
      DB_NAME: "${DB_NAME}"
      REDIS_ADDR: "redis:6379"
    ports:
      - "25500:25500"   # REST API
      - "25501:25501"   # gRPC node mesh (Cluster-Sync)
    networks:
      - dylaris_net
    deploy:
      # Multiple replicas are safe: Redis leader-election keeps singleton jobs
      # (discovery, Warp resync) on one replica; all replicas serve the API.
      replicas: 2
      placement:
        constraints: [node.role == manager]
      restart_policy:
        condition: any
      update_config:
        order: start-first
        parallelism: 1
      resources:
        limits:
          memory: 512M

  node:
    image: ghcr.io/bartis-dev/dylaris-platform-node:latest
    environment:
      # Templated per host → every swarm host is a distinct Node.
      NODE_ID: "{{.Node.Hostname}}"
      CLUSTER_SECRET: "${CLUSTER_SECRET}"
      NODE_TAGS: "${NODE_TAGS:-}"
      NODE_REGION: "${NODE_REGION:-}"
      REDIS_ADDR: "redis:6379"
      PORT_RANGE_START: "${PORT_RANGE_START:-25600}"
      PORT_RANGE_END: "${PORT_RANGE_END:-30000}"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - dylaris_data:/app/dylaris_data
    networks:
      - dylaris_net
    deploy:
      # One Node task per swarm host.
      mode: global
      restart_policy:
        condition: any
    # NOTE: external/home (Warp) nodes are deployed separately with a
    # `node.labels.dylaris_role == external` constraint + `NODE_EXTERNAL=true`.
    # See docs/superpowers/warp-deploy.md.

  panel:
    image: ghcr.io/bartis-dev/dylaris-platform-panel:latest
    environment:
      NEXT_PUBLIC_API_URL: "${PANEL_API_URL:-http://localhost:25500}"
    ports:
      - "25510:25510"
    networks:
      - dylaris_net
    deploy:
      replicas: 2
      restart_policy:
        condition: any
      update_config:
        order: start-first
        parallelism: 1

  timescaledb:
    image: timescale/timescaledb:latest-pg16
    environment:
      POSTGRES_USER: "${DB_USER}"
      POSTGRES_PASSWORD: "${DB_PASSWORD}"
      POSTGRES_DB: "${DB_NAME}"
    volumes:
      - timescaledb_data:/var/lib/postgresql/data
    networks:
      - dylaris_net
    deploy:
      # Single replica pinned to a labelled host (the named volume is node-local).
      # For real HA, point Core at an external/managed PostgreSQL instead.
      replicas: 1
      placement:
        constraints: [node.labels.dylaris.db == true]
      restart_policy:
        condition: any

  redis:
    # Valkey — drop-in Redis-compatible fork. Service name stays "redis" so
    # REDIS_ADDR=redis:6379 resolves. In-memory coordination bus.
    image: valkey/valkey:8-alpine
    command: valkey-server --save "" --appendonly no
    networks:
      - dylaris_net
    deploy:
      replicas: 1
      placement:
        constraints: [node.role == manager]
      restart_policy:
        condition: any

volumes:
  timescaledb_data:
  dylaris_data:

networks:
  dylaris_net:
    driver: overlay
    attachable: true
```

```bash
# On a manager node:
docker swarm init                                  # (once)

# Label the host that should run the database (its volume is node-local):
docker node update --label-add dylaris.db=true <manager-node-id>

# Deploy (env from your shell / an env file):
set -a; . ./.env; set +a
docker stack deploy -c docker-stack.yml dylaris

docker stack services dylaris                      # status
docker service logs -f dylaris_core                # tail
docker stack rm dylaris                            # remove

# Add worker (Node) hosts:
docker swarm join-token worker                     # on the manager, get the token
# then run the printed `docker swarm join ...` command on each new host.

# Scale:
docker service scale dylaris_core=3                # more API replicas (leader election handles singletons)
docker service scale dylaris_panel=2               # more panel replicas
```

In the stack, the `node` service is **global** — exactly one Node task runs on every swarm host, and its `NODE_ID` is templated from the hostname (`{{.Node.Hostname}}`), so every host becomes a distinct Node automatically.

**External / home nodes (Warp):** to add a NAT'd home machine as a Node, see [`docs/superpowers/warp-deploy.md`](../docs/superpowers/warp-deploy.md) — it joins over an encrypted WireGuard tunnel, is labelled `dylaris_role=external`, and runs with `NODE_EXTERNAL=true` so it forces gateway+beam locally (no exposed ports/SFTP).

### The compose files explained

Both files declare the same five services:

| Service | Image | Role |
|---|---|---|
| **core** | `ghcr.io/bartis-dev/dylaris-platform-core` | REST API (`:25500`) + gRPC mesh endpoint (`:25501`). Auth, scheduler, RCON, SSE, library, leader election. |
| **node** | `ghcr.io/bartis-dev/dylaris-platform-node` | Per-host agent. Mounts `/var/run/docker.sock` to create/manage MC server containers; persists data to the `dylaris_data` volume. |
| **panel** | `ghcr.io/bartis-dev/dylaris-platform-panel` | Next.js web UI (`:25510`). |
| **timescaledb** | `timescale/timescaledb:latest-pg16` | PostgreSQL 16 + TimescaleDB for relational data and time-series stats. Data on the `timescaledb_data` volume. |
| **redis** | `valkey/valkey:8-alpine` | In-memory store for command queues, pub/sub, service discovery, settings mirroring and stats streams. **Valkey** is a drop-in, Redis-compatible fork; the service keeps the hostname `redis` so `REDIS_ADDR=redis:6379` works everywhere. |

Notable details:

- **`node` needs the Docker socket** (`/var/run/docker.sock`) — that is how it launches Minecraft containers on its host. Treat any host running a Node as trusted.
- **`redis`/Valkey runs in-memory** (`--save "" --appendonly no`) — it is used as a coordination bus, not the source of truth (Postgres is). Losing it loses transient queue state, not your servers.
- **`timescaledb` has a healthcheck** (single-host) so Core waits for the DB to accept connections on first boot.
- **The Panel needs a browser-reachable Core URL** via `PANEL_API_URL` (`NEXT_PUBLIC_API_URL` inside the container), because the browser — not the container — calls the API.
- **Swarm DB is a single replica** pinned to a labelled host (`dylaris.db=true`) because the named volume is node-local. For real HA, point Core at an external/managed PostgreSQL.

## Configuration reference

All configuration is passed as **Docker environment variables** — set them in the service's `environment:` block (compose), the **stack environment** in Portainer, or `docker service ... --env` (Swarm). There are no CLI flags. A `.env` file is only a convenience for local `docker compose` (interpolated into `${VAR}`) and for local dev (the Go services also auto-load a `.env` via godotenv); production / Portainer deployments set env vars in the orchestrator, not a file on disk.

Every variable below maps to a real env read in the code; nothing is invented. Core **refuses to boot** if `JWT_SECRET` or `CLUSTER_SECRET` is empty or left at its placeholder.

### Secrets (Docker / Portainer secrets via `*_FILE`)

Every secret can be supplied from a **file** instead of a plain env value by setting `<NAME>_FILE` to a readable path. The service reads the file (whitespace-trimmed), so you can use Docker/Swarm secrets (`/run/secrets/...`) or Portainer secrets without ever putting the value in the environment. Precedence per secret: `<NAME>_FILE` → `<NAME>` → default; an unreadable or empty `*_FILE` logs and falls back rather than booting blank.

Supported:

- **Core:** `JWT_SECRET_FILE`, `CLUSTER_SECRET_FILE`, `DB_PASSWORD_FILE`, `REDIS_PASSWORD_FILE`
- **Node:** `CLUSTER_SECRET_FILE`, `REDIS_PASSWORD_FILE`, `SIDECAR_REDIS_PASSWORD_FILE`, `BEAM_JWT_SECRET_FILE`
- **Log shipper:** `REDIS_PASS_FILE`

Example (Swarm / Portainer with external secrets):

```yaml
services:
  core:
    environment:
      JWT_SECRET_FILE: /run/secrets/jwt_secret
      CLUSTER_SECRET_FILE: /run/secrets/cluster_secret
      DB_PASSWORD_FILE: /run/secrets/db_password
    secrets: [jwt_secret, cluster_secret, db_password]
secrets:
  jwt_secret:     { external: true }
  cluster_secret: { external: true }
  db_password:    { external: true }
```

### Core

| Variable | Default | Required | Description |
|---|---|---|---|
| `JWT_SECRET` | `change-this-secret` (rejected) | **Yes** | Signing key for panel auth tokens and Beam tickets. Must be a strong random value — boot fails on empty/placeholder. |
| `CLUSTER_SECRET` | `dylaris-cluster-secret` (rejected) | **Yes** | Shared secret authenticating Core ↔ Node ↔ Link ↔ Warp and deriving service keys. Same value across the whole cluster. Boot fails on empty/placeholder. |
| `DB_USER` | `postgres` | **Yes** | Postgres user. |
| `DB_PASSWORD` | *(empty)* | **Yes** | Postgres password. |
| `DB_NAME` | `dylaris` | Recommended | Postgres database name. |
| `DB_HOST` | `localhost` | No (set in compose: `timescaledb`) | Postgres host. |
| `DB_PORT` | `5432` | No | Postgres port. |
| `DB_SSLMODE` | `disable` | No | Postgres TLS mode (`disable`/`require`/`verify-full`). Set `require`/`verify-full` for a remote DB. |
| `API_PORT` | `25500` | No | Core REST API port. |
| `DYLARIS_GRPC_PORT` | `25501` | No | Core gRPC mesh port (Core ↔ Node). |
| `DYLARIS_CORE_ID` | *(hostname)* | No | Identifier for this Core instance; falls back to the OS hostname. |
| `DYLARIS_REGION` | `default` | No | Region label stamped into heartbeat + system info. |
| `FRONTEND_URL` | `http://localhost:25510` (compose: `http://panel:25510`) | No | Panel origin Core trusts for CORS and uses to build email links (verify/reset). For a **cross-origin** deployment set it to the public panel URL (e.g. `https://panel.example.com`) so CORS accepts it; for a **same-origin** reverse-proxy layout it is not needed for CORS. Host-level config — kept as env. |
| `REDIS_ADDR` | `localhost:6379` (compose: `redis:6379`) | No | Redis/Valkey address. |
| `REDIS_USER` | *(empty)* | No | Redis/Valkey username (ACL). |
| `REDIS_PASSWORD` | *(empty)* | No | Redis/Valkey password. |
| `REDIS_DB` | `0` | No | Redis/Valkey logical DB index. |
| `EXTERNAL_TICKET_DB_URL` | *(empty)* | No | Optional external ticket DB URL; surfaces as a target in the migration/backup/restore UI. Live queries always hit the main DB. |
| `DYLARIS_TELEMETRY` | *(unset = on)* | No | Set to `false` to hard-disable anonymous usage stats (bypasses the in-panel toggle). See [Anonymous usage stats](#anonymous-usage-stats). |
| `DYLARIS_RESET_ADMINS` | *(empty)* | No | Break-glass: set to a new nonce to demote all admins + wipe 2FA on boot, enabling lost-admin recovery. Change the value to reset again. |

### Node

| Variable | Default | Required | Description |
|---|---|---|---|
| `CLUSTER_SECRET` | *(none — fatal)* | **Yes** | Must match Core. Node exits if missing. |
| `REDIS_ADDR` | *(none — fatal)* | **Yes** | Redis/Valkey address. Node exits if missing. |
| `NODE_ID` | *(hostname)* | No | Unique id for this Node. In Swarm, templated from the hostname. Falls back to OS hostname. |
| `NODE_TAGS` | *(empty)* | No | Comma-separated placement tags (e.g. `eu,fast`). The tag `external` flags a home/external node. |
| `NODE_REGION` | *(empty)* | No | Region this Node belongs to. |
| `NODE_EXTERNAL` | `false` | No | If `true` (or `NODE_TAGS` contains `external`), the Node forces `gateway` routing + `beam` file access locally (no host ports, no SFTP). |
| `REDIS_USER` | *(empty)* | No | Redis/Valkey username. |
| `REDIS_PASSWORD` | *(empty)* | No | Redis/Valkey password. |
| `REDIS_DB` | `0` | No | Redis/Valkey logical DB index. |
| `SIDECAR_REDIS_ADDR` | *(falls back to `REDIS_ADDR`)* | No | Redis address handed to MC containers, which can't resolve Swarm overlay DNS. Set to the leader node's private IP in Swarm. |
| `SIDECAR_REDIS_USER` | *(falls back to `REDIS_USER`)* | No | Redis username for MC containers. |
| `SIDECAR_REDIS_PASSWORD` | *(falls back to `REDIS_PASSWORD`)* | No | Redis password for MC containers. |
| `SIDECAR_REDIS_DB` | *(falls back to `REDIS_DB`)* | No | Redis DB index for MC containers. |
| `PORT_RANGE` | *(unset)* | No | Host port range as `START-END` (e.g. `25600-30000`). Takes precedence over the split vars below. |
| `PORT_RANGE_START` | `25600` | No | Start of host port range for MC servers (`ip_port`/`both`). Ignored if `PORT_RANGE` is set. |
| `PORT_RANGE_END` | `30000` | No | End of host port range for MC servers. Ignored if `PORT_RANGE` is set. |
| `SFTP_PORT` | `25520` | No | SFTP server port (file access `sftp`/`both`). |
| `BEAM_GRPC_PORT` | `25521` | No | Beam file-transfer gRPC server port. |
| `MIGRATION_PORT` | `25522` | No | Auto-move pull endpoint: the source node serves the staged archive to the target node, authenticated by a CLUSTER_SECRET-derived HMAC token. |
| `BEAM_JWT_SECRET` | *(empty — Beam rejects all tickets)* | No (required for Beam) | Must match the gateway beam-relay's JWT secret so relay-validated tickets pass the node-side gate. |
| `DYLARIS_CPUSET_CPUS` | *(empty)* | No | Default `cpuset-cpus` CPU pinning applied to all MC containers on this node. |
| `STORAGE_PATHS` | `./dylaris_data/servers` | No | Comma-separated list of storage roots (multi-disk). |
| `DYLARIS_STATS_BUFFER_MAXLEN` | `1800` | No | MaxLen of the per-server stats buffer stream (~1h at 2s). Reduce for very large fleets. |
| `STATS_STREAM_MAXLEN` | `360` | No | MaxLen of the node system-stats stream (~3h at 30s). |

### Panel

| Variable | Default | Required | Description |
|---|---|---|---|
| `PANEL_API_URL` → `NEXT_PUBLIC_API_URL` | *(same origin)* | No | **Browser-reachable** Core API base URL (build-time; the compose maps `PANEL_API_URL` into `NEXT_PUBLIC_API_URL`). If unset, the panel defaults to the **same origin** it is served from (`https://<panel-host>/api`) — the usual reverse-proxy layout where `/api` is routed to Core. Override at runtime (no rebuild) via the shim below. |

The panel resolves its API URL in this order: `window.__DYLARIS_CONFIG__.apiUrl` (runtime) → `NEXT_PUBLIC_API_URL` (build-time) → same origin (`/api`). The runtime value lives in **`/config.js`** (served from the panel image's `public/`), so a self-hoster can point a prebuilt image at a different API host by editing or bind-mounting that one file — no rebuild needed. Leave `apiUrl` empty for same-origin:

```js
// config.js (bind-mount to override)
window.__DYLARIS_CONFIG__ = { apiUrl: "https://api.example.com" };
```

### Log Shipper (inside the MC container)

These are set by the Node when it launches a container; they are listed for completeness. Note `REDIS_PASS` (not `REDIS_PASSWORD`) here.

| Variable | Default | Description |
|---|---|---|
| `SERVER_UUID` | *(empty — required)* | Server UUID; used to build the log stream key `dylaris:server:{uuid}:logs`. |
| `SUB_SERVER` | *(empty)* | Optional sub-server name appended to the log stream key. |
| `REDIS_ADDR` | `localhost:6379` | Redis/Valkey address. |
| `REDIS_USER` | *(empty)* | Redis/Valkey username. |
| `REDIS_PASS` | *(empty)* | Redis/Valkey password. |
| `REDIS_DB` | `0` | Redis/Valkey logical DB index. |

## Ports

| Port | Service | Purpose |
|---|---|---|
| `25500` | core | REST API (`API_PORT`) |
| `25501` | core | gRPC node mesh / Cluster-Sync (`DYLARIS_GRPC_PORT`) |
| `25510` | panel | Web UI |
| `25520` | node | SFTP (`SFTP_PORT`; file access = `sftp`/`both`) |
| `25521` | node | Beam gRPC (`BEAM_GRPC_PORT`; overlay-only, JWT-gated) |
| `25522` | node | Auto-move pull endpoint (`MIGRATION_PORT`; CLUSTER_SECRET-HMAC) |
| `25600–30000` | node | MC server host ports (`PORT_RANGE_START`–`PORT_RANGE_END`; `ip_port`/`both` routing) |

> The optional Gateway stack adds public ingress ports (`25565` Minecraft, `80`/`443` HTTP(S)) and the Warp leader (`25599/udp`) — see the `dylaris-gateway` repo.

## Reverse proxy and TLS

Keep Core, the Panel, Nodes, Postgres and Redis on a private network and put **one reverse proxy** in front for public TLS. Two layouts:

**Same-origin (recommended).** The proxy serves the Panel at `https://panel.example.com` and routes `/api` (and `/api/system/events` for SSE) on that same host to `core:25500`. The Panel then talks to its own origin (`/api`) — no `NEXT_PUBLIC_API_URL`, no `config.js`, and **no CORS** to configure. Auth is Bearer-token, so there is no cookie/CSRF surface to widen.

**Cross-origin.** Panel and API on different hostnames (e.g. `panel.example.com` + `api.example.com`). Then point the Panel at the API (`config.js` `apiUrl` or build-time `NEXT_PUBLIC_API_URL`) **and** set Core's `FRONTEND_URL` to the panel origin so CORS accepts it.

TLS is terminated at the proxy (Let's Encrypt). Core and the Panel speak plain HTTP behind it. For a remote database set `DB_SSLMODE=require` (or `verify-full`).

### Nginx Proxy Manager (the reference production setup)

Add a **Proxy Host** for `panel.example.com` → `panel:25510`, request a Let's Encrypt cert, then under **Custom locations** add `/api` → `core:25500`. Enable **Websockets support** on the host: the Panel uses Server-Sent Events (system events, live console) which must not be buffered or short-timed out. In NPM's *Advanced* tab:

```nginx
location /api/ {
    proxy_pass http://core:25500;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;          # required for SSE (events, console stream)
    proxy_read_timeout 1h;        # keep long-lived SSE connections open
}
```

### Traefik (alternative)

Terminate TLS with a cert resolver and route by host/path. Example labels on the Core/Panel services:

```yaml
labels:
  - "traefik.enable=true"
  # Panel
  - "traefik.http.routers.dylaris-panel.rule=Host(`panel.example.com`)"
  - "traefik.http.routers.dylaris-panel.tls.certresolver=le"
  - "traefik.http.services.dylaris-panel.loadbalancer.server.port=25510"
  # API on the same host under /api (same-origin)
  - "traefik.http.routers.dylaris-api.rule=Host(`panel.example.com`) && PathPrefix(`/api`)"
  - "traefik.http.routers.dylaris-api.tls.certresolver=le"
  - "traefik.http.services.dylaris-api.loadbalancer.server.port=25500"
```

Traefik streams responses by default (no extra SSE buffering tweak needed).

### Gateway ingress

Minecraft and gateway-routed HTTP go through the separate **Gateway** stack, not this proxy: the Edge maps public `80`/`443`/`25565` to `25561`/`25562`/Edge. If you front the Edge with a reverse proxy, point it at `Edge:25561` (HTTP) / `Edge:25562` (HTTPS) — see the `dylaris-gateway` repo.

## Scalability

DYLARIS scales on three axes:

- **More API throughput → scale Core.** Run N Core replicas behind a load balancer. Redis leader election keeps singleton jobs (discovery, Warp resync) on one replica while all replicas serve traffic. `docker service scale dylaris_core=N`.
- **More server capacity → add Nodes.** Every Docker host you join to the swarm becomes a Node (global service) and a scheduling target. Use **regions** + **tags** to steer placement; the scheduler picks a node by region/tag/capacity.
- **Reach beyond the DC → Warp.** Pull NAT'd home/off-site machines into the overlay over WireGuard and run servers on them; player traffic is routed back through the DC edge.

**State & HA notes**

- **Postgres is the source of truth.** For real HA, point Core at an external/managed PostgreSQL (or a replicated TimescaleDB) instead of the bundled single-replica container — the in-stack DB volume is node-local.
- **Redis/Valkey** is a coordination bus (in-memory by design). A single replica is fine; for HA, run Valkey with replication/Sentinel and update `REDIS_ADDR`.
- **Ingress** scales via the separate Gateway stack (multiple edges), keeping node IPs private.

## Operations

```bash
# Update to the latest images
docker compose pull && docker compose up -d                                              # single host
docker service update --image ghcr.io/bartis-dev/dylaris-platform-core:latest dylaris_core   # swarm (per service)

# Logs
docker compose logs -f core                            # single host
docker service logs -f dylaris_core                    # swarm

# Backups (most important: the database)
docker compose exec timescaledb pg_dump -U "$DB_USER" "$DB_NAME" > dylaris-backup.sql
```

Server files live on the Node host under the `dylaris_data` volume; back that up alongside the database.

## Development

The backend is a Go workspace (`go.work`) with `core`, `node`, the `log-shipper`, the `agent` library, and shared `pkg`/`proto` modules. The frontend is a Next.js app in `panel/`.

```bash
# Backend (Go workspace)
go work sync
go build ./core         # → dylaris-core(.exe)
go build ./node         # → dylaris-node(.exe)
go test ./...
cd log-shipper && ./build_shipper.sh

# Frontend
cd panel && npm install
cd panel && npm run dev      # dev server
cd panel && npm run build    # production build
```

**Code conventions**

- **Go:** keep the **Handlers / Services / Store** separation strict. Always handle errors (`if err != nil`, wrap with `fmt.Errorf("...: %w", err)`); no runtime panics. After changing any module, run `go work sync`. Platform and Gateway are separate workspaces — never import across the repo boundary; cross-repo communication is only via shared Redis keys/queues.
- **Panel (Next.js, App Router):** use the App Router (`/app`) — no Pages Router. Server Components are the default; add `'use client'` only when you need state/effects/browser APIs. TypeScript strict, with interfaces for props and API responses.
- **Tailwind v4:** there is **no** `tailwind.config.{js,ts}`. Theme lives in the `@theme` directive in `panel/src/app/globals.css` (the single source of truth for design tokens — colors, spacing, radius, fonts). Don't scatter hardcoded values.
- **Secrets** are env-only; never hardcode them.

## Tech stack

- **Backend:** Go (gorilla/mux, gRPC, go-redis), structured as a `go.work` workspace (`core`, `node`, plus the log-shipper and shared libraries).
- **Frontend:** Next.js (App Router) + TypeScript + Tailwind CSS v4.
- **Data:** PostgreSQL 16 / TimescaleDB, Redis-compatible **Valkey**.
- **Runtime:** Docker / Docker Compose / Docker Swarm.

## Anonymous usage stats

Each Dylaris Core sends a tiny anonymized payload to `dylaris.dev/api/heartbeat` every 10 min so a public live counter on the website can show *"N platforms · N gateways · N containers · N players online"*. The payload contains: a hash of the Core ID (so the hostname is never exposed), instance type, container counts, total players, and version. No user data, no server names, no IPs.

Disable any time: **Settings → Features → "Anonymous Usage Stats"** toggle, or set `DYLARIS_TELEMETRY=false` (hard kill, bypasses the DB toggle).

---

<div align="center">
Maintained by <a href="https://github.com/Bartis-Dev">Bartis-Dev</a>. Self-host it, own it.
</div>
