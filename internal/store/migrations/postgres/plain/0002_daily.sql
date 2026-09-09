-- Applied when the timescaledb extension is not available — a managed Postgres
-- that will not install it, say. items_daily is then an ordinary view over the
-- ordinary table, so every query in the store reads the same name and the only
-- thing lost is the materialisation behind it.
--
-- date_trunc runs in the session timezone, which the connection pins to UTC, but
-- the round trip through UTC is spelled out anyway so the bucket matches
-- time_bucket() in the timescale variant whatever the server default is.
CREATE OR REPLACE VIEW items_daily AS
SELECT project_id,
       type,
       name,
       date_trunc('day', ts AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS day,
       count(*) AS n
FROM items
GROUP BY 1, 2, 3, 4;
