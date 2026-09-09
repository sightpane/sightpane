# feat(backend,docker): SQLite → PostgreSQL + TimescaleDB (hypertable, time_bucket, retention policy)

> **Done** in `feat(store,docker)!: replace SQLite with TimescaleDB`. The plan
> below is kept as written; what actually shipped differs from it in three ways
> and is recorded under [Outcome](#outcome) at the end. Read that first.

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

**Superseded:** keeping SQLite was rejected in turn while this was being built — see Outcome.

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

---

## Outcome

Shipped as one commit. Three decisions were taken during the work that this plan
did not have, and they are the difference between reading the plan and reading
the code.

**1. SQLite is gone, not kept alongside.** The plan's "Rejected: dropping SQLite"
line did not survive contact with the dialect layer: two dialects meant a
`rebind` step on every query, two schemas, two sets of timestamp handling and a
second CI job, all to keep a database that cannot do a hypertable, a continuous
aggregate or a retention policy — the three things this issue exists for. Every
query is now written once with `$n` placeholders and no translation layer. What
that cost:

- **no migration path.** `sightpane migrate --from sightpane.db` was written and
  then deleted with the rest. An existing `sightpane.db` cannot be moved into
  Postgres by this backend; the acceptance item for it is dropped, not deferred.
- **the `hog.db` fallback went with it**, and with it one of the pre-rename
  compatibility promises in `CLAUDE.md`. `X-Hog-Key`, `HOG_*` and
  `package:flutter_hog/` are untouched.
- **`SIGHTPANE_DB` is required** and has no default. Starting without it fails.

**2. Plain Postgres is a supported fallback.** Not in the plan, but cheap: the
second migration has two variants, `postgres/timescale/` and `postgres/plain/`,
and `items_daily` is a continuous aggregate in one and an ordinary view in the
other. Every query reads the same names either way. A managed Postgres that will
not install the extension works; nothing compresses or expires there. The variant
is part of the recorded migration version, so a database that later gains the
extension picks the timescale file up. CI runs both.

**3. `frames` stayed an ordinary table.** The plan left this open. A hypertable's
unique index has to include the partitioning column, which would turn the
`(session_id, seq)` upsert of a resent frame into a second row, and a retention
policy can only drop the row while the PNG next to it lives in the blob store.
Consequence: **nothing expires frames or their PNGs today** — that is issue 08's
job and it is not written yet. `items` retention does not touch them, because
`items` rows have no PNG.

Two smaller deviations: `body_json` and the other JSON columns are `TEXT`, not
`JSONB` (the dashboard gets back exactly what the SDK sent, and nothing queries
inside a body); and `EventSummary` reads raw `items` rather than `items_daily`,
because its distinct-visitor count needs the session behind every event, which a
continuous aggregate cannot hold — see the measurement below.

One fix nobody asked for: an incoming `ts` now goes through `parseTS` and is
normalised to UTC. A timestamp carrying a `+03:00` offset used to land in the
wrong day bucket, because the day was the first ten characters of the string.

### Measured

`tool/seed` produces the numbers; 1,000,003 items over 91 chunks, 20k sessions,
through the full `docker compose` stack:

| | |
|---|---|
| `GET /projects/1/stats?days=14` | **9–11 ms** (target < 50) |
| `GET /projects/1/live` | **0.2–1.1 ms** (target < 5) |
| `GET /projects/1/events/summary?days=14` | **165 ms** |
| `daily errors` against `items_daily` alone | 0.6 ms |

The 165 ms is the known cost of the `COUNT(DISTINCT visitor_key)` join: chunk
exclusion works (14 of 91 chunks touched) and 124k rows are joined and sorted.
It is a page load, not the once-a-second poll `stats` and `live` are, so it was
left alone. Bringing it down means an approximate distinct count
(`timescaledb_toolkit` hyperloglog) or denormalising `visitor_key` onto `items`,
neither of which is worth it yet.

### Acceptance, as it stands

- [x] `SIGHTPANE_DB=postgres://…` creates the extension, the `items` hypertable
      and the `items_daily` continuous aggregate — pinned by
      `internal/store/schema_test.go`, which also checks
      `materialized_only = false`; without it a freshly ingested event would be
      missing from the chart meant to show it
- [ ] ~~`SIGHTPANE_DB` empty behaves exactly as SQLite did~~ — dropped, see (1)
- [x] the same test files pass on both databases; two CI jobs in
      `.github/workflows/ci.yml` — `timescaledb` and `postgres` rather than
      `sqlite` and `timescaledb`
- [x] the JSON of `/stats` and `/events/summary` is unchanged field for field
      (timestamps are `TIMESTAMPTZ` in the column and still RFC3339Nano UTC
      strings on the wire); the dashboard was built and driven through
      `docker compose up`, and neither `sightpane/ui` nor `sightpane/flutter`
      needs a change
- [x] 1M items: `Stats` and `Live` inside budget, measured above, reproducible
      with `go run ./tool/seed`
- [x] the retention policy is live and tracks `SIGHTPANE_RETENTION_DAYS` on every
      restart (zero removes it); **but** no PNG is swept, because `frames` is not
      a hypertable — see (3). A 91-day-old `items` row is dropped by the policy;
      that drop was not observed against real data, only the job asserted
- [ ] ~~`go run . migrate --from sightpane.db --to $SIGHTPANE_DB`~~ — dropped, see (1)
- [x] `docker compose up -d --build` brings up the dashboard, the backend and
      timescaledb; the backend waits on the database's health check and migrates
      the schema itself. There is no `--profile postgres`: the database is not
      optional any more. README, root README and `CLAUDE.md` are updated

### Left for 08

Frame expiry: a job that deletes `frames` rows and the PNGs behind them.
`SIGHTPANE_RETENTION_DAYS` already exists and is the value it should read.
