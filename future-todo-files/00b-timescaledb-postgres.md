# feat(backend,docker): SQLite → PostgreSQL + TimescaleDB (hypertable, time_bucket, retention policy)

## Problem

The backend is tied to a single SQLite file with a single writer: `Open` sets
`SetMaxOpenConns(1)` (`backend/internal/store/store.go`), WAL + `busy_timeout(5000)`. Every
envelope is one transaction inside `Ingest` (`backend/internal/store/ingest.go`); the connection
is held even while the frame PNG is base64-decoded and written to disk. The time columns are
`TEXT` (an RFC3339Nano string, the `sessions` and `items` tables in `migrate`) and comparison
happens in string order (`started_at>=?`). The daily series is four separate full scans:
`SELECT substr(started_at,1,10), COUNT(*) … GROUP BY 1` × 2 and
`substr(ts,1,10)` × 2 (`backend/internal/store/stats.go` — `Stats`); `EventSummary` does
`items ⋈ sessions` + `substr` (`backend/internal/store/query.go`);
`Live` (`backend/internal/store/query.go`) is called by the dashboard every second
(`OverviewPage` invalidates every 1 s). `items` grows without bound — 08 (retention) does not
exist yet, and the only deletion pattern available is `DeleteProject`
(`backend/internal/store/project.go`). Once 03 (spans) and 07 (record on error) land, the row
volume goes up several-fold; under that load SQLite lets a stats query block ingest.

## Why it matters

This is a time-series product: `items`, `frames` and the future `spans` are insert-heavy,
time-keyed data that is thrown away once it expires. A TimescaleDB hypertable + `time_bucket` +
`add_retention_policy` + compression map exactly onto that load; the retention work in 08 comes
down to a single SQL statement, and the aggregation in 03 becomes a `continuous aggregate`.
Postgres allows more than one backend replica and concurrent reads/writes. The SQL half of issue
11 lives here; only the frame/object store (S3) is left in 11. It is done **after 0a (i18n)**:
0a will add the `users.locale` column, and the migration list is moved over to Postgres once,
here.

## Where to look

**Code**
- `backend/internal/store/store.go` — `Open`: `sql.Open("sqlite", …?_pragma=…)`, the `frames`
  directory; the `_ "modernc.org/sqlite"` import.
- `backend/internal/store/store.go` — `migrate`: 8 × `CREATE TABLE IF NOT EXISTS` (`users`,
  `auth_tokens`, `projects`, `project_members`, `sessions`, `items`, `issues`, `frames`),
  4 indexes, and an `ALTER … ADD COLUMN` list for later-added columns that ignores errors.
  `INTEGER PRIMARY KEY AUTOINCREMENT`.
- SQLite-specific / driver-sensitive spots: the `?` placeholder (136 of them, spread over
  `backend/internal/store/`; `$1…` on Postgres), `LastInsertId()`
  (`backend/internal/store/auth.go` — `CreateUser`, `backend/internal/store/project.go` —
  `CreateProject`; pgx does not support it → `RETURNING id`), `substr(ts,1,10)`
  (`backend/internal/store/stats.go` — `Stats`, `backend/internal/store/query.go` —
  `EventSummary`), `ON CONFLICT … DO UPDATE` (`backend/internal/store/auth.go` — `AddMember`;
  `backend/internal/store/ingest.go` — `Ingest`, the `sessions` and `issues` upserts — Postgres
  has it too, `excluded` is the same), `COUNT(DISTINCT visitor_key)`
  (`backend/internal/store/stats.go` — `Stats`, `backend/internal/store/query.go` —
  `EventSummary`), string time comparison (`backend/internal/store/auth.go` — `Login`,
  `UserByToken`; and across `project.go`, `stats.go`, `ingest.go` and `query.go` in
  `backend/internal/store/`).
- The time fields are `string` on the Go side (`Envelope.Session.StartedAt`, `itemHead.TS` in
  `backend/internal/store/ingest.go`; `Session.StartedAt`, `Item.TS`, `Frame.TS`,
  `Issue.LastSeen`, `LiveViewer.LastSeen/StartedAt` in `backend/internal/store/query.go`) and go
  out to JSON verbatim; the dashboard parses RFC3339 with `_t()` in `core/models.dart` —
  **the API contract must not change**.
- `backend/internal/store/ingest.go` — `Ingest`: `sessions` upsert, `items` insert, the `frames`
  row + `saveFrame` to disk, `issues` upsert, counter updates.
- `backend/internal/store/project.go` — `DeleteProject`: the rows in one transaction, the frame
  directories deleted afterwards; on a hypertable `DELETE … WHERE project_id=?` spreads across
  chunks.
