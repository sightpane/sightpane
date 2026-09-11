// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"sightpane/internal/store"
)

func TestFunnelEndpoints(t *testing.T) {
	app, _ := newTestServer(t)
	projID := int64(1)

	// 1. Create a funnel
	createBody := map[string]any{
		"name":        "Onboarding Flow",
		"description": "User sign-up to activation",
		"steps": []store.FunnelStep{
			{Name: "page_view"},
			{Name: "signup_submit"},
			{Name: "profile_completed"},
		},
		"conversion_window_seconds": 86400,
	}
	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/funnels", projID), "", createBody)
	if rr.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var created store.Funnel
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created funnel: %v", err)
	}
	if created.ID == 0 || created.Name != "Onboarding Flow" || len(created.Steps) != 3 {
		t.Fatalf("unexpected created funnel: %+v", created)
	}

	// 2. List funnels
	var list []*store.Funnel
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/funnels", projID), &list)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("expected 1 funnel, got %d", len(list))
	}

	// 3. Ingest events:
	// Session 1 converts all 3 steps
	// Session 2 drops off at step 1 (does page_view, signup_submit, but not profile_completed)
	// Session 3 drops off at step 0 (does page_view only)
	now := time.Now().UTC()
	ingestEvents := func(sessionID string, events []string) {
		var items []map[string]any
		for i, evName := range events {
			items = append(items, map[string]any{
				"type": "event",
				"name": evName,
				"ts":   now.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano),
			})
		}
		rrEnv := post(t, app, "/api/v1/envelope", "key1", envelope(sessionID, items...))
		if rrEnv.Code != 202 {
			t.Fatalf("envelope failed: %d %s", rrEnv.Code, rrEnv.Body.String())
		}
	}

	ingestEvents("sess_full", []string{"page_view", "signup_submit", "profile_completed"})
	ingestEvents("sess_drop1", []string{"page_view", "signup_submit"})
	ingestEvents("sess_drop0", []string{"page_view"})

	// 4. Calculate results
	var res store.FunnelResult
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/funnels/%d/results?days=7", projID, created.ID), &res)
	if rr.Code != 200 {
		t.Fatalf("GET /funnels/:id/results: %d %s", rr.Code, rr.Body.String())
	}
	if len(res.Steps) != 3 {
		t.Fatalf("expected 3 step results, got %d", len(res.Steps))
	}
	// Step 0: 3 sessions
	if res.Steps[0].Count != 3 {
		t.Errorf("expected step 0 count 3, got %d", res.Steps[0].Count)
	}
	// Step 1: 2 sessions
	if res.Steps[1].Count != 2 {
		t.Errorf("expected step 1 count 2, got %d", res.Steps[1].Count)
	}
	// Step 2: 1 session
	if res.Steps[2].Count != 1 {
		t.Errorf("expected step 2 count 1, got %d", res.Steps[2].Count)
	}

	// 5. Get drop-offs at step 1 (should be sess_drop1)
	var dropoffs struct {
		Step       int      `json:"step"`
		SessionIDs []string `json:"session_ids"`
	}
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/funnels/%d/dropoffs?step=1&days=7", projID, created.ID), &dropoffs)
	if rr.Code != 200 {
		t.Fatalf("GET /dropoffs: %d %s", rr.Code, rr.Body.String())
	}
	if len(dropoffs.SessionIDs) != 1 || dropoffs.SessionIDs[0] != "sess_drop1" {
		t.Errorf("expected [sess_drop1], got %v", dropoffs.SessionIDs)
	}

	// 6. Delete funnel
	delReq := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/projects/%d/funnels/%d", projID, created.ID), nil)
	delReq.Header.Set("Authorization", "Bearer "+userTok)
	delResp := send(t, app, delReq)
	if delResp.Code != 204 {
		t.Fatalf("DELETE /funnels: %d %s", delResp.Code, delResp.Body.String())
	}

	// Verify deleted
	getReq := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/projects/%d/funnels/%d", projID, created.ID), nil)
	getReq.Header.Set("Authorization", "Bearer "+userTok)
	getResp := send(t, app, getReq)
	if getResp.Code != 404 {
		t.Errorf("expected 404 after delete, got %d", getResp.Code)
	}
}
