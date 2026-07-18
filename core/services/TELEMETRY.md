# Telemetry heartbeat contract

Core posts an anonymous usage heartbeat to the Dylaris website so the public
site can show a live count of self-hosted instances. This document is the wire
contract for the RECEIVER (the website / store, which lives in a separate repo).
The sender is [`telemetry_heartbeat.go`](telemetry_heartbeat.go).

## Who sends, and how often

- Only the Core **leader** posts (Redis leader election). A load-balanced
  deployment with several Cores therefore sends one heartbeat, not one per Core.
- First post 30s after boot, then every 10 minutes.
- Fully silent on network errors; a non-2xx response is not retried before the
  next tick. The sender never blocks startup or requests on telemetry.
- Opt-out: `DYLARIS_TELEMETRY=false` (env, hard kill) or the in-panel
  `telemetry_enabled=false` setting. Default is ON.

## Endpoint

- Default: `POST https://dylaris.dev/api/heartbeat`
- Overridable per instance via the `telemetry_endpoint` DB setting.

## Request

Headers:

| Header          | Value                                   | Notes                         |
| --------------- | --------------------------------------- | ----------------------------- |
| `Content-Type`  | `application/json`                      | always                        |
| `User-Agent`    | `Dylaris-Telemetry/<version>`           | e.g. `Dylaris-Telemetry/0.17.0` |
| `Authorization` | `Bearer <DYLARIS_TELEMETRY_KEY>`        | only when the key env is set  |

Body (JSON):

```json
{
  "instanceId": "a1b2c3d4e5f60718",
  "type": "platform",
  "servers": 3,
  "online": 2,
  "players": 0,
  "version": "0.17.0"
}
```

| Field        | Type   | Meaning                                                                 |
| ------------ | ------ | ----------------------------------------------------------------------- |
| `instanceId` | string | 16 hex chars. `SHA256("dylaris-telemetry-instance-v1:" + CLUSTER_SECRET)[:16]`. Stable per deployment, identical across all Cores of one cluster, survives restarts/redeploys/leader changes. This is the deduplication key. |
| `type`       | string | `platform` or `gateway` (from the `routing_mode` setting).              |
| `servers`    | int    | Total active MC servers on this deployment.                             |
| `online`     | int    | Servers currently in status `online`.                                   |
| `players`    | int    | Aggregate current player count. Sent, but NOT surfaced by the counter yet (see below). |
| `version`    | string | Core version, for build-adoption correlation.                          |

## Auth

The `Authorization: Bearer` header is present only when the Core operator sets
`DYLARIS_TELEMETRY_KEY`. The receiver SHOULD enforce a shared secret
(`HEARTBEAT_SECRET`) and reject mismatches with `401`, otherwise anyone can POST
fabricated `instanceId`s and inflate the public counter. Compare in constant
time. An empty/absent header on a secret-enforcing endpoint is a `401`.

## Counting self-hosters / instances

An "instance" for the public counter is one **deployment**, keyed by
`instanceId` (not one Core, not one MC server). Recommended receiver logic:

1. Upsert a row per `instanceId` on every accepted heartbeat, updating a
   `last_seen` timestamp (and optionally `type`, `version`, `servers`).
2. Active instances = `COUNT(DISTINCT instanceId)` where
   `last_seen > now() - 25 minutes`.

The 25-minute window tolerates two missed 10-minute beats before a deployment
is treated as offline, which absorbs a restart or a brief leader handover
without the count flickering. Tune to taste.

Because `instanceId` is deployment-stable, a self-hoster running multiple Cores
for load balancing counts once, and a Swarm redeploy (new container hostnames)
keeps the same id.

## Privacy

No hostname, IP, server name, user, or any identifying string is sent. The only
identifier is a salted, truncated one-way hash of `CLUSTER_SECRET`; it is not
reversible to the secret and carries no host information.

## Not tracked yet

`players` is on the wire but intentionally not displayed. Player-count display
is deferred until the gateway is public. Until then the receiver should ignore
it (or store it without surfacing it).
