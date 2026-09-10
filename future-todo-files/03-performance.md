# feat(sdk,backend,dashboard): performance monitoring (transactions/spans, route timings, app start, jank)

## Problem

The SDK sends the duration of gRPC/HTTP calls as breadcrumbs
(the `grpc` breadcrumb's `ms`, `SightpaneHttpClient`'s `ms`), but the backend only shows them
on the timeline; there is no aggregated view at all. Route load time, app start time and
dropped frames are not measured at all. There is no equivalent of Sentry's Performance /
Tracing product.

## Why it matters

For complaints like "the customer list is slow to open" we have no data; we cannot see
which gRPC call's p95 regressed or which screen got slower.

## Where to look

**Code**
- `package/lib/src/http_client.dart:7` — `SightpaneHttpClient`: the duration is already
  measured and goes out as a breadcrumb.
- `package/lib/src/models.dart` — a new `SightpaneItem.span(...)` / `transaction` factory.
- `package/lib/src/widgets/navigator_observer.dart` — route transitions; the right place
  to start the "route load" transaction, finishing it after the first frame
  (`WidgetsBinding.instance.addPostFrameCallback`).
- `package/lib/src/hog.dart:109` — `SightpaneClient`; a `startTransaction(name)` API.
- `backend/internal/store/store.go` — `migrate`, the `items` table's type column is free-form,
  so `type='span'` would work, but for aggregation a separate `spans(project_id, session_id, ts, op, name,
  duration_ms, status, parent)` table fits better.
- `backend/internal/store/stats.go` — `Stats`; performance summaries follow a similar query shape.
- `frontend/lib/shell/app_shell.dart` — `projectNavEntries`; a new "Performance" entry.
- `frontend/lib/features/projects/overview_page.dart:16` — the bar chart component
  (`BarChart`, `shared/widgets.dart`) can be reused for p50/p95.

**Contract / data**
- Envelope item `span{op, name, start_ms(offset), duration_ms, status, tags}` and
  `transaction` (span + children). An older backend counts an unknown type as `rejected`;
  for backward compatibility, ship the backend first.
- Endpoint: `GET /api/v1/projects/{id}/performance?days=&op=` → count, p50, p95, error
  rate and a daily series per op/name; `GET …/performance/{name}` → slow samples (with a
  session link).

**Tests that pin current behaviour**
- `package/test/widgets_test.dart` "SightpaneHttpClient leaves http breadcrumbs".
- On the frontend2 side, `test/hog_integration_test.dart` (the gRPC interceptor breadcrumb).

**Docs**
- `package/README.md` (the API list), `backend/README.md` (API), `frontend/README.md` (pages).

## Fix shape

1. SDK: `Hog.startTransaction('route:/cashier')` → `HogTransaction` (start/finish,
   `child(op, name)`); `SightpaneNavigatorObserver` and the router listener on the
   dashboard/app side open the route transaction automatically and close it after the
   first frame. `SightpaneHttpClient` and the gRPC interceptor emit spans in addition to
   breadcrumbs. App start: from `Sightpane.init` to the first frame. Jank: count frames over
   16 ms via `SchedulerBinding.addTimingsCallback` and emit a per-minute `frame_stats`
   event.
2. Backend: a `spans` table plus a `(project_id, op, name, ts)` index; a `span` branch in
   ingest; compute percentiles in SQLite with `ORDER BY duration_ms LIMIT/OFFSET` (the
   volume is small), or use an hourly summary table.
3. Dashboard: a "Performance" page — a transaction list (name, count, p50, p95, error %),
   and on click the daily series and the 20 slowest samples (linking to the session
   recording, scrolled to that moment).

Difficulty: volume. Casino CRM makes dozens of gRPC calls per second; spans will need
sampling (`SightpaneOptions.tracesSampleRate`) — see 08.

## Acceptance

- [x] Route load, gRPC/HTTP and app start transactions are listed in the dashboard with p50/p95
- [x] You can go from a transaction's slowest sample to the session recording
- [x] Older SDK envelopes are unaffected; against a backend that does not know `span`, the SDK can turn it off via `beforeSend`
- [x] SDK: transaction lifecycle in `hog_test.dart`; backend: a percentile test with literal values
