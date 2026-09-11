# feat(backend,dashboard): uptime & synthetic HTTP/SSL endpoint monitoring

## Problem

When a server crashes completely, a domain expires, a DNS provider has an outage, or an SSL/TLS certificate lapses, client-side applications cannot even load, meaning the SDK cannot transmit errors or sessions. Passive telemetry is blind to outages where no traffic can connect. Sightpane currently has no active synthetic monitoring mechanism to verify that servers, APIs, and websites are reachable from the outside.

## Why it matters

Sentry's Uptime & Synthetic Monitoring completes the observability picture by providing active exterior health checks. It periodically pings URLs, verifies expected HTTP status codes, tracks response latencies, warns engineers before SSL certificates expire, and computes 99.9% / 99.99% SLA uptime percentages.

## Where to look

**Code**
- `backend/internal/uptime/checker.go` (new): Concurrent HTTP/TCP worker pool executing scheduled checks against configured URLs with configurable timeouts, request headers, and payload validation.
- `backend/internal/uptime/ssl.go` (new): TLS handshake probe inspecting certificate expiration dates and warning when expiry is within 30, 14, or 7 days.
- `backend/internal/store/uptime.go` (new): Database queries for monitor configurations and TimescaleDB hypertable for check logs.
- `backend/internal/server/uptime_handlers.go` (new): Endpoints for managing monitors and fetching uptime history.
- `backend/internal/alerts/evaluator.go`: Dispatch alerts to Slack, Discord, and PagerDuty when an endpoint becomes unreachable or recovers.
- `frontend/lib/features/uptime/uptime_page.dart` (new): Status overview with 90-day green/red operational bars, SLA percentages, and current status badges.
- `frontend/lib/features/uptime/uptime_detail_page.dart` (new): Latency graph over time (p50, p95), incident history with down durations, and SSL certificate expiration countdown.

**Contract / data**
- Tables `uptime_monitors` and `uptime_checks`:
  ```sql
  CREATE TABLE uptime_monitors (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      name TEXT NOT NULL,
      url TEXT NOT NULL,
      method TEXT NOT NULL DEFAULT 'GET' CHECK (method IN ('GET', 'HEAD', 'POST')),
      headers JSONB DEFAULT '{}',
      expected_status_code INT NOT NULL DEFAULT 200,
      interval_seconds INT NOT NULL DEFAULT 60 CHECK (interval_seconds IN (30, 60, 300, 600)),
      timeout_seconds INT NOT NULL DEFAULT 10,
      status TEXT NOT NULL DEFAULT 'up' CHECK (status IN ('up', 'degraded', 'down')),
      ssl_check_enabled BOOLEAN NOT NULL DEFAULT true,
      ssl_expires_at TIMESTAMPTZ,
      last_checked_at TIMESTAMPTZ,
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );

  CREATE TABLE uptime_checks (
      monitor_id UUID NOT NULL REFERENCES uptime_monitors(id) ON DELETE CASCADE,
      checked_at TIMESTAMPTZ NOT NULL,
      status_code INT,
      response_time_ms INT NOT NULL,
      is_up BOOLEAN NOT NULL,
      error_message TEXT,
      PRIMARY KEY (monitor_id, checked_at)
  );
  SELECT create_hypertable('uptime_checks', 'checked_at');
  ```
- Endpoint `GET /api/v1/projects/:id/uptime/:monitorId?days=30`:
  ```json
  {
    "status": "up",
    "uptime_percentage": 99.98,
    "current_response_time_ms": 42,
    "ssl": {
      "valid": true,
      "expires_at": "2026-12-15T00:00:00Z",
      "days_remaining": 95,
      "issuer": "Let's Encrypt"
    },
    "history_90d": [
      { "date": "2026-09-10", "status": "up", "avg_ms": 45 },
      { "date": "2026-09-11", "status": "up", "avg_ms": 42 }
    ]
  }
  ```

## Fix shape

1. **Worker Pool Engine**:
   - Run a worker pool in Go with configurable concurrency (e.g. 20 concurrent goroutines) executing checks using `net/http` with strict timeout controls.
   - For HTTPS URLs, extract `conn.ConnectionState().PeerCertificates[0].NotAfter` to track SSL expiration.
2. **Failure Confirmation**:
   - To avoid false alarms due to transient network hiccups, retry once after 5 seconds before marking the monitor as `down` and dispatching alerts.
3. **Dashboard Status Overview**:
   - Render 90-day status bars similar to GitHub Status / Datadog Status pages.
   - Show SSL expiration badge with warning when under 14 days remaining.

## Acceptance

- [ ] Endpoints can be monitored with HTTP GET/POST/HEAD checks on 30s, 60s, or 300s intervals.
- [ ] TLS/SSL certificates are probed and expiry warnings are dispatched before certificates lapse.
- [ ] Down incidents send immediate alerts with HTTP status and response error details.
- [ ] Uptime percentage SLA is calculated accurately over 24h, 7d, 30d, and 90d periods.
