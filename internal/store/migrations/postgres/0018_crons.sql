-- 0018_crons.sql: cron job & periodic task heartbeat monitoring

CREATE TABLE IF NOT EXISTS cron_monitors (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    schedule TEXT NOT NULL,
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

CREATE INDEX IF NOT EXISTS idx_cron_monitors_project ON cron_monitors(project_id);

CREATE TABLE IF NOT EXISTS cron_checkins (
    id BIGSERIAL PRIMARY KEY,
    monitor_id BIGINT NOT NULL REFERENCES cron_monitors(id) ON DELETE CASCADE,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('in_progress', 'ok', 'error')),
    duration_ms INT,
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cron_checkins ON cron_checkins(monitor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cron_checkins_project ON cron_checkins(project_id, created_at DESC);
