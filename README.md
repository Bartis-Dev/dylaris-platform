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
- [Deployment variants](#deployment-variants)
  - [Single-host (`docker-compose.yml`)](#1-single-host-docker-composeyml)
  - [Docker Swarm (`docker-stack.yml`)](#2-docker-swarm-docker-stackyml)
- [The compose files explained](#the-compose-files-explained)
- [Configuration reference](#configuration-reference)
- [Ports](#ports)
- [Scalability](#scalability)
- [Operations](#operations)
- [Tech stack](#tech-stack)

---

## What is DYLARIS?

DYLARIS is a service-oriented platform for hosting Minecraft servers at any scale. A web **Panel** drives a Go **Core** API, which orchestrates one or more **Node** agents. Each Node talks to its local Docker daemon to create, start, stop, and manage Minecraft server containers — so a "server" in DYLARIS is just a managed Docker container with persistent data, networking, and lifecycle handled for you.

It is built to run anywhere from a **single VPS** (everything on one host) up to a **Docker Swarm fleet** spanning multiple data-centre nodes and even NAT'd home/off-site machines (via the optional WireGuard bridge, *Warp*).

Everything is self-hosted: your servers, your data, your infrastructure.

## Features

- **Server lifecycle** — create, start/stop/kill, restart, delete; multiple *sub-servers* per slot with switching.
- **Modpack-as-a-server** — deploy a `.mrpack` modpack directly as a runnable server.
- **Modrinth integration** — in-panel mod browser, install mods into a server, and a modpack **builder + publisher** (publish your own modpacks to Modrinth).
- **RCON & player management** — gRPC-backed RCON console, player list, ban/kick/op, plus an external API-key surface for automation.
- **File access** — in-panel file browser, plus pluggable file transport: **Beam** (overlay, no exposed IP) or **SFTP**.
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

DYLARIS is a small set of independently deployable services that coordinate through **Redis/Valkey** (queues, pub/sub, discovery, settings) and a **gRPC mesh** (Core ↔ Node). State lives in **TimescaleDB** (PostgreSQL + time-series for stats).

```
                                  ┌──────────────────────────────┐
              Browser  ─────────▶ │            PANEL             │  Next.js web UI (:25510)
                                  └───────────────┬──────────────┘
                                                  │ REST
                                                  ▼
   TimescaleDB ◀───────── SQL ─────────┐  ┌──────────────────────┐
   (Postgres 16)                       └──│         CORE         │  Go API (:25500) + gRPC (:25520)
                                          │  leader election,    │
   Redis / Valkey ◀── queues ────────────│  scheduler, auth,    │
   (queues / pub-sub / discovery)        │  RCON, SSE, library  │
        ▲   ▲                            └───────┬──────────────┘
        │   │                                    │ Redis queue: dylaris:node:{id}:queue
        │   │                                    │ + outbound gRPC mesh (node → core)
        │   │                                    ▼
        │   └──────────────────────────┐  ┌──────────────────────┐
        └──────────────────────────────│──│         NODE         │  Go agent — drives local Docker
                                        │  │  creates/manages MC  │
                                        │  │  server containers   │
                                        │  └───────┬──────────────┘
                                        │          │ docker.sock
                                        │          ▼
                                        │   ┌──────────────┐ ┌──────────────┐
                                        │   │ mc-server A  │ │ mc-server B  │  (one container per server)
                                        │   └──────────────┘ └──────────────┘
                                        │
              (optional) Gateway stack ─┘  edge (ingress) · hub · link · warp leader
```

**Key ideas**

- **Core is stateless-ish and horizontally scalable.** Run several Core replicas; a Redis-based **leader election** ensures singleton work (node discovery, the Warp resync watcher) runs on exactly one Core, while every replica serves the API.
- **Nodes are outbound-only.** A Node dials *out* to Core over gRPC and reads commands from a Redis queue — it never needs inbound reachability. This is what makes NAT'd / home nodes (Warp) possible.
- **One container per server.** The Node mounts the host Docker socket and manages real containers, with RAM limits, port allocation, and persistent volumes.

## How it works

1. **Provision** — an admin creates a server via the Panel → Core writes it to Postgres and pushes a `create` command onto the target Node's Redis queue.
2. **Setup** — the user picks a Java/loader/software (or a modpack), Core pushes a `setup` command, the Node installs it and the server goes `stopped`.
3. **Run** — start/stop/restart commands flow Core → Redis → Node → Docker. Console, stats, and player data stream back over Redis streams + SSE.
4. **Reach the server** — depending on the platform **routing mode**:
   - `ip_port` — the Node binds a host port (`PORT_RANGE_START`–`PORT_RANGE_END`); players connect to `node-ip:port`.
   - `gateway` — the Node binds **no** host port; player traffic is routed through the optional Gateway **edge** via a reverse tunnel (no node IP exposed). Required for home/external (Warp) nodes.
   - `both` — direct ports *and* gateway routes.
5. **Files** — `sftp` exposes SFTP (:2222), `beam` uses an overlay transport that never exposes the node IP. (External/Warp nodes force `gateway`+`beam` automatically.)

## Quick start (single host)

Everything on one machine (Docker + Docker Compose v2 required).

```bash
git clone https://github.com/Bartis-Dev/dylaris-platform.git
cd dylaris-platform

# 1. Configure secrets
cp .env.example .env
#   edit .env — at minimum set JWT_SECRET, CLUSTER_SECRET, DB_PASSWORD

# 2. Start the stack
docker compose up -d

# 3. Open the panel and run the first-run setup wizard
#    http://localhost:25510   →   /setup  (creates the first admin)
```

That's it — Core, a Node, the Panel, TimescaleDB and Valkey are now running. Create your first server from the Panel.

> **Production note:** put the Panel/Core behind a reverse proxy with TLS and set `PANEL_API_URL` to the **public** URL the browser will use to reach the Core API (e.g. `https://panel.example.com:25500`).

## Deployment variants

Two compose files are provided. They run the **same images** and differ only in topology.

### 1. Single-host (`docker-compose.yml`)

All services on one host, a local **bridge** network, one Node. Best for a single VPS / homelab box.

```bash
docker compose up -d          # start
docker compose logs -f core   # tail
docker compose pull && docker compose up -d   # update
docker compose down           # stop (keeps volumes)
```

### 2. Docker Swarm (`docker-stack.yml`)

Multi-host fleet on an **overlay** network with `deploy:` blocks (replicas, placement, restart policy, resource limits). Best for scaling Core/Panel and running Nodes across many machines.

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
```

**Adding worker (Node) hosts:**

```bash
# On the manager, get the worker join token:
docker swarm join-token worker
# On each new host, run the printed `docker swarm join ...` command.
```

In the stack, the `node` service is **global** — exactly one Node task runs on every swarm host, and its `NODE_ID` is templated from the hostname (`{{.Node.Hostname}}`), so every host becomes a distinct Node automatically.

**Scaling examples:**

```bash
docker service scale dylaris_core=3      # more API replicas (leader election handles singletons)
docker service scale dylaris_panel=2     # more panel replicas
```

**External / home nodes (Warp):** to add a NAT'd home machine as a Node, see [`docs/superpowers/warp-deploy.md`](../docs/superpowers/warp-deploy.md) — it joins over an encrypted WireGuard tunnel, is labelled `dylaris_role=external`, and runs with `NODE_EXTERNAL=true` so it forces gateway+beam locally (no exposed ports/SFTP).

## The compose files explained

Both files declare the same five services:

| Service | Image | Role |
|---|---|---|
| **core** | `ghcr.io/callmebartis/dylaris/core` | REST API (`:25500`) + gRPC mesh endpoint (`:25520`). Auth, scheduler, RCON, SSE, library, leader election. |
| **node** | `ghcr.io/callmebartis/dylaris/node` | Per-host agent. Mounts `/var/run/docker.sock` to create/manage MC server containers; persists data to the `dylaris_data` volume. |
| **panel** | `ghcr.io/callmebartis/dylaris/panel` | Next.js web UI (`:3000`, published on `:25510`). |
| **timescaledb** | `timescale/timescaledb:latest-pg16` | PostgreSQL 16 + TimescaleDB for relational data and time-series stats. Data on the `timescaledb_data` volume. |
| **redis** | `valkey/valkey:8-alpine` | In-memory store for command queues, pub/sub, service discovery, settings mirroring and stats streams. **Valkey** is a drop-in, Redis-compatible fork; the service keeps the hostname `redis` so `REDIS_ADDR=redis:6379` works everywhere. |

Notable details:

- **`node` needs the Docker socket** (`/var/run/docker.sock`) — that is how it launches Minecraft containers on its host. Treat any host running a Node as trusted.
- **`redis`/Valkey runs in-memory** (`--save "" --appendonly no`) — it is used as a coordination bus, not the source of truth (Postgres is). Losing it loses transient queue state, not your servers.
- **`timescaledb` has a healthcheck** so Core waits for the DB to accept connections on first boot.
- **The Panel needs a browser-reachable Core URL** via `PANEL_API_URL` (`NEXT_PUBLIC_API_URL` inside the container), because the browser — not the container — calls the API.

## Configuration reference

Set these in `.env` (single-host) or your shell/secret store (swarm). See [`.env.example`](.env.example).

### Core

| Variable | Default | Description |
|---|---|---|
| `JWT_SECRET` | — | **Required.** Signing key for panel auth tokens. Use a long random string. |
| `CLUSTER_SECRET` | — | **Required.** Shared secret authenticating Core ↔ Node ↔ Link ↔ Warp, and deriving service keys. Same value across the whole cluster. |
| `API_PORT` | `25500` | Core REST API port. |
| `DB_HOST` / `DB_PORT` | `timescaledb` / `5432` | Postgres host/port. |
| `DB_USER` / `DB_PASSWORD` / `DB_NAME` | — | **Required.** Postgres credentials/database. |
| `REDIS_ADDR` | `redis:6379` | Redis/Valkey address. |
| `DYLARIS_REGION` | `default` | Region label for this Core. |
| `FRONTEND_URL` | `http://panel:3000` | Internal panel URL (CORS / links). |
| `GATEWAY_ENABLED` | `false` | Enable integration with the optional Gateway stack. |

### Node

| Variable | Default | Description |
|---|---|---|
| `NODE_ID` | `node-01` | Unique id for this Node. In Swarm, templated from the hostname. |
| `CLUSTER_SECRET` | — | **Required.** Must match Core. |
| `REDIS_ADDR` | `redis:6379` | Redis/Valkey address. |
| `NODE_TAGS` | — | Comma-separated placement tags (e.g. `eu,fast`). The tag `external` flags a home/external node. |
| `NODE_REGION` | — | Region this Node belongs to. |
| `NODE_EXTERNAL` | `false` | If `true` (or tag `external`), the Node forces `gateway`+`beam` locally (no host ports / no SFTP). |
| `PORT_RANGE_START` / `PORT_RANGE_END` | `25600` / `30000` | Host port range for MC servers in `ip_port`/`both` routing mode. |

### Panel

| Variable | Default | Description |
|---|---|---|
| `PANEL_API_URL` | `http://localhost:25500` | **Browser-reachable** Core API base URL (becomes `NEXT_PUBLIC_API_URL`). Set to your public URL in production. |

## Ports

| Port | Service | Purpose |
|---|---|---|
| `25500` | core | REST API |
| `25520` | core | gRPC node mesh |
| `25510` | panel | Web UI |
| `25600–30000` | node | MC server host ports (`ip_port`/`both` routing) |
| `2222` | node | SFTP (when file access = `sftp`/`both`) |

> The optional Gateway stack adds public ingress ports (`25565` Minecraft, `80`/`443` HTTP(S)) and the Warp leader (`51820/udp`) — see the `dylaris-gateway` repo.

## Scalability

DYLARIS scales on three axes:

- **More API throughput → scale Core.** Run N Core replicas behind a load balancer. Redis leader election keeps singleton jobs (discovery, Warp resync) on one replica while all replicas serve traffic. `docker service scale dylaris_core=N`.
- **More server capacity → add Nodes.** Every Docker host you join to the swarm becomes a Node (global service) and a scheduling target. Use **regions** + **tags** + per-node CPU/RAM overcommit ratios to steer placement; the scheduler picks a node by region/tag/capacity.
- **Reach beyond the DC → Warp.** Pull NAT'd home/off-site machines into the overlay over WireGuard and run servers on them; player traffic is routed back through the DC edge. Great for harnessing strong home hardware without exposing it.

**State & HA notes**

- **Postgres is the source of truth.** For real HA, point Core at an external/managed PostgreSQL (or a replicated TimescaleDB) instead of the bundled single-replica container — the in-stack DB volume is node-local.
- **Redis/Valkey** is a coordination bus (in-memory by design). A single replica is fine; for HA, run Valkey with replication/Sentinel and update `REDIS_ADDR`.
- **Ingress** scales via the separate Gateway stack (multiple edges), keeping node IPs private.

## Operations

```bash
# Update to the latest images
docker compose pull && docker compose up -d            # single host
docker service update --image ghcr.io/callmebartis/dylaris/core:latest dylaris_core   # swarm (per service)

# Logs
docker compose logs -f core                            # single host
docker service logs -f dylaris_core                    # swarm

# Backups (most important: the database)
docker compose exec timescaledb pg_dump -U "$DB_USER" "$DB_NAME" > dylaris-backup.sql
```

Server files live on the Node host under the `dylaris_data` volume; back that up alongside the database.

## Tech stack

- **Backend:** Go (gorilla/mux, gRPC, go-redis), structured as a `go.work` workspace (`core`, `node`, plus microservices).
- **Frontend:** Next.js (App Router) + TypeScript + Tailwind CSS v4.
- **Data:** PostgreSQL 16 / TimescaleDB, Redis-compatible **Valkey**.
- **Runtime:** Docker / Docker Compose / Docker Swarm.

---

<div align="center">
Maintained by <a href="https://github.com/Bartis-Dev">Bartis-Dev</a>. Self-host it, own it.
</div>
