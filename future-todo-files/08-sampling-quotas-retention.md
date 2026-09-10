# feat(sdk,backend): sampling rates, ingest quota and a data retention job

## Problem

Every session, every frame and every error is ingested and kept forever. Frame PNGs pile up
under `frames/<session>/<seq>.png`; the only way to clean them out is to delete the project
(`DeleteProject`). The backend `ingest` accepts an unlimited number of requests (only a
32 MB envelope limit). Sentry lets you configure sampling, quotas and a retention period.

## Why it matters

Once the disk is full SQLite cannot write and ingest returns 500; a single misbehaving
client (a screen producing errors in an infinite loop) drowns the whole project.

## Where to look

**Code**
- `package/lib/src/options.dart:39` — `SightpaneOptions`; the `sessionSampleRate`,
  `errorSampleRate`, `tracesSampleRate` (03) fields.
- `package/lib/src/hog.dart:109` — the `SightpaneClient` constructor: the sampling decision is made
  at the start of the session.
- `backend/internal/server/ingest_handler.go` — `ingest`: the place for the quota/rate limit
  (a per-project per-minute envelope and item counter, `429` + `Retry-After`).
- `backend/internal/store/project.go` — `DeleteProject`: the pattern for deleting frame files; the
  retention job follows the same pattern for `sessions.started_at < now - retention`.
- `backend/main.go` — `main` is wiring only now and starts no background goroutine of its own;
  the retention job goes here too, on a daily tick.
- `backend/internal/store/store.go` — `migrate`, the `projects` table: the `retention_days`,
  `quota_items_per_minute` columns.

**Contract / data**
- SDK: when `SightpaneQueue` gets a 429 it waits for `Retry-After` (today 4xx counts as accepted,
  `transport.dart` `statusCode < 500`) — that behaviour has to change.
- Env: `SIGHTPANE_RETENTION_DAYS` (default 30), `SIGHTPANE_INGEST_RATE` (default 0 = unlimited).

**Tests that pin current behaviour**
- `package/test/queue_test.dart` (backoff), `backend/internal/server/server_test.go::TestAuth`.

## Fix shape

- SDK: at the start of the session, if `Random().nextDouble() < sessionSampleRate` does not
  hold, recording and breadcrumbs are off but errors still go out (`errorSampleRate` is
  separate); `Sightpane.client.sampled`.
- Backend: a per-project token bucket (in memory), 429 when it overflows; the items count as
  `rejected` and the dashboard gets a "quota exceeded" counter (`dropped` in Stats).
- Retention job: once a day, delete the rows of sessions older than `retention_days` and
  their `frames/<session>` directories; issue counters are preserved, `VACUUM` overnight.
- Dashboard: Settings → retention period and quota fields (owner).

## Acceptance

- [x] 429 + `Retry-After` produce correct backoff in the SDK (test: fake transport returns 429)
- [x] The retention job deletes old sessions + frame directories and leaves recent ones alone (temp dir test)
- [x] Sampling: with `sessionSampleRate: 0` no frames/breadcrumbs are sent, errors are
- [x] README env table
