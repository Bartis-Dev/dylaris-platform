# Platform updates

Release notes for people who RUN the platform: self-hosters and operators.
Customer-facing notes for BYON and route-only live in `hosted.md`.

Newest release first. The format is fixed and checked in CI - see the
"Release notes" section of the repository's CLAUDE.md before editing.

<!-- Everything in this file is English, including text dictated in German. -->

## 2026.08.28

### Features
- Updates now have a version. Every image carries the release it was built from,
  and the panel shows which of your components are behind rather than a running
  count of changelog lines. `core` `panel` `node` `log-shipper`
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
- A node cap now counts every pending node identity, not just its own kind.
  Enrolment tokens and warp keys are separate records, and each gate counted
  only one of them, so a cap of 2 could hand out 4. `core`
- A plan with zero protected addresses now means zero. It was read as "no
  setting" and fell through to the platform default, which is unbounded when
  none is configured. `core`
