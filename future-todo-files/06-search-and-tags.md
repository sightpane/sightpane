# feat(backend,dashboard): session/error search with tags and a query language

## Problem

The session list can only be filtered by `user` (exact match) and `errors=1`
(`ListSessions`, `SessionFilter`); the issue list only by `resolved`. There is no way to
search by release, platform, browser, IP, route, `session.props` (location, role) or error
message. There is no Sentry-style `release:1.2 browser:Chrome user.email:x` query.

## Why it matters

Finding something by eye works with 250 sessions; not with thousands. "Today's errors from
Firefox users at the NOVO location" cannot be answered today.

## Where to look

**Code**
- `backend/internal/store/query.go` — `ListSessions` + `SessionFilter{ProjectID, UserID,
  OnlyErrors, Limit}`; `backend/internal/server/data_handler.go` — `listSessions` query
  parameters.
- `backend/internal/store/store.go` — `migrate`, the `sessions` columns (`platform`, `release`,
  `browser`, `ip`, `current_route`, `props_json`, `device_json`); the error message/exception inside
  `items.body_json`.
- `frontend/lib/features/sessions/sessions_page.dart:13` — user box + toggle.
- `frontend/lib/core/api.dart:20` — `SightpaneApi.sessions(...)` signature.

**Contract / data**
- `(project_id, release)`, `(project_id, platform)`, `(project_id, browser)` indexes on
  `sessions`; `json_extract` queries for `props_json` (SQLite JSON1).
- Endpoints: `GET /projects/{id}/sessions?q=release:1.0 browser:Chrome route:/cashier
  props.location:NOVO after:2026-09-01` and `GET /projects/{id}/issues?q=…`.

**Tests that pin current behaviour**
- `backend/internal/server/server_test.go::TestIngestAndQuery` ("errors filter").
- `frontend/test/data_pages_test.dart` "sessions list, error filter and row navigation".

## Fix shape

- A small query parser (a new `search.go` in `backend/internal/store`): `key:value` pairs, quoted
  values,
  `after:/before:` dates, free text → LIKE against `user_json`/`title`; an unknown key is a
  400. Each key maps to a fixed column/JSON path (no SQL injection, parameterised).
- A single search box in the dashboard + quick filter chips (platform, browser, release);
  "save" for per-project saved searches (later on).

## Acceptance

- [x] `q=` supports at least release/platform/browser/route/user/props.*/after/before
- [x] A wrong key returns 400 and a readable error in the dashboard
- [x] Query tests assert against result sets on real SQLite, not against literal expected SQL
- [x] Pagination (`cursor`) is added; the 500-row limit is kept
