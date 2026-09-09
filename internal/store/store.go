// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store is the whole persistence layer: the SQLite schema, envelope
// ingest and every read query. It knows nothing about HTTP.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"sightpane/internal/apierr"
	"sightpane/internal/blob"

	_ "modernc.org/sqlite"
)

// Store owns the SQLite handle and the frame store. Frames live outside the
// database — on disk or in an object store — so the database stays small enough
// to copy around and the two can be scaled apart.
type Store struct {
	db    *sql.DB
	blobs blob.Store
}

// Frames reports which frame backend is in use, for logs and /health.
func (s *Store) Frames() string { return s.blobs.Kind() }

var ErrNotFound = errors.New("not found")

// Known failures carry their HTTP status and code from here, so the handler
// layer needs no mapping table (see internal/apierr).
var (
	ErrProjectNameRequired = apierr.New(400, apierr.CodeProjectNameNeeded, "project name required")
	ErrSessionIDRequired   = apierr.New(400, apierr.CodeEnvelopeBad, "session.id required")
)

// Open prepares the data directory, opens the database and migrates it. Frames go to
// [frames]; pass nil for the default directory under [dataDir].
func Open(dataDir string, frames blob.Store) (*Store, error) {
	if frames == nil {
		fs, err := blob.NewFS(filepath.Join(dataDir, "frames"))
		if err != nil {
			return nil, err
		}
		frames = fs
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", databaseFile(dataDir)+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, blobs: frames}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	err := s.db.Close()
	if e := s.blobs.Close(); err == nil {
		err = e
	}
	return err
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  locale TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_tokens (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS projects (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  api_key TEXT NOT NULL UNIQUE,
  platform TEXT NOT NULL DEFAULT 'flutter',
  created_by INTEGER,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS project_members (
  project_id INTEGER NOT NULL REFERENCES projects(id),
  user_id INTEGER NOT NULL REFERENCES users(id),
  role TEXT NOT NULL DEFAULT 'member',
  PRIMARY KEY(project_id, user_id)
);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id),
  started_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  ended_at TEXT,
  user_id TEXT NOT NULL DEFAULT '',
  user_json TEXT NOT NULL DEFAULT '{}',
  device_json TEXT NOT NULL DEFAULT '{}',
  props_json TEXT NOT NULL DEFAULT '{}',
  platform TEXT NOT NULL DEFAULT '',
  release TEXT NOT NULL DEFAULT '',
  error_count INTEGER NOT NULL DEFAULT 0,
  event_count INTEGER NOT NULL DEFAULT 0,
  frame_count INTEGER NOT NULL DEFAULT 0,
  ip TEXT NOT NULL DEFAULT '',
  browser TEXT NOT NULL DEFAULT '',
  visitor_key TEXT NOT NULL DEFAULT '',
  current_route TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_project_seen ON sessions(project_id, last_seen_at DESC);
CREATE TABLE IF NOT EXISTS items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  project_id INTEGER NOT NULL,
  ts TEXT NOT NULL,
  type TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  body_json TEXT NOT NULL DEFAULT '{}',
  issue_id INTEGER
);
CREATE INDEX IF NOT EXISTS items_session_ts ON items(session_id, ts);
CREATE INDEX IF NOT EXISTS items_project_type_ts ON items(project_id, type, ts);
CREATE INDEX IF NOT EXISTS items_issue ON items(issue_id);
CREATE TABLE IF NOT EXISTS issues (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL,
  fingerprint TEXT NOT NULL,
  title TEXT NOT NULL,
  exception TEXT NOT NULL DEFAULT '',
  first_seen TEXT NOT NULL,
  last_seen TEXT NOT NULL,
  count INTEGER NOT NULL DEFAULT 0,
  resolved INTEGER NOT NULL DEFAULT 0,
  UNIQUE(project_id, fingerprint)
);
CREATE TABLE IF NOT EXISTS frames (
  session_id TEXT NOT NULL REFERENCES sessions(id),
  seq INTEGER NOT NULL,
  ts TEXT NOT NULL,
  width INTEGER NOT NULL,
  height INTEGER NOT NULL,
  taps_json TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY(session_id, seq)
);`)
	if err != nil {
		return err
	}
	// Columns added after the first release. An existing install misses them,
	// while a fresh one already got them from the CREATE TABLE above, so the
	// "duplicate column name" error is the normal outcome here and is discarded.
	for _, q := range []string{
		`ALTER TABLE projects ADD COLUMN platform TEXT NOT NULL DEFAULT 'flutter'`,
		`ALTER TABLE projects ADD COLUMN created_by INTEGER`,
		`ALTER TABLE sessions ADD COLUMN ip TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN browser TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN visitor_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN current_route TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN locale TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = s.db.Exec(q)
	}
	return nil
}

// databaseFile picks the SQLite file inside the data directory. The database was
// called hog.db before the project was renamed; an existing one is opened where
// it is rather than starting empty beside it, which would look like total data
// loss. A fresh install gets the new name.
func databaseFile(dataDir string) string {
	current := filepath.Join(dataDir, "sightpane.db")
	if _, err := os.Stat(current); err == nil {
		return current
	}
	legacy := filepath.Join(dataDir, "hog.db")
	if _, err := os.Stat(legacy); err == nil {
		log.Printf("opening %s; rename it to sightpane.db when convenient", legacy)
		return legacy
	}
	return current
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
