// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"fmt"
	"testing"
	"time"

	"sightpane/internal/store"
)

func TestPathFlowEndpoints(t *testing.T) {
	app, _ := newTestServer(t)
	projID := int64(1)

	now := time.Now().UTC()

	// Helper to ingest session events
	ingestSession := func(sessionID string, items []map[string]any, startTime time.Time) {
		env := map[string]any{
			"sdk": map[string]any{"name": "sightpane", "version": "0.1.0"},
			"session": map[string]any{
				"id":           sessionID,
				"started_at":   startTime.Format(time.RFC3339Nano),
				"last_seen_at": startTime.Add(10 * time.Minute).Format(time.RFC3339Nano),
				"user_id":      "user-" + sessionID,
			},
			"items": items,
		}
		rrEnv := post(t, app, "/api/v1/envelope", "key1", env)
		if rrEnv.Code != 202 {
			t.Fatalf("ingest %s: status %d %s", sessionID, rrEnv.Code, rrEnv.Body.String())
		}
	}

	// Ingest session 1: /home -> /products -> add_to_cart -> checkout_success
	t1 := now.Add(-2 * time.Hour)
	ingestSession("sess-path-1", []map[string]any{
		{"type": "breadcrumb", "category": "navigation", "message": "/home", "ts": t1.Format(time.RFC3339Nano)},
		{"type": "breadcrumb", "category": "navigation", "message": "/products", "ts": t1.Add(1 * time.Minute).Format(time.RFC3339Nano)},
		{"type": "event", "name": "add_to_cart", "ts": t1.Add(2 * time.Minute).Format(time.RFC3339Nano)},
		{"type": "event", "name": "checkout_success", "ts": t1.Add(3 * time.Minute).Format(time.RFC3339Nano)},
	}, t1)

	// Ingest session 2: /home -> /products -> Exit
	t2 := now.Add(-1 * time.Hour)
	ingestSession("sess-path-2", []map[string]any{
		{"type": "breadcrumb", "category": "navigation", "message": "/home", "ts": t2.Format(time.RFC3339Nano)},
		{"type": "breadcrumb", "category": "navigation", "message": "/products", "ts": t2.Add(1 * time.Minute).Format(time.RFC3339Nano)},
	}, t2)

	// Ingest session 3: /home -> /login -> error
	t3 := now.Add(-30 * time.Minute)
	ingestSession("sess-path-3", []map[string]any{
		{"type": "breadcrumb", "category": "navigation", "message": "/home", "ts": t3.Format(time.RFC3339Nano)},
		{"type": "breadcrumb", "category": "navigation", "message": "/login", "ts": t3.Add(1 * time.Minute).Format(time.RFC3339Nano)},
		{"type": "error", "name": "AuthException", "ts": t3.Add(2 * time.Minute).Format(time.RFC3339Nano)},
	}, t3)

	// Test 1: Forward user paths starting at route:/home
	var pathRes store.PathResult
	rr := get(t, app, fmt.Sprintf("/api/v1/projects/%d/paths?root_event=route:/home&direction=forward&step_limit=3", projID), &pathRes)
	if rr.Code != 200 {
		t.Fatalf("GET /paths: %d %s", rr.Code, rr.Body.String())
	}

	if len(pathRes.Nodes) == 0 || len(pathRes.Links) == 0 {
		t.Fatalf("expected non-empty nodes and links, got %+v", pathRes)
	}

	// Verify root node is 0:route:/home
	foundRoot := false
	for _, n := range pathRes.Nodes {
		if n.ID == "0:route:/home" && n.Count == 3 {
			foundRoot = true
			break
		}
	}
	if !foundRoot {
		t.Errorf("did not find root node 0:route:/home with count 3: %+v", pathRes.Nodes)
	}

	// Test 2: Get sessions for link 0:route:/home -> 1:route:/products
	var sessMap struct {
		SessionIDs []string `json:"session_ids"`
	}
	rrSess := get(t, app, fmt.Sprintf("/api/v1/projects/%d/paths/sessions?source=0:route:/home&target=1:route:/products", projID), &sessMap)
	if rrSess.Code != 200 {
		t.Fatalf("GET /paths/sessions: %d %s", rrSess.Code, rrSess.Body.String())
	}
	if len(sessMap.SessionIDs) != 2 {
		t.Errorf("expected 2 sessions for /home -> /products, got %v", sessMap.SessionIDs)
	}
}
