ALTER TABLE issues
  ADD COLUMN IF NOT EXISTS status VARCHAR(16) NOT NULL DEFAULT 'open',
  ADD COLUMN IF NOT EXISTS assignee_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS snooze_until TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS snooze_count_threshold INTEGER DEFAULT 0,
  ADD COLUMN IF NOT EXISTS snooze_start_count INTEGER DEFAULT 0,
  ADD COLUMN IF NOT EXISTS merged_into BIGINT REFERENCES issues(id) ON DELETE SET NULL;

UPDATE issues SET status = 'resolved' WHERE resolved = true AND status = 'open';

CREATE INDEX IF NOT EXISTS idx_issues_status ON issues(project_id, status);
CREATE INDEX IF NOT EXISTS idx_issues_assignee ON issues(project_id, assignee_user_id);

CREATE TABLE IF NOT EXISTS issue_comments (
  id BIGSERIAL PRIMARY KEY,
  issue_id BIGINT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_issue_comments_issue ON issue_comments(issue_id, created_at);

CREATE TABLE IF NOT EXISTS project_fingerprint_rules (
  id BIGSERIAL PRIMARY KEY,
  project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  exception_match TEXT NOT NULL DEFAULT '',
  message_glob TEXT NOT NULL DEFAULT '',
  stack_contains TEXT NOT NULL DEFAULT '',
  action VARCHAR(16) NOT NULL, -- 'ignore' or 'group_as'
  group_fingerprint TEXT NOT NULL DEFAULT '',
  priority INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_fingerprint_rules_proj ON project_fingerprint_rules(project_id, priority DESC);
