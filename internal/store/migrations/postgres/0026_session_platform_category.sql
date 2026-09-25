-- 0026_session_platform_category.sql: the device's platform category as a column
--
-- A server SDK (sightpane-go) reports `platform_category: backend` and opens one
-- "session" per process so its items have a home. That row is not a visit, and
-- every query that lists or counts visits leaves it out by this column.

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS platform_category TEXT NOT NULL DEFAULT '';

-- device_json has only ever held JSON the envelope decoder already accepted, so
-- the cast cannot fail; the LIKE keeps the rewrite to rows that carry the field.
UPDATE sessions
SET platform_category = COALESCE(device_json::jsonb->>'platform_category', '')
WHERE platform_category = '' AND device_json LIKE '%platform_category%';
