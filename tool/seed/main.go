// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command seed fills a database with enough sessions and items to say something
// about how the queries behave, and prints how long the interesting ones take.
//
// It exists because "the dashboard is fast" is not a claim anyone can check on a
// development database with forty rows in it. The numbers in the pull request
// that moved this backend to TimescaleDB came from here.
//
//	docker compose up -d timescaledb
//	SIGHTPANE_DB=postgres://sightpane:sightpane@127.0.0.1:5432/sightpane?sslmode=disable ./sightpane &
//	go run ./tool/seed --items 1000000
//
// The rows go in with COPY, not through the ingest API: this is about the read
// path, and a million envelopes over HTTP would measure the wrong thing. Every
// run uses a fresh session id prefix, so running it twice adds data rather than
// colliding with what is already there.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

// eventNames and the error/event split are only there to make the aggregates
// non-degenerate: one name with a million rows would measure a different plan
// than a realistic spread.
var eventNames = []string{
	"deposit", "withdraw", "login", "logout", "open_game", "close_game",
	"add_to_cart", "checkout", "share", "search", "settings_open", "support_open",
}

func main() {
	log.SetFlags(0)
	dsn := flag.String("db", os.Getenv("SIGHTPANE_DB"), "database DSN (default: SIGHTPANE_DB)")
	project := flag.Int64("project", 1, "the project to attach the data to")
	sessions := flag.Int("sessions", 20_000, "sessions to create")
	items := flag.Int("items", 1_000_000, "items to spread across them")
	days := flag.Int("days", 90, "how far back to spread the data")
	errorEvery := flag.Int("error-every", 7, "every Nth item is an error rather than an event")
	flag.Parse()

	if err := run(*dsn, *project, *sessions, *items, *days, *errorEvery); err != nil {
		log.Fatalf("seed: %v", err)
	}
}

func run(dsn string, project int64, sessions, items, days, errorEvery int) error {
	if dsn == "" {
		return fmt.Errorf("no database: pass --db or set SIGHTPANE_DB")
	}
	if sessions < 1 || items < 1 || days < 1 || errorEvery < 1 {
		return fmt.Errorf("--sessions, --items, --days and --error-every all have to be at least 1")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, project).Scan(&exists); err != nil {
		return fmt.Errorf("reading projects (start the server once against this database so the schema exists): %w", err)
	}
	if !exists {
		return fmt.Errorf("no project %d; the server creates one on first start", project)
	}

	// A prefix per run keeps a second run additive instead of a primary key
	// collision, and makes the rows easy to find and delete afterwards.
	prefix := fmt.Sprintf("seed-%d-", time.Now().Unix())
	now := time.Now().UTC()
	window := time.Duration(days) * 24 * time.Hour

	start := time.Now()
	n, err := conn.CopyFrom(ctx, pgx.Identifier{"sessions"},
		[]string{"id", "project_id", "started_at", "last_seen_at", "user_id", "platform", "release", "visitor_key"},
		pgx.CopyFromSlice(sessions, func(i int) ([]any, error) {
			at := now.Add(-time.Duration(rand.Int64N(int64(window))))
			return []any{
				prefix + fmt.Sprint(i),
				project,
				at,
				at.Add(time.Duration(rand.IntN(600)) * time.Second),
				fmt.Sprintf("u%d", i%(sessions/4+1)),
				[]string{"android", "ios", "web", "linux"}[i%4],
				[]string{"1.0.0", "1.1.0", "2.0.0"}[i%3],
				fmt.Sprintf("v%d", i%(sessions/4+1)),
			}, nil
		}))
	if err != nil {
		return fmt.Errorf("copying sessions: %w", err)
	}
	log.Printf("%d sessions in %s", n, time.Since(start).Round(time.Millisecond))

	start = time.Now()
	n, err = conn.CopyFrom(ctx, pgx.Identifier{"items"},
		[]string{"session_id", "project_id", "ts", "type", "name", "body_json"},
		pgx.CopyFromSlice(items, func(i int) ([]any, error) {
			kind, name := "event", eventNames[i%len(eventNames)]
			if i%errorEvery == 0 {
				kind, name = "error", "StateError: seeded failure"
			}
			return []any{
				prefix + fmt.Sprint(i%sessions),
				project,
				now.Add(-time.Duration(rand.Int64N(int64(window)))),
				kind,
				name,
				`{"seeded":true}`,
			}, nil
		}))
	if err != nil {
		return fmt.Errorf("copying items: %w", err)
	}
	log.Printf("%d items in %s", n, time.Since(start).Round(time.Millisecond))

	// A continuous aggregate only materialises on its schedule, and the whole
	// point of the measurement is the materialised path, so force it here.
	var timescale bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='timescaledb')`).Scan(&timescale); err != nil {
		return err
	}
	if timescale {
		start = time.Now()
		if _, err := conn.Exec(ctx, `CALL refresh_continuous_aggregate('items_daily', NULL, NULL)`); err != nil {
			return fmt.Errorf("refreshing items_daily: %w", err)
		}
		log.Printf("items_daily refreshed in %s", time.Since(start).Round(time.Millisecond))
	}
	if _, err := conn.Exec(ctx, `ANALYZE sessions, items`); err != nil {
		return err
	}

	return report(ctx, conn, project, days, timescale)
}

// report times the two queries the dashboard leans on. Both are run more than
// once and the best time kept: the first is paying for a cold cache, which is
// not what a dashboard polling every second is doing.
func report(ctx context.Context, conn *pgx.Conn, project int64, days int, timescale bool) error {
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
	queries := []struct {
		label string
		sql   string
		args  []any
	}{
		{"daily errors (Stats)",
			`SELECT to_char(day AT TIME ZONE 'UTC','YYYY-MM-DD'), SUM(n) FROM items_daily
			 WHERE project_id=$1 AND type='error' AND day>=$2 GROUP BY 1`,
			[]any{project, since}},
		{"top events (Stats)",
			`SELECT name, SUM(n) FROM items_daily WHERE project_id=$1 AND type='event' AND day>=$2
			 GROUP BY 1 ORDER BY 2 DESC LIMIT 8`,
			[]any{project, since}},
		{"open sessions (Live)",
			`SELECT id FROM sessions WHERE project_id=$1 AND last_seen_at>=$2 AND ended_at IS NULL
			 ORDER BY last_seen_at DESC LIMIT 500`,
			[]any{project, time.Now().UTC().Add(-time.Minute)}},
	}
	fmt.Println()
	for _, q := range queries {
		best := time.Duration(1<<62 - 1)
		for i := 0; i < 5; i++ {
			start := time.Now()
			rows, err := conn.Query(ctx, q.sql, q.args...)
			if err != nil {
				return err
			}
			for rows.Next() {
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			if d := time.Since(start); d < best {
				best = d
			}
		}
		fmt.Printf("%-24s %s\n", q.label, best.Round(time.Microsecond))
	}
	if !timescale {
		fmt.Println("\nitems_daily is a plain view here; on TimescaleDB it is a continuous aggregate and the first two are far quicker")
	}
	return nil
}
