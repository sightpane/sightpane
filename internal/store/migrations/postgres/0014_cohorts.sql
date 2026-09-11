-- 0014_cohorts.sql: behavioral cohorts and user retention tracking

CREATE TABLE IF NOT EXISTS cohorts (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_static BOOLEAN NOT NULL DEFAULT false,
    rules JSONB NOT NULL DEFAULT '[]'::jsonb,
    user_count INT NOT NULL DEFAULT 0,
    last_calculated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cohorts_project ON cohorts(project_id);

CREATE TABLE IF NOT EXISTS cohort_members (
    cohort_id BIGINT NOT NULL REFERENCES cohorts(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(cohort_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_cohort_members_user ON cohort_members(user_id);
