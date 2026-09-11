// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"testing"

	"sightpane/internal/store"
)

func TestExperimentsEndpoints(t *testing.T) {
	app, st := newTestServer(t)
	projID := int64(1)

	// 1. Create a feature flag to link the experiment to
	flagBody := map[string]any{
		"key":                "cta_button_color",
		"name":               "CTA Button Color",
		"enabled":            true,
		"rollout_percentage": 100,
		"variants": []store.FlagVariant{
			{Key: "blue", Rollout: 50},
			{Key: "green", Rollout: 50},
		},
	}
	rrFlag := post(t, app, fmt.Sprintf("/api/v1/projects/%d/feature-flags", projID), "", flagBody)
	if rrFlag.Code != 201 {
		t.Fatalf("POST feature flag: %d %s", rrFlag.Code, rrFlag.Body.String())
	}

	// 2. Create Experiment
	expBody := map[string]any{
		"name":                 "Sign-up Button Color Test",
		"description":          "Testing blue vs green CTA button on signup page",
		"feature_flag_key":     "cta_button_color",
		"primary_metric_event": "signup_completed",
		"variants": []store.ExperimentVariant{
			{Key: "blue", Name: "Control (Blue)"},
			{Key: "green", Name: "Treatment (Green)"},
		},
		"minimum_sample_size": 100,
	}
	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/experiments", projID), "", expBody)
	if rr.Code != 201 {
		t.Fatalf("POST /experiments: %d %s", rr.Code, rr.Body.String())
	}
	var created store.Experiment
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal experiment: %v", err)
	}
	if created.Name != "Sign-up Button Color Test" || created.FeatureFlagKey != "cta_button_color" {
		t.Fatalf("unexpected experiment: %+v", created)
	}
	if len(created.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(created.Variants))
	}

	// 3. List experiments
	var list []*store.Experiment
	rrList := get(t, app, fmt.Sprintf("/api/v1/projects/%d/experiments", projID), &list)
	if rrList.Code != 200 {
		t.Fatalf("GET /experiments: %d %s", rrList.Code, rrList.Body.String())
	}
	if len(list) == 0 {
		t.Fatalf("expected at least 1 experiment in list")
	}

	// 4. Get by ID
	var fetched store.Experiment
	rrGet := get(t, app, fmt.Sprintf("/api/v1/projects/%d/experiments/%d", projID, created.ID), &fetched)
	if rrGet.Code != 200 {
		t.Fatalf("GET /experiments/:id: %d %s", rrGet.Code, rrGet.Body.String())
	}
	if fetched.ID != created.ID {
		t.Fatalf("expected ID %d, got %d", created.ID, fetched.ID)
	}

	// 5. Update status to running
	updateBody := map[string]any{
		"name":                "Sign-up Button Color Test (Updated)",
		"description":         "Running experiment",
		"status":              "running",
		"minimum_sample_size": 200,
	}
	rrUpdate := put(t, app, fmt.Sprintf("/api/v1/projects/%d/experiments/%d", projID, created.ID), "", updateBody)
	if rrUpdate.Code != 200 {
		t.Fatalf("PUT /experiments/:id: %d %s", rrUpdate.Code, rrUpdate.Body.String())
	}
	var updated store.Experiment
	if err := json.Unmarshal(rrUpdate.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated: %v", err)
	}
	if updated.Status != "running" || updated.MinimumSampleSize != 200 {
		t.Fatalf("unexpected updated experiment: %+v", updated)
	}

	// 6. Calculate results endpoint (even with empty data, should return valid calculations)
	var results store.ExperimentResults
	rrRes := get(t, app, fmt.Sprintf("/api/v1/projects/%d/experiments/%d/results?days=30", projID, created.ID), &results)
	if rrRes.Code != 200 {
		t.Fatalf("GET /experiments/:id/results: %d %s", rrRes.Code, rrRes.Body.String())
	}
	if results.ExperimentID != created.ID {
		t.Fatalf("expected experiment_id %d, got %d", created.ID, results.ExperimentID)
	}
	if len(results.Variants) != 2 {
		t.Fatalf("expected 2 variant results, got %d", len(results.Variants))
	}

	// 7. Conclude winner
	winnerBody := map[string]any{
		"winner_variant": "green",
	}
	rrWinner := post(t, app, fmt.Sprintf("/api/v1/projects/%d/experiments/%d/winner", projID, created.ID), "", winnerBody)
	if rrWinner.Code != 200 {
		t.Fatalf("POST /winner: %d %s", rrWinner.Code, rrWinner.Body.String())
	}
	var concluded store.Experiment
	if err := json.Unmarshal(rrWinner.Body.Bytes(), &concluded); err != nil {
		t.Fatalf("unmarshal concluded: %v", err)
	}
	if concluded.Status != "concluded" || concluded.WinnerVariant == nil || *concluded.WinnerVariant != "green" {
		t.Fatalf("unexpected concluded: %+v", concluded)
	}

	// Check that the linked feature flag had its rollout updated to 100% winner variant
	flagAfter, err := st.GetFeatureFlag(projID, "cta_button_color")
	if err != nil {
		t.Fatalf("get feature flag: %v", err)
	}
	for _, v := range flagAfter.Variants {
		if v.Key == "green" && v.Rollout != 100 {
			t.Fatalf("expected green variant to have 100%% rollout, got %d", v.Rollout)
		}
		if v.Key == "blue" && v.Rollout != 0 {
			t.Fatalf("expected blue variant to have 0%% rollout, got %d", v.Rollout)
		}
	}

	// 8. Delete experiment
	rrDel := do(t, app, "DELETE", fmt.Sprintf("/api/v1/projects/%d/experiments/%d", projID, created.ID), "", nil)
	if rrDel.Code != 200 {
		t.Fatalf("DELETE /experiments/:id: %d %s", rrDel.Code, rrDel.Body.String())
	}
}
