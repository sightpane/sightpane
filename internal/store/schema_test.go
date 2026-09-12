// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"os"
	"testing"

	"sightpane/internal/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

func open(t *testing.T, retentionDays int) *Store {
	t.Helper()
	st, err := Open(Options{DSN: testdb.DSN(t), DataDir: t.TempDir(), RetentionDays: retentionDays})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// The shape of the schema is the point of running on TimescaleDB at all: items
// has to be a hypertable, items_daily a continuous aggregate over it, and the
// configured retention has to be a live job rather than a value nobody acts on.
func TestTimescaleSchema(t *testing.T) {
	st := open(t, 90)
	if !st.timescale {
		t.Skip("timescaledb is not installed on the test server")
	}

	var chunkInterval string
	if err := st.db.QueryRow(`SELECT time_interval::text FROM timescaledb_information.dimensions
		WHERE hypertable_schema = current_schema() AND hypertable_name='items' AND column_name='ts'`).Scan(&chunkInterval); err != nil {
		t.Fatalf("items is not a hypertable partitioned on ts: %v", err)
	}
	if chunkInterval != "1 day" {
		t.Fatalf("chunk interval %q, want \"1 day\"", chunkInterval)
	}

	var realtime bool
	if err := st.db.QueryRow(`SELECT NOT materialized_only FROM timescaledb_information.continuous_aggregates
		WHERE view_schema = current_schema() AND view_name='items_daily'`).Scan(&realtime); err != nil {
		t.Fatalf("items_daily is not a continuous aggregate: %v", err)
	}
	// Without real-time aggregation the chart would not show what was ingested
	// since the last refresh, which is most of what anyone looks at.
	if !realtime {
		t.Fatal("items_daily must not be materialized_only, or fresh data is invisible until the next refresh")
	}

	var drop string
	if err := st.db.QueryRow(`SELECT config->>'drop_after' FROM timescaledb_information.jobs
		WHERE proc_name='policy_retention' AND hypertable_schema = current_schema() AND hypertable_name='items'`).Scan(&drop); err != nil {
		t.Fatalf("no retention policy on items: %v", err)
	}
	if drop != "90 days" {
		t.Fatalf("retention policy drops after %q, want \"90 days\"", drop)
	}
}

// Zero means "keep everything", so the policy has to be gone rather than merely
// unused — a leftover job from an earlier setting would keep deleting data after
// the operator turned retention off.
func TestRetentionZeroRemovesThePolicy(t *testing.T) {
	st := open(t, 0)
	if !st.timescale {
		t.Skip("timescaledb is not installed on the test server")
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM timescaledb_information.jobs
		WHERE proc_name='policy_retention' AND hypertable_schema = current_schema() AND hypertable_name='items'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d retention jobs left after RetentionDays=0", n)
	}
}

// A DSN is the one setting with no sensible default: starting against the wrong
// database silently would be worse than refusing to start.
func TestOpenWithoutDSN(t *testing.T) {
	if _, err := Open(Options{DataDir: t.TempDir()}); err == nil {
		t.Fatal("opening without SIGHTPANE_DB should fail")
	}
}
