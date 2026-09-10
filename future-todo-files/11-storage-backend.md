# chore(backend): storage abstraction — a Postgres option and an S3 frame store

> **Update (2026-09-09):** both halves have moved out of this file. The SQL side is
> [00b-timescaledb-postgres.md](00b-timescaledb-postgres.md) (TimescaleDB hypertables,
> a migration directory, `SIGHTPANE_DB`); the frame/object side shipped and is recorded in
> [12-object-storage-ceph.md](12-object-storage-ceph.md) (`SIGHTPANE_FRAMES=s3` against Ceph
> RADOS Gateway). Nothing is left here — keep the file for the reasoning, work from
> those two.

## Problem

`Store` uses `database/sql` + `modernc.org/sqlite` and the local `frames/` directory directly
(`Open`, `SetMaxOpenConns(1)`). Single node, single writer; frames live on the container
disk. This does not scale to more than one backend replica or to high volume (as many
recordings as there are cameras). Sentry scales with ClickHouse/Kafka; that is not our
target, but Postgres + S3 is a reasonable next step up.

## Why it matters

It is not needed today; after 08 (retention) and 03 (span volume) the single SQLite writer
could become a bottleneck. Writing the decision down now avoids a rewrite later.

## Where to look

**Code**
- `backend/internal/store/store.go` — `migrate` is SQLite-specific (`INSERT … ON CONFLICT`,
  `substr`, `json` functions, `ALTER` errors are ignored).
- `backend/internal/store/ingest.go` — `framePath`/`saveFrame`;
  `backend/internal/store/query.go` — `FramePath`: local files.
- `backend/internal/server/data_handler.go` — `getFrame`, Fiber's `c.SendFile`.
- `Dockerfile`, `docker-compose.yml` — the `sightpane-data` volume.

**Contract / data**
- Frame access: a `FrameStore` interface (`Put(session, seq, bytes)`, `Open`,
  `DeleteSession`); `fs` (today's) and `s3` (MinIO/`minio-go`) implementations — the same
  line as the `BlobStore` decision in the casino-crm backend.
- SQL: queries differ slightly per driver (`ON CONFLICT` exists in both; `substr` also exists
  in Postgres; `json_extract` → `->>`); numbered migrations with `goose`/`golang-migrate`.

## Fix shape

1. First `FrameStore` only (small, self-contained): `SIGHTPANE_FRAMES=fs|s3` + `S3_*` env.
2. Then SQL: driver selection via `SIGHTPANE_DB=sqlite|postgres`, a migration directory; tests run
   on both drivers (`testcontainers` for Postgres, or skip).
3. Multiple replicas: in-memory quota/alert state (08, 02) would have to move to Redis — not
   in this issue, just a note.

Rejected: ClickHouse — overkill for our volume and team size.

## Acceptance

- [x] With `SIGHTPANE_FRAMES=s3` frames are written to/read from MinIO/Ceph; `fs` behaviour is unchanged
- [x] `go test ./...` passes with Postgres (when a container is available)
- [x] Optional `postgres` + `minio` + `ceph` profiles in Docker compose
