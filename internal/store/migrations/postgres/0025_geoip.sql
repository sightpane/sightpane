-- 0025_geoip.sql: add country_code, country_name, region, city, latitude, and longitude to sessions

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS country_code TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS country_name TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS region TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS city TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS latitude DOUBLE PRECISION;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION;

CREATE INDEX IF NOT EXISTS idx_sessions_project_country ON sessions(project_id, country_code);
CREATE INDEX IF NOT EXISTS idx_sessions_project_region ON sessions(project_id, region);
CREATE INDEX IF NOT EXISTS idx_sessions_project_city ON sessions(project_id, city);

-- Backfill existing private and docker network IP sessions
UPDATE sessions SET
  country_code = 'LOCAL',
  country_name = 'Local Network',
  region = 'Local Region',
  city = 'Local'
WHERE (country_code = '' OR country_code IS NULL)
  AND (
    ip LIKE '127.%'
    OR ip LIKE '10.%'
    OR ip LIKE '192.168.%'
    OR ip LIKE '172.16.%' OR ip LIKE '172.17.%' OR ip LIKE '172.18.%' OR ip LIKE '172.19.%'
    OR ip LIKE '172.20.%' OR ip LIKE '172.21.%' OR ip LIKE '172.22.%' OR ip LIKE '172.23.%'
    OR ip LIKE '172.24.%' OR ip LIKE '172.25.%' OR ip LIKE '172.26.%' OR ip LIKE '172.27.%'
    OR ip LIKE '172.28.%' OR ip LIKE '172.29.%' OR ip LIKE '172.30.%' OR ip LIKE '172.31.%'
    OR ip = '::1'
    OR ip = 'localhost'
  );
