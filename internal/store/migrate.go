// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"strings"
	"time"
)

// The schema lives in numbered .sql files, applied in filename order and
// recorded in schema_migrations so each runs once. That is all the machinery
// there is — a migration library would be a dependency for a job that fits in
// this file, and neither golang-migrate nor goose can run this schema without
// help: creating a continuous aggregate is not allowed inside a transaction,
// which is what those wrap a migration in.
//
// There are two variants of the second file: what a plain server can do, and the
// hypertable and continuous aggregate on top when timescaledb is installed. The
// variant is part of the recorded version, so a database that later gains the
// extension picks up the timescale file rather than skipping it as done.
//
//go:embed all:migrations
var migrationsFS embed.FS

// migrationDirs are the directories to apply, in order.
func (s *Store) migrationDirs() []string {
	if s.timescale {
		return []string{"postgres", "postgres/timescale"}
	}
	return []string{"postgres", "postgres/plain"}
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL
)`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}
	applied, err := s.appliedMigrations()
	if err != nil {
		return err
	}
	for _, dir := range s.migrationDirs() {
		names, err := migrationFiles(dir)
		if err != nil {
			return err
		}
		for _, name := range names {
			version := dir + "/" + name
			if applied[version] {
				continue
			}
			body, err := migrationsFS.ReadFile("migrations/" + version)
			if err != nil {
				return err
			}
			if err := s.applyMigration(version, string(body)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) appliedMigrations() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func migrationFiles(dir string) ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations/"+dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// applyMigration runs one file statement by statement rather than as a single
// batch, and outside a transaction, because creating a continuous aggregate is
// not allowed inside one. Every statement is written to be idempotent, so a file
// interrupted halfway is safe to re-run.
func (s *Store) applyMigration(version, body string) error {
	for _, st := range splitStatements(body) {
		if _, err := s.db.Exec(st.sql); err != nil {
			if st.ignoreErrors {
				continue
			}
			return fmt.Errorf("migration %s: %w\n%s", version, err, firstSQLLine(st.sql))
		}
	}
	_, err := s.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES($1,$2)`, version, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("recording migration %s: %w", version, err)
	}
	log.Printf("applied migration %s", version)
	return nil
}

func firstSQLLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "--") {
			return l
		}
	}
	return s
}

// statement is one statement out of a migration file, plus whether a
// `-- +ignore-errors` directive above it said to carry on when it fails. It
// exists for TimescaleDB syntax that has moved between releases: a server that
// does not understand how compression is turned on must not stop the process
// from starting.
type statement struct {
	sql          string
	ignoreErrors bool
}

// splitStatements cuts a file on semicolons, stepping over string literals,
// dollar-quoted bodies and comments so a semicolon inside one does not split it.
func splitStatements(src string) []statement {
	var out []statement
	var b strings.Builder
	i := 0
	for i < len(src) {
		switch c := src[i]; {
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			n := strings.IndexByte(src[i:], '\n')
			if n < 0 {
				n = len(src) - i
			}
			b.WriteString(src[i : i+n])
			i += n
		case c == '\'':
			j := i + 1
			for j < len(src) {
				if src[j] == '\'' {
					if j+1 < len(src) && src[j+1] == '\'' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			b.WriteString(src[i:j])
			i = j
		case c == '$':
			tag := dollarTag(src[i:])
			if tag == "" {
				b.WriteByte(c)
				i++
				continue
			}
			end := strings.Index(src[i+len(tag):], tag)
			if end < 0 {
				b.WriteString(src[i:])
				i = len(src)
				continue
			}
			j := i + len(tag) + end + len(tag)
			b.WriteString(src[i:j])
			i = j
		case c == ';':
			out = appendStatement(out, b.String())
			b.Reset()
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return appendStatement(out, b.String())
}

// dollarTag returns the `$$` or `$tag$` opener at the start of s, or "".
func dollarTag(s string) string {
	if len(s) == 0 || s[0] != '$' {
		return ""
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '$' {
			return s[:i+1]
		}
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return ""
		}
	}
	return ""
}

func appendStatement(out []statement, raw string) []statement {
	s := strings.TrimSpace(raw)
	if s == "" || firstSQLLine(s) == "" || strings.HasPrefix(firstSQLLine(s), "--") {
		return out
	}
	return append(out, statement{sql: s, ignoreErrors: strings.Contains(s, "+ignore-errors")})
}
