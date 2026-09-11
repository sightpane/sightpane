// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"sightpane/internal/store"
)

func TestDashboardAndInsightEndpoints(t *testing.T) {
	app, _ := newTestServer(t)
	projID := int64(1)

	// 1. Create Dashboard
	dashReq := map[string]any{
		"name":        "Executive Overview",
		"description": "High-level metrics and KPIs",
		"is_default":   true,
		"layout": []map[string]any{
			{"insight_id": "ins-1", "col": 0, "row": 0, "w": 6, "h": 4},
		},
	}
	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/dashboards", projID), "", dashReq)
	if rr.Code != 201 {
		t.Fatalf("POST /dashboards failed: %d %s", rr.Code, rr.Body.String())
	}
	var createdDash store.Dashboard
	if err := json.Unmarshal(rr.Body.Bytes(), &createdDash); err != nil {
		t.Fatalf("decode created dashboard: %v", err)
	}
	if createdDash.ID == "" || createdDash.Name != "Executive Overview" || !createdDash.IsDefault || len(createdDash.Layout) != 1 {
		t.Fatalf("unexpected dashboard: %+v", createdDash)
	}

	// 2. List Dashboards
	var dashList []store.Dashboard
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/dashboards", projID), &dashList)
	if rr.Code != 200 || len(dashList) != 1 {
		t.Fatalf("GET /dashboards failed: %d (count: %d)", rr.Code, len(dashList))
	}

	// 3. Update Dashboard
	updateReq := map[string]any{
		"name":        "Executive Overview V2",
		"description": "Updated description",
		"is_default":   false,
		"layout": []map[string]any{
			{"insight_id": "ins-1", "col": 0, "row": 0, "w": 12, "h": 6},
		},
	}
	rr = do(t, app, "PUT", fmt.Sprintf("/api/v1/projects/%d/dashboards/%s", projID, createdDash.ID), "", updateReq)
	if rr.Code != 200 {
		t.Fatalf("PUT /dashboards failed: %d %s", rr.Code, rr.Body.String())
	}

	// 4. Set Default Dashboard
	rr = do(t, app, "POST", fmt.Sprintf("/api/v1/projects/%d/dashboards/%s/default", projID, createdDash.ID), "", nil)
	if rr.Code != 200 {
		t.Fatalf("POST /dashboards/:id/default failed: %d %s", rr.Code, rr.Body.String())
	}

	// 5. Create Insight
	insightReq := map[string]any{
		"dashboard_id": createdDash.ID,
		"name":         "Weekly Purchase Volume",
		"chart_type":   "bar",
		"query": map[string]any{
			"date_range": "14d",
			"interval":   "day",
			"events": []map[string]any{
				{"name": "purchase", "math": "count"},
			},
			"breakdown": "platform",
		},
	}
	rr = post(t, app, fmt.Sprintf("/api/v1/projects/%d/insights", projID), "", insightReq)
	if rr.Code != 201 {
		t.Fatalf("POST /insights failed: %d %s", rr.Code, rr.Body.String())
	}
	var createdInsight store.Insight
	if err := json.Unmarshal(rr.Body.Bytes(), &createdInsight); err != nil {
		t.Fatalf("decode created insight: %v", err)
	}
	if createdInsight.ID == "" || createdInsight.Name != "Weekly Purchase Volume" || createdInsight.ChartType != "bar" {
		t.Fatalf("unexpected insight: %+v", createdInsight)
	}

	// 6. List Insights (with and without dashboard filter)
	var insightList []store.Insight
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/insights?dashboard_id=%s", projID, createdDash.ID), &insightList)
	if rr.Code != 200 || len(insightList) != 1 {
		t.Fatalf("GET /insights failed: %d count: %d", rr.Code, len(insightList))
	}

	// 7. Ingest Events for analytical query testing
	now := time.Now().UTC()
	ingestEvents := func(sessionID string, events []map[string]any) {
		var items []map[string]any
		for _, e := range events {
			items = append(items, e)
		}
		env := envelope(sessionID, items...)
		rrEnv := post(t, app, "/api/v1/envelope", "key1", env)
		if rrEnv.Code != 202 && rrEnv.Code != 200 {
			t.Fatalf("envelope ingest failed: %d %s", rrEnv.Code, rrEnv.Body.String())
		}
	}

	ingestEvents("sess_web_1", []map[string]any{
		{
			"type": "event",
			"name": "purchase",
			"ts":   now.Add(-2 * time.Hour).Format(time.RFC3339Nano),
			"body": map[string]any{"amount": 120.50},
		},
		{
			"type": "event",
			"name": "purchase",
			"ts":   now.Add(-1 * time.Hour).Format(time.RFC3339Nano),
			"body": map[string]any{"amount": 80.00},
		},
	})

	// 8. Execute Dynamic Analytical Query (POST /insights/query)
	qReq := store.InsightQuery{
		DateRange: "7d",
		Interval:  "day",
		Events: []store.InsightEvent{
			{Name: "purchase", Math: "count"},
		},
	}
	rr = post(t, app, fmt.Sprintf("/api/v1/projects/%d/insights/query", projID), "", qReq)
	if rr.Code != 200 {
		t.Fatalf("POST /insights/query failed: %d %s", rr.Code, rr.Body.String())
	}
	var qRes store.InsightQueryResult
	if err := json.Unmarshal(rr.Body.Bytes(), &qRes); err != nil {
		t.Fatalf("decode query result: %v", err)
	}
	if len(qRes.Series) == 0 {
		t.Fatalf("expected at least 1 series, got 0")
	}
	if qRes.Series[0].AggregatedValue < 2 {
		t.Errorf("expected aggregated count >= 2, got %f", qRes.Series[0].AggregatedValue)
	}

	// 9. Repeat Query to verify 60s in-memory caching
	rrCache := post(t, app, fmt.Sprintf("/api/v1/projects/%d/insights/query", projID), "", qReq)
	if rrCache.Code != 200 {
		t.Fatalf("repeated POST /insights/query failed: %d", rrCache.Code)
	}
	var qResCache store.InsightQueryResult
	if err := json.Unmarshal(rrCache.Body.Bytes(), &qResCache); err != nil {
		t.Fatalf("decode cached query result: %v", err)
	}
	if !qResCache.Cached {
		t.Errorf("expected cached = true on second immediate execution")
	}

	// 10. Execute Stored Insight Query (GET /insights/:id/results)
	var storedRes store.InsightQueryResult
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/insights/%s/results", projID, createdInsight.ID), &storedRes)
	if rr.Code != 200 {
		t.Fatalf("GET /insights/:id/results failed: %d %s", rr.Code, rr.Body.String())
	}

	// 11. Delete Insight & Delete Dashboard
	rr = do(t, app, "DELETE", fmt.Sprintf("/api/v1/projects/%d/insights/%s", projID, createdInsight.ID), "", nil)
	if rr.Code != http.StatusNoContent {
		t.Errorf("DELETE insight expected 204, got %d", rr.Code)
	}

	rr = do(t, app, "DELETE", fmt.Sprintf("/api/v1/projects/%d/dashboards/%s", projID, createdDash.ID), "", nil)
	if rr.Code != http.StatusNoContent {
		t.Errorf("DELETE dashboard expected 204, got %d", rr.Code)
	}
}