- `backend/main.go` — `store.Open(cfg.DataDir)`; `SIGHTPANE_DATA`
  (`backend/internal/config/config.go` — `Config`, `Load`) is only SQLite + frames.
- `backend/go.mod` — modernc.org/sqlite (cgo-free); `Dockerfile:24` `CGO_ENABLED=0` — `pgx/v5`
  is pure Go too, so the image does not change.
- `docker-compose.yml` — a single service, the `sightpane-data` volume.

**Contract / data**
- Schema (Postgres): `items(id BIGINT GENERATED ALWAYS AS IDENTITY, session_id, project_id,
  ts TIMESTAMPTZ, type, name, body_json JSONB, issue_id)` → `create_hypertable('items','ts',
  chunk_time_interval => interval '1 day')`; primary key `(id, ts)` (on a hypertable a unique
  index has to include the partitioning column). `frames(session_id, seq, ts, …)` the same way
  (on `ts`), PK `(session_id, seq, ts)`. `sessions`, `issues`, `users`, `projects`,
  `auth_tokens`, `project_members` stay **normal tables** (with `sessions.id TEXT` as the PK the
  `ON CONFLICT(id)` upsert is preserved; making it a hypertable breaks the upsert).
- Continuous aggregate `items_daily(project_id, type, day, n)` (`time_bucket('1 day', ts)`,
  refreshed hourly) → `Stats.Daily` and `EventSummary` come from it; `COUNT(DISTINCT
  visitor_key)` (daily visitors) does not combine in a continuous aggregate → either the raw
  query over `sessions` stays, or `approx_count_distinct` (timescaledb-toolkit; optional).
- Policies: `add_retention_policy('items', interval '90 days')` — wired to the
  `retention_days` value from 08; `add_compression_policy('items', interval '7 days')`
  (`segmentby project_id, type`). When a `frames` row is deleted the PNG on disk has to go too →
  the retention job deletes the file before the row (`FrameStore.DeleteSession` from 11).
- Environment: `SIGHTPANE_DB` (defaults to `sqlite`; or `postgres://user:pw@host:5432/hog?sslmode=…`);
  `SIGHTPANE_DATA` stays, for frames. `SIGHTPANE_RETENTION_DAYS` (shared with 08).
- JSON time fields: scan into `time.Time` in Go, write `RFC3339Nano` UTC in `MarshalJSON` — the
  dashboard/SDK see nothing.

**Tests that pin current behaviour**
- `backend/internal/server/server_test.go` — `newTestServer`: `store.Open(t.TempDir())` — tied
  to SQLite. `TestIngestAndQuery`, `TestUsersProjectsMembersStats` (`Stats.daily`, day matched
  against `time.Now()`), `TestVisitorsAndLive` (the `Live` window), `TestAuth`
  (token expiry as a string comparison).
- Today `Stats` builds the day array in Go and matches it against the `substr` day coming back
  from SQL (`backend/internal/store/stats.go` — `Stats`); the day that comes out of
  `time_bucket` is UTC midnight — it has to be reduced to the same format (`2006-01-02`), otherwise the days do not match and the
  chart stays empty.

**Docs / prior art**
- `backend/README.md` (the environment variable table, "Visitor", "Live"); the root
  `README.md` "Docker"; `CLAUDE.md` "Backend" (the single-writer and ALTER-list notes need
  updating).
- 11-storage-backend.md (the SQL half moved here; S3 stays), 08 (retention → policy),
  03 (a `spans` hypertable), 02 (alert rate calculations via `time_bucket`).

## Fix shape

1. **A dialect layer, no ORM.** The SQL in `backend/internal/store/` stays; in a new
   `dialect.go` in that package a `dialect{placeholders, dayExpr(col), nowExpr, returningID}`; the `?` → `$n` conversion in a
   single helper (`rebind`), `RETURNING id` instead of `LastInsertId` (SQLite 3.35+ supports it
   too and modernc is compatible → one path for both drivers). `substr(x,1,10)` → unchanged on
   SQLite, `to_char(time_bucket('1 day', x), 'YYYY-MM-DD')` on Postgres. Scan the time columns
   as `TIMESTAMPTZ` on Postgres and `time.Time` in Go; on SQLite they stay strings (the driver
   does the `time.Time` ↔ RFC3339 conversion).
