# feat(backend,sdk,dashboard): distributed tracing & OpenTelemetry/W3C waterfall span visualizer

## Problem

While Sightpane monitors client-side transaction latencies (issue 03), it lacks end-to-end distributed tracing. When a user clicks "Checkout" in a React or Flutter app, the request travels across an API gateway, multiple backend microservices, and database queries. If that request takes 3.4 seconds, developers cannot see how much time was spent in network transit, backend Go handlers, database locks, or external APIs. There is no W3C `traceparent` propagation and no multi-service waterfall flame chart.

## Why it matters

Distributed tracing is the core of Sentry Performance and OpenTelemetry. Without distributed context propagation, frontend developers blame the backend ("API is slow") and backend developers blame the database. Visualizing the entire call tree in a single waterfall timeline immediately highlights the exact bottleneck (e.g. a 2.8s sequential SQL query or an N+1 query loop).

## Where to look

**Code**
- `backend/internal/store/spans.go` (new): TimescaleDB hypertable operations for ingesting and querying span hierarchies indexed by `trace_id`.
- `backend/internal/middleware/trace.go` (new): Fiber middleware extracting `traceparent` headers, continuing existing traces or generating new trace IDs, and wrapping HTTP responses.
- `backend/internal/store/db_tracing.go` (new): Database query wrappers capturing SQL query text, parameter counts, row counts, and execution durations as child spans.
- `flutter/lib/src/http_client.dart`: Automatically inject `traceparent` (`00-{trace_id}-{span_id}-01`) header into outgoing HTTP requests.
- `react-sdk/src/instrumentation.ts`: Automatically inject `traceparent` headers in wrapped `window.fetch` and `XMLHttpRequest`.
- `frontend/lib/features/performance/trace_detail_page.dart` (new): Hierarchical waterfall span viewer with expandable service trees, timing bars, and detail drawer showing SQL/HTTP payloads.

**Contract / data**
- W3C TraceContext Specification:
  - Header: `traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`
  - Version (`00`), Trace ID (32 hex characters), Span ID (16 hex characters), Trace Flags (`01` = sampled).
- TimescaleDB Hypertable `spans`:
  ```sql
  CREATE TABLE spans (
      project_id BIGINT NOT NULL,
      trace_id TEXT NOT NULL,
      span_id TEXT NOT NULL,
      parent_span_id TEXT,
      session_id UUID,
      op TEXT NOT NULL, -- "http.client", "http.server", "db.query", "ui.load"
      name TEXT NOT NULL, -- "SELECT * FROM orders", "POST /api/v1/checkout"
      service_name TEXT NOT NULL DEFAULT 'client',
      start_time TIMESTAMPTZ NOT NULL,
      duration_ms NUMERIC(10, 3) NOT NULL,
      status TEXT NOT NULL DEFAULT 'ok', -- 'ok', 'error', 'cancelled'
      data JSONB, -- { "db.system": "postgresql", "db.statement": "SELECT ...", "http.status_code": 200 }
      PRIMARY KEY (project_id, start_time, trace_id, span_id)
  );
  SELECT create_hypertable('spans', 'start_time');
  CREATE INDEX idx_spans_trace ON spans(project_id, trace_id);
  ```
- Endpoint `GET /api/v1/projects/:id/traces/:traceId`:
  ```json
  {
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
    "total_duration_ms": 1420.5,
    "service_count": 3,
    "span_count": 8,
    "spans": [
      { "span_id": "span_01", "parent_span_id": null, "op": "ui.action", "name": "click:checkout", "start_offset_ms": 0, "duration_ms": 1420.5, "service": "react-web" },
      { "span_id": "span_02", "parent_span_id": "span_01", "op": "http.client", "name": "POST /api/v1/orders", "start_offset_ms": 15.2, "duration_ms": 1395.1, "service": "react-web" },
      { "span_id": "span_03", "parent_span_id": "span_02", "op": "http.server", "name": "POST /api/v1/orders", "start_offset_ms": 35.0, "duration_ms": 1350.0, "service": "api-gateway" },
      { "span_id": "span_04", "parent_span_id": "span_03", "op": "db.query", "name": "SELECT balance FROM wallets", "start_offset_ms": 50.0, "duration_ms": 12.0, "service": "billing-service" },
      { "span_id": "span_05", "parent_span_id": "span_03", "op": "http.client", "name": "POST https://api.stripe.com/v1/charges", "start_offset_ms": 75.0, "duration_ms": 1280.0, "service": "billing-service" }
    ]
  }
  ```

## Fix shape

1. **W3C Header Injection**:
   - Both Flutter and React SDKs generate a 16-byte random trace ID and 8-byte span ID for outgoing HTTP requests.
   - Inject standard `traceparent` headers so downstream servers can join the trace.
2. **Backend Fiber Integration**:
   - Middleware reads `traceparent`, instruments handlers, and emits server-side spans.
3. **Waterfall Visualizer**:
   - Build a Gantt-style tree visualizer where child spans are indented and nested under parent spans.
   - Distinct color-coding per service and span type (blue for HTTP, orange for DB, purple for UI).
   - Flag suspect performance patterns (e.g., duplicate consecutive queries marked as "Possible N+1 query issue").

## Acceptance

- [ ] Outgoing network requests from SDKs carry standard W3C `traceparent` headers.
- [ ] TimescaleDB hypertable stores spans and serves trace trees in sub-100ms.
- [ ] Waterfall view visually shows time spent across frontend, network, backend, and database queries.
- [ ] Slow database queries display sanitized SQL statements in a detail drawer.
