# feat(backend,dashboard): cron job & periodic task heartbeat monitoring

## Problem

Backend microservices, background workers, and batch scripts rely heavily on periodic tasks (e.g., nightly billing invoices, hourly cache warmers, database vacuuming, backup scripts). When a cron job silently fails, crashes due to an out-of-memory error, or never runs because the host scheduler died, engineering teams remain unaware until critical business functions break or data is lost.

## Why it matters

Sentry's Crons feature provides passive heartbeat monitoring for periodic jobs. By notifying Sightpane when a job starts and completes, teams can detect both execution failures and silent missing runs (missed heartbeats past the crontab schedule + grace period), routing alerts to Slack, email, or PagerDuty.

## Where to look

**Code**
- `backend/internal/crons/evaluator.go` (new): Background goroutine running every 30s using `github.com/robfig/cron/v3` to compute expected check-in times and detect missed execution deadlines.
- `backend/internal/store/crons.go` (new): Database queries for updating monitor statuses, recording check-in telemetry, and calculating uptime ratios.
- `backend/internal/server/cron_handlers.go` (new): Lightweight HTTP check-in API and monitor CRUD.
- `backend/internal/alerts/evaluator.go`: Dispatch alerts when a monitor status transitions to `missed` or `error`.
- `frontend/lib/features/crons/crons_page.dart` (new): Overview grid of scheduled jobs with status indicators (`OK` in green, `Missed` in red, `In Progress` in blue), schedule crontab string, and last run duration.
- `frontend/lib/features/crons/cron_detail_page.dart` (new): Hourly/daily run matrix (24-hour timeline bar), latency trend chart, error logs, and setup snippets for Bash/cURL, Python, Go, and Node.js.

**Contract / data**
- Tables `cron_monitors` and `cron_checkins`:
  ```sql
  CREATE TABLE cron_monitors (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      slug TEXT NOT NULL,
      name TEXT NOT NULL,
      schedule TEXT NOT NULL, -- Crontab expression: "0 * * * *" or "@daily"
      timezone TEXT NOT NULL DEFAULT 'UTC',
      grace_period_minutes INT NOT NULL DEFAULT 15,
      max_runtime_minutes INT NOT NULL DEFAULT 60,
      status TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'in_progress', 'error', 'missed')),
      last_checkin_at TIMESTAMPTZ,
      next_expected_at TIMESTAMPTZ,
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      UNIQUE(project_id, slug)
  );

  CREATE TABLE cron_checkins (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      monitor_id UUID NOT NULL REFERENCES cron_monitors(id) ON DELETE CASCADE,
      status TEXT NOT NULL CHECK (status IN ('in_progress', 'ok', 'error')),
      duration_ms INT,
      message TEXT,
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  CREATE INDEX idx_cron_checkins ON cron_checkins(monitor_id, created_at DESC);
  ```
- Ingest Check-In API (`POST /api/v1/crons/:slug/checkin` with header `X-Sightpane-Key`):
  ```json
  {
    "status": "ok",
    "duration_ms": 1420
  }
  ```
  *(Or simple cURL ping: `curl -m 10 https://sightpane.domain/api/v1/crons/backup-db/checkin?key=sp_key&status=ok`)*

## Fix shape

1. **Next Expected Timestamp Calculation**:
   - Parse `schedule` using standard crontab parser. Whenever a check-in completes with `status: ok`, compute `next_expected_at = schedule.Next(now)`.
2. **Missed Deadline Checker**:
   - Background worker checks: `WHERE status != 'missed' AND now > next_expected_at + INTERVAL 'grace_period_minutes' MINUTE`.
   - If exceeded, atomically mark `status = 'missed'` and trigger an alert notification.
3. **Timeout Checker**:
   - If a job checked in with `in_progress` but did not complete within `max_runtime_minutes`, mark `status = 'error'` with "Job execution timed out".

## Acceptance

- [ ] Jobs can be configured with standard 5-part crontab expressions and custom grace periods.
- [ ] Lightweight HTTP and cURL endpoints allow zero-dependency integration into existing shell scripts and cron jobs.
- [ ] Missed runs trigger immediate alerts through configured alert channels (Slack, email, webhook).
- [ ] The dashboard provides a visual 24-hour timeline of successful, failed, and missed executions.
