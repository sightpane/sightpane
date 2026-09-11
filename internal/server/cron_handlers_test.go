// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sightpane/internal/crons"
	"sightpane/internal/store"
)

func TestCronEndpoints(t *testing.T) {
	app, st := newTestServer(t)
	projID := int64(1)

	p, err := st.ProjectByID(projID)
	if err != nil {
		t.Fatalf("project by id: %v", err)
	}

	// 1. Create Cron Monitor with invalid schedule (should fail)
	invalidBody := map[string]any{
		"slug":     "backup-db",
		"name":     "Nightly DB Backup",
		"schedule": "not a valid schedule",
	}
	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/crons", projID), "", invalidBody)
	if rr.Code != 400 {
		t.Fatalf("expected 400 for invalid schedule, got %d", rr.Code)
	}

	// 2. Create Cron Monitor with valid schedule
	createBody := map[string]any{
		"slug":                 "backup-db",
		"name":                 "Nightly Database Backup",
		"schedule":             "0 2 * * *",
		"timezone":             "UTC",
		"grace_period_minutes": 15,
		"max_runtime_minutes":  60,
	}
	rr = post(t, app, fmt.Sprintf("/api/v1/projects/%d/crons", projID), "", createBody)
	if rr.Code != 201 {
		t.Fatalf("POST /crons: %d %s", rr.Code, rr.Body.String())
	}
	var created store.CronMonitor
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal monitor: %v", err)
	}
	if created.Slug != "backup-db" || created.NextExpectedAt == nil {
		t.Fatalf("unexpected monitor: %+v", created)
	}

	// 3. List Cron Monitors
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/crons", projID), nil)
	if rr.Code != 200 {
		t.Fatalf("GET /crons: %d %s", rr.Code, rr.Body.String())
	}
	var listResp struct {
		Monitors []*store.CronMonitor `json:"monitors"`
		Stats    *store.CronStats     `json:"stats"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(listResp.Monitors) != 1 || listResp.Stats.TotalMonitors != 1 {
		t.Fatalf("unexpected list response: %+v", listResp)
	}

	// 4. Get Monitor by ID
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/crons/%d", projID, created.ID), nil)
	if rr.Code != 200 {
		t.Fatalf("GET /crons/:id: %d", rr.Code)
	}

	// 5. Checkin via POST (in_progress)
	inProgBody := map[string]any{
		"status": "in_progress",
	}
	bodyBytes, _ := json.Marshal(inProgBody)
	req := httptest.NewRequest("POST", "/api/v1/crons/backup-db/checkin", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sightpane-Key", p.APIKey)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("POST checkin: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("POST checkin status: %d", resp.StatusCode)
	}

	// Verify monitor is in_progress
	m, err := st.GetCronMonitor(projID, created.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if m.Status != "in_progress" {
		t.Fatalf("expected in_progress, got %s", m.Status)
	}

	// 6. Checkin via GET (cURL ping with ?key=...&status=ok&duration_ms=1200)
	getReq := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/crons/backup-db/checkin?key=%s&status=ok&duration_ms=1200", p.APIKey), nil)
	resp, err = app.Test(getReq)
	if err != nil {
		t.Fatalf("GET checkin: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET checkin status: %d", resp.StatusCode)
	}

	// Verify monitor is ok and duration recorded
	m, err = st.GetCronMonitor(projID, created.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if m.Status != "ok" || m.LastCheckinAt == nil {
		t.Fatalf("expected ok with last checkin, got %+v", m)
	}

	// 7. Checkins history
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/crons/%d/checkins", projID, created.ID), nil)
	if rr.Code != 200 {
		t.Fatalf("GET /checkins: %d", rr.Code)
	}
	var checkinsResp struct {
		Checkins []*store.CronCheckin `json:"checkins"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &checkinsResp); err != nil {
		t.Fatalf("unmarshal checkins: %v", err)
	}
	if len(checkinsResp.Checkins) != 2 {
		t.Fatalf("expected 2 checkins, got %d", len(checkinsResp.Checkins))
	}

	// 8. Update Cron Monitor
	updateBody := map[string]any{
		"name":                 "Nightly DB Backup Updated",
		"schedule":             "@daily",
		"timezone":             "America/New_York",
		"grace_period_minutes": 20,
		"max_runtime_minutes":  90,
	}
	rr = put(t, app, fmt.Sprintf("/api/v1/projects/%d/crons/%d", projID, created.ID), "", updateBody)
	if rr.Code != 200 {
		t.Fatalf("PUT /crons/:id: %d %s", rr.Code, rr.Body.String())
	}
	m, err = st.GetCronMonitor(projID, created.ID)
	if err != nil {
		t.Fatalf("get updated monitor: %v", err)
	}
	if m.Name != "Nightly DB Backup Updated" || m.GracePeriodMinutes != 20 {
		t.Fatalf("unexpected updated monitor: %+v", m)
	}

	// 9. Test Evaluator Deadlines
	evaluator := crons.NewEvaluator(st, nil)

	// Simulate missed deadline: set next_expected_at 2 hours ago
	pastTime := time.Now().Add(-2 * time.Hour).UTC()
	_, err = st.DB().ExecContext(context.Background(), `
		UPDATE cron_monitors
		SET next_expected_at = $1, status = 'ok'
		WHERE id = $2
	`, pastTime, created.ID)
	if err != nil {
		t.Fatalf("update next_expected_at: %v", err)
	}

	alerted, err := evaluator.EvaluateOnce()
	if err != nil {
		t.Fatalf("evaluate once: %v", err)
	}
	if len(alerted) == 0 {
		t.Fatalf("expected at least 1 missed monitor alerted")
	}
	m, _ = st.GetCronMonitor(projID, created.ID)
	if m.Status != "missed" {
		t.Fatalf("expected status missed, got %s", m.Status)
	}

	// Simulate execution timeout: set status in_progress and last_checkin_at 3 hours ago
	timeoutTime := time.Now().Add(-3 * time.Hour).UTC()
	_, err = st.DB().ExecContext(context.Background(), `
		UPDATE cron_monitors
		SET last_checkin_at = $1, status = 'in_progress'
		WHERE id = $2
	`, timeoutTime, created.ID)
	if err != nil {
		t.Fatalf("update timeout: %v", err)
	}

	alerted, err = evaluator.EvaluateOnce()
	if err != nil {
		t.Fatalf("evaluate timeout: %v", err)
	}
	if len(alerted) == 0 {
		t.Fatalf("expected at least 1 timeout monitor alerted")
	}
	m, _ = st.GetCronMonitor(projID, created.ID)
	if m.Status != "error" {
		t.Fatalf("expected status error, got %s", m.Status)
	}

	// 10. Delete Cron Monitor
	deleteReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/projects/%d/crons/%d", projID, created.ID), nil)
	deleteReq.Header.Set("Authorization", "Bearer "+userTok)
	resp, err = app.Test(deleteReq)
	if err != nil {
		t.Fatalf("DELETE /crons: %v", err)
	}
	if resp.StatusCode != 204 {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	_, err = st.GetCronMonitor(projID, created.ID)
	if !errors.Is(err, store.ErrCronMonitorNotFound) {
		t.Fatalf("expected ErrCronMonitorNotFound, got %v", err)
	}
}
