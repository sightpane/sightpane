ALTER TABLE issues
  ADD COLUMN IF NOT EXISTS first_release TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS last_release TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS resolved_in_release TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS project_releases (
  project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  version TEXT NOT NULL,
  first_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  session_count BIGINT NOT NULL DEFAULT 0,
  error_session_count BIGINT NOT NULL DEFAULT 0,
  error_count BIGINT NOT NULL DEFAULT 0,
  user_count BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (project_id, version)
);

CREATE INDEX IF NOT EXISTS idx_project_releases_last_seen ON project_releases(project_id, last_seen DESC);
