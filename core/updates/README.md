# Platform update feed

`feed.jsonl` is the platform's append-only changelog, one JSON object per line:

    {"date":"2026-07-21","service":"core","type":"feature","summary":"Short human summary"}

Fields: `date` (`YYYY-MM-DD`), `service` (e.g. `core`, `panel`, `node`), `type`
(one of `feature | fix | change | security`), `summary` (short human text).
Append one line per user-facing change; never edit, reorder, or delete existing
lines. The panel derives each user's "since your install" delta from the line
COUNT, so the order and count are load-bearing (an append-only log, not an
editable changelog). Blank lines are ignored and a single malformed line is
skipped, so one bad row will not blank the feed.

The file ships empty. An empty feed means "no updates", never an error
(`GET /api/updates`, admin-only, is fail-open: any fetch error falls back to the
empty baked baseline).

## How the panel sources it

Two layers:

1. **Build-baked baseline** — this file is `//go:embed`-ed into Core and its
   line count is the "installed" mark. It normally stays empty.
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
