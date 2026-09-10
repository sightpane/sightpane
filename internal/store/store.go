// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store is the whole persistence layer: the schema, envelope ingest and
// every read query. It knows nothing about HTTP.
//
// The database is Postgres, and TimescaleDB where the extension is installed —
// `items` is then a hypertable and the daily counts behind the dashboard come
// from a continuous aggregate. Without the extension the same names resolve to
// an ordinary table and view, so a managed Postgres that will not install it is
// still a supported deployment; only compression and the retention policy are
// lost. The SQL is written by hand, no ORM: the queries are the interesting part.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"sightpane/internal/apierr"
	"sightpane/internal/blob"
	"sightpane/internal/symbol"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// Store owns the database handle and the frame store. Frames live outside the
// database — on disk or in an object store — so the database stays small enough
// to back up quickly and the two can be scaled apart.
type Store struct {
	db    *sql.DB
	blobs blob.Store
	// timescale reports whether the extension was available. The queries do not
	// care, because items_daily exists either way; retention and compression do.
	timescale bool
	// symbols resolves minified release frames to source locations, over the
	// maps uploaded for a release. It is a cache in front of the blob store, so
	// the hot ingest path parses a 30 MB map once rather than per error.
	symbols *symbol.Cache
	// connName is the pgx registration this handle was opened through, kept so
	// Close can drop it again.
	connName string
	// onIssueEvent is called when new or regressed issues are committed by Ingest.
	onIssueEvent func([]IssueEvent)
}

// OnIssueEvent registers a callback triggered after ingest commits new or regressed issues.
func (s *Store) OnIssueEvent(cb func([]IssueEvent)) {
	s.onIssueEvent = cb
}

// Frames reports which frame backend is in use, for logs and /health.
func (s *Store) Frames() string { return s.blobs.Kind() }

// Driver reports "timescaledb" or "postgres". Startup logs it, because the
// answer decides whether anything expires on its own.
func (s *Store) Driver() string {
	if s.timescale {
		return "timescaledb"
	}
	return "postgres"
}

var ErrNotFound = errors.New("not found")

// Known failures carry their HTTP status and code from here, so the handler
// layer needs no mapping table (see internal/apierr).
var (
	ErrProjectNameRequired = apierr.New(400, apierr.CodeProjectNameNeeded, "project name required")
	ErrSessionIDRequired   = apierr.New(400, apierr.CodeEnvelopeBad, "session.id required")
)

// Options is everything Open needs.
type Options struct {
	// DSN is the database, as a URL (`postgres://user:pw@host:5432/sightpane?sslmode=disable`)
	// or a libpq key/value string. It is SIGHTPANE_DB and it is required.
	DSN string
	// DataDir holds the frame PNGs, unless Frames says otherwise.
	DataDir string
	// Frames is where replay PNGs go; nil means a directory under DataDir.
	Frames blob.Store
	// RetentionDays drops items older than this. It is a TimescaleDB retention
	// policy, so it does nothing without the extension. Zero keeps everything.
	RetentionDays int
	// SourceMapCacheBytes bounds the parsed source maps held in memory. Zero
	// takes symbol.DefaultMaxBytes.
	SourceMapCacheBytes int64
}

// Open connects, migrates the schema and applies the retention policy.
func Open(opt Options) (*Store, error) {
	if strings.TrimSpace(opt.DSN) == "" {
		return nil, errors.New("SIGHTPANE_DB is required: a postgres:// URL or a libpq key/value string")
	}
	frames := opt.Frames
	if frames == nil {
		fs, err := blob.NewFS(filepath.Join(opt.DataDir, "frames"))
		if err != nil {
			return nil, err
		}
		frames = fs
	}
	s := &Store{blobs: frames}
	s.symbols = symbol.New(s, opt.SourceMapCacheBytes)
	if err := s.connect(opt.DSN); err != nil {
		return nil, err
	}
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.applyRetention(opt.RetentionDays); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) connect(dsn string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("SIGHTPANE_DB: %w", err)
	}
	// Every timestamp in this schema is UTC and every day bucket is a UTC day.
	// Pinning the session timezone means date_trunc and to_char agree with the
	// RFC3339 strings the API hands out whatever the server default is.
	cfg.RuntimeParams["timezone"] = "UTC"
	name := stdlib.RegisterConnConfig(cfg)
	db, err := sql.Open("pgx", name)
	if err != nil {
		stdlib.UnregisterConnConfig(name)
		return err
	}
	// A pool size, not a limit on concurrency: reads and ingest run at the same
	// time here, which is the point of not being on a single-writer database.
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	s.db, s.connName = db, name
	s.timescale = enableTimescale(db)
	return nil
}

// enableTimescale installs the extension and reports whether it is usable. It
// looks before creating: installing needs rights an everyday connection has no
// reason to hold, and an extension that is already there should not require them.
func enableTimescale(db *sql.DB) bool {
	var installed bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='timescaledb')`).Scan(&installed); err != nil {
		log.Printf("cannot read pg_extension (%v); continuing without timescaledb", err)
		return false
	}
	if installed {
		return true
	}
	if _, err := db.Exec(`CREATE EXTENSION timescaledb WITH SCHEMA public`); err != nil {
		log.Printf("timescaledb unavailable (%v); continuing on plain postgres, without hypertables, compression or a retention policy", err)
		return false
	}
	return true
}

// applyRetention keeps the retention policy in step with the configured value on
// every start, so changing the setting is a restart rather than a hand-written
// statement. Zero removes the policy: nothing expires.
func (s *Store) applyRetention(days int) error {
	if !s.timescale {
		return nil
	}
	if _, err := s.db.Exec(`SELECT remove_retention_policy('items', if_exists => true)`); err != nil {
		return fmt.Errorf("retention policy: %w", err)
	}
	if days <= 0 {
		return nil
	}
	// days is an int from the configuration, so there is nothing to inject; a
	// placeholder cannot carry an interval literal here.
	if _, err := s.db.Exec(fmt.Sprintf(`SELECT add_retention_policy('items', drop_after => interval '%d days', if_not_exists => true)`, days)); err != nil {
		return fmt.Errorf("retention policy: %w", err)
	}
	log.Printf("items older than %d days are dropped by the timescaledb retention policy", days)
	return nil
}

func (s *Store) Close() error {
	var err error
	if s.db != nil {
		err = s.db.Close()
	}
	if s.connName != "" {
		stdlib.UnregisterConnConfig(s.connName)
		s.connName = ""
	}
	if s.blobs != nil {
		if e := s.blobs.Close(); err == nil {
			err = e
		}
	}
	return err
}

// isUniqueViolation reports whether err is a duplicate-key failure.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// randomKey is a project API key: 16 random bytes, hex encoded.
func randomKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// decodeBase64 accepts both a bare base64 payload and a `data:image/png;base64,…`
// URL, because the SDK has sent both shapes over time.
func decodeBase64(s string) ([]byte, error) {
	if i := strings.Index(s, ","); i >= 0 && strings.HasPrefix(s, "data:") {
		s = s[i+1:]
	}
	return base64.StdEncoding.DecodeString(s)
}
