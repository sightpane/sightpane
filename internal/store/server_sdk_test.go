// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"sightpane/internal/testdb"
)

// A server SDK (sightpane-go) opens one "session" per process so its items have
// somewhere to belong, and reports `platform_category: backend`. That process
// is not a visit: it lasts for days and never has a user. It stays out of every
// view that counts or lists visits, while its errors still count as errors and
// its timeline still opens by id.
func TestServerSDKProcessIsNotASession(t *testing.T) {
	st, err := Open(Options{DSN: testdb.DSN(t), DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	admin, err := st.CreateUser("admin@sightpane.local", "Admin", "pass123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	proj, err := st.CreateProject("Mixed Project", "flutter", "key_mixed", &admin.ID)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	now := time.Now().UTC()
	ingest := func(body string) {
		t.Helper()
		var env Envelope
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("envelope: %v", err)
		}
		if _, err := st.Ingest(ctx, proj.ID, &env, "203.0.113.7"); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}
	// What sightpane-go sends: see newEventQueue in github.com/sightpane/sightpane-go.
	ingest(fmt.Sprintf(`{
		"sdk": {"name": "sightpane-go", "version": "0.1.0"},
		"session": {
			"id": "go-process",
			"started_at": %q,
			"device": {"platform": "go", "platform_category": "backend", "app_type": "server", "os": "linux", "arch": "amd64", "release": "v2.0.0"}
		},
		"items": [
			{"type": "event", "name": "runtime_metrics", "props": {"goroutines": 12}},
			{"type": "error", "exception": "*errors.errorString", "message": "db timeout"}
		]
	}`, now.Add(-2*time.Hour).Format(time.RFC3339Nano)))
	ingest(fmt.Sprintf(`{
		"sdk": {"name": "sightpane", "version": "0.1.0"},
		"session": {
			"id": "phone",
			"started_at": %q,
			"user": {"id": "u-1", "email": "mustafa@example.com", "name": "Mustafa Us"},
			"device": {"platform": "iOS", "platform_category": "mobile", "app_type": "mobile", "os": "iOS", "release": "1.0.0"}
		},
		"items": [{"type": "heartbeat", "route": "/patterns"}]
	}`, now.Add(-time.Minute).Format(time.RFC3339Nano)))

	ids := func(list []Session) []string {
		out := []string{}
		for _, s := range list {
			out = append(out, s.ID)
		}
		return out
	}

	t.Run("session list", func(t *testing.T) {
		list, err := st.ListSessions(SessionFilter{ProjectID: proj.ID})
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(list); len(got) != 1 || got[0] != "phone" {
			t.Fatalf("ListSessions = %v, want [phone]", got)
		}
	})

	t.Run("asking for backend lists it", func(t *testing.T) {
		list, err := st.ListSessions(SessionFilter{ProjectID: proj.ID, Query: "category:backend"})
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(list); len(got) != 1 || got[0] != "go-process" {
			t.Fatalf("ListSessions(category:backend) = %v, want [go-process]", got)
		}
	})

	t.Run("detail still opens", func(t *testing.T) {
		d, err := st.GetSession("go-process")
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		if len(d.Items) != 2 {
			t.Fatalf("items = %d, want 2", len(d.Items))
		}
	})

	t.Run("overview", func(t *testing.T) {
		stats, err := st.Stats(proj.ID, 7)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Sessions != 1 || stats.Users != 1 {
			t.Errorf("sessions, users = %d, %d; want 1, 1", stats.Sessions, stats.Users)
		}
		if stats.CrashFree != 1 {
			t.Errorf("crash free = %v, want 1: the server's error is not a crashed visit", stats.CrashFree)
		}
		if stats.Errors != 1 {
			t.Errorf("errors = %d, want 1: the server's error still counts", stats.Errors)
		}
		for _, p := range stats.Platforms {
			if p.Name == "go" {
				t.Errorf("platforms include the server process: %+v", stats.Platforms)
			}
		}
		today := stats.Daily[len(stats.Daily)-1]
		if today.Sessions != 1 {
			t.Errorf("today's sessions = %d, want 1", today.Sessions)
		}
	})

	t.Run("live", func(t *testing.T) {
		live, err := st.Live(proj.ID, 3*60*60)
		if err != nil {
			t.Fatal(err)
		}
		if live.Count != 1 || live.Viewers[0].SessionID != "phone" {
			t.Fatalf("live = %+v, want only the phone", live.Viewers)
		}
	})

	t.Run("users", func(t *testing.T) {
		users, err := st.ListProjectUsers(ctx, proj.ID, 7, "")
		if err != nil {
			t.Fatal(err)
		}
		if users.SessionsPerUser != 1 {
			t.Errorf("sessions per user = %v, want 1", users.SessionsPerUser)
		}
		if users.AvgDurationSec > 60*60 {
			t.Errorf("avg duration = %vs, the process's two hours leaked in", users.AvgDurationSec)
		}
		today := users.Daily[len(users.Daily)-1]
		if today.ActiveUsers != 1 {
			t.Errorf("today's active users = %d, want 1", today.ActiveUsers)
		}
	})

	t.Run("release health", func(t *testing.T) {
		releases, err := st.ListReleases(ctx, proj.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(releases) != 1 || releases[0].Version != "1.0.0" {
			t.Fatalf("releases = %+v, want only the app's 1.0.0", releases)
		}
		if releases[0].AdoptionRate != 100 || releases[0].CrashFreeRate != 100 {
			t.Errorf("1.0.0 adoption, crash free = %v, %v; want 100, 100", releases[0].AdoptionRate, releases[0].CrashFreeRate)
		}
	})

	t.Run("project card and crash-free alert", func(t *testing.T) {
		p, err := st.ProjectByID(proj.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.Sessions24h != 1 {
			t.Errorf("sessions 24h = %d, want 1", p.Sessions24h)
		}
		_, sessions, crashFree, err := st.EvaluateRateMetrics(proj.ID, 24*60)
		if err != nil {
			t.Fatal(err)
		}
		if sessions != 1 || crashFree != 1 {
			t.Errorf("rate metrics sessions, crash free = %d, %v; want 1, 1", sessions, crashFree)
		}
	})
}
