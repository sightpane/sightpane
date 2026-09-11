-- 0020_metric_alerts.sql: metric threshold alerts & dynamic anomaly spike detection

CREATE TABLE IF NOT EXISTS metric_alert_rules (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    metric_type TEXT NOT NULL CHECK (metric_type IN ('error_count', 'error_rate', 'transaction_duration_p95', 'unhandled_crash_count')),
    target_filter TEXT NOT NULL DEFAULT '',
    comparison_operator TEXT NOT NULL CHECK (comparison_operator IN ('gt', 'gte', 'lt', 'spike_multiplier')),
    critical_threshold NUMERIC(10, 2) NOT NULL,
    warning_threshold NUMERIC(10, 2),
    window_minutes INT NOT NULL DEFAULT 5 CHECK (window_minutes IN (1, 5, 10, 15, 30, 60)),
    channel_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    is_active BOOLEAN NOT NULL DEFAULT true,
    current_status TEXT NOT NULL DEFAULT 'ok' CHECK (current_status IN ('ok', 'warning', 'firing')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_metric_alert_rules_project ON metric_alert_rules(project_id);

CREATE TABLE IF NOT EXISTS metric_alert_incidents (
    id BIGSERIAL PRIMARY KEY,
    rule_id BIGINT NOT NULL REFERENCES metric_alert_rules(id) ON DELETE CASCADE,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('firing', 'resolved')),
    triggered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ,
    peak_value NUMERIC(10, 2) NOT NULL,
    summary TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_metric_incidents_rule ON metric_alert_incidents(rule_id, triggered_at DESC);
CREATE INDEX IF NOT EXISTS idx_metric_incidents_project ON metric_alert_incidents(project_id, triggered_at DESC);
