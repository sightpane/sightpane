// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/blob"
	"sightpane/internal/store"
	"sightpane/internal/testdb"
)

func TestRetentionPurge(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(store.Options{
		DSN:           testdb.DSN(t),
		DataDir:       tempDir,
		RetentionDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	u, err := st.CreateUser("retention@x.io", "Retention", "secret")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject("retention-test", "flutter", "retention-key", &u.ID)
	if err != nil {
		t.Fatal(err)
	}

	oldSid := "session-old"
	recentSid := "session-recent"
	oldTime := time.Now().UTC().AddDate(0, 0, -40).Format(time.RFC3339Nano)
	recentTime := time.Now().UTC().Format(time.RFC3339Nano)

	// Ingest old session
	oldEnv := store.Envelope{
		Session: struct {
			ID        string          `json:"id"`
			StartedAt string          `json:"started_at"`
			User      json.RawMessage `json:"user"`
			Device    json.RawMessage `json:"device"`
			Props     json.RawMessage `json:"props"`
		}{
			ID:        oldSid,
			StartedAt: oldTime,
		},
		Items: []json.RawMessage{
			json.RawMessage(fmt.Sprintf(`{"type":"frame","seq":1,"png":%q,"ts":%q}`, tinyPNG, oldTime)),
		},
	}
	if _, err := st.Ingest(context.Background(), p.ID, &oldEnv, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	// Ingest recent session
	recentEnv := store.Envelope{
		Session: struct {
			ID        string          `json:"id"`
			StartedAt string          `json:"started_at"`
			User      json.RawMessage `json:"user"`
			Device    json.RawMessage `json:"device"`
			Props     json.RawMessage `json:"props"`
		}{
			ID:        recentSid,
			StartedAt: recentTime,
		},
		Items: []json.RawMessage{
			json.RawMessage(fmt.Sprintf(`{"type":"frame","seq":1,"png":%q,"ts":%q}`, tinyPNG, recentTime)),
		},
	}
	if _, err := st.Ingest(context.Background(), p.ID, &recentEnv, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	oldFrameFile := filepath.Join(tempDir, "frames", blob.FrameKey(oldSid, 1))
	recentFrameFile := filepath.Join(tempDir, "frames", blob.FrameKey(recentSid, 1))

	if _, err := os.Stat(oldFrameFile); err != nil {
		t.Fatalf("expected old frame file to exist: %v", err)
	}
	if _, err := os.Stat(recentFrameFile); err != nil {
		t.Fatalf("expected recent frame file to exist: %v", err)
	}

	// Run retention purge (cutoff 30 days)
	purged, err := st.PurgeExpired(context.Background(), 30)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 session purged, got %d", purged)
	}

	// Old session should be gone
	if _, err := st.GetSession(oldSid); err == nil {
		t.Fatalf("expected old session to be deleted")
	}
	if _, err := os.Stat(oldFrameFile); !os.IsNotExist(err) {
		t.Fatalf("expected old frame file to be deleted from disk, err: %v", err)
	}

	// Recent session should remain
	if _, err := st.GetSession(recentSid); err != nil {
		t.Fatalf("expected recent session to exist: %v", err)
	}
	if _, err := os.Stat(recentFrameFile); err != nil {
		t.Fatalf("expected recent frame file to still exist: %v", err)
	}
}

func TestIngestRateLimitingAndQuota(t *testing.T) {
	app, st := newTestServer(t)

	// Get project
	p, err := st.ProjectByKey("key1")
	if err != nil {
		t.Fatal(err)
	}

	// Update quota to 2 items per minute
	if err := st.UpdateProject(p.ID, p.Name, p.Platform, 30, 2); err != nil {
		t.Fatal(err)
	}

	sendEnv := func(itemsCount int) (*http.Response, map[string]any) {
		items := make([]json.RawMessage, itemsCount)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for i := 0; i < itemsCount; i++ {
			items[i] = json.RawMessage(fmt.Sprintf(`{"type":"breadcrumb","category":"ui","message":"click","ts":%q}`, now))
		}
		body, _ := json.Marshal(store.Envelope{
			Session: struct {
				ID        string          `json:"id"`
				StartedAt string          `json:"started_at"`
				User      json.RawMessage `json:"user"`
				Device    json.RawMessage `json:"device"`
				Props     json.RawMessage `json:"props"`
			}{
				ID:        fmt.Sprintf("sess-%d", time.Now().UnixNano()),
				StartedAt: now,
			},
			Items: items,
		})
		req := httptest.NewRequest("POST", "/api/v1/envelope", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Sightpane-Key", "key1")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp, m
	}

	// First request with 2 items should succeed (consumes 2 tokens)
	resp1, _ := sendEnv(2)
	if resp1.StatusCode != fiber.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d", resp1.StatusCode)
	}

	// Next request with 1 item should be rate limited (0 tokens left)
	resp2, body2 := sendEnv(1)
	if resp2.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", resp2.StatusCode)
	}
	if resp2.Header.Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header on 429 response")
	}
	if body2["error"] != "quota exceeded" {
		t.Fatalf("expected error: quota exceeded, got %v", body2["error"])
	}

	// Verify stats dropped count
	stats, err := st.Stats(p.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Dropped < 1 {
		t.Fatalf("expected dropped >= 1 in stats, got %d", stats.Dropped)
	}
}
