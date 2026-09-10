package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"sightpane/internal/blob"
	"sightpane/internal/store"
)

func TestPrivacyIPStorageModes(t *testing.T) {
	app, st := newTestServer(t)
	p, err := st.ProjectByKey("key1")
	if err != nil {
		t.Fatal(err)
	}

	sendWithIP := func(sid string, ip string) {
		env := map[string]any{
			"session": map[string]any{
				"id":         sid,
				"started_at": "2026-03-01T12:00:00Z",
				"user":       map[string]any{"id": "u1"},
			},
			"items": []any{
				map[string]any{
					"type":    "breadcrumb",
					"ts":      "2026-03-01T12:00:01Z",
					"message": "ping",
				},
			},
		}
		body, _ := json.Marshal(env)
		req := httptest.NewRequest("POST", "/api/v1/envelope", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Sightpane-Key", "key1")
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("expected 202, got %d", resp.StatusCode)
		}
	}

	getStoredIP := func(sid string) string {
		s, err := st.GetSession(sid)
		if err != nil {
			t.Fatal(err)
		}
		return s.IP
	}

	// 1. Default / Full IP mode
	sendWithIP("s-full", "198.51.100.42")
	if ip := getStoredIP("s-full"); ip != "198.51.100.42" {
		t.Fatalf("expected full IP 198.51.100.42, got %q", ip)
	}

	// 2. Anonymized IP mode
	if err := st.UpdateProject(p.ID, p.Name, p.Platform, p.RetentionDays, p.QuotaItemsPerMinute, "anonymized", "[]"); err != nil {
		t.Fatal(err)
	}
	sendWithIP("s-anon", "198.51.100.42")
	if ip := getStoredIP("s-anon"); ip != "198.51.100.0" {
		t.Fatalf("expected anonymized IP 198.51.100.0, got %q", ip)
	}

	// 3. None IP mode
	if err := st.UpdateProject(p.ID, p.Name, p.Platform, p.RetentionDays, p.QuotaItemsPerMinute, "none", "[]"); err != nil {
		t.Fatal(err)
	}
	sendWithIP("s-none", "198.51.100.42")
	if ip := getStoredIP("s-none"); ip != "" {
		t.Fatalf("expected empty IP for mode none, got %q", ip)
	}
}

func TestPrivacyServerSideScrubbing(t *testing.T) {
	app, st := newTestServer(t)
	p, err := st.ProjectByKey("key1")
	if err != nil {
		t.Fatal(err)
	}

	scrubRules := `[{"field":"message","regex":"[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}","replace":"[EMAIL]"}]`
	if err := st.UpdateProject(p.ID, p.Name, p.Platform, p.RetentionDays, p.QuotaItemsPerMinute, "full", scrubRules); err != nil {
		t.Fatal(err)
	}

	env := map[string]any{
		"session": map[string]any{
			"id":         "s-scrub",
			"started_at": "2026-03-01T12:00:00Z",
		},
		"items": []any{
			map[string]any{
				"type":    "breadcrumb",
				"ts":      "2026-03-01T12:00:01Z",
				"message": "User alice@example.com logged in",
			},
		},
	}
	body, _ := json.Marshal(env)
	req := httptest.NewRequest("POST", "/api/v1/envelope", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sightpane-Key", "key1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	// Check stored breadcrumb message
	s, err := st.GetSession("s-scrub")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Items) == 0 {
		t.Fatal("expected items in session")
	}
	var m map[string]any
	_ = json.Unmarshal(s.Items[0].Body, &m)
	if m["message"] != "User [EMAIL] logged in" {
		t.Fatalf("expected scrubbed message, got %v", m["message"])
	}
}

func TestPrivacyUserDataDeletionAndExport(t *testing.T) {
	app, st := newTestServer(t)
	p, err := st.ProjectByKey("key1")
	if err != nil {
		t.Fatal(err)
	}

	sid := "s-user-delete"
	userID := "target-user-99"

	env := map[string]any{
		"session": map[string]any{
			"id":         sid,
			"started_at": "2026-03-01T12:00:00Z",
			"user":       map[string]any{"id": userID, "name": "Target User"},
		},
		"items": []any{
			map[string]any{
				"type":   "frame",
				"seq":    1,
				"width":  100,
				"height": 100,
				"png":    tinyPNG,
			},
			map[string]any{
				"type":    "error",
				"message": "Crash for target user",
				"ts":      "2026-03-01T12:00:02Z",
			},
		},
	}
	body, _ := json.Marshal(env)
	req := httptest.NewRequest("POST", "/api/v1/envelope", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sightpane-Key", "key1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	// Verify session and frame exist
	if _, err := st.GetSession(sid); err != nil {
		t.Fatalf("session should exist: %v", err)
	}
	rc, _, err := st.Blobs().Get(t.Context(), blob.FrameKey(sid, 1))
	if err != nil || rc == nil {
		t.Fatalf("frame PNG should exist in blob store: %v", err)
	}
	rc.Close()

	// Test Export endpoint: GET /api/v1/projects/:id/users/:userId/export
	req = httptest.NewRequest("GET", fmt.Sprintf("/api/v1/projects/%d/users/%s/export", p.ID, userID), nil)
	req.Header.Set("Authorization", "Bearer "+userTok)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected export 200, got %d", resp.StatusCode)
	}
	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("failed to parse zip archive: %v", err)
	}
	foundExportJSON := false
	foundFrame := false
	for _, f := range zr.File {
		if f.Name == "export.json" {
			foundExportJSON = true
		}
		if f.Name == fmt.Sprintf("frames/%s/1.png", sid) {
			foundFrame = true
		}
	}
	if !foundExportJSON {
		t.Fatal("export.json missing from zip archive")
	}
	if !foundFrame {
		t.Fatalf("frames/%s/1.png missing from zip archive", sid)
	}

	// Test Deletion endpoint: DELETE /api/v1/projects/:id/users/:userId
	req = httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/projects/%d/users/%s", p.ID, userID), nil)
	req.Header.Set("Authorization", "Bearer "+userTok)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected delete 200, got %d", resp.StatusCode)
	}

	// Verify session is gone
	if _, err := st.GetSession(sid); err != store.ErrNotFound {
		t.Fatalf("expected session to be gone, got err=%v", err)
	}

	// Verify frame blob is gone
	rcAfter, _, err := st.Blobs().Get(t.Context(), blob.FrameKey(sid, 1))
	if err == nil && rcAfter != nil {
		rcAfter.Close()
		t.Fatal("expected frame PNG to be deleted from blob store")
	}
}
