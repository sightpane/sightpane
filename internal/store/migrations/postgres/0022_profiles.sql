-- 0022_profiles.sql: continuous profiling CPU and memory flame charts

CREATE TABLE IF NOT EXISTS profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    transaction_name TEXT NOT NULL,
    session_id UUID,
    trace_id TEXT NOT NULL DEFAULT '',
    duration_ms NUMERIC(10, 2) NOT NULL,
    cpu_time_ms NUMERIC(10, 2) NOT NULL,
    thread_name TEXT NOT NULL DEFAULT 'main',
    platform TEXT NOT NULL DEFAULT '',
    profile_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_profiles_project_tx ON profiles(project_id, transaction_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_profiles_project_created ON profiles(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_profiles_trace ON profiles(project_id, trace_id) WHERE trace_id != '';
