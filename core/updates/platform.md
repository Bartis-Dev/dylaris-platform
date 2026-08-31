# Platform updates

Release notes for people who RUN the platform: self-hosters and operators.
Customer-facing notes for BYON and route-only live in `hosted.md`.

Newest release first. The format is fixed and checked in CI - see the
"Release notes" section of the repository's CLAUDE.md before editing.

<!-- Everything in this file is English, including text dictated in German. -->

## 2026.08.31

**Update required now** - the panel is no longer a separate image.

### Features
- The panel is served by Core. It is compiled into the binary as a static
  bundle, so there is one image, one port and one version instead of two, and
  the API is on the same origin as the pages by construction. `core`
- Sessions can now travel as an HttpOnly cookie instead of a header, which the
  same-origin panel made possible. Core sets one at login and clears it at the
  new `POST /api/auth/logout`; the Authorization header keeps working
  unchanged. `core`
- Backups on our storage are now kept for one week after suspension rather than
  three months. Backups on a bucket a tenant connected themselves are never
  deleted at any deadline. `core`

### Breaking
- The `panel` image and service are GONE. Remove the `panel` service from your
  compose or stack file and point your reverse proxy at Core on `:25500`; it
  serves the pages and `/api` together. Nothing else to configure - no path
  rule, no CORS. `core`
- `PANEL_API_URL` should now be EMPTY. Core serves the panel, so the API is
  same-origin and is found without being told. Set it only if you terminate the
  API on a second hostname; Core then renders it into both the page config and
  the CSP. `core`
- `FRONTEND_URL` is Core's own public URL now, not a separate panel host. It
  still drives email links and the tab-proxy same-site check. `core`
- `TAB_PROXY_HOST_SUFFIX` is set on Core ONLY. It used to be needed on the panel
  container as well, because the panel wrote its own CSP - and nothing compared
  the two values, so a mismatch broke every proxied tab silently. `core`

### Security
- Nothing.

### Fixes
- A local `docker build` no longer bakes a developer's `.env` into the image.
  The ignore pattern matched the repository root only, so `panel/.env` was in
  the build context and Next inlined its `NEXT_PUBLIC_*` values. CI never saw
  it, which is why it went unnoticed. `core`

## 2026.08.30.5

### Features
- Traffic allowances are now set per region for player traffic, and once for file
  transfers, under Settings, Traffic limits. The two are counted apart, so a
  tenant past one is not past the other. `core` `panel`
- Changing the Java version or the JVM flags no longer reinstalls the server. It
  used to run the installer over a live server directory because that was the
  only path that could rebuild a start command. `core` `node` `panel`
- The setup tab remembers how each sub-server was installed, the exact modpack
  included, and a version or modpack change now asks what to clear first with the
  usual answers ticked. Worlds are never on that list. `core` `node` `panel`
- Settings, Billing now carries the included and bookable backup storage per
  purchased unit. Lowering the bookable figure notifies every tenant who agreed
  to be charged for storage, since it moves the ceiling they agreed to.
  `core` `panel`
- Users can connect an S3 bucket of their own for backups, under Account, Backup
  storage. It needs the new `backupstorage.*` capabilities, which no preset
  grants: connecting arbitrary endpoints makes Core talk to hosts you did not
  choose. `core` `panel`

### Breaking
- Backup storage now scales with the purchase: 50 GB per unit held, editable
  under Settings, Billing. It was one figure for the whole account, so a tenant
  with three units got three times the addresses and traffic and one times the
  backup space. `core` `panel`
- Traffic allowances have no account-wide fallback any more. Until something is
  set under Settings, Traffic limits, nothing is capped and nothing is billed.
  The screen says so when it is empty. `core`
- The "Platform default" traffic scope is gone. Its rows were moved to the tenant
  default on upgrade, and writing to it is now refused. `core`

### Security
- Nothing.

### Fixes
- Backup archives now record which storage they went to. Changing a job's storage
  used to make every earlier archive unrestorable, undownloadable and
  undeletable while the panel still listed it as present. `core`
- Saving the Billing screen no longer caps every tenant's backups at zero. An
  unset R2 quota read back as "0", which the next save stored as a real cap of
  none, while the help text beside it called that "no cap". `core` `panel`
- Metered traffic consent reached Core again. It stopped being recorded when the
  ceiling left the provision payload: the receiving side required BOTH, so a
  tenant who switched metered billing on stayed recorded as having refused it
  and was stopped instead of billed. `core`
- The module bar no longer loses entries for good when the window is narrowed.
  They now come back when it is widened, and its overflow menu opens instead of
  being clipped out of sight. `panel`
- A domain route added during setup appears straight away. It used to read as
  already taken until the routes dialog was opened or the page reloaded. `panel`
- The Java version is now recognised from a selected modpack, which previously
  left whatever was chosen last in place. `panel`

## 2026.08.30.4

### Features
- Nothing.

### Breaking
- Nothing.

### Security
- Deleting a user now removes their protected addresses. Those rows had no
  constraint tying them to the account, and the republisher wrote every stored
  row back into Redis each minute, so a deleted user's address kept routing
  players indefinitely - and came back within a minute if the key was cleared
  by hand. `core`

### Fixes
- A region that starts carrying traffic on a server already being metered is now
  counted. Its first reading was mistaken for history and skipped, so the
  per-region breakdown fell permanently short of the total it breaks down.
  `core`
- Setting one half of a traffic limit to "default" is refused instead of stored
  as "no limit". An override meant to cap purchases alone silently granted
  unlimited included traffic. `core`
