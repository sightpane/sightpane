-- 0024_app_type_and_os.sql: separate app_type, os, and os_version on sessions

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS app_type TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS os TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS os_version TEXT NOT NULL DEFAULT '';

-- Backfill existing sessions: derive app_type from platform
UPDATE sessions SET app_type = CASE
  WHEN platform = 'web' THEN 'browser'
  WHEN platform IN ('android', 'ios', 'fuchsia') THEN 'mobile'
  ELSE 'desktop'
END WHERE app_type = '';

CREATE INDEX IF NOT EXISTS idx_sessions_project_app_type ON sessions(project_id, app_type);
CREATE INDEX IF NOT EXISTS idx_sessions_project_os ON sessions(project_id, os);
