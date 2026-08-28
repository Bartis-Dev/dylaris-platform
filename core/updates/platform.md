# Platform updates

Release notes for people who RUN the platform: self-hosters and operators.
Customer-facing notes for BYON and route-only live in `hosted.md`.

Newest release first. The format is fixed and checked in CI - see the
"Release notes" section of the repository's CLAUDE.md before editing.

<!-- Everything in this file is English, including text dictated in German. -->

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
