-- 0016_experiments.sql: A/B testing and experimentation platform with statistical significance

CREATE TABLE IF NOT EXISTS experiments (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    feature_flag_id BIGINT REFERENCES feature_flags(id) ON DELETE SET NULL,
    feature_flag_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'running', 'concluded')),
    primary_metric_event TEXT NOT NULL,
    secondary_metric_events JSONB NOT NULL DEFAULT '[]'::jsonb,
    variants JSONB NOT NULL DEFAULT '[]'::jsonb,
    minimum_sample_size INT NOT NULL DEFAULT 1000,
    winner_variant TEXT,
    started_at TIMESTAMPTZ,
    concluded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_experiments_project ON experiments(project_id);
