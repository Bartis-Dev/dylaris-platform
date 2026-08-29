# Platform updates

Release notes for people who RUN the platform: self-hosters and operators.
Customer-facing notes for BYON and route-only live in `hosted.md`.

Newest release first. The format is fixed and checked in CI - see the
"Release notes" section of the repository's CLAUDE.md before editing.

<!-- Everything in this file is English, including text dictated in German. -->

## 2026.08.29.8

### Features
- Nothing.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- The Gateway settings screen no longer shows its own defaults when it cannot
  load. Both panels used to lift the skeleton either way, so a failed request
  rendered as "no limits, no domains, everything off" - and nothing typed in
  could be saved, without saying so. `panel`

## 2026.08.29.7

### Features
- Nothing.

### Breaking
- Nothing.

### Security
- Route and link listings no longer return the tunnel token a link
  authenticates with. For a managed server that token is derived from the NODE,
  so it was the same credential for every server that node hosts, and reading
  it needed only `network.read` on one of them. `core`
- A holder of that token could open a tunnel to the edge and receive a share of
  the player connections meant for the real link. Nothing on the client side
  ever read the field. `core`

### Fixes
- Nothing.

## 2026.08.29.6

### Features
- Protected addresses can be edited. Change a route's link, local address or
  port in place instead of deleting it and creating it again. `core` `panel`

### Breaking
- Nothing.

### Security
- The route listing no longer returns each link's tunnel token. That token is
  what a link presents to claim its tunnel at the edge, and nothing read it.
  `core`

### Fixes
- Editing a route no longer counts as a new address, so an account at its
  address limit can still change a route it already owns. `core`

## 2026.08.29.5

### Features
- A link on a customer machine now reaches the edge over the internet instead of
  through the warp tunnel. Players no longer share that tunnel with the same
  customer's uploads, and a warp restart no longer drops their sessions.
  `core` `node` `panel`

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Nothing.

## 2026.08.29.4

### Features
- Warp leaders register themselves. A leader announces its region, subnet and
  public endpoint, so adding an edge host no longer means typing a row into
  Settings -> Warp. Existing rows are kept, and a leader you disable stays
  disabled. `core` `panel`

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Nothing.

## 2026.08.29.3

### Features
- Nothing.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Enrolling into a region with no leader endpoint is now refused with a reason.
  It used to succeed and hand out an address with nothing to dial, so the
  machine retried a config that could not work, every five seconds, silently.
  `core` `panel`

## 2026.08.29.2

### Features
- Every setting in a deploy snippet is now marked "keep" or "EDIT", so it is
  clear what must stay as it is. `panel`

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- A route limit stored as -1 refused every route instead of allowing any. It is
  the old spelling of "unlimited", and caps are tested as "count is at the
  limit", which a negative meets with nothing held. `core`
- The Gateway settings screen no longer starts its four route limits at that
  -1, which saving before the screen had loaded wrote to the database. `panel`

## 2026.08.29

### Features
- The deploy snippets now say how to create the compose file, with separate
  steps for Linux and for Windows, and a note for Portainer. `panel`

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- BYON and route-only deploy snippets took Core's address from the panel's own
  URL. An install that serves the API on a second host handed the customer a
  file that could never enroll. `panel`

## 2026.08.28

### Features
- Updates now have a version. Core, the panel and every node report the release
  they were built from, and the updates view shows which of yours are behind.
  `core` `panel` `node`
- The updates view is no longer admin-only. Anyone signed in sees the releases
  that concern them and what their own machines report. `core` `panel`
- Releases are announced on our Discord. Pick the **Platform** role under
  Channels & Roles. The panel shows the same releases either way.
- One control and one meaning for every operator-set limit: no limit, or a
  number where 0 means none. Covers sub-servers, the node backup quota, ticket
  attachment quotas, beam uploads and route allowances. `core` `panel` `node`

### Breaking
- STARTTLS is now required when the mail encryption setting says starttls. It
  used to fall back to plain text and report success. Send a test mail before
  opening registration. `core`
- Changing the SMTP host, port or username now requires re-entering the
  password. Same for the mod-cache Redis address. `core`

### Security
- A blank password field no longer keeps the old credential when the server it
  belongs to has changed. SMTP and the mod-cache Redis both transmit theirs, so
  repointing a configuration would have handed it to the new host. `core`
- Changing a password now ends every other session for that account. A reset
  used to leave open sessions alive for up to 24 hours. `core`

### Fixes
- A limit of 0 now means none instead of switching the check off. Backup
  quotas, ticket attachment quotas and beam upload limits all tested "greater
  than zero" and so skipped themselves on that one value. `core` `node`
- The sub-server limit could express neither extreme: a saved 0 fell back to
  the built-in default, so none and unlimited both produced a cap of three.
  `core` `panel`
- A server whose node dies mid-install now says so instead of showing a
  spinner. The install resumes on its own when the node reconnects. `core`
  `panel`
- A node that missed a mandatory update is warned at connect and refused past
  the deadline, with the reason stated. `RELEASE_ENFORCE_MIN_VERSION=false`
  warns without refusing. `core` `node`
- A node cap now counts every pending node identity. Enrolment tokens and warp
  keys were counted by separate gates, so a cap of 2 could hand out 4. `core`
- A plan with zero protected addresses now means zero, not "unset". `core`
