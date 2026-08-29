# Service updates

Release notes for DYLARIS customers: BYON (you run a node on your own hardware)
and route-only (we route, you run nothing).

Route-only entries name no service on purpose - there is nothing on your side to
update, and those entries tell you what changes on ours.

Newest release first. The format is fixed and checked in CI - see the
"Release notes" section of the repository's CLAUDE.md before editing.

<!-- Everything in this file is English, including text dictated in German. -->

## 2026.08.29.5

### Features
- Your machine now reaches our edges directly instead of through the tunnel.
  Your players no longer share that tunnel with your own uploads, and they are
  no longer dropped when it restarts. Copy your deploy file from the panel again
  to get it. `node`

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
- A tunnel that cannot be set up now says so instead of retrying quietly. If
  your warp log repeated "no leader endpoint", that was this. Fixed on our side.

## 2026.08.29.2

### Features
- Your deploy file now marks every setting as "keep" or "EDIT", and says how to
  create and start it.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Creating an address could fail with "You have used all -1 addresses" even on a
  plan that allows them. Fixed on our side; try again.

## 2026.08.29

### Features
- The deploy file for a BYON node or a route-only link now comes with the steps
  to create and start it, for Linux and for Windows.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- The deploy file could carry the wrong address for our API, so the tunnel never
  came up. Copy it from the panel again if yours did not connect. Fixed on our
  side.

## 2026.08.28

### Features
- Your node now reports which build it is running, so the panel can tell you
  when an update is waiting. `node`
- Updates are announced on our Discord. Pick the **BYON** or **Route-Only**
  role under Channels & Roles, or ignore it - the panel tells you the same.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- If your node goes offline mid-install, the panel now says so instead of
  showing a spinner. Nothing is lost; it resumes when the node reconnects.
  `node`
- Node allowances now count correctly. Two kinds of pending node identity were
  counted separately, so an account could exceed its plan and then be warned
  for it. Fixed on our side.
- An address allowance of zero now means zero. It was read as "no setting" and
  fell back to unlimited. Addresses on your own domain stay unlimited and
  uncounted. Fixed on our side.
