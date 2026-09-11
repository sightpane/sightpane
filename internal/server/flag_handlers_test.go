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

	"github.com/gofiber/fiber/v3"
	"sightpane/internal/store"
)

func put(t *testing.T, app *fiber.App, path, key string, body any) resp {
	return do(t, app, "PUT", path, key, body)
}

func TestFeatureFlagsEndpoints(t *testing.T) {
	app, _ := newTestServer(t)
	projID := int64(1)

	// 1. Create a 100% rollout flag
	createBody := map[string]any{
		"key":                "new_checkout_flow",
		"name":               "New Checkout Flow",
		"description":        "Canary rollout for streamlined checkout",
		"enabled":            true,
		"rollout_percentage": 100,
	}
	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/feature-flags", projID), "", createBody)
	if rr.Code != 201 {
		t.Fatalf("POST /feature-flags: %d %s", rr.Code, rr.Body.String())
	}
	var f1 store.FeatureFlag
	if err := json.Unmarshal(rr.Body.Bytes(), &f1); err != nil {
		t.Fatalf("unmarshal flag: %v", err)
	}
	if f1.Key != "new_checkout_flow" || f1.RolloutPercentage != 100 {
		t.Fatalf("unexpected flag: %+v", f1)
	}

	// 2. Create a targeted flag for role: "admin"
	targetedBody := map[string]any{
		"key":                "admin_dashboard_v2",
		"name":               "Admin Dashboard v2",
		"enabled":            true,
		"rollout_percentage": 100,
		"filters": []store.FlagFilter{
			{Property: "role", Operator: "exact", Value: "admin"},
		},
	}
	rr2 := post(t, app, fmt.Sprintf("/api/v1/projects/%d/feature-flags", projID), "", targetedBody)
	if rr2.Code != 201 {
		t.Fatalf("POST targeted flag: %d %s", rr2.Code, rr2.Body.String())
	}

	// 3. Create a multivariate flag
	multiBody := map[string]any{
		"key":                "pricing_card_test",
		"name":               "Pricing Card A/B Test",
		"enabled":            true,
		"rollout_percentage": 100,
		"variants": []store.FlagVariant{
			{Key: "control", Rollout: 50},
			{Key: "treatment_yearly", Rollout: 50},
		},
	}
	rr3 := post(t, app, fmt.Sprintf("/api/v1/projects/%d/feature-flags", projID), "", multiBody)
	if rr3.Code != 201 {
		t.Fatalf("POST multi flag: %d %s", rr3.Code, rr3.Body.String())
	}
	var f3 store.FeatureFlag
	_ = json.Unmarshal(rr3.Body.Bytes(), &f3)

	// 4. List feature flags
	var flags []*store.FeatureFlag
	rrList := get(t, app, fmt.Sprintf("/api/v1/projects/%d/feature-flags", projID), &flags)
	if rrList.Code != 200 {
		t.Fatalf("GET /feature-flags: %d %s", rrList.Code, rrList.Body.String())
	}
	if len(flags) != 3 {
		t.Fatalf("expected 3 flags, got %d", len(flags))
	}

	// 5. Test single flag evaluation endpoint
	testReq := map[string]any{
		"distinct_id": "user_123",
		"properties": map[string]any{
			"role": "admin",
		},
	}
	rrTest := post(t, app, fmt.Sprintf("/api/v1/projects/%d/feature-flags/%d/test", projID, f1.ID), "", testReq)
	if rrTest.Code != 200 {
		t.Fatalf("POST /test: %d %s", rrTest.Code, rrTest.Body.String())
	}
	var testResp map[string]any
	_ = json.Unmarshal(rrTest.Body.Bytes(), &testResp)
	if testResp["active"] != true {
		t.Fatalf("expected flag active: %+v", testResp)
	}

	// 6. SDK Evaluation Endpoint: POST /api/v1/flags/evaluate with X-Sightpane-Key: key1
	evalUser1 := map[string]any{
		"distinct_id": "usr_regular_42",
		"properties": map[string]any{
			"role": "member",
		},
	}
	rrEval1 := post(t, app, "/api/v1/flags/evaluate", "key1", evalUser1)
	if rrEval1.Code != 200 {
		t.Fatalf("POST /flags/evaluate: %d %s", rrEval1.Code, rrEval1.Body.String())
	}
	var evalResult1 struct {
		Flags map[string]any `json:"flags"`
	}
	if err := json.Unmarshal(rrEval1.Body.Bytes(), &evalResult1); err != nil {
		t.Fatalf("unmarshal eval result: %v", err)
	}

	// new_checkout_flow should be true (100% rollout)
	if evalResult1.Flags["new_checkout_flow"] != true {
		t.Errorf("expected new_checkout_flow=true, got %v", evalResult1.Flags["new_checkout_flow"])
	}
	// admin_dashboard_v2 should be false for role=member
	if evalResult1.Flags["admin_dashboard_v2"] != false {
		t.Errorf("expected admin_dashboard_v2=false, got %v", evalResult1.Flags["admin_dashboard_v2"])
	}
	// pricing_card_test should be one of "control" or "treatment_yearly"
	v1 := evalResult1.Flags["pricing_card_test"]
	if v1 != "control" && v1 != "treatment_yearly" {
		t.Errorf("expected variant string, got %v", v1)
	}

	// Deterministic test: Evaluate usr_regular_42 again, variant MUST be identical!
	rrEval1Repeat := post(t, app, "/api/v1/flags/evaluate", "key1", evalUser1)
	var repeatResult struct {
		Flags map[string]any `json:"flags"`
	}
	_ = json.Unmarshal(rrEval1Repeat.Body.Bytes(), &repeatResult)
	if repeatResult.Flags["pricing_card_test"] != v1 {
		t.Errorf("non-deterministic variant: was %v, now %v", v1, repeatResult.Flags["pricing_card_test"])
	}

	// Now evaluate user with role="admin"
	evalAdmin := map[string]any{
		"distinct_id": "usr_admin_99",
		"properties": map[string]any{
			"role": "admin",
		},
	}
	rrEvalAdmin := post(t, app, "/api/v1/flags/evaluate", "key1", evalAdmin)
	var evalAdminResult struct {
		Flags map[string]any `json:"flags"`
	}
	_ = json.Unmarshal(rrEvalAdmin.Body.Bytes(), &evalAdminResult)
	if evalAdminResult.Flags["admin_dashboard_v2"] != true {
		t.Errorf("expected admin_dashboard_v2=true for admin, got %v", evalAdminResult.Flags["admin_dashboard_v2"])
	}

	// 7. Update feature flag (disable f1)
	disabled := false
	rrUpdate := put(t, app, fmt.Sprintf("/api/v1/projects/%d/feature-flags/%d", projID, f1.ID), "", map[string]any{
		"name":               "New Checkout Flow (Paused)",
		"description":        "Temporarily disabled",
		"enabled":            &disabled,
		"rollout_percentage": 0,
	})
	if rrUpdate.Code != 200 {
		t.Fatalf("PUT /feature-flags: %d %s", rrUpdate.Code, rrUpdate.Body.String())
	}

	// After disabling, evaluation should return false
	rrEvalAfterPause := post(t, app, "/api/v1/flags/evaluate", "key1", evalUser1)
	var pauseResult struct {
		Flags map[string]any `json:"flags"`
	}
	_ = json.Unmarshal(rrEvalAfterPause.Body.Bytes(), &pauseResult)
	if pauseResult.Flags["new_checkout_flow"] != false {
		t.Errorf("expected new_checkout_flow=false when paused, got %v", pauseResult.Flags["new_checkout_flow"])
	}

	// 8. Delete flag
	reqDel := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/projects/%d/feature-flags/%d", projID, f1.ID), nil)
	if userTok != "" {
		reqDel.Header.Set("Authorization", "Bearer "+userTok)
	}
	rrDel := send(t, app, reqDel)
	if rrDel.Code != 200 {
		t.Fatalf("DELETE /feature-flags: %d %s", rrDel.Code, rrDel.Body.String())
	}

	// Check 404 after delete
	rrGet404 := get(t, app, fmt.Sprintf("/api/v1/projects/%d/feature-flags/%d", projID, f1.ID), nil)
	if rrGet404.Code != 404 {
		t.Fatalf("expected 404 after delete, got %d", rrGet404.Code)
	}
}