- Region and kind are validated when a limit is written. A misspelt one saved
  fine and limited nothing, which looks identical to a working limit. `core`
- The Errors tab no longer leaves an empty panel when the last error ages out
  while it is open. `panel`

## 2026.08.30.3

### Features
- Settings has a Traffic limits tab: the included allowance and the purchase cap
  per region, for player traffic and for file transfers separately. It lists
  every region an edge reports, including the ones nothing limits yet, because
  "no row" and "no limit" look the same from the database and only one of them
  is something an operator meant. `panel`

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Nothing.

## 2026.08.30.2

### Features
- Traffic allowances can be set per region and per kind, with a purchase cap
  beside each one, and overridden per user. A cap of zero is a region where
  extra traffic cannot be bought at all, which is a different thing from no cap
  and is now expressible. `core`
- `/store/usage` returns the per-region breakdown with the allowance that
  applies to each cell, so the store can judge a ceiling per region instead of
  against one summed number. `core`

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Nothing.

## 2026.08.30

### Features
- The Infrastructure view has an Errors tab. Every service already wrote its
  diagnostics to Redis and Core already sent them with the overview, and the
  panel dropped the field - so six components reported into nothing. Only
  errors and warnings drive the tab's count. `panel`

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- The two columns on "My infrastructure" have a floor and stack when the window
  cannot hold both, instead of squeezing the compose file into a column too
  narrow to read it in. `panel`
- The scrollbar on that page sits at the edge of the content area again rather
  than floating mid-screen next to the centred column. `panel`

## 2026.08.29.15

### Features
- Traffic is now recorded per region and per kind, not just as one monthly
  total. Player traffic is attributed to the edge that served it and file
  transfers to the relay that carried them, which are not always the same
  region for the same account. `core`

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Nothing.

## 2026.08.29.14

### Features
- Nothing.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Route-only traffic is now measured. Those addresses carry no server UUID, and
  metering was keyed on the server, so every byte a protected address moved
  reached no counter and no tenant's monthly usage. `core`
- The traffic aggregator no longer stops when a tenant owns no server. An
  account holding only route-only addresses was skipped entirely, which was the
  second half of the same gap. `core`

## 2026.08.29.13

### Features
- Nothing.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Five permissions that did nothing are gone from the role editor: "Delete any
  server", "Delete plans" and the three staff modpack ones. No endpoint ever
  checked them, so granting one conferred nothing and withholding it withheld
  nothing. Roles keep working; only the entries disappear. `core`

## 2026.08.29.12

### Features
- Nothing.

### Breaking
- Nothing.

### Security
- The Redis credential inside a Minecraft container is now limited to the six
  keys the log shipper actually uses. It held the whole `dylaris:server:<uuid>:`
  namespace, which includes the marker that enforces the disk quota - so a
  plugin could delete it and the server was started straight back up. Applied on
  the next node connect; containers keep their credential. `core`

### Fixes
- A warp key that names a region with no leader is no longer placed there. The
  automatic choice already refused such a region; a key stating a preference
  skipped that check, and a machine enrolling into one gets an overlay address
  nothing ever programs. `core`

## 2026.08.29.11

### Features
- Nothing.

### Breaking
- A machine outside the cluster can no longer reach an edge's tunnel port
  through the warp overlay. Its edges must publish `:25560` publicly; until
  they do, route-only and BYON links have no path at all. `core`

### Security
- A tenant can no longer route their players through our overlay. Everything a
  customer machine could claim about itself is environment on that machine, so
  the warp allowlist is now what decides it. `core`
- A bring-your-own node now sends its link to the public edge address whatever
  `NODE_EXTERNAL` and `NODE_TAGS` say. Holding no `CLUSTER_SECRET` is what makes
  a node external, and that is not a claim the machine can clear. `node`

### Fixes
- Nothing.

## 2026.08.29.10

### Features
- The file you deploy on your own machine now sits permanently beside the form
  for both bring-your-own-node and protected addresses, instead of behind a
  fold-out. `panel`
- The address picker says what the region choice decides, and that a domain you
  own can be pointed at us with a CNAME. `panel`

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Protected addresses now survive Redis losing its data. They are recorded in
  the database and written back within a minute; before, each one existed only
  as a Redis key that nothing ever rewrote, so a restart without persistence
  lost every one of them for good while managed routes came back on their own.
  `core`
- Revoking a link kit now removes its addresses even when Redis no longer lists
  them. The teardown read the live routing table, so an emptied cache made it
  report nothing to remove and leave the addresses behind. `core`
- Deleting a protected address whose Redis entry had gone answered "not found".
  Ownership is read from the stored record as well as the cache. `core`
- Addresses that already existed are recorded once at startup, so the repair
  above covers them too rather than only the ones created from now on. `core`

## 2026.08.29.9

### Features
- Nothing.

### Breaking
- `GET /api/gateway/links` now names each link by a digest instead of returning
  its tunnel token. Announced last release as a security fix; naming it here
  because a script reading the `token` field will not find it. `core`

### Security
- Nothing.

### Fixes
- A failed read of the warp region registry could rewrite a live region's
  subnet and re-enable a disabled one. Absence and "could not tell" arrived as
  the same answer, and the write path treated both as absent. `core`
- A peer is no longer assigned to a region that has no leader at all while a
  region with a leader is merely offline. `core`
- Editing a protected address only skips the address allowance for a
  route-only entry, not for a managed server's route on the same name. `core`

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
