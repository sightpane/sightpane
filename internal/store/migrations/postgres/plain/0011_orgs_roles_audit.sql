-- plain postgres migration 0011
CREATE TABLE IF NOT EXISTS orgs (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS org_members (
  org_id BIGINT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'member',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY(org_id, user_id)
);

ALTER TABLE projects ADD COLUMN IF NOT EXISTS org_id BIGINT REFERENCES orgs(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS audit_log (
  id BIGSERIAL PRIMARY KEY,
  org_id BIGINT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  project_id BIGINT REFERENCES projects(id) ON DELETE SET NULL,
  user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_log_org_created ON audit_log(org_id, created_at DESC);

CREATE TABLE IF NOT EXISTS api_tokens (
  id BIGSERIAL PRIMARY KEY,
  org_id BIGINT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  project_id BIGINT REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  token_prefix TEXT NOT NULL,
  scopes TEXT NOT NULL DEFAULT '[]',
  expires_at TIMESTAMPTZ,
  created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash);

DO $$
DECLARE
  p RECORD;
  new_org_id BIGINT;
  m RECORD;
BEGIN
  FOR p IN SELECT id, name, created_at, created_by FROM projects WHERE org_id IS NULL LOOP
    INSERT INTO orgs (name, created_at) VALUES (p.name, p.created_at) RETURNING id INTO new_org_id;
    UPDATE projects SET org_id = new_org_id WHERE id = p.id;
    FOR m IN SELECT user_id, role FROM project_members WHERE project_id = p.id LOOP
      INSERT INTO org_members (org_id, user_id, role, created_at)
      VALUES (new_org_id, m.user_id, m.role, NOW())
      ON CONFLICT (org_id, user_id) DO NOTHING;
    END LOOP;
    IF p.created_by IS NOT NULL THEN
      INSERT INTO org_members (org_id, user_id, role, created_at)
      VALUES (new_org_id, p.created_by, 'owner', NOW())
      ON CONFLICT (org_id, user_id) DO NOTHING;
    END IF;
  END LOOP;
END $$;
