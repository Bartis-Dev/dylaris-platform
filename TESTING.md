# Testing

DYLARIS platform has a real, maintained test suite. This documents the conventions so contributions stay consistent.

## Policy

- New code ships with tests. Security-critical and high-value pure logic gets tests as a standing practice, not a one-time backfill.
- No vanity coverage percentage. Chasing a number produces junk tests; the bar is "does this test catch a regression a reviewer would care about."
- (Historical note: platform ran without an automated test suite by an earlier project decision. That decision has been reversed - tests are expected going forward, starting with the Phase 1 slice covering zip-slip/decompression-bomb guards, SSRF-safe fetch, mrpack manifest parsing, config secret handling, backup path containment, JWT issuance, and Redis-ACL credential derivation.)

## Go

- stdlib `testing` only. No `testify`, no `gomock`, no other assertion/mocking library. Table-driven tests, `_test.go` beside the code it tests, same package by default (a `_test` external package only when it reads meaningfully cleaner, e.g. to avoid an import cycle).
- Run per Go module (the workspace root itself is not a runnable package set - `go test ./...` from the repo root fails with "directory prefix . does not contain modules listed in go.work"): `cd core && go test -count=1 ./...` (repeat for `node`, `pkg`, `log-shipper`). Keep `go build ./...` and `go vet ./...` clean alongside tests.
- For code coupled to `store.Store`, Redis, or raw SQL (Phase 2+):
  - **Store**: embed `store.Store` (interface, nil-valued) inside a small fake struct and override only the methods your code path touches. Any unoverridden method call panics - the test never makes one. See `core/handlers/warp_test.go` for the canonical example.
  - **Redis**: `github.com/alicebob/miniredis/v2` - an in-process Redis, point a real `*redis.Client` at it.
  - **Raw SQL**: `github.com/DATA-DOG/go-sqlmock`.
  - **HTTP**: `net/http/httptest`.
- Golden-vector tests (e.g. `core/services/redisacl`) pin an exact known-answer value so a silent algorithm drift breaks the build loudly. Do not "fix" a failing golden test by updating the expected value without understanding why the output changed - a mismatch there usually means a real behavior change that needs to ship in lockstep with any other side depending on that exact byte format (see the `redisacl` package doc comment for a concrete case: Core and `node/redisacl.go` must derive byte-identical credentials, verified by pinning the same golden vectors in both, since the two are separate Go modules that cannot import each other).

## Panel (TypeScript)

- `vitest`, already configured (`panel/vitest.config.ts`, `environment: 'node'`). Phase 1 covers pure `src/lib/**/*.ts` logic only - no component/RSC/E2E testing yet (that needs jsdom + Testing Library + Playwright, a separate future workstream).
- `*.test.ts` beside the module it tests. See `panel/src/lib/api/core.test.ts` or `panel/src/lib/cpuset.test.ts` for the house style: `describe`/`it`/`expect`, one `describe` block per exported function, one `it` per behavior.
- Run: `cd panel && npx vitest run`.

## CI

Every push and pull request runs the Go test matrix (`core`, `node`, `pkg`, `log-shipper`) and the panel vitest suite; see `.github/workflows/test.yml`. A failing test fails the build.
