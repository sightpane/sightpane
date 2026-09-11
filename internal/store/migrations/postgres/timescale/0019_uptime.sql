SELECT create_hypertable('uptime_checks', 'checked_at', chunk_time_interval => interval '1 day', migrate_data => true, if_not_exists => true);
