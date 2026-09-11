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

func TestCohortAndRetentionEndpoints(t *testing.T) {
	app, _ := newTestServer(t)
	projID := int64(1)

	// 1. Create a dynamic cohort: Power Users (>= 2 checkout events in 30 days)
	createBody := map[string]any{
		"name":        "Power Users",
		"description": "Users with multiple checkouts",
		"is_static":   false,
		"rules": []store.CohortRule{
			{Event: "checkout", Operator: "gte", Count: 2, WindowDays: 30},
		},
	}
	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/cohorts", projID), "", createBody)
	if rr.Code != 201 {
		t.Fatalf("POST /cohorts: %d %s", rr.Code, rr.Body.String())
	}
	var cohort store.Cohort
	if err := json.Unmarshal(rr.Body.Bytes(), &cohort); err != nil {
		t.Fatalf("decode cohort: %v", err)
	}
	if cohort.ID == 0 || cohort.Name != "Power Users" {
		t.Fatalf("unexpected cohort: %+v", cohort)
	}

	// 2. Ingest sessions and events:
	// User "alice" has 2 checkouts (should qualify for Power Users)
	// User "bob" has 1 checkout (should not qualify)
	now := time.Now().UTC()
	ingestUserSession := func(sessionID, userID string, eventNames []string, sessionTime time.Time) {
		var items []map[string]any
		for i, ev := range eventNames {
			items = append(items, map[string]any{
				"type": "event",
				"name": ev,
				"ts":   sessionTime.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano),
			})
		}
		env := map[string]any{
			"sdk": map[string]any{"name": "sightpane", "version": "0.1.0"},
			"session": map[string]any{
				"id":         sessionID,
				"started_at": sessionTime.Format(time.RFC3339Nano),
				"user":       map[string]any{"id": userID, "email": userID + "@test.io"},
			},
			"items": items,
		}
		rrEnv := post(t, app, "/api/v1/envelope", "key1", env)
		if rrEnv.Code != 202 {
			t.Fatalf("envelope failed: %d %s", rrEnv.Code, rrEnv.Body.String())
		}
	}

	// Day 0: Alice starts, Bob starts
	d0 := now.AddDate(0, 0, -3)
	ingestUserSession("s_alice_1", "alice", []string{"page_view", "checkout"}, d0)
	ingestUserSession("s_bob_1", "bob", []string{"page_view", "checkout"}, d0)

	// Day 1: Alice returns and does checkout again (now has 2 checkouts)
	d1 := d0.Add(24 * time.Hour)
	ingestUserSession("s_alice_2", "alice", []string{"page_view", "checkout"}, d1)

	// Day 2: Alice returns again (no checkout)
	d2 := d1.Add(24 * time.Hour)
	ingestUserSession("s_alice_3", "alice", []string{"page_view"}, d2)

	// 3. Refresh cohort
	rr = post(t, app, fmt.Sprintf("/api/v1/projects/%d/cohorts/%d/refresh", projID, cohort.ID), "", nil)
	if rr.Code != 200 {
		t.Fatalf("POST /refresh: %d %s", rr.Code, rr.Body.String())
	}
	var refreshed store.Cohort
	_ = json.Unmarshal(rr.Body.Bytes(), &refreshed)
	if refreshed.UserCount != 1 {
		t.Errorf("expected 1 power user (alice), got %d", refreshed.UserCount)
	}

	// 4. List cohort members
	var members []string
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/cohorts/%d/members", projID, cohort.ID), &members)
	if rr.Code != 200 {
		t.Fatalf("GET /members: %d %s", rr.Code, rr.Body.String())
	}
	if len(members) != 1 || members[0] != "alice" {
		t.Errorf("expected [alice], got %v", members)
	}

	// 5. Query Retention Matrix
	var retention store.RetentionResult
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/retention?period=day&days=7", projID), &retention)
	if rr.Code != 200 {
		t.Fatalf("GET /retention: %d %s", rr.Code, rr.Body.String())
	}
	if len(retention.Cohorts) == 0 {
		t.Fatalf("expected at least 1 retention cohort bucket, got 0")
	}

	// Check that the d0 cohort has 2 users (alice & bob)
	d0Key := d0.Format("2006-01-02")
	var foundD0Bucket *store.RetentionCohortBucket
	for i := range retention.Cohorts {
		if retention.Cohorts[i].Date == d0Key {
			foundD0Bucket = &retention.Cohorts[i]
			break
		}
	}
	if foundD0Bucket == nil {
		t.Fatalf("cohort bucket for %s not found in %+v", d0Key, retention.Cohorts)
	}
	if foundD0Bucket.TotalUsers != 2 {
		t.Errorf("expected 2 users in d0 bucket, got %d", foundD0Bucket.TotalUsers)
	}
	// Period 0: 2 users (100%)
	if len(foundD0Bucket.Activity) == 0 || foundD0Bucket.Activity[0].Count != 2 {
		t.Errorf("expected period 0 count 2, got %+v", foundD0Bucket.Activity)
	}
	// Period 1: 1 user (alice returned)
	if len(foundD0Bucket.Activity) < 2 || foundD0Bucket.Activity[1].Count != 1 {
		t.Errorf("expected period 1 count 1 (alice), got %+v", foundD0Bucket.Activity)
	}

	// 6. Delete cohort
	delReq := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/projects/%d/cohorts/%d", projID, cohort.ID), nil)
	delReq.Header.Set("Authorization", "Bearer "+userTok)
	delResp := send(t, app, delReq)
	if delResp.Code != 204 {
		t.Fatalf("DELETE /cohorts: %d %s", delResp.Code, delResp.Body.String())
	}
}
