---
name: spec-first-testing
description: Implement a spec-first, test-driven approach in sightpane/sightpane: write the test before the implementation, watch it fail, then make it pass. Use whenever the user asks to implement a feature or fix with TDD, "write tests first", "önce test yaz", when adding tests to existing code, or when a bug must be locked down with a regression test.
---

> **This repository is one of three.** [sightpane/sightpane](https://github.com/sightpane/sightpane)
> is the Go backend, [sightpane/ui](https://github.com/sightpane/ui) the Flutter web
> dashboard, [sightpane/flutter](https://github.com/sightpane/flutter) the Dart SDK.
> The only thing they share is the envelope contract (`POST /api/v1/envelope`), so a
> change to it needs a matching change in the other two — say so explicitly rather
> than assuming whoever reads this can see them.

# Spec-First Testing (TDD)

Write the tests before the implementation, so the tests describe the behaviour you
*intend* rather than the behaviour you happened to produce. A test written after the
code tends to assert whatever the code already does — including its bugs.

## The one rule that matters

**Do not read the implementation of the behaviour you are about to specify.** Derive the
tests from the spec, not from the code.

| Read it | Don't read it |
|---|---|
| The envelope contract (`package/lib/src/models.dart` ↔ `internal/store/store.go` `itemHead`) | The function body you're about to change |
| The schema, in `internal/store/migrations/*.sql` | `internal/store/schema_test.go` and the existing store tests (they encode today's behaviour) |
| The fakes: `FakeTransport`, `FakeApi`, `newTestServer` | The implementation you are about to replace |
| Sibling modules for conventions | |

In a brownfield codebase you often cannot scope the task without reading the current
implementation — that's fine. What matters is the ordering of *commitment*: once you
know what the new behaviour should be, write its tests **before** you write or modify a
line of it, and say in your summary that you read implementation for the gap analysis.

## Where tests live and how they run

| Part | Command | Harness |
|---|---|---|
| SDK | `cd package && flutter test` | `test/fake_transport.dart` (`FakeTransport`), `fakeAsync` for the queue, `tester.runAsync` for `toImage`/PNG decode, `Sightpane.init(... transport: t)` |
| Backend | `go test ./...` | `newTestServer(t)` (a schema of its own in the container `internal/testdb` started + owner token), `httptest`, raw sockets for PROXY protocol |
| Dashboard | `cd frontend && flutter test` | `test/helpers/test_app.dart`: `FakeApi`, `pumpApp` (one `ProviderContainer` for router and pages), `setUpLoggedIn` |

## Workflow

1. **Read the spec.** If it's a bug: what *exactly* is wrong, and what should it be?

2. **Enumerate the failure modes — the ways it could break silently.** In this repo they
   cluster around:
   - **Contract drift.** SDK sends a field the backend ignores (counted as `rejected`
     silently) or the backend adds a column the dashboard parser drops.
   - **Queue invariants.** An error dropped under `maxQueue` pressure; a failed batch
     re-queued at the tail instead of the head; backoff measured with `DateTime.now()`
     instead of `clock.now()` so `fakeAsync` never reaches it.
   - **Timers.** A `Timer` not cancelled in `close()`/`dispose()` — the widget test
     fails on "pending timers", which is the test catching a real leak.
   - **Throttles and dedupe.** Unchanged frames skipped when they carry taps; pointer
     samples suppressed by the time gate when the distance gate should let them through.
   - **Idempotence.** `EnsureIndexes`-style startup work, `Ingest` for the same session
     id, `FramePrefetcher.setCursor` on the same index — the second call must be a no-op.
   - **Date-bound fixtures.** A fixture with a literal date passes today and fails next
     week (our stats test broke exactly this way). Derive dates from `time.Now()` /
     `DateTime.now()` or from the query the fake receives.
   - **Falsy-vs-absent.** `browser: ""` vs no `browser`; `SIGHTPANE_API_URL` empty (same-origin)
     vs unset (localhost).

3. **Write the tests.** Each test names the failure it prevents (a comment saying *why
   it exists*, not restating the assertion).

4. **Run them and WATCH THEM FAIL.** A test that has never failed has not been shown to
   test anything. If it passes on the first run against unmodified code, either the
   behaviour already existed (say so) or the test doesn't exercise what you think. A test
   that *errors* (missing import, wrong finder, `Bad state: No element`) has not gone
   red — read the message and confirm it is your assertion failing for your reason.

5. **Review the tests against the spec.** Every clause covered? Any test that would still
   pass if you deleted the feature?

6. **Implement.** Only now read/modify the implementation.

7. **Run them and watch them pass.** Then the *whole* suite of that part, and `flutter
   analyze` / `go vet`. A green new test with a broken old one is a net loss.

## Derive the expected value by hand

If the expectation is computed by the code under test, the assertion is a mirror.

```dart
// Mirror: same helper on both sides. Always passes.
expect(HourPrefix(id, t), HourPrefix(id, t));

// Hand-derived literal: fails the moment the format changes.
expect(hourPrefix('cam1', DateTime.utc(2026, 9, 8, 14)), 'cam1/2026-09-08/14/');
```

Go: table-driven cases with literal `want`. Dart: literal maps in `models_test.dart`.

## Mocks test your intent. They do not test reality.

A unit test with a faked boundary proves your code did what you meant. It cannot prove
the other side accepts it. In this repo the boundaries that need at least one real check:

- **SDK → backend.** `FakeTransport` proves the envelope was built; only a real `POST`
  proves the backend accepts it. `package/tool/smoke_send.dart` sends a real envelope
  to a running backend — use it (or `curl`) once per contract change.
- **The database.** `newTestServer` opens a real schema in a real TimescaleDB, so query tests are real. Use
  it for every new query; don't fake `Store`.
- **Sockets and proxies.** PROXY protocol policies (`USE`/`IGNORE`/`REQUIRE`) only
  showed their real behaviour on a real listener (`proxyproto_test.go`); the library's
  default was REQUIRE, which no mock would have revealed.
- **Images.** `toImage` and PNG decoding need `tester.runAsync`; assert on decoded pixels
  (`replay_test.dart`), not on the byte length.
- **External services** (LiveKit egress, MinIO, Caddy, `requestFullscreen`): fake them
  behind an interface (`recording.EgressAPI`, `CameraStreamService`), test the
  reconciliation logic, and state plainly that the live path is unverified.

Never point verification at a real project's `sightpane.db` or a production backend.

## Test-harness traps that produce false failures

- **Two `ProviderContainer`s.** If the router is read from one container and the widget
  tree is pumped in another, auth redirects never fire and every page test "fails".
  Use `pumpApp(tester, testContainer(api))` — one container.
- **Pending timers.** SDK widget tests must end with `await Sightpane.close()` inside the test
  body (tearDown runs after the pending-timer check). Dashboard periodic refreshers
  (`OverviewPage` 1 s timer, `ReplayController`) must be disposed by the widget.
- **Real time vs fake time.** `fakeAsync` does not advance `DateTime.now()`; production
  code on the retry path uses `clock.now()`. A test that "randomly" throttles is usually
  real time leaking in — set the interval to `Duration.zero` or inject a clock.
- **Formatting rewrites.** `dart format` reflows code; a scripted text replacement that
  silently matched nothing leaves the old behaviour in place and the new test failing
  for the wrong reason. Confirm the edit landed (`grep -c`) before trusting the red.
- **Turkish uppercase.** `toUpperCase()` turns `Siyah` into `SIYAH`; finders for
  `SİYAH` fail. Use `trUpper`.

## Regression tests

When fixing a bug, the test must fail on the *old* code — verify it. Write the comment
so the next reader knows what they'd be reintroducing:

```dart
// REGRESSION. PageRouteBuilder's reverseTransitionDuration defaults to 300 ms, so the
// fullscreen page was still in the tree after ESC and every "closes on ESC" assertion
// failed. Both durations are pinned to zero; don't "simplify" this away.
```

## Anti-patterns

- ❌ Tests written by reading the implementation and restating it.
- ❌ Happy path only, then "fully tested".
- ❌ A suite that passes on the first run, unexamined.
- ❌ Asserting internals (a private method was called) rather than behaviour.
- ❌ Fake-only coverage of a boundary whose correctness lives elsewhere (SQL, sockets, PNG).
- ❌ Loosening a test until it passes.
- ❌ Literal dates in fixtures.

## Rationalisations, and what is actually true

| Excuse | Reality |
|---|---|
| "I'll write the tests after" | They pass immediately and prove nothing; you never saw them catch the bug. |
| "Too simple to need a test" | The heartbeat case that never cleared `ended_at` was three lines. |
| "The suite is green, so it's covered" | Green is not coverage — revert the fix and watch the new test go red. |
| "Verified it by hand in the browser" | Leaves no record, cannot re-run, skips cases under time pressure. Say what you saw, then add the test. |
| "It's just a label / a regex" | `toUpperCase()` on Turkish text broke a finder and hid a real rendering bug. |
| "I'd have to delete an hour of work" | Sunk cost. The hour is spent either way; only one path ends with code you can trust. |

## Report honestly

State plainly: which tests were written before the implementation and which after;
whether you observed RED; what is verified against a real backend/database/socket and
what is only faked; what you did **not** cover.

---

*The rationalisations table, the red-flag list and the mirror-assertion rule are adapted
from [obra/superpowers](https://github.com/obra/superpowers) (MIT, © 2025 Jesse Vincent),
`test-driven-development` at `b36e082`. See `SOURCE-vendored-skills.md`.*
