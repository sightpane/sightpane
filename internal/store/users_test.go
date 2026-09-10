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

func TestListProjectUsers(t *testing.T) {
	st, err := Open(Options{
		DSN:     testdb.DSN(t),
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	admin, err := st.CreateUser("admin@sightpane.local", "Admin", "pass123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	proj, err := st.CreateProject("User Analytics Project", "web", "key_users", &admin.ID)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	now := time.Now().UTC()
	t1 := now.Add(-10 * time.Minute)
	t2 := now.Add(-5 * time.Minute)

	// Ingest session 1: user1 (Kaslyer)
	env1 := fmt.Sprintf(`{
		"sdk": {"name": "@sightpane/browser", "version": "1.0.0"},
		"session": {
			"id": "sess-user-1",
			"started_at": %q,
			"user": {"id": "kaslyer@example.com", "email": "kaslyer@example.com", "name": "Kaslyer"}
		},
		"items": [
			{"type": "error", "message": "boom", "exception": "Error"}
		]
	}`, t1.Format(time.RFC3339Nano))
	var envelope1 Envelope
	if err := json.Unmarshal([]byte(env1), &envelope1); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Ingest(ctx, proj.ID, &envelope1, "192.168.1.50"); err != nil {
		t.Fatalf("Ingest session 1: %v", err)
	}

	// Update last_seen_at for session 1 to simulate a 3-minute session (180s)
	_, err = st.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = $1 WHERE id = 'sess-user-1'`, t1.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("Update last_seen_at: %v", err)
	}

	// Ingest session 2: user2 (Ayşe)
	env2 := fmt.Sprintf(`{
		"sdk": {"name": "@sightpane/browser", "version": "1.0.0"},
		"session": {
			"id": "sess-user-2",
			"started_at": %q,
			"user": {"id": "ayse-123", "email": "ayse@example.com", "name": "Ayşe Yılmaz"}
		},
		"items": [
			{"type": "event", "name": "checkout"}
		]
	}`, t2.Format(time.RFC3339Nano))
	var envelope2 Envelope
	if err := json.Unmarshal([]byte(env2), &envelope2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Ingest(ctx, proj.ID, &envelope2, "192.168.1.60"); err != nil {
		t.Fatalf("Ingest session 2: %v", err)
	}

	// Update last_seen_at for session 2 to simulate a 1-minute session (60s)
	_, err = st.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = $1 WHERE id = 'sess-user-2'`, t2.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Update last_seen_at: %v", err)
	}

	// Fetch users
	res, err := st.ListProjectUsers(ctx, proj.ID, 14, "")
	if err != nil {
		t.Fatalf("ListProjectUsers: %v", err)
	}

	if res.TotalUsers != 2 {
		t.Errorf("expected 2 total users, got %d", res.TotalUsers)
	}
	if res.ActiveUsers != 2 {
		t.Errorf("expected 2 active users, got %d", res.ActiveUsers)
	}
	if res.ErrorUserCount != 1 {
		t.Errorf("expected 1 error-impacted user, got %d", res.ErrorUserCount)
	}
	if len(res.Users) != 2 {
		t.Fatalf("expected 2 users in list, got %d", len(res.Users))
	}

	// Test search query
	searchRes, err := st.ListProjectUsers(ctx, proj.ID, 14, "ayse")
	if err != nil {
		t.Fatalf("ListProjectUsers with search: %v", err)
	}
	if len(searchRes.Users) != 1 || searchRes.Users[0].UserID != "ayse-123" {
		t.Fatalf("expected 1 user 'ayse-123', got %+v", searchRes.Users)
	}
	if searchRes.Users[0].Name != "Ayşe Yılmaz" {
		t.Errorf("expected name 'Ayşe Yılmaz', got %q", searchRes.Users[0].Name)
	}
}
