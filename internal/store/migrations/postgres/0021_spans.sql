-- 0021_spans.sql: distributed tracing and OpenTelemetry/W3C waterfall span visualizer

-- Allow backend spans to be recorded without a client session
ALTER TABLE spans ALTER COLUMN session_id DROP NOT NULL;

-- Add service_name to distinguish client, api-gateway, microservices, etc.
ALTER TABLE spans ADD COLUMN IF NOT EXISTS service_name TEXT NOT NULL DEFAULT 'client';

-- Add data JSONB for OpenTelemetry attributes (db.statement, http.status_code, etc.)
ALTER TABLE spans ADD COLUMN IF NOT EXISTS data JSONB DEFAULT '{}'::jsonb;

-- Index for fast sub-100ms trace lookup by project and trace_id
CREATE INDEX IF NOT EXISTS idx_spans_project_trace ON spans(project_id, trace_id);

-- Index for trace listing ordered by timestamp and duration
CREATE INDEX IF NOT EXISTS idx_spans_project_ts_dur ON spans(project_id, ts DESC, duration_ms DESC);
