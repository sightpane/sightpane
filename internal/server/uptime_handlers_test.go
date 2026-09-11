// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sightpane/internal/store"
	"sightpane/internal/uptime"

	"github.com/gofiber/fiber/v3"
)

func del(t *testing.T, app *fiber.App, path, key string) resp {
	return do(t, app, "DELETE", path, key, nil)
}

func TestUptimeEndpoints(t *testing.T) {
	app, st := newTestServer(t)
	projID := int64(1)

	// Local test HTTP target
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("healthy"))
	}))
	defer targetServer.Close()

	// 1. Create with invalid URL (should fail)
	invalidBody := map[string]any{
		"name": "Invalid Target",
		"url":  "ftp://invalid-scheme.com",
	}
	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/uptime", projID), "", invalidBody)
	if rr.Code != 400 {
		t.Fatalf("expected 400 for invalid url, got %d", rr.Code)
	}

	// 2. Create valid monitor
	createBody := map[string]any{
		"name":                 "API Health Check",
		"url":                  targetServer.URL,
		"method":               "GET",
		"expected_status_code": 200,
		"interval_seconds":     60,
		"timeout_seconds":      5,
		"ssl_check_enabled":    false,
	}
	rr = post(t, app, fmt.Sprintf("/api/v1/projects/%d/uptime", projID), "", createBody)
	if rr.Code != 201 {
		t.Fatalf("POST /uptime: %d %s", rr.Code, rr.Body.String())
	}
	var created store.UptimeMonitor
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal monitor: %v", err)
	}
	if created.Name != "API Health Check" || created.URL != targetServer.URL {
		t.Fatalf("unexpected created monitor: %+v", created)
	}

	// 3. List monitors & stats
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/uptime", projID), nil)
	if rr.Code != 200 {
		t.Fatalf("GET /uptime: %d %s", rr.Code, rr.Body.String())
	}
	var listResp struct {
		Monitors []*store.UptimeMonitor `json:"monitors"`
		Stats    *store.UptimeStats     `json:"stats"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(listResp.Monitors) != 1 || listResp.Stats.TotalMonitors != 1 {
		t.Fatalf("unexpected list response: %+v", listResp)
	}

	// 4. Trigger manual check
	rr = post(t, app, fmt.Sprintf("/api/v1/projects/%d/uptime/%d/check", projID, created.ID), "", nil)
	if rr.Code != 200 {
		t.Fatalf("POST /uptime/:id/check: %d %s", rr.Code, rr.Body.String())
	}
	var checkResult uptime.CheckResult
	if err := json.Unmarshal(rr.Body.Bytes(), &checkResult); err != nil {
		t.Fatalf("unmarshal check result: %v", err)
	}
	if !checkResult.IsUp || checkResult.StatusCode == nil || *checkResult.StatusCode != 200 {
		t.Fatalf("unexpected check result: %+v", checkResult)
	}

	// 5. Get monitor history detail
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/uptime/%d?days=7", projID, created.ID), nil)
	if rr.Code != 200 {
		t.Fatalf("GET /uptime/:id: %d %s", rr.Code, rr.Body.String())
	}
	var historyResp store.UptimeHistoryDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &historyResp); err != nil {
		t.Fatalf("unmarshal history response: %v", err)
	}
	if historyResp.Monitor == nil || len(historyResp.History90d) != 7 || len(historyResp.RecentChecks) == 0 {
		t.Fatalf("unexpected history response: %+v", historyResp)
	}

	// 6. Update monitor
	newExpectedStatus := 204
	updateBody := map[string]any{
		"expected_status_code": newExpectedStatus,
		"interval_seconds":     300,
	}
	rr = put(t, app, fmt.Sprintf("/api/v1/projects/%d/uptime/%d", projID, created.ID), "", updateBody)
	if rr.Code != 200 {
		t.Fatalf("PUT /uptime/:id: %d %s", rr.Code, rr.Body.String())
	}
	var updated store.UptimeMonitor
	if err := json.Unmarshal(rr.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated monitor: %v", err)
	}
	if updated.ExpectedStatusCode != 204 || updated.IntervalSeconds != 300 {
		t.Fatalf("unexpected updated monitor: %+v", updated)
	}

	// 7. Test Runner RunOnce
	checker := uptime.NewChecker(5)
	runner := uptime.NewRunner(st, checker, nil)
	runner.SetTickInterval(10 * time.Millisecond)
	n, err := runner.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	t.Logf("runner checked %d monitors", n)

	// 8. Delete monitor
	rr = del(t, app, fmt.Sprintf("/api/v1/projects/%d/uptime/%d", projID, created.ID), "")
	if rr.Code != 200 {
		t.Fatalf("DELETE /uptime/:id: %d %s", rr.Code, rr.Body.String())
	}

	// Verify deleted
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/uptime/%d", projID, created.ID), nil)
	if rr.Code != 404 {
		t.Fatalf("expected 404 after delete, got %d", rr.Code)
	}
}
