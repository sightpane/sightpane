// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"time"
)

type PerformanceSummaryItem struct {
	Op          string  `json:"op"`
	Name        string  `json:"name"`
	Count       int64   `json:"count"`
	P50         float64 `json:"p50"`
	P95         float64 `json:"p95"`
	AvgDuration float64 `json:"avg_duration"`
	ErrorCount  int64   `json:"error_count"`
	ErrorRate   float64 `json:"error_rate"`
}

type PerformanceResponse struct {
	Summary []PerformanceSummaryItem `json:"summary"`
	Ops     []string                 `json:"ops"`
}

type SpanSample struct {
	ID         int64     `json:"id"`
	SessionID  string    `json:"session_id"`
	TraceID    string    `json:"trace_id"`
	TS         time.Time `json:"ts"`
	DurationMs float64   `json:"duration_ms"`
	Status     string    `json:"status"`
	TagsJSON   string    `json:"tags_json"`
}

type DailyPerformancePoint struct {
	Date        string  `json:"date"`
	Count       int64   `json:"count"`
	P50         float64 `json:"p50"`
	P95         float64 `json:"p95"`
	AvgDuration float64 `json:"avg_duration"`
}

type TransactionDetailResponse struct {
	Op          string                  `json:"op"`
	Name        string                  `json:"name"`
	Count       int64                   `json:"count"`
	P50         float64                 `json:"p50"`
	P95         float64                 `json:"p95"`
	AvgDuration float64                 `json:"avg_duration"`
	ErrorCount  int64                   `json:"error_count"`
	ErrorRate   float64                 `json:"error_rate"`
	Daily       []DailyPerformancePoint `json:"daily"`
	Samples     []SpanSample            `json:"samples"`
}

// GetPerformanceSummary returns aggregated performance metrics for all transactions/spans.
func (s *Store) GetPerformanceSummary(ctx context.Context, projectID int64, days int, op string) (*PerformanceResponse, error) {
	if days <= 0 {
		days = 14
	}
	since := time.Now().UTC().AddDate(0, 0, -days)

	// Fetch distinct operations for filtering
	opRows, err := s.db.QueryContext(ctx, `SELECT DISTINCT op FROM spans WHERE project_id=$1 AND ts>=$2 ORDER BY op`, projectID, since)
	if err != nil {
		return nil, err
	}
	defer opRows.Close()

	var ops []string
	for opRows.Next() {
		var o string
		if err := opRows.Scan(&o); err == nil && o != "" {
			ops = append(ops, o)
		}
	}

	// Fetch performance summary grouped by op, name
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			op,
			name,
			COUNT(*) AS n,
			COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY duration_ms), 0) AS p50,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0) AS p95,
			COALESCE(AVG(duration_ms), 0) AS avg_dur,
			COUNT(CASE WHEN status != 'ok' AND status != '200' AND status NOT LIKE '2__' THEN 1 END) AS err_count
		FROM spans
		WHERE project_id=$1 AND ts>=$2 AND ($3 = '' OR op = $3)
		GROUP BY op, name
		ORDER BY n DESC
	`, projectID, since, op)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summary []PerformanceSummaryItem
	for rows.Next() {
		var item PerformanceSummaryItem
		if err := rows.Scan(&item.Op, &item.Name, &item.Count, &item.P50, &item.P95, &item.AvgDuration, &item.ErrorCount); err != nil {
			return nil, err
		}
		if item.Count > 0 {
			item.ErrorRate = float64(item.ErrorCount) / float64(item.Count)
		}
		summary = append(summary, item)
	}

	return &PerformanceResponse{
		Summary: summary,
		Ops:     ops,
	}, nil
}

// GetTransactionDetail returns detailed metrics, daily trend and top slowest samples for a specific transaction/span.
func (s *Store) GetTransactionDetail(ctx context.Context, projectID int64, op string, name string, days int) (*TransactionDetailResponse, error) {
	if days <= 0 {
		days = 14
	}
	since := time.Now().UTC().AddDate(0, 0, -days)

	res := &TransactionDetailResponse{
		Op:   op,
		Name: name,
	}

	// 1. Overall stats
	row := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS n,
			COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY duration_ms), 0) AS p50,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0) AS p95,
			COALESCE(AVG(duration_ms), 0) AS avg_dur,
			COUNT(CASE WHEN status != 'ok' AND status != '200' AND status NOT LIKE '2__' THEN 1 END) AS err_count
		FROM spans
		WHERE project_id=$1 AND ts>=$2 AND name=$3 AND ($4 = '' OR op = $4)
	`, projectID, since, name, op)

	if err := row.Scan(&res.Count, &res.P50, &res.P95, &res.AvgDuration, &res.ErrorCount); err != nil {
		return nil, err
	}
	if res.Count > 0 {
		res.ErrorRate = float64(res.ErrorCount) / float64(res.Count)
	}

	// 2. Daily breakdown
	dailyRows, err := s.db.QueryContext(ctx, `
		SELECT
			TO_CHAR(ts AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
			COUNT(*) AS n,
			COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY duration_ms), 0) AS p50,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0) AS p95,
			COALESCE(AVG(duration_ms), 0) AS avg_dur
		FROM spans
		WHERE project_id=$1 AND ts>=$2 AND name=$3 AND ($4 = '' OR op = $4)
		GROUP BY 1
		ORDER BY 1
	`, projectID, since, name, op)
	if err != nil {
		return nil, err
	}
	defer dailyRows.Close()

	for dailyRows.Next() {
		var d DailyPerformancePoint
		if err := dailyRows.Scan(&d.Date, &d.Count, &d.P50, &d.P95, &d.AvgDuration); err != nil {
			return nil, err
		}
		res.Daily = append(res.Daily, d)
	}

	// 3. Slowest samples (top 20)
	sampleRows, err := s.db.QueryContext(ctx, `
		SELECT
			id, session_id, trace_id, ts, duration_ms, status, tags_json
		FROM spans
		WHERE project_id=$1 AND ts>=$2 AND name=$3 AND ($4 = '' OR op = $4)
		ORDER BY duration_ms DESC
		LIMIT 20
	`, projectID, since, name, op)
	if err != nil {
		return nil, err
	}
	defer sampleRows.Close()

	for sampleRows.Next() {
		var smp SpanSample
		if err := sampleRows.Scan(&smp.ID, &smp.SessionID, &smp.TraceID, &smp.TS, &smp.DurationMs, &smp.Status, &smp.TagsJSON); err != nil {
			return nil, err
		}
		res.Samples = append(res.Samples, smp)
	}

	return res, nil
}
