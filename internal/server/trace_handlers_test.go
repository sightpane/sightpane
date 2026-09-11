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

func TestTraceEndpointsAndNPlusOneDetection(t *testing.T) {
	app, _ := newTestServer(t)
	projID := int64(1)

	now := time.Now().UTC()
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"

	// 1. Ingest batch of spans forming a distributed trace with N+1 queries
	spansPayload := map[string]any{
		"spans": []map[string]any{
			{
				"trace_id":     traceID,
				"span_id":      "span_root",
				"parent_span_id": "",
				"op":           "ui.action",
				"name":         "click:checkout",
				"service_name": "react-web",
				"start_time":   now.Format(time.RFC3339Nano),
				"duration_ms":  1420.5,
				"status":       "ok",
				"data":         map[string]any{"component": "CheckoutButton"},
			},
			{
				"trace_id":     traceID,
				"span_id":      "span_client_http",
				"parent_span_id": "span_root",
				"op":           "http.client",
				"name":         "POST /api/v1/orders",
				"service_name": "react-web",
				"start_time":   now.Add(15 * time.Millisecond).Format(time.RFC3339Nano),
				"duration_ms":  1395.0,
				"status":       "200",
				"data":         map[string]any{"http.method": "POST", "http.status_code": 200},
			},
			{
				"trace_id":     traceID,
				"span_id":      "span_srv_http",
				"parent_span_id": "span_client_http",
				"op":           "http.server",
				"name":         "POST /api/v1/orders",
				"service_name": "api-gateway",
				"start_time":   now.Add(35 * time.Millisecond).Format(time.RFC3339Nano),
				"duration_ms":  1350.0,
				"status":       "ok",
				"data":         map[string]any{"http.route": "/api/v1/orders"},
			},
			// 3 consecutive identical queries to trigger N+1 query detection
			{
				"trace_id":     traceID,
				"span_id":      "span_db_1",
				"parent_span_id": "span_srv_http",
				"op":           "db.query",
				"name":         "SELECT balance FROM wallets WHERE user_id = $1",
				"service_name": "billing-service",
				"start_time":   now.Add(50 * time.Millisecond).Format(time.RFC3339Nano),
				"duration_ms":  12.0,
				"status":       "ok",
				"data":         map[string]any{"db.system": "postgresql"},
			},
			{
				"trace_id":     traceID,
				"span_id":      "span_db_2",
				"parent_span_id": "span_srv_http",
				"op":           "db.query",
				"name":         "SELECT balance FROM wallets WHERE user_id = $1",
				"service_name": "billing-service",
				"start_time":   now.Add(65 * time.Millisecond).Format(time.RFC3339Nano),
				"duration_ms":  14.0,
				"status":       "ok",
				"data":         map[string]any{"db.system": "postgresql"},
			},
			{
				"trace_id":     traceID,
				"span_id":      "span_db_3",
				"parent_span_id": "span_srv_http",
				"op":           "db.query",
				"name":         "SELECT balance FROM wallets WHERE user_id = $1",
				"service_name": "billing-service",
				"start_time":   now.Add(82 * time.Millisecond).Format(time.RFC3339Nano),
				"duration_ms":  11.5,
				"status":       "ok",
				"data":         map[string]any{"db.system": "postgresql"},
			},
			{
				"trace_id":     traceID,
				"span_id":      "span_ext_stripe",
				"parent_span_id": "span_srv_http",
				"op":           "http.client",
				"name":         "POST https://api.stripe.com/v1/charges",
				"service_name": "billing-service",
				"start_time":   now.Add(100 * time.Millisecond).Format(time.RFC3339Nano),
				"duration_ms":  1280.0,
				"status":       "ok",
				"data":         map[string]any{"http.url": "https://api.stripe.com/v1/charges"},
			},
		},
	}

	// Ingest using project API key
	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/spans", projID), "key1", spansPayload)
	if rr.Code != 202 {
		t.Fatalf("POST /projects/%d/spans: %d %s", projID, rr.Code, rr.Body.String())
	}
	var ingestRes map[string]int
	if err := json.Unmarshal(rr.Body.Bytes(), &ingestRes); err != nil || ingestRes["accepted"] != 7 {
		t.Fatalf("unexpected ingest response: %v", ingestRes)
	}

	// 2. List traces with no filter
	var listRes []store.TraceSummary
	get(t, app, fmt.Sprintf("/api/v1/projects/%d/traces", projID), &listRes)
	if len(listRes) == 0 {
		t.Fatalf("expected at least 1 trace, got 0")
	}

	foundTrace := false
	for _, tr := range listRes {
		if tr.TraceID == traceID {
			foundTrace = true
			if tr.SpanCount != 7 {
				t.Errorf("expected 7 spans in trace summary, got %d", tr.SpanCount)
			}
			if tr.ServiceCount != 3 {
				t.Errorf("expected 3 services, got %d (%v)", tr.ServiceCount, tr.Services)
			}
			if tr.Status != "ok" {
				t.Errorf("expected status ok, got %s", tr.Status)
			}
		}
	}
	if !foundTrace {
		t.Fatalf("trace %s not found in list response", traceID)
	}

	// 3. Filter traces by service
	var reactTraces []store.TraceSummary
	get(t, app, fmt.Sprintf("/api/v1/projects/%d/traces?service=react-web", projID), &reactTraces)
	if len(reactTraces) == 0 {
		t.Fatalf("expected trace with service=react-web, got none")
	}

	var nonExistentServiceTraces []store.TraceSummary
	get(t, app, fmt.Sprintf("/api/v1/projects/%d/traces?service=nonexistent-service", projID), &nonExistentServiceTraces)
	if len(nonExistentServiceTraces) != 0 {
		t.Errorf("expected 0 traces for nonexistent service, got %d", len(nonExistentServiceTraces))
	}

	// 4. Retrieve full trace waterfall and verify N+1 detection
	var detail store.TraceDetail
	get(t, app, fmt.Sprintf("/api/v1/projects/%d/traces/%s", projID, traceID), &detail)

	if detail.TraceID != traceID {
		t.Fatalf("expected trace ID %s, got %s", traceID, detail.TraceID)
	}
	if detail.SpanCount != 7 {
		t.Fatalf("expected 7 spans, got %d", detail.SpanCount)
	}
	if detail.ServiceCount != 3 {
		t.Fatalf("expected 3 services, got %d", detail.ServiceCount)
	}

	// Verify tree depth
	spanDepthMap := make(map[string]int)
	spanNPlus1Map := make(map[string]bool)
	for _, s := range detail.Spans {
		spanDepthMap[s.SpanID] = s.Depth
		spanNPlus1Map[s.SpanID] = s.IsSuspectNPlus1
	}

	if spanDepthMap["span_root"] != 0 {
		t.Errorf("expected root span depth 0, got %d", spanDepthMap["span_root"])
	}
	if spanDepthMap["span_client_http"] != 1 {
		t.Errorf("expected client http span depth 1, got %d", spanDepthMap["span_client_http"])
	}
	if spanDepthMap["span_srv_http"] != 2 {
		t.Errorf("expected server http span depth 2, got %d", spanDepthMap["span_srv_http"])
	}
	if spanDepthMap["span_db_1"] != 3 {
		t.Errorf("expected db span depth 3, got %d", spanDepthMap["span_db_1"])
	}

	// Verify N+1 suspect detection
	if !spanNPlus1Map["span_db_1"] || !spanNPlus1Map["span_db_2"] || !spanNPlus1Map["span_db_3"] {
		t.Errorf("expected all 3 db spans to be flagged as suspect N+1: %v", spanNPlus1Map)
	}
	if len(detail.SuspectIssues) != 1 {
		t.Fatalf("expected 1 suspect issue, got %d", len(detail.SuspectIssues))
	}
	if detail.SuspectIssues[0].Type != "n_plus_one" {
		t.Errorf("expected suspect issue type 'n_plus_one', got %s", detail.SuspectIssues[0].Type)
	}
	if len(detail.SuspectIssues[0].SpanIDs) != 3 {
		t.Errorf("expected 3 span IDs in suspect issue, got %d", len(detail.SuspectIssues[0].SpanIDs))
	}

	// 5. 404 for unknown trace
	rr = get(t, app, fmt.Sprintf("/api/v1/projects/%d/traces/unknown_trace_9999", projID), nil)
	if rr.Code != 404 {
		t.Fatalf("expected 404 for unknown trace, got %d", rr.Code)
	}
}
