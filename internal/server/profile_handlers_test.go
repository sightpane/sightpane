// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"sightpane/internal/store"
)

func TestProfileEndpointsAndFlameChartData(t *testing.T) {
	app, _ := newTestServer(t)
	projID := int64(1)

	// 1. Create a profile directly
	profileData := map[string]any{
		"shared": map[string]any{
			"frames": []map[string]any{
				{"name": "root", "file": "main.dart", "line": 10},
				{"name": "loadFeed", "file": "feed.dart", "line": 45},
				{"name": "parseJSON", "file": "json_parser.dart", "line": 120},
				{"name": "regexMatch", "file": "regex.dart", "line": 80},
			},
		},
		"samples": []map[string]any{
			{"elapsed_ms": 10.0, "stack_id": []int{0, 1}},
			{"elapsed_ms": 20.0, "stack_id": []int{0, 1, 2}},
			{"elapsed_ms": 30.0, "stack_id": []int{0, 1, 2}},
			{"elapsed_ms": 40.0, "stack_id": []int{0, 1, 3}},
			{"elapsed_ms": 50.0, "stack_id": []int{0, 1, 3}},
			{"elapsed_ms": 60.0, "stack_id": []int{0, 1, 3}},
		},
	}
	rawProfileData, _ := json.Marshal(profileData)

	payload := map[string]any{
		"transaction_name": "route:/feed",
		"duration_ms":      650.0,
		"cpu_time_ms":       480.0,
		"thread_name":      "main",
		"platform":         "flutter",
		"trace_id":         "trace-prof-12345",
		"profile_data":     json.RawMessage(rawProfileData),
	}

	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/profiles", projID), "key1", payload)
	if rr.Code != 201 {
		t.Fatalf("POST /projects/%d/profiles failed: %d %s", projID, rr.Code, rr.Body.String())
	}

	var created store.ProfileRecord
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created profile: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("expected created profile ID, got empty")
	}

	// 2. List profiles
	var list []store.ProfileSummary
	get(t, app, fmt.Sprintf("/api/v1/projects/%d/profiles?transaction=route:/feed", projID), &list)
	if len(list) != 1 {
		t.Fatalf("expected 1 profile in list, got %d", len(list))
	}
	if list[0].TransactionName != "route:/feed" {
		t.Fatalf("expected transaction route:/feed, got %s", list[0].TransactionName)
	}
	if list[0].TraceID != "trace-prof-12345" {
		t.Fatalf("expected trace_id trace-prof-12345, got %s", list[0].TraceID)
	}

	// 3. Get profile detail
	var detail store.ProfileRecord
	get(t, app, fmt.Sprintf("/api/v1/projects/%d/profiles/%s", projID, created.ID), &detail)
	if detail.ID != created.ID {
		t.Fatalf("expected profile ID %s, got %s", created.ID, detail.ID)
	}
	if len(detail.ProfileData) == 0 {
		t.Fatalf("expected non-empty profile_data")
	}

	// 4. Get top slow functions
	var slowFuncs []store.SlowFunction
	get(t, app, fmt.Sprintf("/api/v1/projects/%d/profiles/functions/top?transaction=route:/feed", projID), &slowFuncs)
	if len(slowFuncs) == 0 {
		t.Fatalf("expected slow functions, got 0")
	}
	// "regexMatch" had 3 samples as top of stack -> highest self-time
	if slowFuncs[0].Name != "regexMatch" {
		t.Fatalf("expected top self-time function to be regexMatch, got %s", slowFuncs[0].Name)
	}
	if slowFuncs[0].SelfTimeMs != 30.0 {
		t.Fatalf("expected self-time 30ms, got %f", slowFuncs[0].SelfTimeMs)
	}

	// 5. Test Envelope ingestion with type: "profile"
	envPayload := map[string]any{
		"session": map[string]any{
			"id": "11111111-2222-3333-4444-555555555555",
			"device": map[string]any{
				"platform": "ios",
			},
		},
		"items": []map[string]any{
			{
				"type":             "profile",
				"ts":               time.Now().UTC().Format(time.RFC3339Nano),
				"transaction_name": "route:/checkout",
				"duration_ms":      320.0,
				"cpu_time_ms":       290.0,
				"thread_name":      "worker",
				"profile_data":     profileData,
			},
		},
	}
	rr = post(t, app, "/api/v1/envelope", "key1", envPayload)
	if rr.Code != 202 && rr.Code != 200 {
		t.Fatalf("expected 202/200 from envelope ingest, got %d: %s", rr.Code, rr.Body.String())
	}


	// Verify the envelope-ingested profile is in the list
	var envList []store.ProfileSummary
	get(t, app, fmt.Sprintf("/api/v1/projects/%d/profiles?transaction=route:/checkout", projID), &envList)
	if len(envList) != 1 {
		t.Fatalf("expected 1 profile for route:/checkout, got %d", len(envList))
	}
	if envList[0].Platform != "ios" {
		t.Fatalf("expected platform ios, got %s", envList[0].Platform)
	}
}
