// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package testdb gives the tests a TimescaleDB to run against.
//
// The store has no in-memory mode any more, so a test that touches it needs a
// real server. Rather than making everyone install and seed one, [Main] starts a
// container for the test binary and takes it down again afterwards; [DSN] then
// hands each test a schema of its own inside it, so tests stay independent and
// the container is only paid for once.
//
// SIGHTPANE_TEST_DB overrides the container and points at a server that is
// already running — a CI service container, or a local one during a long
// debugging session, where starting a fresh container per run is the slow part.
//
// It is imported only from _test.go files, so nothing here reaches the binary.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Image is the database the tests run against, pinned so a new upstream release
// cannot change what CI is testing without a commit saying so.
const Image = "timescale/timescaledb:2.30.0-pg17"

// base is the DSN of the server every test connects to, filled in by Main.
var base string

// Main starts the database, runs the tests and stops it again. A package with
// store-backed tests wires it up with:
//
//	func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }
func Main(m *testing.M) int {
	if dsn := os.Getenv("SIGHTPANE_TEST_DB"); dsn != "" {
		base = dsn
		return m.Run()
	}
	ctx := context.Background()
	// A minute is generous for a warm image and enough for a cold pull on a slow
	// link; failing here with a timeout reads better than hanging.
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	c, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("sightpane"),
		postgres.WithUsername("sightpane"),
		postgres.WithPassword("sightpane"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Printf("starting %s: %v", Image, err)
		log.Print("the tests need a database: start Docker, or point SIGHTPANE_TEST_DB at a running TimescaleDB")
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			log.Printf("stopping the test database: %v", err)
		}
	}()
	if base, err = c.ConnectionString(ctx, "sslmode=disable"); err != nil {
		log.Printf("connection string: %v", err)
		return 1
	}
	return m.Run()
}

// DSN returns a connection string for a schema this test has to itself, dropped
// again when the test ends. public stays on the search path because the
// timescaledb extension lives there and time_bucket has to resolve.
func DSN(t *testing.T) string {
	t.Helper()
	if base == "" {
		t.Fatal("testdb.DSN without testdb.Main: add `func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }` to this package")
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("test database: %v", err)
	}
	defer admin.Close()
	// The name comes from the server so parallel packages cannot pick the same
	// one from a clock that has not moved.
	var n int64
	if err := admin.QueryRow(`SELECT floor(random()*1e15)::bigint`).Scan(&n); err != nil {
		t.Fatalf("connecting to the test database: %v", err)
	}
	schema := fmt.Sprintf("t_%d", n)
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatalf("creating the test schema: %v", err)
	}
	t.Cleanup(func() {
		db, err := sql.Open("pgx", base)
		if err != nil {
			return
		}
		defer db.Close()
		if _, err := db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Logf("dropping the test schema: %v", err)
		}
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("the test database DSN is not a URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	return u.String()
}
