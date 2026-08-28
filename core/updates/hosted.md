# Service updates

Release notes for DYLARIS customers: BYON (you run a node on your own hardware)
and route-only (we route, you run nothing).

Route-only entries name no service on purpose - there is nothing on your side to
update, and those entries tell you what changes on ours.

Newest release first. The format is fixed and checked in CI - see the
"Release notes" section of the repository's CLAUDE.md before editing.

<!-- Everything in this file is English, including text dictated in German. -->

## 2026.08.28

### Features
- Your node now reports which build it is running, so the panel can tell you
  when an update is waiting instead of leaving you to guess. `node`
- Update announcements now arrive in Discord. Pick the BYON or route-only role
  under Channels & Roles to get the ones that concern you.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Node allowances now count correctly. Enrolment tokens and warp keys were
  counted by separate gates, so an account could end up holding more pending
  node identities than its plan allows and then be warned for it. Fixed on our
  side; nothing for you to do.
- An address allowance of zero now means zero. It was being read as "no setting"
  and fell back to the platform default, which is unlimited when none is
  configured. Addresses on your own domain stay unlimited and uncounted either
  way. Fixed on our side; nothing for you to do.
