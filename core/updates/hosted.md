# Service updates

Release notes for DYLARIS customers: BYON (you run a node on your own hardware)
and route-only (we route, you run nothing).

Route-only entries name no service on purpose - there is nothing on your side to
update, and those entries tell you what changes on ours.

Newest release first. The format is fixed and checked in CI - see the
"Release notes" section of the repository's CLAUDE.md before editing.

<!-- Everything in this file is English, including text dictated in German. -->

## 2026.08.31.4

### Features
- Nothing.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- The Beam desktop app refused every save and upload. It works again; update the
  app is not needed, the fix is on our side.

## 2026.08.31.2

### Features
- Nothing.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- The panel was briefly unreachable after yesterday's release, showing a
  placeholder page instead. It is back; nothing on your side needed changing.
- Custom tabs such as a map viewer would not open from the panel. They load
  again.

## 2026.08.31

### Features
- Nothing.

### Breaking
- Backups we hold for you are now kept for one week after a suspension instead
  of three months. During that week you can still see and download them, and
  your server files are untouched. Backups in a bucket you connected yourself
  are never deleted by us, at any point.

### Security
- Nothing.

### Fixes
- Nothing.

## 2026.08.30.5

### Features
- Backups now come with 50 GB of storage per subscription, and metered storage is
  there if you need more: off until you turn it on, stopping at a ceiling shown
  before you agree, and we tell you if that ceiling ever changes.
- Your panel shows this month's traffic under My infrastructure, one bar per
  allowance, and your account page shows how much more you may book on each.
  Player traffic and file transfers are separate, and you are warned at 80% and 90%.
- You can connect an S3 bucket of your own for backups, under Account, Backup
  storage. What lands there is not counted against your allowance and never
  billed, and we never delete from it - not on suspension, not ever.
- Changing your Java version or JVM flags no longer reinstalls the server. Your
  files stay exactly as they are and the change applies straight away.
- Setting up a server remembers which modpack you installed and puts it back when
  you edit it. Changing a version or a modpack asks what to clear first and ticks
  the usual answers for you; your world is never on that list.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- A domain you add while setting a server up now appears straight away. It used
  to look like the name was already taken until you reloaded the page.
- The menu bar no longer loses entries when the window is made narrower, and its
  overflow menu opens properly.

## 2026.08.30.4

### Features
- Nothing.

### Breaking
- If you have agreed to be billed for extra traffic, there is now a ceiling on
  how much may be bought in a region. Past it your servers stop rather than
  keep billing. Nothing changes until your provider sets one, and nothing on
  your side needs updating.

### Security
- Nothing.

### Fixes
- Nothing.

## 2026.08.30

### Features
- Nothing.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- On "My infrastructure", the compose file you copy now gets a column wide
  enough to show its longest line, and the two columns stack instead of
  shrinking when your window is narrow. Nothing for you to update.

## 2026.08.29.14

### Features
- Nothing.

### Breaking
- Traffic through a protected address now counts against your included traffic.
  It was not being counted at all, so your usage figure will start moving where
  it previously stayed flat. Nothing on your side changes and there is nothing
  for you to update.

### Security
- Nothing.

### Fixes
- Nothing.

## 2026.08.29.11

### Features
- Nothing.

### Breaking
- Your link now reaches us over the internet only, never through the tunnel it
  uses for everything else. It was doing both, and the tunnel route made your
  players share one connection with your own uploads and drop whenever that
  connection restarted.
- If you run your own node, this now holds no matter how it is configured. The
  setting that used to decide it is on your machine, so it no longer gets a
  vote. `node`

### Security
- Nothing.

### Fixes
- Nothing.

## 2026.08.29.10

### Features
- The file you run on your machine is now shown permanently next to the form,
  for both your own node and your protected addresses. You no longer have to
  open a fold-out to get it back when you rebuild the machine.
- When you pick an address, the panel now says what the region choice decides
  for your players, and that you can point a domain you own at us instead.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- A protected address could disappear after maintenance on our side and never
  come back. Addresses are now stored durably and restored on their own; you do
  not need to create them again.
- Revoking a link kit now reliably removes the addresses that ran through it.

## 2026.08.29.6

### Features
- Your protected addresses can be edited. Change the local address or port in
  place instead of deleting the address and creating it again.

### Breaking
- Nothing.

### Security
- Nothing.

### Fixes
- Changing an address you already have no longer counts against your address
  allowance. Fixed on our side.

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
