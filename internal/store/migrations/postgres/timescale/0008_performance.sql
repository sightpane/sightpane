SELECT create_hypertable('spans', 'ts', chunk_time_interval => interval '1 day', migrate_data => true, if_not_exists => true);
