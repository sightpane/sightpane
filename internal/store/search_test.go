// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"sightpane/internal/testdb"
)

func TestSearchQueryParsing(t *testing.T) {
	t.Run("tokenize search terms with quotes", func(t *testing.T) {
		q := `release:1.0.0 browser:"Google Chrome" route:/dashboard props.team:devs after:2026-09-01 free text`
		tokens := tokenizeSearch(q)
		expected := []searchToken{
			{Key: "release", Value: "1.0.0"},
			{Key: "browser", Value: "Google Chrome"},
			{Key: "route", Value: "/dashboard"},
			{Key: "props.team", Value: "devs"},
			{Key: "after", Value: "2026-09-01"},
			{Key: "", Value: "free"},
			{Key: "", Value: "text"},
		}
		if len(tokens) != len(expected) {
			t.Fatalf("expected %d tokens, got %d: %+v", len(expected), len(tokens), tokens)
		}
		for i := range expected {
			if tokens[i].Key != expected[i].Key || tokens[i].Value != expected[i].Value {
				t.Errorf("token %d: expected %+v, got %+v", i, expected[i], tokens[i])
			}
		}
	})

	t.Run("invalid filter key returns error", func(t *testing.T) {
		args := []any{}
		next := func(v any) string {
			args = append(args, v)
			return "$" + strconv.Itoa(len(args))
		}
		_, err := BuildSessionSearchWhere("unknown_key:val", next)
		if err == nil {
			t.Fatal("expected error on unknown key, got nil")
		}
	})

	t.Run("valid location filter keys parse successfully", func(t *testing.T) {
		args := []any{}
		next := func(v any) string {
			args = append(args, v)
			return "$" + strconv.Itoa(len(args))
		}
		w, err := BuildSessionSearchWhere("country:TR city:Istanbul region:Marmara", next)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if w == "" {
			t.Fatal("expected non-empty where clause")
		}
	})
}

