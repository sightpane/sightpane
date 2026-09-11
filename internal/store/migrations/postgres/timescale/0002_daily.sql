-- Applied only when the timescaledb extension is available; see the plain/
-- directory next to this one for the same view without it.
--
-- items is the one insert-heavy, time-keyed, eventually-discarded table, which
-- is exactly what a hypertable is for: a day of data per chunk, so a stats query
-- over the last two weeks touches fourteen chunks instead of the whole history,
-- and expiring data is a chunk drop rather than a DELETE.
SELECT create_hypertable('items', 'ts', chunk_time_interval => interval '1 day', migrate_data => true, if_not_exists => true);

-- A database that ran on a Postgres without the extension has items_daily as an
-- ordinary view; it has to go before the aggregate can take the name. If it is
-- already a continuous aggregate (pg_matviews), DROP VIEW would fail.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_views
    WHERE schemaname = current_schema() AND viewname = 'items_daily'
  ) THEN
    EXECUTE 'DROP VIEW items_daily';
  END IF;
END $$;

-- The dashboard refetches project stats every second and the events page groups
-- by day, so both read the daily counts from here instead of scanning items.
--
-- materialized_only = false is load-bearing: the policy below only materialises
-- up to an hour ago, and without real-time aggregation an event just ingested
-- would be missing from the chart that is meant to show it.
CREATE MATERIALIZED VIEW IF NOT EXISTS items_daily
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT project_id,
       type,
       name,
       time_bucket(interval '1 day', ts) AS day,
       count(*) AS n
FROM items
GROUP BY 1, 2, 3, 4
WITH NO DATA;

SELECT add_continuous_aggregate_policy('items_daily',
  start_offset => interval '30 days',
  end_offset => interval '1 hour',
  schedule_interval => interval '1 hour',
  if_not_exists => true);

-- Compression is a large win on this shape of data (many rows per project and
-- type, few distinct values) but the syntax for turning it on has moved between
-- TimescaleDB releases, so a server that does not understand it must not stop
-- the process from starting.
-- +ignore-errors
ALTER TABLE items SET (timescaledb.compress, timescaledb.compress_segmentby = 'project_id, type', timescaledb.compress_orderby = 'ts DESC');
-- +ignore-errors
SELECT add_compression_policy('items', interval '7 days', if_not_exists => true);
