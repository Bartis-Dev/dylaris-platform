# Release notes

Two files, one format, two audiences:

| File | Who reads it |
|---|---|
| `platform.md` | people who RUN the platform: self-hosters and operators |
| `hosted.md` | DYLARIS customers: BYON (they run a node) and route-only (they run nothing) |

Both are embedded into Core at build time (`release.go`) and served by
`GET /api/updates`. Core prefers the copy it fetches from the public raw URL so a
running install sees releases published after it was built, and falls back to the
embedded copy when that fetch fails - which is what an air-gapped install wants,
and which reads as "you are current" rather than as an error.

The full format, the rules and the release process live in the repository's
CLAUDE.md under "Release notes". Do not learn the format from here; that section
is the one CI validates against.

## The version

CalVer per REPO: `2026.08.28`, and `2026.08.28.2` for a second release the same
day. It is written by hand as the heading of a new block and CI stamps it into
every image built from that commit.

It is read across BOTH files, because the repo has one version and more than one
audience: a release that only concerns customers adds a block to `hosted.md` and
none to `platform.md`, and reading `platform.md` alone would stamp images with a
version older than the release they were built from.

## How a component knows its own version

Stamped at BUILD time, never derived from these files:

| Component | How |
|---|---|
| `core` | `RELEASE_VERSION` ldflags → `main.releaseVersion` |
| `node` | the same, sent in the heartbeat and at gRPC auth |
| `panel` | `NEXT_PUBLIC_RELEASE_VERSION`, sent as a query parameter - it is a static bundle in a browser and Core cannot see it otherwise |

Core must NOT read its version off the `platform.md` it embeds. After a
customers-only release that block is older than the image, so Core would report
itself behind for a release it already contains.

An empty version means UNKNOWN and is never treated as old. "Is X behind" is
answered per component - is there a release NEWER than X's version that NAMES X -
which is why one version per repo does not mark every component outdated at once.

## What replaced what

`feed.jsonl` was an append-only JSONL changelog whose LINE COUNT stood in for a
version. It worked, and every component already reported its own position, which
is why five nodes on five builds displayed correctly. What it could not do:

- a line count is not something a human can say, compare, or put in a message;
- two releases on one day were indistinguishable;
- there was no delivery channel but the panel, so a BYON customer who never
  opened it learned nothing.

It is DELETED here. The gateway repo's `updates/feed.jsonl` is frozen rather than
deleted, because images built against it still fetch it and read an unchanged
file as "no updates", which is correct for them.

## Announcements

A CI job posts one Discord message per audience whose top block is the release
being cut, after the builds and after an environment wait timer. Idempotency is a
git tag (`platform-<version>`, `hosted-<version>`) pushed BEFORE the message: that
push fails if the tag exists, which is the whole lock. No webhook secret
configured means a green no-op.
