# sightpane

Self-hosted **error tracking, product analytics and session replay** — Sentry and
PostHog in one binary, for apps on any platform.

This repository is the **backend**: the Go service that receives envelopes from
the SDKs, keeps them in TimescaleDB plus a frame store, manages users, projects
and membership, serves the statistics API, and serves the dashboard.

| Repository | What it is |
|---|---|
| **[sightpane/sightpane](https://github.com/sightpane/sightpane)** (here) | Go backend, Docker deployment, product roadmap |
| [sightpane/ui](https://github.com/sightpane/ui) | the Flutter web dashboard this binary serves |
| [sightpane/flutter](https://github.com/sightpane/flutter) | the Dart/Flutter SDK, published as `sightpane` on pub.dev |

The seam between the three is one HTTP contract: `POST /api/v1/envelope`. An SDK
that speaks it works with this backend, and a change to it has to land in all
three. It is specified under "API" below.

---

The Go service that receives envelopes from the `sightpane` SDK, keeps them in
Postgres plus a frame store, manages users / projects / membership and serves
statistics. One binary and one database. Built on
[Fiber v3](https://gofiber.io) over fasthttp, with `pgx` so cgo is not needed. The
dashboard lives in `sightpane/frontend`; its web build is served from here through
`SIGHTPANE_UI_DIR`.

```bash
docker compose up -d timescaledb
go build -o /tmp/sightpane . && \
  SIGHTPANE_DB=postgres://sightpane:sightpane@127.0.0.1:5432/sightpane?sslmode=disable \
  SIGHTPANE_DEFAULT_PROJECT="Casino CRM" SIGHTPANE_DEFAULT_KEY=dev \
  SIGHTPANE_UI_DIR=../frontend/build/web /tmp/sightpane
```

### The database

**TimescaleDB**, which is Postgres with an extension. `items` — every event,
breadcrumb, error and pointer trail — is the table that grows without bound and is
only ever read by time, so it is a hypertable: one chunk per day, chunk exclusion
instead of a full scan, compression after a week, and expiry as a chunk drop
rather than a `DELETE`. The daily counts behind the dashboard come from
`items_daily`, a continuous aggregate refreshed hourly with real-time aggregation
on, so what was just ingested is in the chart immediately. With a million items,
`GET /projects/{id}/stats?days=14` answers in ~10 ms and `/live` in ~1 ms.

`sessions`, `issues`, `frames`, `users` and `projects` stay ordinary tables:
ingest upserts a session by its id alone, which a hypertable's partitioning column
would break.

A plain Postgres without the extension also works — `items_daily` is then an
ordinary view and everything reads the same — but nothing compresses and nothing
expires on its own. The startup log says `db timescaledb` or `db postgres`.

The schema is a set of numbered files under
[`internal/store/migrations/`](internal/store/migrations), applied in order at
startup and recorded in `schema_migrations`. A new column is a new file; there is
no `ALTER` list to keep in step any more.

| Environment variable | Default | Meaning |
|---|---|---|
| `SIGHTPANE_ADDR` | `:8790` | listen address |
| `SIGHTPANE_DB` | — | **required**: `postgres://user:pw@host:5432/sightpane?sslmode=disable`, or a libpq key/value string |
| `SIGHTPANE_RETENTION_DAYS` | `90` | items older than this are dropped by a TimescaleDB retention policy; `0` keeps everything |
| `SIGHTPANE_DATA` | `./data` | `frames/<session>/<seq>.png`, when the frames are on disk |
| `SIGHTPANE_ADMIN_EMAIL` / `SIGHTPANE_ADMIN_PASSWORD` | `admin@sightpane.local` / `admin123` | admin created on first start (if missing) |
| `SIGHTPANE_DEFAULT_PROJECT` / `SIGHTPANE_DEFAULT_KEY` | `default` / `dev` | project guaranteed to exist on start; the admin becomes its owner |
| `SIGHTPANE_UI_DIR` | empty | a Flutter web build; empty serves a small placeholder page |
| `SIGHTPANE_FRAMES` | `fs` | where replay frames live: `fs` (a directory under `SIGHTPANE_DATA`) or `s3` (any S3-compatible object store — Ceph RADOS Gateway, MinIO, AWS S3) |
| `SIGHTPANE_S3_ENDPOINT` | empty | `host:port`, no scheme, e.g. `ceph-rgw:8080` |
| `SIGHTPANE_S3_BUCKET` | `sightpane-frames` | created on start if missing |
| `SIGHTPANE_S3_ACCESS_KEY` / `SIGHTPANE_S3_SECRET_KEY` | empty | S3 credentials |
| `SIGHTPANE_S3_REGION` / `SIGHTPANE_S3_USE_SSL` | empty | region only matters when the zonegroup has one; set `SIGHTPANE_S3_USE_SSL` to anything for HTTPS |
| `SIGHTPANE_PROXY_PROTOCOL` | empty | `1` makes the listener read a PROXY protocol (v1/v2) header — for layer-4 proxies (caddy-l4, HAProxy) |
| `SIGHTPANE_TRUSTED_PROXIES` | empty | comma-separated IPs/CIDRs; when set, the PROXY header is honoured only for connections from those peers |
| `SIGHTPANE_TEST_DB` | empty | tests only: a database to use instead of the container they otherwise start themselves |

### Tests

`go test ./...` needs Docker: the packages that touch the store start a
TimescaleDB container ([`internal/testdb`](internal/testdb)) and give each test a
schema of its own inside it. Point `SIGHTPANE_TEST_DB` at a server that is already
running to skip the container — that is what CI does, and what makes a long
debugging session quicker.

## Layout

```
main.go                  wiring only: config → store → seed → listener → app → graceful shutdown
ui/index.html            placeholder page, embedded with go:embed
internal/config          every environment variable, with its default
internal/apierr          error codes and the Error type (below store and server, so both use it)
internal/netx            PROXY protocol listener
internal/blob            replay frame storage: a directory, or an S3/Ceph object store
internal/store           the database: schema, migrations, ingest, queries. No HTTP
internal/testdb          the TimescaleDB the tests run against (imported only from _test.go)
internal/server          Fiber: routes, middleware, one handler file per resource. No SQL
```

Handlers hold no SQL and the store holds no HTTP. A failure that is both an HTTP
status and a domain fact — a duplicate email is a 409 — is created in the store as
a `apierr.Error` and rendered once by the server's error handler, so no handler
keeps a mapping table.

`SIGTERM` shuts the app down with a 10 s grace period so in-flight envelopes
finish and the connection pool is closed cleanly.

## API

The SDK endpoint authenticates with a project key, everything else with a user
token (`Authorization: Bearer`, issued by `/auth/login` or `/auth/register`;
30 days; `/auth/logout` revokes it). Project endpoints require membership;
`PATCH/DELETE`, key rotation and member management require the `owner` role.
Session and issue details are authorised through the project they belong to.

- `POST /api/v1/envelope` — with `X-Sightpane-Key`; body `{sdk, session{id, started_at, user, device, props}, items[]}`.
  Item types: `breadcrumb`, `event`, `error`, `frame` (base64 PNG + `taps`), `pointer` (`events[{t,x,y,k}]`, a pointer trail at ms offsets), `heartbeat`, `session_end`. 202 + `{accepted, rejected}`.
- `POST /api/v1/auth/register {email,name,password}` · `POST /api/v1/auth/login` → `{token, user}` · `GET /api/v1/auth/me` · `PATCH /api/v1/auth/me {locale}` · `POST /api/v1/auth/logout`
- `GET|POST /api/v1/projects` (`{name, platform}` → `api_key` is generated) · `GET|PATCH|DELETE /api/v1/projects/{id}` · `POST /api/v1/projects/{id}/rotate-key`
- `GET|POST /api/v1/projects/{id}/members` (`{email, role}`; the user must already be registered) · `DELETE /api/v1/projects/{id}/members/{uid}`
- `GET /api/v1/projects/{id}/live?window=` — who is on which page right now
- `GET /api/v1/projects/{id}/stats?days=` — totals, crash-free session rate, the daily series, platform/release/event distribution, the top 5 errors
- `GET /api/v1/projects/{id}/sessions?user=&errors=1&limit=` · `GET /api/v1/sessions/{id}` (items + frame list) · `GET /api/v1/sessions/{id}/frames/{seq}.png` (also accepts `?token=`)
- `GET /api/v1/projects/{id}/issues?resolved=1` · `GET /api/v1/issues/{id}` (last 50 occurrences) · `POST /api/v1/issues/{id}/resolve[?undo=1]`
- `GET /api/v1/projects/{id}/events/summary?days=` · `GET /api/v1/health`

The client IP is recorded on every session. Header precedence:
`CF-Connecting-IP` / `True-Client-IP` (Cloudflare) → `Forwarded: for=` (RFC 7239)
→ `X-Forwarded-For` (first address) → `X-Real-IP` → the connection address.
**In Docker** the connection address is the bridge network (`172.x`); for the real
IP, put the container behind a reverse proxy (nginx / Caddy / Traefik) that sends
`X-Forwarded-For` / `X-Real-IP`, or use `network_mode: host`. It is updated per
envelope, and an empty value keeps the previous one.

**Layer-4 proxy (caddy-l4)**: a TCP passthrough adds no HTTP header, so the
backend sees the proxy's address (`127.0.0.1`). The fix is the PROXY protocol: the
proxy sends the real client address at the start of the connection and the backend
reads it with `SIGHTPANE_PROXY_PROTOCOL=1`. On the caddy-l4 side:

```caddyfile
{
  layer4 {
    :8790 {
      route {
        proxy {
          upstream 127.0.0.1:18790
          proxy_protocol v2
        }
      }
    }
  }
}
```

Run the backend with
`SIGHTPANE_ADDR=127.0.0.1:18790 SIGHTPANE_PROXY_PROTOCOL=1 SIGHTPANE_TRUSTED_PROXIES=127.0.0.1`;
it then honours the header only for connections from Caddy and ignores it for
direct ones, which is what stops a client from inventing its own address. If the
proxy works at the HTTP layer (`reverse_proxy`), none of this is needed and
`X-Forwarded-For` is enough.

**Visitor** = a hash of user name + IP + browser (`visitor_key`); one account
signing in from two browsers counts as two visitors. The "user" numbers in the
statistics are based on this key.

**Live**: the SDK sends a `heartbeat` (the current route) every 20 s; no row is
written, the session's `last_seen_at` / `current_route` are updated.
`GET /api/v1/projects/{id}/live?window=60` returns the open sessions seen in the
last 60 seconds, the visitor count and the number of people per route.

Passwords use PBKDF2-SHA256 (120k rounds, random salt); tokens are stored as a
SHA-256 digest.

## Replay frames

Frames are the only data here that grows without bound, so they live outside the
database: `internal/blob` writes them either to `SIGHTPANE_DATA/frames/<session>/<seq>.png`
(the default) or to an object store as `<session>/<seq>.png`. The HTTP contract is
the same either way — `GET /api/v1/sessions/{id}/frames/{seq}.png` returns the same
bytes — and an existing frame directory keeps working untouched.

`SIGHTPANE_FRAMES=s3` is what makes more than one backend replica possible, since a local
directory is not shared. It is built for Ceph's RADOS Gateway, which replicates and
scrubs the objects itself and grows by adding OSDs; MinIO and AWS S3 work through
the same settings.

```bash
docker compose --profile ceph up -d          # a one-container Ceph with an S3 gateway on :8080
SIGHTPANE_FRAMES=s3 SIGHTPANE_S3_ENDPOINT=127.0.0.1:8080 SIGHTPANE_S3_BUCKET=sightpane-frames \
  SIGHTPANE_S3_ACCESS_KEY=hogaccesskey SIGHTPANE_S3_SECRET_KEY=hogsecretkey ./sightpane

# the S3 tests skip unless they are pointed at a real gateway
SIGHTPANE_TEST_S3_ENDPOINT=127.0.0.1:8080 SIGHTPANE_TEST_S3_ACCESS_KEY=hogaccesskey \
  SIGHTPANE_TEST_S3_SECRET_KEY=hogsecretkey go test ./internal/blob/
```

Frames are streamed through the backend rather than handed out as presigned URLs:
access is decided per project on every request, and a presigned URL would outlive
that decision.

## Error body and codes

Every error returns `{"error": "<English text>", "code": "<code>"}`. The **text**
is for people reading curl output and can be reworded freely; the **code** is a
contract — the dashboard picks its translation from it
(`frontend/lib/core/auth.dart` `describeError`). The codes are constants in
`internal/apierr`:

| Code | Status | When |
|---|---|---|
| `auth.login_required` / `auth.invalid_token` | 401 | no token / invalid token |
| `auth.invalid_credentials` | 401 | sign-in failed |
| `auth.email_taken` · `auth.invalid_email` · `auth.password_too_short` | 409 / 400 | registration |
| `auth.unsupported_locale` | 400 | `PATCH /auth/me` with an unknown language |
| `project.not_found` · `project.owner_required` · `project.name_required` | 404 / 403 / 400 | projects |
| `member.unknown_email` · `member.owner_self_remove` | 404 / 400 | membership |
| `session.not_found` · `frame.not_found` · `issue.not_found` | 404 | detail endpoints |
| `envelope.key_required` · `envelope.unknown_key` · `envelope.too_large` · `envelope.invalid` | 401 / 413 / 400 | SDK ingest |
| `bad_json` · `not_found` · `internal` | 400 / 404 / 500 | general |

Changing a code breaks the dashboard; add a new one for a new situation.
`GET /api/v1/health` also reports the dashboard languages the server knows
(`locales`).

**User language**: `users.locale` (`''` = no choice made, the dashboard falls back
to the browser). `PATCH /api/v1/auth/me {"locale":"en"}` writes it and
`GET /auth/me` reads it, so the same account sees the same language from another
browser.

Error grouping (`internal/store/fingerprint.go`): the exception type plus the
first three stack frames in application code (line/column numbers are stripped;
`package:flutter/`, `dart:` and SDK frames do not count). With no stack, the
message is used with its numbers normalised. A resolved group reopens
automatically when it is seen again.

CORS is open to every origin (Flutter web posts from another port). The envelope
limit is 32 MB. Requests that are not files and not under `/api/` are answered
with the dashboard's `index.html`, because it is a single-page app; `/api/` paths
keep returning a JSON 404.

## Docker

```bash
docker compose up -d --build      # http://localhost:8790 → dashboard + API
```

Two containers: the backend and TimescaleDB. The backend waits for the database's
health check and migrates the schema itself on start, so there is no setup step.

[`Dockerfile`](Dockerfile) has three stages: a dashboard build that clones
[sightpane/ui](https://github.com/sightpane/ui) at `UI_REF` and builds it with the
official Flutter 3.47.0 tarball (`SIGHTPANE_API_URL` empty → the dashboard calls
the API on its own origin), a cgo-free Go build, and an `alpine` runtime image
(~30 MB plus the dashboard). Pin the dashboard with
`--build-arg UI_REF=v0.2.0`, or leave it out entirely with `UI_REF=none` for an
API-only image that serves the placeholder page compiled into the binary.

The database lives in the `pg-data` volume and the frame PNGs in `sightpane-data`
(`/data`). The environment variables are in [`docker-compose.yml`](docker-compose.yml):
the DSN, retention, default project / key, admin email / password — change
`POSTGRES_PASSWORD` and `SIGHTPANE_ADMIN_PASSWORD` before exposing it. Behind a reverse proxy, the
`X-Forwarded-For` header is recorded as the session IP.

Replay frames can also go to an object store instead of the volume, which is what
lets the backend run as more than one replica:

```bash
docker compose --profile ceph up -d   # adds a Ceph cluster with an S3 gateway on :8080
```

Then point the backend at it with `SIGHTPANE_FRAMES=s3` and the `SIGHTPANE_S3_*`
variables — see "Replay frames" above. MinIO and AWS S3 work through the same
settings.

On the SDK side, `endpoint` is the container's external address (for example
`http://sightpane.company.local:8790`) and `apiKey` is the project key from the
dashboard.

## Roadmap

The features Sentry has and this does not are written up as issues under
`future-todo-files/`, with code coordinates, a fix shape and acceptance criteria:
source map symbolication, alerts, performance monitoring, release health, issue
workflow, search, replay-on-error plus an offline queue, sampling/quotas/retention,
privacy, organisations and roles, storage. See
[`future-todo-files/README.md`](future-todo-files/README.md).

## Language

English is the working language of this repository: code comments, READMEs,
issues and commit messages are written in English. The dashboard's user interface
is a separate matter — it is translated, and its strings live in
`frontend/lib/l10n/`.

## License

**AGPL-3.0-or-later** (`LICENSE`). This is a server offered over a network:
anyone who modifies it and serves it to others has to offer the source too. As
§13 requires, `GET /api/v1/health` returns the source address and the dashboard
shows it on the sign-in page and in the top bar.

The dashboard ([sightpane/ui](https://github.com/sightpane/ui)) is under the same
licence. The SDK ([sightpane/flutter](https://github.com/sightpane/flutter)) is
**Apache-2.0** on purpose: it is embedded into other people's applications, so it
has to be usable in closed-source products and carry an explicit patent grant.
Embedding the SDK creates no AGPL obligation.

Copyright (C) 2026 Can Us.
