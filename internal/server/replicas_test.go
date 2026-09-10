// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/blob"
	"sightpane/internal/store"
	"sightpane/internal/testdb"
)

// TestTwoReplicasShareFrames verifies that two backend replicas connected
// to the same database and sharing the same frame store (e.g. S3/Ceph or shared storage)
// can ingest frames on one replica and serve them on the other.
func TestTwoReplicasShareFrames(t *testing.T) {
	dsn := testdb.DSN(t)
	sharedFramesDir := filepath.Join(t.TempDir(), "shared-frames")
	sharedStore, err := blob.NewFS(sharedFramesDir)
	if err != nil {
		t.Fatal(err)
	}

	// Replica 1
	st1, err := store.Open(store.Options{
		DSN:     dsn,
		DataDir: t.TempDir(),
		Frames:  sharedStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st1.Close()
	app1 := New(st1, nil, "", nil)

	// Replica 2
	st2, err := store.Open(store.Options{
		DSN:     dsn,
		DataDir: t.TempDir(),
		Frames:  sharedStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	app2 := New(st2, nil, "", nil)

	u, err := st1.CreateUser("shared@x.io", "Shared User", "password123")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st1.CreateProject("shared-project", "flutter", "shared-key", &u.ID)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st1.IssueToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}

	sid := "session-replica-test-1"
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Ingest frame through Replica 1
	env := store.Envelope{
		Session: struct {
			ID        string          `json:"id"`
			StartedAt string          `json:"started_at"`
			User      json.RawMessage `json:"user"`
			Device    json.RawMessage `json:"device"`
			Props     json.RawMessage `json:"props"`
		}{
			ID:        sid,
			StartedAt: now,
		},
		Items: []json.RawMessage{
			json.RawMessage(fmt.Sprintf(`{"type":"frame","seq":1,"png":%q,"ts":%q}`, tinyPNG, now)),
		},
	}
	envBytes, _ := json.Marshal(env)

	req1 := httptest.NewRequest("POST", "/api/v1/envelope", bytes.NewReader(envBytes))
	req1.Header.Set("content-type", "application/json")
	req1.Header.Set("x-sightpane-key", "shared-key")
	resp1, err := app1.Test(req1)
	if err != nil || resp1.StatusCode != fiber.StatusAccepted {
		t.Fatalf("ingest on replica 1 failed: code=%d err=%v", resp1.StatusCode, err)
	}

	// Fetch frame through Replica 2
	req2 := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/frames/1.png", sid), nil)
	req2.Header.Set("authorization", "Bearer "+tok)
	resp2, err := app2.Test(req2)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("replica 2 get frame status = %d, want 200", resp2.StatusCode)
	}
	gotBytes, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotBytes) == 0 {
		t.Fatal("replica 2 returned empty frame bytes")
	}

	// Ingest frame through Replica 2 and fetch via Replica 1
	sid2 := "session-replica-test-2"
	env2 := env
	env2.Session.ID = sid2
	env2Bytes, _ := json.Marshal(env2)

	req3 := httptest.NewRequest("POST", "/api/v1/envelope", bytes.NewReader(env2Bytes))
	req3.Header.Set("content-type", "application/json")
	req3.Header.Set("x-sightpane-key", "shared-key")
	resp3, err := app2.Test(req3)
	if err != nil || resp3.StatusCode != fiber.StatusAccepted {
		t.Fatalf("ingest on replica 2 failed: code=%d err=%v", resp3.StatusCode, err)
	}

	req4 := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/frames/1.png", sid2), nil)
	req4.Header.Set("authorization", "Bearer "+tok)
	resp4, err := app1.Test(req4)
	if err != nil || resp4.StatusCode != http.StatusOK {
		t.Fatalf("replica 1 get frame status = %d, want 200", resp4.StatusCode)
	}

	// Delete project on Replica 1
	if err := st1.DeleteProject(p.ID); err != nil {
		t.Fatal(err)
	}

	// Replica 2 must return 404 for the frame
	req5 := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%s/frames/1.png", sid), nil)
	req5.Header.Set("authorization", "Bearer "+tok)
	resp5, _ := app2.Test(req5)
	if resp5.StatusCode != http.StatusNotFound {
		t.Fatalf("replica 2 frame after deletion status = %d, want 404", resp5.StatusCode)
	}
}
