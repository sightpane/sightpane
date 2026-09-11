// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"testing"

	"sightpane/internal/alert"
	"sightpane/internal/config"
	"sightpane/internal/store"
)

func TestMetricAlertEndpointsAndWorker(t *testing.T) {
	app, st := newTestServer(t)
	projID := int64(1)

	// 1. Create with invalid metric type (should fail 400)
	invalidBody := map[string]any{
		"name":        "Invalid Rule",
		"metric_type": "unknown_metric",
	}
	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/rules", projID), "", invalidBody)
	if rr.Code != 400 {
		t.Fatalf("expected 400 for invalid metric type, got %d", rr.Code)
	}

	// 2. Create valid error_count rule
	warnThresh := 5.0
	createBody := map[string]any{
		"name":                "High Error Count Alert",
		"metric_type":         "error_count",
		"comparison_operator": "gt",
		"critical_threshold":  10.0,
		"warning_threshold":   warnThresh,
		"window_minutes":      5,
		"channel_ids":         []int64{1},
		"is_active":           true,
	}
	rr = post(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/rules", projID), "", createBody)
	if rr.Code != 201 {
		t.Fatalf("POST metric-alerts/rules: %d %s", rr.Code, rr.Body.String())
	}

	var created store.MetricAlertRule
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created rule: %v", err)
	}
	if created.ID == 0 || created.Name != "High Error Count Alert" {
		t.Fatalf("unexpected created rule: %+v", created)
	}
	ruleID := created.ID

	// 3. Create spike multiplier rule
	spikeBody := map[string]any{
		"name":                "Sudden Error Spike",
		"metric_type":         "error_count",
		"comparison_operator": "spike_multiplier",
		"critical_threshold":  3.0,
		"window_minutes":      5,
		"is_active":           true,
	}
	rrSpike := post(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/rules", projID), "", spikeBody)
	if rrSpike.Code != 201 {
		t.Fatalf("POST spike rule: %d %s", rrSpike.Code, rrSpike.Body.String())
	}

	// 4. List rules
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/rules", projID), nil)
	if rr.Code != 200 {
		t.Fatalf("GET metric-alerts/rules: %d %s", rr.Code, rr.Body.String())
	}
	var rules []store.MetricAlertRule
	if err := json.Unmarshal(rr.Body.Bytes(), &rules); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(rules) < 2 {
		t.Fatalf("expected at least 2 rules, got %d", len(rules))
	}

	// 5. Get single rule
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/rules/%d", projID, ruleID), nil)
	if rr.Code != 200 {
		t.Fatalf("GET rule %d: %d %s", ruleID, rr.Code, rr.Body.String())
	}

	// 6. Update rule
	newName := "Updated High Error Count Alert"
	newCrit := 15.0
	updateBody := map[string]any{
		"name":               newName,
		"critical_threshold": newCrit,
	}
	rr = put(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/rules/%d", projID, ruleID), "", updateBody)
	if rr.Code != 200 {
		t.Fatalf("PUT rule: %d %s", rr.Code, rr.Body.String())
	}
	var updated store.MetricAlertRule
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.Name != newName || updated.CriticalThreshold != newCrit {
		t.Fatalf("updated rule mismatch: %+v", updated)
	}

	// 7. Preview points
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/rules/%d/preview?days=7", projID, ruleID), nil)
	if rr.Code != 200 {
		t.Fatalf("GET preview: %d %s", rr.Code, rr.Body.String())
	}

	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/preview?metric_type=error_count&days=3", projID), nil)
	if rr.Code != 200 {
		t.Fatalf("GET generic preview: %d %s", rr.Code, rr.Body.String())
	}

	// 8. Test evaluation endpoint
	rr = post(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/rules/%d/test", projID, ruleID), "", nil)
	if rr.Code != 200 {
		t.Fatalf("POST test: %d %s", rr.Code, rr.Body.String())
	}

	// 9. Worker evaluation and incident lifecycle test
	notifier := alert.NewNotifier(st, config.Config{PublicURL: "http://localhost:8790"})
	worker := alert.NewMetricWorker(st, notifier)

	// Ingest 20 errors using standard envelope endpoint
	for i := 0; i < 20; i++ {
		sessID := fmt.Sprintf("sess_alert_%d", i)
		post(t, app, "/api/v1/envelope", "key1", envelope(sessID, map[string]any{
			"type":      "error",
			"message":   fmt.Sprintf("DatabaseTimeout %d", i),
			"name":      "DatabaseTimeout",
			"unhandled": true,
		}))
	}

	// Run worker once -> should trigger firing for rule 1 (threshold was 15.0, count is 20)
	worker.RunOnce()

	// Check rule status is firing
	reloadedRule, err := st.GetMetricAlertRule(projID, ruleID)
	if err != nil {
		t.Fatalf("reload rule: %v", err)
	}
	if reloadedRule.CurrentStatus != "firing" {
		t.Fatalf("expected rule status 'firing', got %s", reloadedRule.CurrentStatus)
	}

	// Check incidents list endpoint
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/incidents", projID), nil)
	if rr.Code != 200 {
		t.Fatalf("GET incidents: %d %s", rr.Code, rr.Body.String())
	}
	var incidents []store.MetricAlertIncident
	_ = json.Unmarshal(rr.Body.Bytes(), &incidents)
	if len(incidents) == 0 {
		t.Fatalf("expected at least 1 incident, got 0")
	}
	if incidents[0].Status != "firing" {
		t.Fatalf("expected incident status 'firing', got %s", incidents[0].Status)
	}

	// Update threshold to 50 and warning to 30 -> next worker run should auto-resolve incident to ok
	highCrit := 50.0
	highWarn := 30.0
	reloadedRule.CriticalThreshold = highCrit
	reloadedRule.WarningThreshold = &highWarn
	_, _ = st.UpdateMetricAlertRule(reloadedRule)

	worker.RunOnce()

	reloadedRule, _ = st.GetMetricAlertRule(projID, ruleID)
	if reloadedRule.CurrentStatus != "ok" {
		t.Fatalf("expected rule status 'ok' after recovery, got %s", reloadedRule.CurrentStatus)
	}

	// Verify incident is resolved
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/incidents?rule_id=%d", projID, ruleID), nil)
	_ = json.Unmarshal(rr.Body.Bytes(), &incidents)
	if len(incidents) == 0 || incidents[0].Status != "resolved" {
		t.Fatalf("expected resolved incident, got %+v", incidents)
	}

	// 10. Delete rule
	rr = del(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/rules/%d", projID, ruleID), "")
	if rr.Code != 200 {
		t.Fatalf("DELETE rule: %d %s", rr.Code, rr.Body.String())
	}

	// Verify deleted
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/metric-alerts/rules/%d", projID, ruleID), nil)
	if rr.Code != 404 {
		t.Fatalf("expected 404 after delete, got %d", rr.Code)
	}
}
