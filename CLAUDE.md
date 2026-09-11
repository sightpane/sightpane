# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Three repositories, one contract

sightpane is self-hosted error tracking, product analytics and frame-based session
replay. It is split across three repositories that release independently:

| Repository | What it is | Licence |
|---|---|---|
| [sightpane/sightpane](https://github.com/sightpane/sightpane) | Go backend on Fiber v3 + TimescaleDB, the Docker deployment, the product roadmap under `future-todo-files/` | AGPL-3.0-or-later |
| [sightpane/ui](https://github.com/sightpane/ui) | the Flutter web dashboard the backend serves | AGPL-3.0-or-later |
| [sightpane/flutter](https://github.com/sightpane/flutter) | the Dart/Flutter SDK, `sightpane` on pub.dev | Apache-2.0 |

**You can only see one of them at a time.** When a change touches the envelope
contract below, say plainly which of the other two also needs a change and what
it is — nobody reading this repository can check for themselves.

**English is the working language**: code comments, READMEs, issues and commit
messages. The dashboard's user interface is the exception — it is localized, and
its strings live in `lib/l10n/` in the ui repository.

## Commands

The tests need Docker: `internal/testdb` starts a TimescaleDB container per test
binary and gives each test a schema inside it. `SIGHTPANE_TEST_DB` points them at
a server that is already running instead.

```bash
go vet ./... && go test ./...                              # starts its own database
go test -race ./...                                        # before committing
go test -run TestIngestAndQuery ./internal/server/         # a single test
SIGHTPANE_TEST_DB=postgres://sightpane:sightpane@127.0.0.1:5432/sightpane?sslmode=disable go test ./...

docker compose up -d timescaledb                           # the database on its own, on :5432
go build -o /tmp/sightpane . && SIGHTPANE_DEFAULT_KEY=dev \
  SIGHTPANE_DB=postgres://sightpane:sightpane@127.0.0.1:5432/sightpane?sslmode=disable /tmp/sightpane   # :8790

# enough data to measure the read path, and the timings, in one command
go run ./tool/seed --items 1000000

# the dashboard, from a checkout of github.com/sightpane/ui
SIGHTPANE_UI_DIR=../ui/build/web /tmp/sightpane

# Ceph S3 gateway on :8080; without it the S3 tests skip
docker compose --profile ceph up -d
SIGHTPANE_TEST_S3_ENDPOINT=127.0.0.1:8080 SIGHTPANE_TEST_S3_ACCESS_KEY=hogaccesskey \
  SIGHTPANE_TEST_S3_SECRET_KEY=hogsecretkey go test ./internal/blob/

docker compose up -d --build                               # dashboard + backend + timescaledb
```

Startup seeds an admin (`admin@sightpane.local` / `admin123`) and one project
(key `dev`), so a fresh install can be signed into. `SIGHTPANE_DB` is required and
has no default; `SIGHTPANE_DATA` (default `./data`) is only the frame store now.

`GITHUB_TOKEN` in this environment is a dummy that causes 401. Always run the
GitHub CLI as `env -u GITHUB_TOKEN gh …`.

## The envelope contract

Everything hinges on `POST /api/v1/envelope` (header `X-Sightpane-Key`), body `{sdk, session{id, started_at, user, device, props}, items[]}`. Item `type` values: `breadcrumb`, `event`, `error`, `frame` (base64 PNG + `taps`), `pointer` (`events[{t,x,y,k}]`), `heartbeat` (updates session `last_seen_at`/`current_route`, writes no row), `session_end`. Backend answers 202 `{accepted, rejected}`; unknown items are silently counted as rejected, so contract drift does not fail loudly.

The contract lives in three repositories and they must move together:
- **here** — `internal/store/ingest.go` (`Envelope`, `itemHead`, the `switch` in `Ingest`), pinned by `internal/server/server_test.go`
- [sightpane/flutter](https://github.com/sightpane/flutter) — `lib/src/models.dart` (`SightpaneItem` factories, `SightpaneEnvelope.toJson`), pinned by its `test/models_test.dart`
- [sightpane/ui](https://github.com/sightpane/ui) — `lib/core/models.dart` parsers and the `FakeApi` fixture in `test/helpers/test_app.dart`

A new field here needs a new migration file (see the schema note below), then a pull
request against each of the other two. Say so in the PR body — nobody reviewing
this repository can see them.

## The backend

Fiber v3 on fasthttp. `main.go` is wiring only; the parts live under `internal/`:

| Package | Owns |
|---|---|
| `config` | every environment variable and its default |
| `apierr` | error codes + the `Error` type; sits below store and server so both use it |
| `netx` | the PROXY protocol listener |
| `blob` | replay frame storage: `FS` (a directory) or `S3` (Ceph RADOS Gateway, MinIO, AWS) |
| `store` | the schema, its migrations, ingest, queries. No HTTP |
| `server` | Fiber routes, middleware, one `*_handler.go` per resource. No SQL |
| `testdb` | the TimescaleDB the tests run against; imported only from `_test.go` |

**Load the `go-fiber` skill before touching ``.** It carries the Fiber
traps that have already cost debugging time here — middleware must be the *first*
argument to a route, a guard that calls `c.Next()` runs the whole chain inside
itself, `c.Bind().Body()` silently parses a header-less JSON body as a form — plus
the awesome-fiber and gofiber/recipes selections that match this codebase.

- Handlers **return** errors and never write them; `errorHandler` renders the one
  `{"error", "code"}` shape. A known failure is a `apierr.Error` created where it
  is detected (including inside `store`), so no handler keeps a mapping table.
- **Schema evolution:** numbered `.sql` files under `internal/store/migrations/`,
  applied in filename order at startup and recorded in `schema_migrations`. A new
  column is a new file — there is no `ALTER` list to keep in step any more.
  `internal/store/migrate.go` is ~230 hand-written lines rather than
  golang-migrate or goose: both wrap a migration in a transaction, and creating a
  continuous aggregate is not allowed inside one. A statement under
  `-- +ignore-errors` may fail (TimescaleDB syntax that moves between releases).
- **Two variants of the second migration.** `postgres/timescale/` makes `items` a
  hypertable and `items_daily` a continuous aggregate; `postgres/plain/` makes
  `items_daily` an ordinary view for a Postgres that will not install the
  extension. Every query reads the same names either way. The variant is part of
  the recorded version, so a database that later gains the extension picks the
  timescale file up. `internal/store/schema_test.go` pins the hypertable, the
  real-time aggregate and the retention policy.
- **Timestamps.** Columns are `TIMESTAMPTZ`; the JSON API has always handed out
  RFC3339Nano strings in UTC, so `tsCol`/`nullTSCol` in `internal/store/scan.go`
  adapt the column to the field. Incoming `ts` goes through `parseTS`, which
  accepts what the SDKs actually send and falls back to the receipt time. The
  connection pins `timezone=UTC`.
- Postgres is opened with `SetMaxOpenConns(16)`; `Ingest` is one transaction per
  envelope, with the frame PNGs written to the blob store **before** it opens so
  no pooled connection is held across a round trip to an object store.
- `SIGHTPANE_RETENTION_DAYS` is re-applied as a TimescaleDB retention policy on
  every start, so changing it is a restart. Zero removes the policy.
- Client IP order in `clientIP`: Cloudflare headers → `Forwarded: for=` →
  `X-Forwarded-For` (first) → `X-Real-IP` → the connection. Fiber's `c.IP()` is
  deliberately not used: it reads a single configured header and deployments here
  sit behind two proxies. PROXY protocol only with `SIGHTPANE_PROXY_PROTOCOL=1`, honored
  only from `SIGHTPANE_TRUSTED_PROXIES` (policy IGNORE elsewhere, never SKIP) —
  `internal/server/proxy_test.go` uses real sockets because the library default
  (REQUIRE) was only visible there.
- **Source maps.** `internal/symbol` resolves a minified release stack at ingest:
  the SDK sends `frames[{uri,line,column,member}]` beside the raw `stack`, and a
  map uploaded for that `release` maps them to `lib/…dart:120`. Resolved frames
  are preferred by `fingerprintOf`, which is what stops a release from landing in
  a new group every build. The result goes in `items.symbolicated_json`, never
  inside `body_json` — that stays exactly what the SDK sent. Maps live in the
  blob store under `sourcemaps/<project>/<release>/`, parsed once into a
  size-bounded LRU (`SIGHTPANE_SOURCEMAP_CACHE_MB`). Uploading is owner-only with
  a user token, not the project key. A release does **not** group with its debug
  build; the reason is in `fingerprintOf`'s comment and it is a decision.
- Issue grouping is `internal/store/fingerprint.go`: exception type + first three
  app frames with line numbers stripped (`package:flutter/`, `package:sightpane/`,
  `dart:` frames skipped; message fallback normalizes digits). Changing
  `Fingerprint` regroups every existing issue.
- Visitor = SHA of user id + IP + browser (`VisitorKey`); stats "users" count by
  that key.
- Replay frames live outside the database, behind `blob.Store`. `SIGHTPANE_FRAMES=fs`
  (default) writes `SIGHTPANE_DATA/frames/<session>/<seq>.png`; `SIGHTPANE_FRAMES=s3` puts the
  same keys in an object store, which is the only option that works with more than
  one replica. Frames are **streamed** through `getFrame`, never handed out as
  presigned URLs — access is checked per project on every request. Note the comment
  in `getFrame` about not closing the reader: fasthttp writes the body after the
  handler returns and closes the stream itself.
- `ui/index.html` is embedded with `go:embed` as the fallback page; `SIGHTPANE_UI_DIR`
  serves the built dashboard with an SPA fallback that returns 200 for client
  routes and still returns a JSON 404 under `/api/`. CORS is `*`. Envelope cap
  32 MB. SIGTERM shuts down with a 10 s grace so the pool closes cleanly.
- Tests use `newTestServer(t)` (a schema of its own in the container `testdb`
  started, owner token) and `app.Test` — don't fake `Store`. Fiber's test connection reports `0.0.0.0`, so
  a client address has to arrive in a header; its default timeout is 1 s, which
  PBKDF2 exceeds under `-race`.

## Fiber ecosystem: what we use, what we would add

From [awesome-fiber](https://github.com/gofiber/awesome-fiber). In use today, all
from Fiber core: `cors` (the SDK posts from an app origin, the dashboard from
another port), `recover` (ingest parses data we did not write; one bad envelope
must not kill the process), `static` (the dashboard with its SPA fallback).

Add only when the matching roadmap item lands — each one is a dependency and a
config surface: core `limiter` (per-project ingest quota, issue 08), `healthcheck`
(k8s probes), `compress` + `etag` (stats JSON is refetched every second), `sse`
(replace the dashboard's 1 s polling of `/live`), `requestid` + `logger`
(correlating a complaint with a log line), `pprof` (issue 03); contrib `otel` or
`prometheus` (metrics), `testcontainers` (Postgres in tests, issue 00b),
`swaggerui` (a public API surface); `gofiber/storage` (shared quota and alert
state once there is more than one replica). `testcontainers` is in use, in
`internal/testdb`.

Deliberately not used: any ORM (the store writes SQL directly and the queries are
the interesting part), `jwt` (sessions are opaque database-backed tokens, so they
can be revoked), templating engines (the UI is a Flutter build).

From [gofiber/recipes](https://github.com/gofiber/recipes), the ones that map onto
this codebase: `spa` (the static + fallback pattern used for the dashboard),
`graceful-shutdown` (the signal + `ShutdownWithTimeout` loop in `main.go` came
from it), `404-handler` (custom not-found shaping, which is what `errorHandler`
does), `unit-test` (`app.Test` structure; we assert with the standard library, not
testify), `sse` (the shape of a live endpoint), `local-development-testcontainers`
and `postgresql`/`sqlc` (issue 00b), `validation` (only if request shapes grow past
a handful of fields), `clean-architecture`/`hexagonal` (reference layouts, heavier
than this repo needs — we stopped at store/server).

Porting a recipe: check it is v3 (`func(c fiber.Ctx) error`, not `*fiber.Ctx`),
that it returns errors instead of writing them, and that it does not pull in a
dependency the paragraph above rejects.

## Repo conventions

- `.claude/skills/` carries `go-fiber` (the house pattern for this codebase — load it before touching anything here), `golang-pro`, and the shared workflow skills (`code-auditor`, `spec-first-testing`, `debugging-advanced`, `issue-writer`, `pr-writer`, `pr-reviewer`). Provenance of the vendored ones is in `SOURCE-vendored-skills.md`.
- `future-todo-files/` holds the product roadmap as ready-to-file issues, for all three repositories, with code coordinates and acceptance criteria. Deliberately out of scope: DOM replay on Flutter, profiling, cron and uptime monitoring.
- `main.go` carries the AGPL SPDX header. AGPL §13: `GET /api/v1/health` returns `SourceURL` and the dashboard shows it — keep both when touching either.
- Test fixtures must not use literal "today" dates (a stats test broke this way); derive from `time.Now()`.
- The backend keeps accepting the pre-rename names — the `X-Hog-Key` header, the `HOG_*` environment prefix, and `package:flutter_hog/` stack frames in fingerprinting. Each has a test; do not remove one without removing its test and saying so. (The `hog.db` fallback went with SQLite.)
- **Commit on issue/task completion**: Whenever an issue, bug fix, or task is finished and verified (`go vet`, `go test`), create a descriptive conventional commit explaining what was changed and why. Do not run `git push` unless explicitly asked.
