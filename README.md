<div align="center">

# DYLARIS

**Self-hostable, multi-node Minecraft server hosting platform & control panel.**

Provision, run, route, and scale Minecraft (vanilla, modded, and modpack) servers across one host or a whole fleet — from a single web panel.

[**dylaris.com**](https://dylaris.com)

</div>

> ### Beta, and under active development
>
> DYLARIS is self-hostable and usable today, but it has not reached a stable release. Expect breaking changes between images, schema migrations that run on deploy, and features that are still moving. If you need a fixed target, pin an image tag instead of `latest`.
>
> **[dylaris.com](https://dylaris.com) is the project home and the documentation site**, and it is where the long-form guides are going as they are written. This README deliberately stays at what you need to get running and understand the shape of the system: the [quick start](#quick-start-single-host), both [deployment topologies](#deployment), and a [complete environment variable reference](#configuration-reference) covering every variable the code actually reads. Deploying from this file alone works today.
>
> Bug reports and questions are welcome. If anything here is wrong, unclear or incomplete, that counts as a bug worth reporting.

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
- [Beam desktop client](#beam-desktop-client)
- [Development](#development)
- [Tech stack](#tech-stack)
- [No telemetry](#no-telemetry)

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
- **Warp** - pull external/home nodes behind NAT into the swarm over an encrypted WireGuard tunnel, and run servers on them as if they were in your DC. See `NODE_EXTERNAL` / `NODE_TAGS` in [Configuration reference](#configuration-reference).
- **Optional Gateway stack** — public ingress/proxy (edge), hub and link services for routing player traffic without exposing node IPs. Lives in a separate repo (`dylaris-gateway`).

### Solder API (Technic Launcher)

Core serves a Solder-compatible API for the Technic Launcher: a PUBLIC,
UNAUTHENTICATED route tree at `/solder/api/...` plus a mod/loader-zip mirror at
`/solder/mirror/...`, both on the Core port (`core/routes.go`). These bypass the
setup-lock, maintenance, and auth middleware by design — the launcher must reach
published packs at all times — and gate the modpacks feature in-handler instead.

The `solder_delivery_mode` setting (default `core`) controls how mod download
URLs are served: `core` streams through Core's own mirror route; `presigned`
hands back short-lived signed URLs from the configured S3/R2 backend;
`public` advertises an operator-configured external mirror URL directly. A
read-only probe, `GET /api/admin/settings/modpacks/delivery-capabilities`,
reports which modes the current storage backend actually supports.

**Warning:** in `public` mode, a private/hidden pack's mod files still live in
the same publicly readable bucket as public packs — Core simply avoids handing
out that direct URL through its own API for a gated pack, but the file is not
otherwise protected if the URL is discovered or guessed. Use `presigned` when
private packs must stay confidential.

## Architecture

DYLARIS is a small set of independently deployable services that coordinate through **Redis/Valkey** (queues, pub/sub, discovery, settings) and a **gRPC mesh** (Core ↔ Node). State lives in **TimescaleDB** (PostgreSQL 16 + time-series for stats).

```
   Browser
      |
      v
   +-----------+   REST    +--------------------------+
   |   PANEL   | --------> |           CORE           |
   |   :25510  |           |  :25500 API  :25501 gRPC |
   +-----------+           +--------------------------+
                             |          |          |
                    SQL      |          |          |   queues, pub/sub,
                             v          |          v   discovery, settings
                  +---------------+     |    +----------------+
                  |  TimescaleDB  |     |    | Redis / Valkey |
                  |  Postgres 16  |     |    +----------------+
                  +---------------+     |          ^
                                        |          |  the node shares the
            Redis queue for work,       |          |  same Redis for its
            gRPC mesh for control       v          |  own work queue
                           +--------------------------+
                           |           NODE           |
                           |  one Go agent per host   |
                           +--------------------------+
                                        |
                                        v   /var/run/docker.sock
                           +-------------+  +-------------+
                           | mc-server A |  | mc-server B |
                           +-------------+  +-------------+
                           one container per server, log-shipper as PID 1
```

Optional and in a separate repo: the **Gateway** (edge ingress, hub, link, warp leader). It talks to this repo only through shared Redis keys, never directly.

### Services

| Service | Folder | Role |
|---|---|---|
| **Core** | `core/` | REST API (`:25500`) + gRPC mesh endpoint (`:25501`). Auth/JWT, scheduler, RCON, SSE, library, Beam-ticket signing, DB migrations, Redis-based leader election. |
| **Node** | `node/` | Per-host agent. Mounts `/var/run/docker.sock` to create/manage MC server containers; reads commands from its Redis queue and dials Core over gRPC; persists data to the `dylaris_data` volume. Hosts SFTP (`:25520`) and the Beam gRPC server (`:25521`). |
| **Panel** | `panel/` | Next.js (App Router) web UI (`:25510`). |
| **Log Shipper** | `log-shipper/` | Tiny wrapper binary that runs as **PID 1 inside each MC container**, wrapping the Java process and shipping stdout/stderr to a Redis stream. Built into the MC container image, not deployed as a standalone service. |
| **Agent** | `agent/` | A Go **library** (not a service) imported by Node for host CPU/RAM/network stats collection. No `main.go`. |
| TimescaleDB | (image) | `timescale/timescaledb:latest-pg16` — PostgreSQL 16 + TimescaleDB for relational data and time-series stats. The **source of truth**: users, servers, settings, audit. This is the thing to back up. |
| Redis/Valkey | (image) | `valkey/valkey:8-alpine` — in-memory coordination bus (queues, pub/sub, discovery, settings, stats streams). In-memory **by design** (`--save "" --appendonly no`): losing it loses transient queue and discovery state, not your servers. |

### How the services talk

- **Core ↔ Node — Redis queues + gRPC mesh.** Core pushes commands onto `dylaris:node:{token}:cmds` — a Redis **Stream** read through a consumer group (`dylaris-pkg/queue`), so a node that crashes mid-command gets the work redelivered instead of losing it. `{token}` is the node's `node.Token`, i.e. its `NODE_ID`: `docker-stack.yml` templates that to the Swarm hostname, while the panel-generated BYON env block sets a Core-assigned id. (`dylaris:node:{token}:queue` is the **retired** RPUSH/BLPOP list — nothing reads it any more, so do not go looking there with `redis-cli` when a node looks stuck.) The Node dials *out* to Core over gRPC (`NodeService.NodeConnect`, a bidi stream) for auth and file operations — it never needs inbound reachability, which is what makes NAT'd / home nodes (Warp) possible.
- **Node ↔ Docker.** The Node mounts the host Docker socket and manages real containers (RAM limits, port allocation, persistent volumes).
- **File access.** `beam` uses an overlay gRPC transport (`:25521`, JWT-ticket gated, signed by Core, validated by Node) that never exposes the node IP. `sftp` exposes a classic SFTP server (`:25520`). External/Warp nodes force `beam`.
- **Live data.** MC console logs, stats, and player data stream back through Redis streams; the browser receives them as Server-Sent Events.
- **Optional Gateway (separate repo).** Cross-repo communication is overwhelmingly through shared Redis keys/queues (e.g. `dylaris:hub:queue`, `route:{domain}`), with two exceptions. The Beam data plane (Beam client → relay → link → node) is a separate data path with its own wire protocol. And the gateway's **Warp client** and **route-only Link** call Core's HTTP API directly — `POST /api/warp/enroll`, `GET /api/warp/assignment` and `POST /api/warp/link-boot` — so those two host roles need HTTP reachability to Core.

## How it works

1. **Provision** — an admin creates a server via the Panel → Core writes it to Postgres and pushes a `create` command onto the target Node's Redis queue.
2. **Setup** — the user picks a Java/loader/software (or a modpack), Core pushes a `setup` command, the Node installs it and the server goes `stopped`.
3. **Run** — start/stop/restart commands flow Core → Redis → Node → Docker. Console, stats, and player data stream back over Redis streams + SSE.
4. **Reach the server** — depending on the platform **routing mode**:
   - `ip_port` — the Node binds a host port from `PORT_RANGE`; players connect to `node-ip:port`. This is the default and works without the Gateway.
   - `gateway` — the Node binds **no** host port; player traffic is routed through the optional Gateway **edge** via a reverse tunnel (no node IP exposed). Required for home/external (Warp) nodes.
   - `both` — direct ports *and* gateway routes.
5. **Files** — `sftp` exposes SFTP (`:25520`), `beam` uses an overlay gRPC transport (`:25521`) that never exposes the node IP. (External/Warp nodes force `gateway`+`beam` automatically via `NODE_EXTERNAL`.)

### Main failure sources and operational gotchas

- **Core fails fast at boot on bad secrets.** It refuses to start if `JWT_SECRET`
  or `CLUSTER_SECRET` is empty or left at its placeholder (`change-this-secret` /
  `dylaris-cluster-secret`). Set strong random values.
- **Core cannot run without Postgres and Redis.** Redis is mandatory (coordination
  bus); Postgres is the source of truth. Core fails at startup if it cannot reach
  them.
- **The Redis aclfile is mandatory and not auto-seeded.** The bundled Valkey runs
  the stock non-root image with `--aclfile`, which refuses to start against a
  missing file. Create the admin `default` user in `valkey-acl/users.acl` once
  before the first boot; its password must equal Core's `REDIS_PASSWORD`.
- **ACLs are recoverable from the DB, so a clean Redis is fine.** Core is the Redis
  ACL authority: per-node secrets live in Postgres, and on boot Core re-provisions
  the scoped per-node Redis users with the same derived passwords. You can
  wipe/rebuild Redis (or seed a fresh aclfile with just the `default` admin) and
  Core will redistribute the scoped users. Redis is in-memory by design
  (`--save "" --appendonly no`): losing it loses transient queue/discovery state,
  not your servers.
- **`BEAM_JWT_SECRET` must equal Core's `JWT_SECRET`.** Otherwise every beam
  transfer (including the LAN fast-path) is rejected. The compose files wire it to
  `${JWT_SECRET}` so the two cannot silently diverge.
- **`FRONTEND_URL` must be externally reachable.** An internal Docker-only hostname
  makes every emailed verify/reset link unreachable outside the Docker network.
- **The Node needs the Docker socket.** Treat any host running a Node as trusted;
  it drives the host Docker daemon to launch MC containers.
- **New nodes no longer auto-register from a heartbeat.** A fresh node pairs over
  the gRPC enroll path: an in-cluster node proves possession of `CLUSTER_SECRET`
  and self-enrolls, a BYON node (which never holds that secret) presents a
  single-use `NODE_ENROLL_TOKEN` that binds it to its tenant. Already-paired
  nodes reconnect normally.

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
PANEL_API_URL=http://localhost:25500/api
EOF

# 2. Seed the Valkey ACL file (mandatory: the --aclfile Valkey refuses to start
#    against a missing file and is NOT auto-seeded). Dev is passwordless:
mkdir -p valkey-acl && printf 'user default on nopass ~* &* +@all\n' > valkey-acl/users.acl

# 3. Start the stack
docker compose up -d

# 4. Open the panel and run the first-run setup wizard
#    http://localhost:25510   →   /setup  (creates the first admin)
```

That's it — Core, a Node, the Panel, TimescaleDB and Valkey are now running. Create your first server from the Panel.

> **Production note:** put the Panel/Core behind a reverse proxy with TLS and set `PANEL_API_URL` to the **public** URL the browser will use to reach the Core API, including the `/api` path (e.g. `https://api.example.com/api`), because the browser — not the container — calls the API. Set `DB_SSLMODE=require` if the database is remote.

## Deployment

Two compose files are provided. They run the **same images** and differ only in topology.

### Single-host (`docker-compose.yml`)

All services on one host, a local **bridge** network, one Node. Best for a single VPS / homelab box.

The full file is [`docker-compose.yml`](docker-compose.yml) at the repo root, see it directly rather than a copy here, so this README can't drift out of sync with the real services (env vars, the Valkey ACL `command:` + aclfile setup, healthchecks, etc.). Five services on one bridge network: `core`, `node`, `panel`, `timescaledb`, `redis` (Valkey).

```bash
docker compose up -d          # start
docker compose logs -f core   # tail
docker compose pull && docker compose up -d   # update
docker compose down           # stop (keeps volumes)
```

### Docker Swarm (`docker-stack.yml`)

Multi-host fleet on an **overlay** network with `deploy:` blocks (replicas, placement, restart policy, resource limits). Best for scaling Core/Panel and running Nodes across many machines.

> **Portainer:** paste `docker-stack.yml` into a new Stack and set the variables in the stack's **environment** editor (and secrets via the `*_FILE` pattern above) — the CLI `set -a; . ./.env` step below is only for `docker stack deploy` from a shell.

The full file is [`docker-stack.yml`](docker-stack.yml) at the repo root, see it directly rather than a copy here. It ships **three active services** (`core`, `node`, `panel`) and points at an external managed Postgres/Redis by default (`DB_HOST` / `REDIS_ADDR`); the bundled `timescaledb`/`redis` are opt-in commented blocks. On top of the single-host layout it adds `deploy:` blocks (replicas, placement constraints, `start-first` rolling updates, resource limits) and an `overlay` network.

```bash
# On a manager node:
docker swarm init                                  # (once)

# Only if you uncomment the bundled timescaledb service: label the DB host so
# its node-local volume stays put (skip this when using a managed/external Postgres):
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

**External / home nodes (Warp):** to add a NAT'd home machine as a Node, deploy it separately (not via the global `node` service above) with a `node.labels.dylaris_role == external` placement constraint and `NODE_EXTERNAL=true` (see `NODE_EXTERNAL` / `NODE_TAGS` in [Configuration reference](#configuration-reference)); it joins over an encrypted WireGuard tunnel and forces gateway+beam locally (no exposed ports/SFTP).

### The compose files explained

The single-host `docker-compose.yml` declares all five services below. The Swarm `docker-stack.yml` ships only the first three (`core`, `node`, `panel`) active and points at an external managed Postgres/Redis by default; its `timescaledb` and `redis` are opt-in commented blocks (uncomment for a small single-region deploy):

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
- **The Panel needs a browser-reachable Core URL** via `PANEL_API_URL`, written into `/config.js` by the panel's entrypoint at container **start** (no rebuild needed), because the browser, not the container, calls the API. `NEXT_PUBLIC_API_URL` is the separate build-time fallback baked into the JS bundle (used by the owner's SaaS CI build).
- **Swarm defaults to an external/managed Postgres** (set `DB_HOST`/`DB_PORT`/`DB_SSLMODE` on `core`). The bundled single-replica DB is an opt-in commented block: uncomment it (and the `timescaledb_data` volume), label the DB host so its node-local volume stays put (`dylaris.db=true`), and leave `DB_HOST` unset so it falls back to `timescaledb`. For real HA, use a managed/replicated PostgreSQL.

## Configuration reference

All configuration is passed as **Docker environment variables** — set them in the service's `environment:` block (compose), the **stack environment** in Portainer, or `docker service ... --env` (Swarm). There are no CLI flags. A `.env` file is only a convenience for local `docker compose` (interpolated into `${VAR}`) and for local dev (the Go services also auto-load a `.env` via godotenv); production / Portainer deployments set env vars in the orchestrator, not a file on disk.

Every variable below maps to a real env read in the code; nothing is invented. Core **refuses to boot** if `JWT_SECRET` or `CLUSTER_SECRET` is empty or left at its placeholder.

**Which of these actually reach the container.** This trips people up, so it is worth stating plainly: Compose and Swarm only pass a variable into a service if that service's `environment:` block lists it. A `.env` file is read when the YAML is *parsed*, to expand `${VAR}` placeholders; it is never injected into the containers by itself. A documented variable that is missing from the `environment:` block is therefore ignored no matter what you put in `.env`.

**A set-but-empty variable is not the same as an absent one.** Core reads its
configuration with `os.LookupEnv`, which returns a variable that exists even when
its value is `""`. So writing `SOME_VAR: "${SOME_VAR:-}"` in an `environment:`
block **overrides** whatever default `core/config/config.go` has for it. For most
variables the default is `""` anyway and it makes no difference; for the ones that
have a real default it matters, which is why the shipped files repeat those
defaults literally (`DYLARIS_GRPC_PORT: "${DYLARIS_GRPC_PORT:-25501}"`). Keep
those in sync with `config.go` if you add more.

That behaviour is also the supported way to *disable* something whose default is
non-empty: setting `UPDATES_FEED_URL_PLATFORM=""` turns the update feed off. It is
deliberately not listed in the shipped files, because listing it with any shell
default would take that away.

Both shipped files now forward everything an operator normally needs, including
the owner/SaaS integrations (`DNS_*`, `STORE_URL`, `STORE_SHARED_KEY`,
`UPDATES_FEED_URL_GATEWAY`, `CLAMAV_ADDR`) and the identity/tuning knobs
(`DYLARIS_CORE_ID`, `DYLARIS_GRPC_PORT`, `REDIS_DB`). Earlier versions did not,
and this section used to tell you to add them by hand.

Still not forwarded, and why:

- `UPDATES_FEED_URL_PLATFORM` - see the disable-by-empty note above.
- `BEAM_MANIFEST_URL` - a **Core** variable (see the Core table below), not a Node
  one. It is simply not forwarded in either `environment:` block, so Core falls back
  to its compiled-in manifest URL.
- `NODE_MANAGES_LINK`, `LINK_IMAGE`, `DYLARIS_STATS_BUFFER_MAXLEN`,
  `STATS_STREAM_MAXLEN` - these belong to the **Node**. Both shipped files *do*
  deploy a Node (`docker-stack.yml` runs it as a `global` service); these four are
  just absent from its `environment:` block and take their code defaults there. Add
  them by hand if you need them, or use the panel-generated env block for a BYON node.
- **Port variables.** `SFTP_PORT`, `BEAM_GRPC_PORT`, `BEAM_LAN_PORT` and `MIGRATION_PORT` are not in the `environment:` blocks; they take their code default and the host publication uses literal numbers in `ports:`. `API_PORT`, `DYLARIS_GRPC_PORT`, `PORT_RANGE` and `CORE_GRPC_ADDR` *are* in the YAML (a literal or `${VAR:-default}`) next to their `ports:` line. Either way a port change means editing the `ports:` line too, so change these in the file, not `.env`.

### Secrets (Docker / Portainer secrets via `*_FILE`)

Every secret can be supplied from a **file** instead of a plain env value by setting `<NAME>_FILE` to a readable path. The service reads the file (whitespace-trimmed), so you can use Docker/Swarm secrets (`/run/secrets/...`) or Portainer secrets without ever putting the value in the environment. Precedence per secret: `<NAME>_FILE` → `<NAME>` → default; an unreadable or empty `*_FILE` logs and falls back rather than booting blank.

Supported:

- **Core:** `JWT_SECRET_FILE`, `CLUSTER_SECRET_FILE`, `ADMIN_SECRET_FILE`, `DB_PASSWORD_FILE`, `REDIS_PASSWORD_FILE`, `DNS_API_TOKEN_FILE`, `STORE_SHARED_KEY_FILE`
- **Node:** `CLUSTER_SECRET_FILE`, `BEAM_JWT_SECRET_FILE`
- **Log shipper:** `REDIS_PASS_FILE`. You will not normally set it: the Node injects a scoped per-node Redis credential when it launches the container. It exists for running the shipper standalone.

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
| `DB_TYPE` | `timescaledb` | No | Time-series backend for `server_stats`: `timescaledb` (hypertable + native retention, best for larger fleets) or `postgres` (plain table, retention via the hourly sweep — fine for small/medium setups, no extension needed). |
| `API_PORT` | `25500` | No | Core REST API port. |
| `DYLARIS_GRPC_PORT` | `25501` | No | Core gRPC mesh port (Core ↔ Node). |
| `GRPC_TLS_ENABLED` | `false` | No | Server-authenticated TLS + fingerprint pinning on the Core ↔ Node gRPC control channel. Off (default) is plaintext (rely on an encrypted overlay). Must be flipped together on every Core AND every Node. |
| `DYLARIS_CORE_ID` | *(hostname)* | No | Identifier for this Core instance; falls back to the OS hostname. |
| `CORE_SERVICE_NAME` | `core` | No | The name Core answers to on its own Docker network. Core resolves it and hands the result to each machine's warp, which proxies Core's gRPC on a local port — so the machine gets the service address rather than whichever replica answered, and never holds it in a config file. Set it only if the service is not called `core`. |
| `DYLARIS_REGION` | `default` | No | Region label stamped into heartbeat + system info. |
| `FRONTEND_URL` | `http://localhost:25510` | No | Panel origin Core trusts for CORS and uses to build email links (verify/reset). **Must be externally reachable**: the previous compose default (`http://panel:25510`, an internal Docker-only hostname) made every emailed link unreachable outside the Docker network. For a **cross-origin** deployment set it to the public panel URL (e.g. `https://panel.example.com`) so CORS accepts it; for a **same-origin** reverse-proxy layout it is not needed for CORS. Host-level config, kept as env. |
| `TRUSTED_PROXY_CIDRS` | *(private ranges)* | No | Which reverse-proxy networks' `X-Forwarded-For` Core believes for rate limiting and the audit log. Unset trusts the private ranges (RFC1918, loopback, IPv6 ULA), correct for the reference proxy on the private Docker network. A CIDR/IP list trusts exactly those (proxy on a public IP); `none` ignores XFF entirely (Core exposed directly). |
| `TAB_PROXY_PORT` | *(empty)* | No | Origin-isolated custom-tab proxy (spec B5). When set, Core binds a SECOND HTTP listener on this port serving only the tab-proxy data plane. Front it on the SAME host as the panel, a different port (e.g. `25502`). Required for public tab share links; empty = second listener off (same-origin fallback). |
| `TAB_PROXY_ORIGIN` | *(empty)* | No | Browser-facing absolute base URL of that isolated origin (e.g. `https://panel.example.com:25502`). Its HOST must equal `FRONTEND_URL`'s host (the `dyl_tabproxy` cookie is host-only); a mismatch logs a warning and disables isolation. Empty = same-origin fallback (public shares refused). |
| `REDIS_ADDR` | `localhost:6379` (compose: `redis:6379`) | No | Redis/Valkey address. |
| `REDIS_USER` | *(empty)* | No | Redis/Valkey username (ACL). |
| `REDIS_PASSWORD` | *(empty)* | Recommended | Redis/Valkey password for Core's admin login. Core is the Redis ACL authority: it connects as the aclfile `default` user and provisions per-node scoped users. The bundled Valkey runs the stock image (non-root) with `--aclfile` and is NOT auto-seeded, so this must match the `default` admin password you create in the aclfile before the first boot (see the redis service comment in the compose files). |
| `REDIS_DB` | `0` | No | Redis/Valkey logical DB index. |
| `EXTERNAL_TICKET_DB_URL` | *(empty)* | No | Optional external ticket DB URL; surfaces as a target in the migration/backup/restore UI. Live queries always hit the main DB. |
| `BILLING_SUSPEND_GRACE` | `48h` | No | Grace before a `suspended` (BYON) tenant is cut off (servers stopped, link tunnels dropped), so a transient billing/DB fault cannot instantly kick a paying customer. Go duration; `0` enforces on the next hourly tick. |
| `ADMIN_SECRET` | *(empty)* | No | RAM-only break-glass. When set (>=16 chars), creating an admin via `/setup` requires this exact value in every mode - closes the fresh-install race and re-opens `/setup` to recover or add an admin. Never written to the DB or logs; unset + restart to disable. Supports `ADMIN_SECRET_FILE`. |
| `DNS_UPDATER_ENABLED` | `false` | No (owner) | Leader-gated reconciler that points each region's edge wildcard A record at the live edge IPs. Only for multi-region Gateway deploys; most self-hosters leave it off and set their records by hand. Also settable in Settings -> Infrastructure -> DNS; either switch turns it on. |
| `DNS_PROVIDER` | `cloudflare` | No (owner) | DNS provider for the reconciler above. Backed by [libdns](https://github.com/libdns/libdns), so more providers are a constructor entry rather than a new HTTP client. |
| `DNS_API_TOKEN` | *(empty)* | No (owner) | Provider API token. Lives ONLY in Core, never on the edges - an edge is the most exposed host in the fleet and a token that can rewrite the zone does not belong there. `CF_API_TOKEN` is still read as a fallback. **Setting it here makes the panel field read-only**, so a token saved on screen can never silently retire one supplied as a secret. |
| `DNS_ZONE` | *(empty)* | No (owner) | The zone **name** the edge wildcards live in, e.g. `example.com`. **Replaces `CF_ZONE_ID`**: libdns addresses a zone by name, not by a Cloudflare-assigned id, so the old value cannot be carried over. Required when the updater is on. |
| `DNS_ZONES` | *(empty)* | No (owner) | Comma-separated multi-zone form, for offering several domains from the same edges. Folded together with `DNS_ZONE`, so a single-zone deployment needs no change. |
| `STORE_URL` | *(empty)* | No (SaaS) | Hosted dylaris.com storefront base URL. Set together with `STORE_SHARED_KEY` to enable store-linking + demo showcase; both empty gives a clean open-core build with no store surface. |
| `STORE_SHARED_KEY` | *(empty)* | No (SaaS) | Service-to-service trust key between Core and dylaris.com (must match the key configured on the storefront). |
| `UPDATES_FEED_URL_PLATFORM` | *(public repo feed)* | No (owner) | Raw URL of the append-only JSONL update feed the admin "what's new" bell diffs against the baked baseline. Defaults to the platform public-repo raw feed. Fails open when unset or unreachable. |
| `UPDATES_FEED_URL_GATEWAY` | *(empty)* | No (owner) | Raw URL of the gateway update feed. Empty until it is cross-pushed into the public platform repo. Fails open. |
| `BEAM_MANIFEST_URL` | *(GitHub Releases feed)* | No | **Is** the compiled-in fallback for the Beam desktop-client update manifest, read once at startup — the `beam.release_manifest` admin setting takes precedence when it is non-empty. Set this so a fork points at its OWN releases repo without a rebuild. |
| `CLAMAV_ADDR` | *(empty, scanning off)* | No | `host:port` of a `clamd` instance. When set, every ticket attachment is streamed through ClamAV (INSTREAM) and rejected on a hit before it is ever stored. Empty means uploads are accepted unscanned. |

The DNS updater variables above can also be configured in **Settings -> Infrastructure -> DNS**,
where the token is stored encrypted at rest. The environment wins per field, so a
token kept in a Docker secret still leaves the zones selectable on screen.
Enabling the updater from the panel probes the provider against every configured
zone before the setting is accepted - a credential scoped to only one of them is
rejected on save rather than failing silently in a log every 30 seconds.

Each region can serve several names, picked in the panel per region, with each
edge's own `EDGE_WILDCARD` as the fallback when a region has no selection. Beam
relays get their records from the same loop, using each relay's own
`BEAM_PUBLIC_HOST` - relays are not part of the panel picker, since several
relays in one region deliberately share one name to round-robin. The panel lists
every name in effect labelled by origin (panel, edge env, or relay), because a
leftover `EDGE_WILDCARD` quietly outliving a panel selection is otherwise
invisible.

**Deletion is scoped and never zone-wide.** The reconciler records the names it
creates in a Redis ownership registry and only ever removes names from that
registry - a zone you release can safely carry your website and mail records. A
name is removed only after it has gone unadvertised for the grace period (default
15 minutes), so a rolling edge restart cannot take a live region out of DNS. The
two original rails still hold: nothing is deleted when a listing fails, and a
region whose edges are all offline is left untouched.

### Node

| Variable | Default | Required | Description |
|---|---|---|---|
| `CLUSTER_SECRET` | *(empty)* | For in-cluster | Cluster proof + gRPC TLS pin for in-cluster nodes; must match Core when set. Optional now: a node authenticates to Redis via its gRPC-bootstrapped per-node secret. BYON nodes omit it. |
| `REDIS_ADDR` | *(none — fatal in-cluster; `127.0.0.1:25571` on an external node)* | In-cluster | Redis/Valkey address. An **external** node with this empty uses warp's local proxy, which holds the real overlay address and refreshes it from Core — that is why the BYON deploy snippet no longer carries one. An in-cluster node still exits if it is missing: there is no warp there, and a silent loopback default would hide the misconfiguration. |
| `NODE_ID` | *(hostname)* | No | Unique id for this Node. In Swarm, templated from the hostname. Falls back to OS hostname. |
| `NODE_TAGS` | *(empty)* | No | Comma-separated placement tags (e.g. `eu,fast`). The tag `external` flags a home/external node. |
| `NODE_REGION` | *(empty)* | No | Region this Node belongs to. |
| `NODE_EXTERNAL` | `false` | No | If `true` (or `NODE_TAGS` contains `external`), the Node forces `gateway` routing + `beam` file access locally (no host ports, no SFTP). |
| `NODE_MANAGES_LINK` | *(on when `LINK_IMAGE` is set)* | No | Whether this Node spawns and reconciles its own Link sidecar (container `dylaris_link`). Setting `LINK_IMAGE` is the opt-in; set this to `false` only in the rare case where an image is configured but you deploy Link separately, so the two do not fight over the same container name. A Link is needed per MC Node whenever routing is `gateway`/`both` — not only for BYON. |
| `LINK_IMAGE` | *(empty)* | No | Container image for the Link sidecar this Node spawns. No built-in default: empty means no Link sidecar, only a log line. |
| `REDIS_DB` | `0` | No | Redis/Valkey logical DB index. |
| `CORE_GRPC_ADDR` | *(empty; `127.0.0.1:25570` on an external node)* | For first boot | Core gRPC endpoint (`host:25501`). Needed for a first-boot node to bootstrap its per-node Redis secret over gRPC; a node with an already-cached secret can start without it (see the boot warning). Redis ACL is mandatory and there is no static-password fallback. Same rule as `REDIS_ADDR`: empty on an external node means warp's local proxy. |
| `GRPC_TLS_ENABLED` | `false` | No | TLS + Core-cert fingerprint pinning on the Node ↔ Core gRPC control channel. Off (default) is plaintext. Must match Core's `GRPC_TLS_ENABLED`. |
| `GRPC_TLS_FINGERPRINT` | *(empty)* | No | Pins the Core control-channel cert fingerprint for a BYON node that does NOT hold `CLUSTER_SECRET` (in-cluster nodes derive it from `CLUSTER_SECRET` and leave this empty). Public pinning material, delivered out-of-band at enroll time. |
| `NODE_ENROLL_TOKEN` | *(empty)* | For BYON | One-time enroll token (minted in the panel) that binds a new BYON node to its tenant on first pairing. Not needed by a node that holds `CLUSTER_SECRET` — it self-enrolls via its cluster proof. |
| `NODE_RECOVERY_TOKEN` | *(empty)* | For recovery | Single-use, admin-minted token (Settings → Nodes → Reset pairing) to re-pair a node under its EXISTING identity after its secret was reset. Not needed on a normal boot. |
| `SIDECAR_REDIS_ADDR` | *(falls back to `REDIS_ADDR`, or the bridge gateway on the warp proxy)* | No | Redis address handed to MC containers and the Link sidecar, which can't resolve Swarm overlay DNS. Set to the leader node's private IP in Swarm. On the warp proxy the fallback is **not** the node's own address — the node is host-networked and those containers are not, so `127.0.0.1` would be their own loopback. It resolves to the bridge gateway of the network each container joins, where warp also listens (`PROXY_BIND_DOCKER_BRIDGES=true`). |
| `SIDECAR_REDIS_DB` | *(falls back to `REDIS_DB`)* | No | Redis DB index for MC containers. |
| `PORT_RANGE` | `25600-25699` | No | Host port range for MC servers (`ip_port`/`both`) as `START-END`. Validated: a malformed or inverted range falls back to the default in full (never half-applied) and the reason is shown on the node card in Infrastructure. Replaces the removed `PORT_RANGE_START`/`PORT_RANGE_END`. |
| `SFTP_PORT` | `25520` | No | SFTP server port (file access `sftp`/`both`). |
| `BEAM_GRPC_PORT` | `25521` | No | Beam file-transfer gRPC server port. |
| `BEAM_LAN_FASTPATH` | `true` (on unless `false`) | No | Toggles the Beam LAN fast-path TLS listener on `:25523` (pinned-TLS, direct client ↔ node). Set `false` to disable; any other value leaves it on. |
| `BEAM_LAN_PORT` | `25523` | No | Port for that LAN fast-path listener. Change it together with the `ports:` publication in the compose file, and never publish the plain overlay port `25521` instead. |
| `MIGRATION_PORT` | `25522` | No | Auto-move pull endpoint: the source node serves the staged archive to the target node, authenticated by a per-node-secret-derived HMAC token. |
| `BEAM_JWT_SECRET` | *(empty — Beam rejects all tickets)* | No (required for Beam) | Must match the gateway beam-relay's JWT secret so relay-validated tickets pass the node-side gate. |
| `DYLARIS_CPUSET_CPUS` | *(empty)* | No | Default `cpuset-cpus` CPU pinning applied to all MC containers on this node. |
| `STORAGE_PATHS` | `./dylaris_data/servers` | No | Comma-separated list of storage roots (multi-disk). **Each path must belong to exactly one node** - see below. |
| `MODPACK_MIRROR_HOSTS` | *(empty)* | No | Comma-separated extra hosts the Node may download Core-minted pack-build `.mrpack` files from (e.g. the Core public domain / S3 mirror). Merged into the built-in `.mrpack` allowlist (cdn.modrinth.com, github.com, ...). |
| `DYLARIS_STATS_BUFFER_MAXLEN` | `1800` | No | MaxLen of the per-server stats buffer stream (~1h at 2s). Reduce for very large fleets. |
| `STATS_STREAM_MAXLEN` | `360` | No | MaxLen of the node system-stats stream (~3h at 30s). |

#### Host-networked node (`--network host`)

A node started with `--network host` (typical for a standalone BYON node with no
Swarm overlay) behaves differently: it creates a LOCAL `dylaris_net` bridge for
its MC containers when no overlay is found (a non-host-net node instead treats a
missing overlay as a hard error), per-tenant network isolation is disabled (MC
servers all stay on that shared local network), MC container addresses are
resolved via Docker inspect instead of Docker DNS (which a host-net container
cannot use), and disk quotas degrade to a `du`-based usage estimate instead of
enforced project quotas.

#### Shared node storage is not supported

A `STORAGE_PATHS` entry must be backed by a filesystem that belongs to exactly
one node. Pointing several nodes at one NAS share or one host directory is an
unsupported topology, and the reason is not that migrations are fragile:

- **Node identity lives in the first storage path.** `.node_secret`, `.node_id`
  and `.tenant_networks.json` all sit there, so two nodes overwrite each other's
  identity. The tenant-network allocator writes the whole file without a lock and
  will hand the same subnet to two different owners.
- **A migration between two such nodes destroys the server.** The target extracts
  the archive over the live directory, and the source's cleanup then deletes the
  running server's data. Open file descriptors keep it alive until the next
  restart, and the panel reports the migration as successful.
- **Capacity accounting is wrong.** One 10 TB share behind five nodes is counted
  as 50 TB of free space by the scheduler.

Each node detects this itself: it writes a beacon into every storage path and
reads back the beacons it finds. A foreign one means the mount is shared, and the
node logs it and reports it in its heartbeat, so the Infrastructure page shows it
on the node card. The migration path refuses independently of that, so a shared
mount cannot destroy a server even if nobody reads the warning.

The fix is always the same: give each node its own path. There is no supported
way to share one.

A brand-new node no longer auto-registers from its heartbeat: the Core-minted,
unguessable node identity can only be created via the gRPC enroll path. There are
exactly two doors, and which one a node uses is decided by what it holds:

- **In-cluster node** — sends a `cluster_proof` (an HMAC of its id under
  `CLUSTER_SECRET`) and self-enrolls as an operator-owned node. No token, no
  panel step, so a first deploy comes up without a mint-and-redeploy round trip.
- **BYON node** — never holds `CLUSTER_SECRET`, so it presents a single-use
  `NODE_ENROLL_TOKEN`. That token is what binds the node to its tenant; it is the
  only path that can produce an owned node.

Admission control (IP allowlist, join toggle) runs before both. When a node
presents both, the enroll token wins: setting it is an explicit request for an
owned node. Already-paired nodes keep reconnecting with their per-node secret.

### Panel

| Variable | Default | Required | Description |
|---|---|---|---|
| `PANEL_API_URL` | *(same origin)* | No | **Browser-reachable** Core API base URL, **including the `/api` path** (e.g. `https://api.example.com/api`) — the panel appends route paths to it verbatim, so a value without `/api` 404s every call. Runtime (not build-time): the container's entrypoint writes it into `/config.js` on every start, so it takes effect without a rebuild. If unset, the panel defaults to the **same origin** it is served from (`https://<panel-host>/api`), the usual reverse-proxy layout where `/api` is routed to Core. See the shim details below. |

The panel resolves its API URL in this order: `window.__DYLARIS_CONFIG__.apiUrl` (runtime) → `NEXT_PUBLIC_API_URL` (build-time) → same origin (`/api`). The runtime value lives in **`/config.js`**; the container's entrypoint (`panel/docker-entrypoint.sh`) regenerates that file from `PANEL_API_URL` on every start, so set the env var rather than bind-mounting `/app/public/config.js` directly - the entrypoint overwrites it on the next restart:

```js
// what the entrypoint writes when PANEL_API_URL=https://api.example.com/api
window.__DYLARIS_CONFIG__ = { apiUrl: "https://api.example.com/api" };
```

### Log Shipper (inside the MC container)

These are set by the Node when it launches a container; they are listed for completeness. Note `REDIS_PASS` (not `REDIS_PASSWORD`) here. With mandatory Redis ACL, the Node injects a per-node SHIPPER ACL user (scoped to this node's server keys only) - not a static password.

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
| `25522` | node | Auto-move pull endpoint (`MIGRATION_PORT`; per-node-secret-HMAC) |
| `25523` | node | Beam LAN fast-path (`BEAM_LAN_PORT`; pinned-TLS, direct client<->node). Publish on the node's LAN (self-host/BYON) to enable the fast path even when a gateway is present; unpublished simply falls back to relay/public. |
| `25502` | core | Origin-isolated tab-proxy (`TAB_PROXY_PORT`, spec B5; opt-in, same host as panel, different port) |
| `25600-25699` | node | MC server host ports (`PORT_RANGE`; `ip_port`/`both` routing) |
| `5432` | postgres | Database (internal) |
| `6379` | redis | Cache + queues (internal) |

> The optional Gateway stack adds public ingress ports (`25565` Minecraft) and the Warp leader (`25599/udp`); it no longer serves HTTP(S) (`80`/`443`) - see the `dylaris-gateway` repo.

### DNS (minimal single-IP setup)

Everything behind one IP, with a reverse proxy terminating TLS for the web surfaces:

```
dylaris.com.        A    <ip>     ; landing + web
panel.dylaris.com.  A    <ip>     ; admin panel (25510)
api.dylaris.com.    A    <ip>     ; core REST (25500)
play.dylaris.com.   A    <ip>     ; MC players

; lets players omit ":25565"
_minecraft._tcp.play.dylaris.com.  SRV  0 5 25565 play.dylaris.com.
```

Optional, per used features:

```
sftp.dylaris.com.   A    <ip>     ; node SFTP (25520), when file access = sftp/both
beam.dylaris.com.   A    <ip>     ; Beam desktop sync (via the Gateway relay)
warp.dylaris.com.   A    <ip>     ; Warp leader (UDP 25599), external/home nodes
```

> The `_minecraft._tcp` SRV and `play` record assume the Gateway edge fronts
> `:25565`. In standalone `ip_port` mode (no Gateway) players connect directly to
> `node-ip:port` in the `25600-25699` range instead.

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

Core reads the real client IP from `X-Forwarded-For` (set above) for rate
limiting and the audit log, but only from a **trusted** proxy. It trusts the
private ranges by default, which covers a proxy on the private Docker network
like this one — so no extra config is needed. If your proxy reaches Core from a
public address instead, set `TRUSTED_PROXY_CIDRS` to that address; if Core is
exposed directly with no proxy, set `TRUSTED_PROXY_CIDRS=none`. See
`.env.example`.

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

Minecraft routing goes through the separate **Gateway** stack, not this proxy: the Edge is a pure Minecraft-TCP router and maps public `25565` to the player ingress. It no longer serves HTTP/HTTPS (the 80/443 web data-plane was removed in WS7). See the `dylaris-gateway` repo.

## Scalability

DYLARIS scales on three axes:

- **More API throughput → scale Core.** Run N Core replicas behind a load balancer. Redis leader election keeps singleton jobs (discovery, Warp resync) on one replica while all replicas serve traffic. `docker service scale dylaris_core=N`.
- **More server capacity → add Nodes.** Every Docker host you join to the swarm becomes a Node (global service) and a scheduling target. Use **regions** + **tags** to steer placement; the scheduler picks a node by region/tag/capacity.
- **Reach beyond the DC → Warp.** Pull NAT'd home/off-site machines into the overlay over WireGuard and run servers on them; player traffic is routed back through the DC edge.

**State & HA notes**

- **Postgres is the source of truth.** For real HA, point Core at an external/managed PostgreSQL (or a replicated TimescaleDB) instead of the bundled single-replica container — the in-stack DB volume is node-local.
- **Redis/Valkey** is a coordination bus (in-memory by design). A single replica is fine; for HA, run Valkey with replication/Sentinel and update `REDIS_ADDR`.
- **Ingress** scales via the separate Gateway stack (multiple edges), keeping node IPs private.

**Core file storage across multiple Cores**

Every online Core must be able to write to and read from the SAME shared Core file storage (Library, ticket attachments, ticket backups). A per-host volume that only *looks* shared is a silent failure: blobs split across hosts and nothing errors, because each Core reads its own writes back perfectly. Core proves the share rather than assuming it:

- **A filesystem share must be writable by uid 1000.** Core runs as the non-root `dylaris` user (uid 1000), so the configured path has to be writable by that uid on every host. This is the most common reason a first multi-Core setup is refused: the check reports `write-denied` on every Core, which is loud and names the remedy, but only once you try to save. Either own the share as 1000 (NFS: the exported directory itself on the server, or squash the export with `anonuid=1000,anongid=1000,all_squash`; SMB/CIFS: mount with `uid=1000,gid=1000`, not `uid=0`), or leave the mount root as it is and point the panel at a subdirectory you created once with `chown 1000:1000`, after which Core's own mkdir creates everything below it as uid 1000. The commented fstab recipe in `docker-stack.yml` carries the exact mount lines. S3 has no uid semantics and sidesteps this entirely.
- **Saving a shared storage config is proven, not assumed.** On a deployment with more than one online Core, saving the Core file storage settings (or switching the target backend mid storage-migration) runs a reachability round across every online Core: each one must prove it can write its own file, read every peer's, and cross-write into every peer's namespace. The save is refused unless **all** of them pass. The round has a 15-second deadline and no override, with a worst case of about 16 seconds: the coordinator's own probe gets up to one more second of grace past that deadline before it is ruled on, so a healthy-but-slow final check is not mistaken for a hang. A deployment with only one online Core skips the round (there is nothing to prove). This covers S3 as well as filesystem paths — the previous guard only counted Cores and ignored S3 entirely.
- **Every Core re-verifies on its own, too.** Independent of any config save, each Core re-checks its own access to the persisted config at boot and every 120 seconds.
- **A failing Core is gated, not taken down.** A Core that fails its check keeps serving auth, server management and every other route; only the storage-dependent routes — the Library, ticket attachment upload/download/delete, and ticket backup create/list/download/delete/restore-init/restore-execute — return `503` with a machine-readable reason and an operator-facing remedy. The storage settings routes themselves (viewing, saving or test-connecting the config) are deliberately never gated, so a broken config can always be inspected and fixed. The reason is one of: `ok`, `offline`, `no-response`, `unreachable`, `write-denied`, `not-shared`, `fingerprint-mismatch`, `cross-write-denied`.
- **Faults are visible fleet-wide.** Each Core's current fault (which Core, since when, why) is recorded in Redis and shown on the panel's Core file storage tab; `GET /api/settings/storage-reach` backs a read-only fleet-health view.
- **Rolling upgrades block storage config changes, but not ordinary traffic.** A Core still running the pre-upgrade binary keeps sending heartbeats, so it still counts as an online participant in a config-save round, but it never runs the storagereach service loop at all — no self-check, no round participation, no beacon-write claim — so it never answers and is reported `no-response`. Every storage-settings save and every storage migration's backend switch is refused until the **whole fleet** runs the upgraded binary — this is the intended fail-closed behavior, not a bug. Already-upgraded Cores, however, keep serving their storage routes normally throughout the rollout: each one's own periodic self-check only expects peers that recently published a beacon-write claim, so an old-binary peer that writes no claim at all is simply not expected, never miscounted as a `not-shared` fault. If a save is refused naming a Core, check whether that Core is still on the old image before assuming the storage itself is broken.
- **A wedged mount leaks a goroutine, not the service.** A hung NFS/CIFS mount blocks inside plain syscalls that no context cancellation can interrupt. Three separate paths run their storage probe on a watchdogged child goroutine with a bounded budget instead of inline: the periodic self-check, a Core's participation in a round it did not start (both in the background service loop), and — the one that matters most for an admin saving the config, since it otherwise runs inline on that HTTP request — the coordinator's own probe when it starts a round. Past the budget the affected path is correctly reported `unreachable`/`no-response` and keeps moving — the service loop ticks on, and a save request returns its refusal — but the underlying goroutine cannot be reclaimed until the syscall itself returns. This is a deliberate trade-off (a leaked goroutine beats a stalled Core, or a save that never returns while holding the config-round lock): the periodic self-check and round-participation loops never start a second check against the same wedge while one is still outstanding, so those two do not compound into one leaked goroutine per tick — but an admin retrying a save past the config-round lock's own TTL can still start another coordinator probe against the same wedge.

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

## Custom-tab reverse proxy

Custom tabs can run in two modes. A **direct** tab renders a browser-reachable
URL in an iframe (or popout). A **proxied** tab streams a Minecraft server
container's own web UI (BlueMap, squaremap, Dynmap, plugin dashboards) through
Core over the existing reverse gRPC mesh, so the browser only ever talks to Core
on the panel origin - no public container port and no extra ingress needed. It
works even on gateway-routed / BYON nodes with no browser-reachable node address.

- In-dashboard: `ANY /api/servers/{id}/tabs/{tabId}/proxy/...` (session + overview
  access, same gate as reading the tab).
- Standalone share link: `/c/<token>` -> `ANY /api/tabproxy/{token}/...`. A
  `public` link serves anyone with the unguessable token; a `private` link needs
  a logged-in session with access.
- HTTP and WebSocket are both proxied. Proxied apps must use relative asset paths
  or a configurable web base-path; Core injects `<base href>` into `text/html`.
  Apps that hard-code absolute-root paths (`/tiles/...`) are a known V1 limitation.
- Admin gates live in Settings -> Features: master toggle
  (`feature_tab_proxy_enabled`, default off), anonymous public links
  (`tab_proxy_allow_public_links`, default off), and per-server / per-user caps.
- Origin isolation (spec B5): set `TAB_PROXY_PORT` + `TAB_PROXY_ORIGIN` to serve
  proxied content from a dedicated same-host, different-port origin. This closes
  the same-origin token-theft vector and is REQUIRED for public share links. Left
  unset, the in-dashboard proxy still works same-origin (single-operator only).

> **Security:** proxied content runs under `allow-same-origin`, so it must not
> share the panel's origin. Set `TAB_PROXY_PORT` + `TAB_PROXY_ORIGIN` (same host,
> different port) so proxied content is served from a dedicated origin - a
> container's JS then runs on that origin and cannot read the panel token from
> the panel origin's localStorage (spec B5). Public share links are REFUSED until
> this origin isolation is active. Without it, only the in-dashboard proxy works,
> same-origin, which is safe solely for a single-operator self-host where you
> control your own containers. Origin isolation fully protects PUBLIC shares; the
> in-dashboard proxy may briefly use the same origin during app-data load, so an
> admin viewing another tenant's container in-dashboard is best-effort (the
> single-operator / self-host assumption).

## Beam desktop client

Beam is the optional desktop client for browsing and transferring server files
over the overlay gRPC transport (LAN fast-path, relay, or pinned-TLS direct) that
never exposes a node IP. It is a Wails app in `beam/app/`, released from the
`beam-release.yml` workflow on `beam-v*` tags as Ed25519-signed GitHub Release
assets.

**Platform builds**

- `linux/amd64` - `DylarisBeam-linux-amd64`.
- `windows/amd64` - `DylarisBeam-windows-amd64.exe`, a **single** artifact that is
  both the double-click download and the in-app updater's target (the signer derives
  the manifest URL with the `.exe` extension, so the signed and published bytes are
  one file - there is no extensionless duplicate and no installer asset).
  Cross-compiled from the Linux CI runner: Wails renders Windows through the pure-Go
  go-webview2 loader, so no CGO and no Windows runner are needed.

The release publishes exactly six assets: `DylarisBeam-linux-amd64`,
`DylarisBeam-windows-amd64.exe`, `latest.json`, and a `.sig` for each.

**Windows: known limitations**

- Unsigned binary. The `.exe` is not Authenticode-signed, so
  Windows SmartScreen shows an "unknown publisher" warning on first run. Click
  "More info", then "Run anyway". This first manual download is not verified at
  download time, so only get it from the official GitHub Releases page;
  subsequent in-app auto-updates are covered by the fail-closed sha256 +
  Ed25519 verification chain (below). OV/EV Authenticode signing (a paid
  certificate) would remove the warning and is a documented follow-up, not
  shipped in this build.
- WebView2 runtime. Beam renders through the Microsoft Edge WebView2 Evergreen
  runtime, preinstalled on Windows 11 and most current Windows 10. If it is
  missing the app cannot render its UI; install the free WebView2 runtime from
  Microsoft. Beam ships as a bare `.exe`, so nothing installs it for you.
- Auto-updates. The in-app updater covers `windows-amd64` with the same
  fail-closed sha256 + Ed25519 verification as Linux; the real signing key is
  already embedded in `beam/app/update_pubkey.go`. The release workflow signs a
  `windows-amd64` entry into every manifest it produces (stable and dev channel
  alike) and the updater keys on `runtime.GOOS-runtime.GOARCH`, so Windows
  auto-update is live as soon as a release exists.

## Development

The backend is a Go workspace (`go.work`) with `core`, `node`, the `log-shipper`, the `agent` library, the Beam desktop app (`beam/app`), and shared `pkg`/`proto` modules. The frontend is a Next.js app in `panel/`.

```bash
# Backend (Go workspace)
go work sync

# Build, vet and test PER MODULE. The workspace root is not a runnable package
# set (`go test ./...` from the root fails with "directory prefix . does not
# contain modules listed in go.work"), so run these inside each module.
# Repeat for core, node, pkg, log-shipper:
(cd core && go build ./... && go vet ./... && go test -count=1 ./...)

# Build the log-shipper binary baked into the MC container image (from the repo root):
./build_shipper.sh

# Frontend - this is an npm workspaces monorepo; the lockfile lives at the repo
# root (root package.json declares "workspaces": ["packages/*", "panel"]), so
# install from the root, not from panel/:
npm install --workspaces --include-workspace-root
cd panel && npm run dev      # dev server
cd panel && npm run build    # production build
```

CI (`ci.yml`) gates every build job on: the Go test matrix, **staticcheck**, a
`-race` gate over every Go module, a `db-tests` job against a real Postgres, and
panel `npx vitest run` - all must pass before an image is built.

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

## No telemetry

Dylaris sends nothing anywhere. There is no usage heartbeat, no phone-home, no
opt-out to configure, and no endpoint to block. Your instance talks to your
database, your Redis, your nodes, and whatever mod/plugin sources you ask it to
fetch from. Nothing else.

---

<div align="center">
Maintained by <a href="https://github.com/Bartis-Dev">Bartis-Dev</a>. Self-host it, own it.
</div>
