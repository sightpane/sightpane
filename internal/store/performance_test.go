// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"sightpane/internal/testdb"
)

func TestPerformanceMetricsAndPercentiles(t *testing.T) {
	st, err := Open(Options{
		DSN:     testdb.DSN(t),
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	user, err := st.CreateUser("perf@sightpane.local", "Perf User", "pass123")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject("Perf Proj", "flutter", "key_perf", &user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Ingest 100 spans with durations 1 to 100
	var items []json.RawMessage
	for i := 1; i <= 100; i++ {
		status := "ok"
		if i%10 == 0 {
			status = "500" // 10 errors out of 100
		}
		itemJSON := fmt.Sprintf(`{
			"type": "span",
			"ts": %q,
			"op": "http.client",
			"name": "GET /api/data",
			"duration_ms": %f,
			"status": %q,
			"tags": {"index": %d}
		}`, time.Now().UTC().Format(time.RFC3339Nano), float64(i), status, i)
		items = append(items, json.RawMessage(itemJSON))
	}

	// Also add a transaction with 2 child spans
	txJSON := fmt.Sprintf(`{
		"type": "transaction",
		"ts": %q,
		"op": "navigation",
		"name": "route:/checkout",
		"duration_ms": 250.0,
		"status": "ok",
		"span_id": "tx1",
		"trace_id": "tr1",
		"spans": [
			{
				"op": "http.client",
				"name": "POST /api/pay",
				"duration_ms": 150.0,
				"status": "ok",
				"span_id": "sp1",
				"parent_span_id": "tx1"
			},
			{
				"op": "ui.render",
				"name": "render_view",
				"duration_ms": 50.0,
				"status": "ok",
				"span_id": "sp2",
				"parent_span_id": "tx1"
			}
		]
	}`, time.Now().UTC().Format(time.RFC3339Nano))
	items = append(items, json.RawMessage(txJSON))

	var env Envelope
	envJSON := fmt.Sprintf(`{
		"sdk": {"name": "sp", "version": "1.0"},
		"session": {"id": "sess_perf_1", "started_at": %q},
		"items": []
	}`, time.Now().UTC().Format(time.RFC3339Nano))
	if err := json.Unmarshal([]byte(envJSON), &env); err != nil {
		t.Fatal(err)
	}
	env.Items = items

	res, err := st.Ingest(ctx, proj.ID, &env, "127.0.0.1")
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}
	if res.Accepted != 101 { // 100 spans + 1 transaction
		t.Fatalf("expected 101 accepted items, got %d (rejected: %d)", res.Accepted, res.Rejected)
	}

	// 2. Query Performance Summary
	summary, err := st.GetPerformanceSummary(ctx, proj.ID, 14, "")
	if err != nil {
		t.Fatalf("GetPerformanceSummary: %v", err)
	}

	if len(summary.Ops) == 0 {
		t.Fatalf("expected ops, got none")
	}

	// Find the GET /api/data entry
	var getDataItem *PerformanceSummaryItem
	for i := range summary.Summary {
		if summary.Summary[i].Name == "GET /api/data" {
			getDataItem = &summary.Summary[i]
			break
		}
	}
	if getDataItem == nil {
		t.Fatalf("GET /api/data not found in summary: %+v", summary.Summary)
	}

	if getDataItem.Count != 100 {
		t.Fatalf("expected count 100, got %d", getDataItem.Count)
	}
	// For 1..100, p50 is 50.5
	if math.Abs(getDataItem.P50-50.5) > 1.0 {
		t.Fatalf("expected p50 around 50.5, got %f", getDataItem.P50)
	}
	// p95 is 95.05
	if math.Abs(getDataItem.P95-95.05) > 1.0 {
		t.Fatalf("expected p95 around 95.05, got %f", getDataItem.P95)
	}
	// avg is 50.5
	if math.Abs(getDataItem.AvgDuration-50.5) > 1.0 {
		t.Fatalf("expected avg around 50.5, got %f", getDataItem.AvgDuration)
	}
	// error count is 10, error rate is 0.10
	if getDataItem.ErrorCount != 10 {
		t.Fatalf("expected error count 10, got %d", getDataItem.ErrorCount)
	}
	if math.Abs(getDataItem.ErrorRate-0.10) > 0.01 {
		t.Fatalf("expected error rate 0.10, got %f", getDataItem.ErrorRate)
	}

	// 3. Query Transaction Detail for GET /api/data
	detail, err := st.GetTransactionDetail(ctx, proj.ID, "http.client", "GET /api/data", 14)
	if err != nil {
		t.Fatalf("GetTransactionDetail: %v", err)
	}
	if len(detail.Samples) != 20 {
		t.Fatalf("expected 20 slowest samples, got %d", len(detail.Samples))
	}
	// First sample should be duration 100
	if detail.Samples[0].DurationMs != 100.0 {
		t.Fatalf("expected slowest sample to be 100.0, got %f", detail.Samples[0].DurationMs)
	}
	if detail.Samples[1].DurationMs != 99.0 {
		t.Fatalf("expected second slowest sample to be 99.0, got %f", detail.Samples[1].DurationMs)
	}
	if len(detail.Daily) == 0 {
		t.Fatalf("expected daily points, got none")
	}

	// 4. Verify transaction & children were captured
	txDetail, err := st.GetTransactionDetail(ctx, proj.ID, "navigation", "route:/checkout", 14)
	if err != nil {
		t.Fatalf("GetTransactionDetail route:/checkout: %v", err)
	}
	if txDetail.Count != 1 {
		t.Fatalf("expected route:/checkout count 1, got %d", txDetail.Count)
	}
	if txDetail.P50 != 250.0 {
		t.Fatalf("expected route:/checkout p50 250, got %f", txDetail.P50)
	}
}
