# Platform updates

Release notes for people who RUN the platform: self-hosters and operators.
Customer-facing notes for BYON and route-only live in `hosted.md`.

Newest release first. The format is fixed and checked in CI - see the
"Release notes" section of the repository's CLAUDE.md before editing.

<!-- Everything in this file is English, including text dictated in German. -->

## 2026.08.28

### Features
- Updates now have a version. Core, the panel and every node report the release
  they were built from, and the updates view shows which of yours are behind
  rather than a running count of changelog lines. `core` `panel` `node`
- The updates view is no longer admin-only. Anyone signed in sees the releases
  that concern them and the versions their own machines report, which for a
  BYON customer is the only place their node's build was ever visible. `core`
  `panel`
- Releases are announced on our Discord, one message per audience. Pick the
  **Platform** role under Channels & Roles to be notified. Nothing here depends
  on it: the panel keeps showing the same releases whether or not you use
  Discord, and a self-hosted build announces nothing of its own.
- Every operator-set limit now uses the same control and the same meaning: No
  limit for uncapped, or a number where 0 means none. That covers sub-servers per
  server, the node-local backup quota, ticket attachment quotas and the beam
  upload limits, which each had their own widget and their own reading of a
  zero. `core` `panel` `node`
- Limits read the same way everywhere: leave a limit empty for unlimited, or
  switch it off and type a number, where 0 means none. Route allowances used to
  spell unlimited as -1 on one screen and as 0 on another. `core` `panel`

### Breaking
- STARTTLS is now required when the mail encryption setting says starttls. It
  used to fall back to sending in the clear if the relay did not offer it, and
  report success. Send a test mail before opening registration: these messages
  carry verification and password-reset links, and if the relay genuinely has no
  TLS the honest setting is encryption "none". `core`
- Changing the SMTP host, port or username now requires re-entering the
  password, and the same applies to the mod-cache Redis address. Have the
  credential to hand when you edit either. `core`

### Security
- A blank password field no longer means "keep the old one" when the server it
  belongs to has changed. Both SMTP and the mod-cache Redis transmit their
  credential, so pointing an existing configuration at a new host would have
  handed that credential to the new host. `core`
- Changing a password now ends every other session for that account. A reset
  used to stop the old password working while leaving open sessions alive for up
  to 24 hours, including the one it was meant to close. `core`

### Fixes
- A limit of 0 now means none instead of switching the check off. A per-user
  backup quota of 0 granted unlimited storage, ticket attachment quotas of 0
  allowed any attachment, and beam upload limits of 0 allowed any upload - in
  each case the guard tested "greater than zero" and so skipped itself on the one
  value an operator would type to forbid something. `core` `node`
- The sub-server limit could express neither extreme. A saved 0 was discarded by
  the same kind of guard and fell back to the built-in default, so asking for
  none and asking for unlimited both produced a cap of three. `core` `panel`
- A server whose node dies mid-install now says so. It used to keep showing
  "installing" with nothing anywhere explaining why, which on a node somebody
  runs at home is an ordinary support ticket. The status is unchanged because it
  is still true - the install resumes on its own when the node reconnects - and
  the server page now says that instead of showing a spinner forever. `core`
  `panel`
- A node whose owner has missed a mandatory update is now warned at connect and
  refused once the deadline passes, with the reason stated instead of a
  connection that simply stops working. Set RELEASE_ENFORCE_MIN_VERSION=false on
  Core to warn without refusing. `core` `node`
- A node cap now counts every pending node identity, not just its own kind.
  Enrolment tokens and warp keys are separate records, and each gate counted
  only one of them, so a cap of 2 could hand out 4. `core`
- A plan with zero protected addresses now means zero. It was read as "no
  setting" and fell through to the platform default, which is unbounded when
  none is configured. `core`