func TestSearchExecution(t *testing.T) {
	st, err := Open(Options{
		DSN:     testdb.DSN(t),
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	admin, err := st.CreateUser("admin@sightpane.local", "Admin", "password123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	proj, err := st.CreateProject("Search Project", "web", "", &admin.ID)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Ingest sessions
	sess1 := &Session{
		ID:        "sess-1",
		ProjectID: proj.ID,
		UserID:    "user-alpha",
		Platform:  "web",
		Release:   "1.0.0",
		Browser:   "Chrome 120",
		Route:     "/checkout",
		IP:        "192.168.1.10",
		Props:     json.RawMessage(`{"location":"EU","plan":"enterprise"}`),
	}
	sess2 := &Session{
		ID:        "sess-2",
		ProjectID: proj.ID,
		UserID:    "user-beta",
		Platform:  "mobile",
		Release:   "2.0.0",
		Browser:   "Mobile Safari",
		Route:     "/home",
		IP:        "10.0.0.1",
		Props:     json.RawMessage(`{"location":"US","plan":"free"}`),
	}

	now := time.Now().UTC()
	st.db.Exec(`INSERT INTO sessions(id, project_id, started_at, last_seen_at, user_id, user_json, platform, release, browser, current_route, ip, props_json, error_count, event_count, frame_count)
		VALUES($1, $2, $3, $4, $5, '{"email":"alpha@example.com"}', $6, $7, $8, $9, $10, $11, 2, 5, 1)`,
		sess1.ID, sess1.ProjectID, now.Add(-time.Hour), now, sess1.UserID, sess1.Platform, sess1.Release, sess1.Browser, sess1.Route, sess1.IP, string(sess1.Props))

	st.db.Exec(`INSERT INTO sessions(id, project_id, started_at, last_seen_at, user_id, user_json, platform, release, browser, current_route, ip, props_json, error_count, event_count, frame_count)
		VALUES($1, $2, $3, $4, $5, '{"email":"beta@example.com"}', $6, $7, $8, $9, $10, $11, 0, 1, 0)`,
		sess2.ID, sess2.ProjectID, now.Add(-2*time.Hour), now.Add(-time.Hour), sess2.UserID, sess2.Platform, sess2.Release, sess2.Browser, sess2.Route, sess2.IP, string(sess2.Props))

	// Test release filter
	res, err := st.ListSessions(SessionFilter{ProjectID: proj.ID, Query: "release:1.0.0"})
	if err != nil {
		t.Fatalf("ListSessions with release: %v", err)
	}
	if len(res) != 1 || res[0].ID != "sess-1" {
		t.Fatalf("expected 1 session with release:1.0.0, got %d", len(res))
	}

	// Test props filter
	res, err = st.ListSessions(SessionFilter{ProjectID: proj.ID, Query: "props.plan:enterprise"})
	if err != nil {
		t.Fatalf("ListSessions with props: %v", err)
	}
	if len(res) != 1 || res[0].ID != "sess-1" {
		t.Fatalf("expected sess-1 for props.plan:enterprise, got %d", len(res))
	}

	// Test errors filter
	res, err = st.ListSessions(SessionFilter{ProjectID: proj.ID, Query: "errors:true"})
	if err != nil {
		t.Fatalf("ListSessions with errors:true: %v", err)
	}
	if len(res) != 1 || res[0].ID != "sess-1" {
		t.Fatalf("expected sess-1 for errors:true, got %d", len(res))
	}

	// Test free text search
	res, err = st.ListSessions(SessionFilter{ProjectID: proj.ID, Query: "checkout"})
	if err != nil {
		t.Fatalf("ListSessions with free text: %v", err)
	}
	if len(res) != 1 || res[0].ID != "sess-1" {
		t.Fatalf("expected sess-1 for 'checkout', got %d", len(res))
	}

	_ = ctx
}

// The session columns holding JSON are TEXT, so a filter that reads a field out
// of one has to cast it first; without the cast Postgres rejects the whole query
// with "operator does not exist: text ->> unknown" (SQLSTATE 42883). Every such
// filter runs against the database here, because the SQL text alone cannot show
// the missing cast.
func TestSearchJSONColumnFilters(t *testing.T) {
	st, err := Open(Options{DSN: testdb.DSN(t), DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	admin, err := st.CreateUser("admin@sightpane.local", "Admin", "password123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	proj, err := st.CreateProject("JSON Filters", "web", "", &admin.ID)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	now := time.Now().UTC()
	insert := func(id, userID, user, device string) {
		t.Helper()
		if _, err := st.db.Exec(`INSERT INTO sessions(id, project_id, started_at, last_seen_at, user_id, user_json, device_json)
			VALUES($1, $2, $3, $3, $4, $5, $6)`, id, proj.ID, now, userID, user, device); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("mobile", "6a3b3a64edece80ecf272d15",
		`{"id":"6a3b3a64edece80ecf272d15","email":"mustafa@privaterelay.appleid.com","name":"Mustafa Us"}`,
		`{"platform":"ios","platform_category":"mobile","os":"iOS","arch":"arm64"}`)
	insert("desktop", "",
		`{}`,
		`{"platform":"linux","platform_category":"desktop","os":"Ubuntu","arch":"x86_64","kernel":"Linux","browser_version":"128.0"}`)

	for _, tc := range []struct {
		query string
		want  string
	}{
		{"user:6a3b3a64edece80ecf272d15", "mobile"},
		{"user:privaterelay", "mobile"},
		{"user:Mustafa", "mobile"},
		{"category:mobile", "mobile"},
		{"os:ubuntu", "desktop"},
		{"kernel:linux", "desktop"},
		{"browser_version:128", "desktop"},
		{"arch:x86_64", "desktop"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			res, err := st.ListSessions(SessionFilter{ProjectID: proj.ID, Query: tc.query})
			if err != nil {
				t.Fatalf("ListSessions(%q): %v", tc.query, err)
			}
			if len(res) != 1 || res[0].ID != tc.want {
				ids := []string{}
				for _, s := range res {
					ids = append(ids, s.ID)
				}
				t.Fatalf("ListSessions(%q) = %v, want [%s]", tc.query, ids, tc.want)
			}
		})
	}
}
