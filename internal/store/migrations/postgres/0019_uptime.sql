-- 0019_uptime.sql: uptime & synthetic HTTP/SSL endpoint monitoring

CREATE TABLE IF NOT EXISTS uptime_monitors (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    method TEXT NOT NULL DEFAULT 'GET' CHECK (method IN ('GET', 'HEAD', 'POST')),
    headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    expected_status_code INT NOT NULL DEFAULT 200,
    interval_seconds INT NOT NULL DEFAULT 60 CHECK (interval_seconds IN (30, 60, 300, 600)),
    timeout_seconds INT NOT NULL DEFAULT 10,
    status TEXT NOT NULL DEFAULT 'up' CHECK (status IN ('up', 'degraded', 'down')),
    ssl_check_enabled BOOLEAN NOT NULL DEFAULT true,
    ssl_issuer TEXT NOT NULL DEFAULT '',
    ssl_expires_at TIMESTAMPTZ,
    last_checked_at TIMESTAMPTZ,
    uptime_percentage NUMERIC(5, 2) NOT NULL DEFAULT 100.00,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_uptime_monitors_project ON uptime_monitors(project_id);

CREATE TABLE IF NOT EXISTS uptime_checks (
    id BIGSERIAL,
    monitor_id BIGINT NOT NULL REFERENCES uptime_monitors(id) ON DELETE CASCADE,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status_code INT,
    response_time_ms INT NOT NULL DEFAULT 0,
    is_up BOOLEAN NOT NULL,
    error_message TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (id, checked_at)
);

CREATE INDEX IF NOT EXISTS idx_uptime_checks_monitor_time ON uptime_checks(monitor_id, checked_at DESC);
CREATE INDEX IF NOT EXISTS idx_uptime_checks_project ON uptime_checks(project_id, checked_at DESC);
