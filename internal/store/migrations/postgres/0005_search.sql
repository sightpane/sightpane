-- 0005_search.sql
-- Indexes to accelerate sessions & issues search queries by release, platform, browser, and route.

CREATE INDEX IF NOT EXISTS idx_sessions_project_release ON sessions (project_id, release);
CREATE INDEX IF NOT EXISTS idx_sessions_project_platform ON sessions (project_id, platform);
CREATE INDEX IF NOT EXISTS idx_sessions_project_browser ON sessions (project_id, browser);
CREATE INDEX IF NOT EXISTS idx_sessions_project_current_route ON sessions (project_id, current_route);
