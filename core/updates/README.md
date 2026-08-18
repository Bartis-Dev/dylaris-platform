# Platform update feed

`feed.jsonl` is the platform's append-only changelog, one JSON object per line:

    {"date":"2026-07-21","service":"core","type":"feature","summary":"Short human summary"}

Fields: `date` (`YYYY-MM-DD`), `service` (e.g. `core`, `panel`, `node`), `type`
(one of `feature | fix | change | security | breaking`), `summary` (short human
text). Append one line per user-facing change; never edit, reorder, or delete
existing lines.

`service` must name the component that ACTUALLY changed: it drives the
per-component grouping in the modal and the per-component "is this one behind"
answer below, so a wrong value misfiles the entry twice.

`breaking` means the update does NOT finish applying itself and the operator
must DO something. It is not "this is significant" — the panel leads the modal
with a callout for it. A feature that needs a switch flipped to turn ON is not
breaking; say that in the summary. The panel derives each user's "since your install" delta from the line
COUNT, so the order and count are load-bearing (an append-only log, not an
editable changelog). Blank lines are ignored and a single malformed line is
skipped, so one bad row will not blank the feed.

An empty feed means "no updates", never an error (`GET /api/updates`,
admin-only, is fail-open: any fetch error falls back to the baked baseline).

**Appending is part of shipping, not a follow-up.** Two full rounds of work once
went out with nothing appended; the remote feed stayed exactly as long as the
baseline baked into every deployed build, the delta was zero, and the panel
correctly rendered "no updates" — which read as a broken feature. Nothing in the
build catches this: everything compiles, every test passes, and the only symptom
is a screen that says nothing.

## How the panel sources it

Two layers:

1. **Build-baked baseline** — this file is `//go:embed`-ed into Core and its
   line count is the "installed" mark.
2. **Runtime remote fetch** — Core fetches a raw JSONL URL live (8s timeout,
   1 MiB cap, 15-min cache) and diffs its line count against the baked baseline
   to compute "new since your build". The default URL is:

       https://raw.githubusercontent.com/Bartis-Dev/dylaris-platform/main/core/updates/feed.jsonl

Because the delta is computed against the running build's baked count, you do
NOT need to rebuild the image to publish updates: appending lines to the remote
file is enough, and the running Core picks them up within the 15-minute TTL.
The panel polls every 60s, but is served from that cache, so 15 minutes is the
worst-case delay between a push and the bell lighting up.

One consequence of the baked baseline is worth knowing when publishing: a line
appended in the same commit an image is built from is baked into that image, so
whoever runs that image will NOT see it (correctly — they already have the
change). Entries only ever surface to builds older than the commit that added
them.

## Per-component baselines

One baseline for the whole feed is only correct when everything is deployed
together. An operator who updates Core and leaves the nodes alone was previously
told the node's changes were installed, because Core's baseline had moved past
them. Each component therefore reports where IT is:

| Component | Source |
| --- | --- |
| `core` | the feed embedded in its own binary |
| `node` | stamped at build time (`FEED_BASELINE` → `-X main.feedBaseline`), sent on the heartbeat; Core publishes the LOWEST value any live node reports, because a fleet is only as updated as its oldest member |
| `panel` | stamped at build time (`NEXT_PUBLIC_FEED_BASELINE`) and sent with the request — the panel is a static bundle in a browser and Core cannot see it otherwise |

Both stamps are computed in `.github/workflows/build-service.yml` (non-blank line
count, matching `updates.LineCount`). `build-core` deliberately does NOT take
one: Core reads its embedded copy.

A component that reports nothing falls back to Core's baseline and is flagged
`baselineKnown: false`, rendered as "assumed". That flag is load-bearing — "up
to date" and "nobody asked" must not look identical. `log-shipper` has no
channel to Core and stays assumed.

## Owner-population steps

1. In the PUBLIC `Bartis-Dev/dylaris-platform` repo, edit this file
   (`core/updates/feed.jsonl`) and **append** one JSON line per change, using
   the schema above. Commit to `main`.
2. The repo/branch/path in the default URL must be publicly raw-readable. If the
   repo stays private, or you want a self-hosted mirror, set the env var on the
   Core service instead:

       UPDATES_FEED_URL_PLATFORM=<your raw JSONL URL>

   It is unset by default, so the GitHub raw URL above is used.
3. That is all for the platform feed. The panel's update bell (admin-only)
   surfaces the new lines within the cache TTL; each admin's badge clears when
   they open the panel (`PUT /api/me/updates-seen`).

The optional gateway feed is documented separately in
`gateway/updates/README.md` (private repo, cross-pushed into
`core/updates/gateway-feed.jsonl`, surfaced only when
`UPDATES_FEED_URL_GATEWAY` is set and gateway routing is enabled).