2. **Numbered migrations.** `backend/migrations/{sqlite,postgres}/0001_init.sql …`, a
   `schema_migrations` table, applied at startup (no `golang-migrate` needed, 40 lines).
   Postgres 0001: `CREATE EXTENSION IF NOT EXISTS timescaledb`, the tables, the hypertables, the
   continuous aggregate, the policies. Today's `ALTER` list is folded into SQLite 0001 (the `IF
   NOT EXISTS` logic is preserved for existing `sightpane.db` files).
3. **Driver selection.** `SIGHTPANE_DB` empty/`sqlite` → today's path; `postgres://` → `pgx/v5/stdlib`,
   `SetMaxOpenConns(16)`. The `Ingest` transaction is unchanged; frame base64 decoding and the
   file write move **outside** the transaction (to keep the connection short on Postgres too).
4. **Move the queries.** The four daily `Stats` queries → `time_bucket` over `items_daily` +
   `sessions` on Postgres; `EventSummary` → `items_daily` (with a raw `sessions` subquery for
   the user count); `Live` → `last_seen_at > now() - $window * interval '1 second'` and a partial
   index `sessions(project_id, last_seen_at DESC) WHERE ended_at IS NULL`.
5. **Data migration.** `go run . migrate --from ./data/sightpane.db --to $SIGHTPANE_DB`: the tables in order,
   `items`/`frames` in 5k `COPY` batches; the frames stay on disk (`SIGHTPANE_DATA` unchanged).
6. **Tests and CI.** `newTestServer` reads `SIGHTPANE_TEST_DB`: empty → SQLite (today's speed), set →
   connect to Postgres and open a schema per test (`CREATE SCHEMA t_<rand>` + `search_path`).
   A `timescale/timescaledb:2-pg17` service container on GitHub Actions; the same test files run
   on both drivers.
7. **Docker.** A `timescaledb` service in `docker-compose.yml` (`timescale/timescaledb:2-pg17`,
   a `pg-data` volume) and the `SIGHTPANE_DB` env; the single-container SQLite path **stays the
   default** (ease of setup, `Dockerfile` unchanged).

The hard part: once the hypertable PK is `(id, ts)`, the `items.issue_id` index and `GetIssue`'s
"last 50 occurrences" query (`backend/internal/store/query.go` — `GetIssue`) have to fit chunk
exclusion with `ORDER BY ts DESC`
— add `WHERE ts > now() - interval`, otherwise every chunk is scanned. A `frames` hypertable
plus the PNG on disk means deletion from two sources: the file first, then the row; because a
retention policy only deletes the row, `frames` gets **its own job** (08) instead of a policy, or
`frames` is not made a hypertable. `COUNT(DISTINCT visitor_key)` is not available in a continuous
aggregate; `Stats.Users` and `DayStat.Users` keep coming from the raw `sessions` table (which is
small).

Rejected: dropping SQLite (development, tests and the single-container setup run on it in 3 s);
ClickHouse (rejected in 11); `sqlc`/`gorm` (template complexity for two dialects, not worth it
for 52 queries); making `sessions` a hypertable (it breaks the `ON CONFLICT(id)` upsert and the
`SessionProject` authorization query).

## Acceptance

- [ ] starting up with `SIGHTPANE_DB=postgres://…` creates the `timescaledb` extension, the `items`
      hypertable and the `items_daily` continuous aggregate; `\d+ items` shows the chunks
- [ ] with `SIGHTPANE_DB` empty, the behaviour and the `sightpane.db` file are bit for bit what they are
      today (the existing `backend/internal/server/server_test.go` green, unchanged)
- [ ] with `SIGHTPANE_TEST_DB` set, `go test ./...` passes on Postgres with the same test files;
      two jobs in CI (`sqlite`, `timescaledb`)
- [ ] the JSON of `GET /projects/{id}/stats?days=14` and `/events/summary` is field for field the
      same on both drivers (`days`, `daily[].day` `2006-01-02`, `crash_free`); the dashboard
      works unchanged
- [ ] with 1M `items` rows (`tool/seed`), `Stats` < 50 ms (continuous aggregate), `Live` < 5 ms
      (partial index); the measurement, `go test -bench` or `EXPLAIN ANALYZE` output, in the PR
- [ ] `add_retention_policy('items', '<SIGHTPANE_RETENTION_DAYS> days')` is active; 91-day-old data is
      deleted via `timescaledb_information.jobs`, and the matching PNGs are not left on disk
- [ ] `go run . migrate --from sightpane.db --to $SIGHTPANE_DB` moves the existing data; the row counts and
      the `Stats` output are equal before and after the migration
- [ ] `docker compose --profile postgres up` brings up the dashboard + backend + timescaledb;
      the env table in `backend/README.md` (`SIGHTPANE_DB`, `SIGHTPANE_TEST_DB`), the root README "Docker"
      and the Backend section of `CLAUDE.md` (ALTER list → migrations directory) are up to date
