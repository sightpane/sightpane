// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type SpanRecord struct {
	ID           int64           `json:"id,omitempty"`
	ProjectID    int64           `json:"project_id"`
	TraceID      string          `json:"trace_id"`
	SpanID       string          `json:"span_id"`
	ParentSpanID string          `json:"parent_span_id,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	Op           string          `json:"op"`
	Name         string          `json:"name"`
	ServiceName  string          `json:"service_name"`
	StartTime    time.Time       `json:"start_time"`
	DurationMs   float64         `json:"duration_ms"`
	Status       string          `json:"status"`
	Data         json.RawMessage `json:"data,omitempty"`
}

type TraceSummary struct {
	TraceID      string    `json:"trace_id"`
	RootOp       string    `json:"root_op"`
	RootName     string    `json:"root_name"`
	ServiceName  string    `json:"service_name"`
	StartTime    time.Time `json:"start_time"`
	DurationMs   float64   `json:"duration_ms"`
	Status       string    `json:"status"`
	SpanCount    int       `json:"span_count"`
	ServiceCount int       `json:"service_count"`
	Services     []string  `json:"services"`
	HasErrors    bool      `json:"has_errors"`
}

type TraceSpan struct {
	SpanID          string          `json:"span_id"`
	ParentSpanID    string          `json:"parent_span_id,omitempty"`
	Op              string          `json:"op"`
	Name            string          `json:"name"`
	ServiceName     string          `json:"service_name"`
	StartTime       time.Time       `json:"start_time"`
	StartOffsetMs   float64         `json:"start_offset_ms"`
	DurationMs      float64         `json:"duration_ms"`
	Status          string          `json:"status"`
	Data            json.RawMessage `json:"data,omitempty"`
	Depth           int             `json:"depth"`
	IsSuspectNPlus1 bool            `json:"is_suspect_n_plus_one"`
}

type SuspectIssue struct {
	Type    string   `json:"type"` // "n_plus_one"
	Message string   `json:"message"`
	SpanIDs []string `json:"span_ids"`
}

type TraceDetail struct {
	TraceID         string         `json:"trace_id"`
	RootSpanID      string         `json:"root_span_id"`
	RootName        string         `json:"root_name"`
	RootOp          string         `json:"root_op"`
	StartTime       time.Time      `json:"start_time"`
	TotalDurationMs float64        `json:"total_duration_ms"`
	Status          string         `json:"status"`
	ServiceCount    int            `json:"service_count"`
	SpanCount       int            `json:"span_count"`
	Services        []string       `json:"services"`
	Spans           []TraceSpan    `json:"spans"`
	SuspectIssues   []SuspectIssue `json:"suspect_issues"`
}

type ListTracesOptions struct {
	Days          int
	Service       string
	Status        string // "ok", "error"
	MinDurationMs float64
	Query         string
	Limit         int
}

// IngestSpans inserts a batch of spans into the hypertable.
func (s *Store) IngestSpans(ctx context.Context, projectID int64, spans []SpanRecord) error {
	if len(spans) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO spans (
			project_id, session_id, ts, op, name, duration_ms, status, parent_span_id, span_id, trace_id, tags_json, service_name, data
		) VALUES (
			$1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb
		)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, span := range spans {
		st := span.StartTime
		if st.IsZero() {
			st = time.Now().UTC()
		}
		op := span.Op
		if op == "" {
			op = "custom"
		}
		status := span.Status
		if status == "" {
			status = "ok"
		}
		service := span.ServiceName
		if service == "" {
			service = "client"
		}
		dataStr := "{}"
		if len(span.Data) > 0 && string(span.Data) != "null" {
			dataStr = string(span.Data)
		}

		if _, err := stmt.ExecContext(ctx,
			projectID,
			span.SessionID,
			st,
			op,
			span.Name,
			span.DurationMs,
			status,
			span.ParentSpanID,
			span.SpanID,
			span.TraceID,
			dataStr,
			service,
			dataStr,
		); err != nil {
			return fmt.Errorf("insert span %s: %w", span.SpanID, err)
		}
	}

	return tx.Commit()
}

// ListTraces queries recent traces for a project with optional filters.
func (s *Store) ListTraces(ctx context.Context, projectID int64, opts ListTracesOptions) ([]TraceSummary, error) {
	days := opts.Days
	if days <= 0 {
		days = 14
	}
	limit := opts.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	query := `
		SELECT
			trace_id,
			COALESCE(MIN(ts), NOW()) as start_time,
			COUNT(*) as span_count,
			COALESCE(MAX(duration_ms), 0) as max_duration,
			ARRAY_AGG(DISTINCT service_name) as services,
			BOOL_OR(status != 'ok' AND status != '200') as has_errors
		FROM spans
		WHERE project_id = $1 AND ts >= $2 AND trace_id != ''
	`
	args := []any{projectID, cutoff}
	idx := 3

	if opts.Service != "" {
		query += fmt.Sprintf(" AND trace_id IN (SELECT trace_id FROM spans WHERE project_id = $1 AND ts >= $2 AND service_name = $%d)", idx)
		args = append(args, opts.Service)
		idx++
	}

	if opts.Status == "error" {
		query += " AND (status != 'ok' AND status != '200')"
	} else if opts.Status == "ok" {
		query += " AND (status = 'ok' OR status = '200')"
	}

	if opts.Query != "" {
		query += fmt.Sprintf(" AND (name ILIKE $%d OR trace_id ILIKE $%d)", idx, idx)
		args = append(args, "%"+opts.Query+"%")
		idx++
	}

	query += `
		GROUP BY trace_id
	`

	if opts.MinDurationMs > 0 {
		query += fmt.Sprintf(" HAVING MAX(duration_ms) >= $%d", idx)
		args = append(args, opts.MinDurationMs)
		idx++
	}

	query += fmt.Sprintf(" ORDER BY start_time DESC LIMIT $%d", idx)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list traces: %w", err)
	}
	defer rows.Close()

	var traces []TraceSummary
	for rows.Next() {
		var (
			t         TraceSummary
			services  []string
			hasErrors sql.NullBool
		)
		var servicesBytes []byte
		if err := rows.Scan(
			&t.TraceID,
			&t.StartTime,
			&t.SpanCount,
			&t.DurationMs,
			&servicesBytes,
			&hasErrors,
		); err != nil {
			return nil, err
		}

		// Parse postgres text array format {service1,service2}
		sStr := strings.Trim(string(servicesBytes), "{}")
		if sStr != "" {
			parts := strings.Split(sStr, ",")
			for _, p := range parts {
				cleaned := strings.Trim(p, "\" ")
				if cleaned != "" {
					services = append(services, cleaned)
				}
			}
		}
		t.Services = services
		t.ServiceCount = len(services)
		t.HasErrors = hasErrors.Valid && hasErrors.Bool
		if t.HasErrors {
			t.Status = "error"
		} else {
			t.Status = "ok"
		}

		traces = append(traces, t)
	}

	// For each trace, enrich with root span information (root name & root op)
	for i := range traces {
		var rootOp, rootName, rootService string
		err := s.db.QueryRowContext(ctx, `
			SELECT op, name, service_name
			FROM spans
			WHERE project_id = $1 AND trace_id = $2
			ORDER BY (parent_span_id = '' OR parent_span_id IS NULL) DESC, ts ASC, duration_ms DESC
			LIMIT 1
		`, projectID, traces[i].TraceID).Scan(&rootOp, &rootName, &rootService)
		if err == nil {
			traces[i].RootOp = rootOp
			traces[i].RootName = rootName
			traces[i].ServiceName = rootService
		} else {
			traces[i].RootOp = "transaction"
			traces[i].RootName = traces[i].TraceID
			traces[i].ServiceName = "client"
		}
	}

	if traces == nil {
		traces = []TraceSummary{}
	}

	return traces, nil
}

// GetTrace retrieves all spans in a trace, constructs the hierarchical tree with offsets and depths,
// and identifies suspect N+1 query patterns.
func (s *Store) GetTrace(ctx context.Context, projectID int64, traceID string) (*TraceDetail, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			span_id,
			COALESCE(parent_span_id, ''),
			op,
			name,
			service_name,
			ts,
			duration_ms,
			status,
			COALESCE(data::text, tags_json, '{}')
		FROM spans
		WHERE project_id = $1 AND trace_id = $2
		ORDER BY ts ASC, id ASC
	`, projectID, traceID)
	if err != nil {
		return nil, fmt.Errorf("get trace %s: %w", traceID, err)
	}
	defer rows.Close()

	type rawSpan struct {
		spanID       string
		parentSpanID string
		op           string
		name         string
		serviceName  string
		ts           time.Time
		durationMs   float64
		status       string
		dataStr      string
	}

	var rawSpans []rawSpan
	serviceSet := make(map[string]bool)

	for rows.Next() {
		var r rawSpan
		if err := rows.Scan(
			&r.spanID,
			&r.parentSpanID,
			&r.op,
			&r.name,
			&r.serviceName,
			&r.ts,
			&r.durationMs,
			&r.status,
			&r.dataStr,
		); err != nil {
			return nil, err
		}
		rawSpans = append(rawSpans, r)
		if r.serviceName != "" {
			serviceSet[r.serviceName] = true
		}
	}

	if len(rawSpans) == 0 {
		return nil, sql.ErrNoRows
	}

	// Find the earliest start time to use as reference origin (0.0 ms)
	earliestTS := rawSpans[0].ts
	latestEndTS := rawSpans[0].ts.Add(time.Duration(rawSpans[0].durationMs * float64(time.Millisecond)))

	for _, rs := range rawSpans {
		if rs.ts.Before(earliestTS) {
			earliestTS = rs.ts
		}
		end := rs.ts.Add(time.Duration(rs.durationMs * float64(time.Millisecond)))
		if end.After(latestEndTS) {
			latestEndTS = end
		}
	}

	totalDurMs := float64(latestEndTS.Sub(earliestTS).Microseconds()) / 1000.0
	if totalDurMs <= 0 {
		totalDurMs = rawSpans[0].durationMs
	}

	// Map parent to children for hierarchy and tree depth calculation
	parentToChildren := make(map[string][]string)
	spanMap := make(map[string]rawSpan)
	hasParent := make(map[string]bool)

	for _, rs := range rawSpans {
		spanMap[rs.spanID] = rs
		if rs.parentSpanID != "" {
			hasParent[rs.spanID] = true
			parentToChildren[rs.parentSpanID] = append(parentToChildren[rs.parentSpanID], rs.spanID)
		}
	}

	// Roots: spans with no parentSpanID, or whose parentSpanID is not in spanMap
	var rootIDs []string
	for _, rs := range rawSpans {
		if rs.parentSpanID == "" || !hasParent[rs.spanID] || spanMap[rs.parentSpanID].spanID == "" {
			rootIDs = append(rootIDs, rs.spanID)
		}
	}
	if len(rootIDs) == 0 {
		rootIDs = append(rootIDs, rawSpans[0].spanID)
	}

	// Assign depths using BFS/DFS
	depthMap := make(map[string]int)
	var orderedIDs []string
	visited := make(map[string]bool)

	var traverse func(id string, depth int)
	traverse = func(id string, depth int) {
		if visited[id] {
			return
		}
		visited[id] = true
		depthMap[id] = depth
		orderedIDs = append(orderedIDs, id)
		for _, childID := range parentToChildren[id] {
			traverse(childID, depth+1)
		}
	}

	for _, rid := range rootIDs {
		traverse(rid, 0)
	}
	// Add any orphaned spans not reached
	for _, rs := range rawSpans {
		if !visited[rs.spanID] {
			traverse(rs.spanID, 0)
		}
	}

	// Detect suspect N+1 query patterns:
	// Find 3+ consecutive spans where op is "db.query" and name or normalized statement is identical
	suspectSpanSet := make(map[string]bool)
	var suspectIssues []SuspectIssue

	type dbRun struct {
		name    string
		spanIDs []string
	}
	var currentRun *dbRun

	for _, id := range orderedIDs {
		sp := spanMap[id]
		isDB := sp.op == "db.query" || strings.HasPrefix(sp.op, "db") || strings.Contains(strings.ToLower(sp.op), "query")
		if isDB {
			normName := strings.TrimSpace(sp.name)
			if currentRun == nil {
				currentRun = &dbRun{name: normName, spanIDs: []string{id}}
			} else if currentRun.name == normName {
				currentRun.spanIDs = append(currentRun.spanIDs, id)
			} else {
				if len(currentRun.spanIDs) >= 3 {
					for _, sid := range currentRun.spanIDs {
						suspectSpanSet[sid] = true
					}
					suspectIssues = append(suspectIssues, SuspectIssue{
						Type:    "n_plus_one",
						Message: fmt.Sprintf("%d consecutive identical queries to '%s'", len(currentRun.spanIDs), currentRun.name),
						SpanIDs: currentRun.spanIDs,
					})
				}
				currentRun = &dbRun{name: normName, spanIDs: []string{id}}
			}
		} else {
			if currentRun != nil && len(currentRun.spanIDs) >= 3 {
				for _, sid := range currentRun.spanIDs {
					suspectSpanSet[sid] = true
				}
				suspectIssues = append(suspectIssues, SuspectIssue{
					Type:    "n_plus_one",
					Message: fmt.Sprintf("%d consecutive identical queries to '%s'", len(currentRun.spanIDs), currentRun.name),
					SpanIDs: currentRun.spanIDs,
				})
			}
			currentRun = nil
		}
	}
	if currentRun != nil && len(currentRun.spanIDs) >= 3 {
		for _, sid := range currentRun.spanIDs {
			suspectSpanSet[sid] = true
		}
		suspectIssues = append(suspectIssues, SuspectIssue{
			Type:    "n_plus_one",
			Message: fmt.Sprintf("%d consecutive identical queries to '%s'", len(currentRun.spanIDs), currentRun.name),
			SpanIDs: currentRun.spanIDs,
		})
	}

	// Build final TraceSpan list in tree order
	traceSpans := make([]TraceSpan, 0, len(orderedIDs))
	overallStatus := "ok"

	for _, id := range orderedIDs {
		rs := spanMap[id]
		offsetMs := float64(rs.ts.Sub(earliestTS).Microseconds()) / 1000.0
		if offsetMs < 0 {
			offsetMs = 0
		}

		if rs.status != "ok" && rs.status != "200" {
			overallStatus = "error"
		}

		traceSpans = append(traceSpans, TraceSpan{
			SpanID:          rs.spanID,
			ParentSpanID:    rs.parentSpanID,
			Op:              rs.op,
			Name:            rs.name,
			ServiceName:     rs.serviceName,
			StartTime:       rs.ts,
			StartOffsetMs:   offsetMs,
			DurationMs:      rs.durationMs,
			Status:          rs.status,
			Data:            json.RawMessage(rs.dataStr),
			Depth:           depthMap[id],
			IsSuspectNPlus1: suspectSpanSet[id],
		})
	}

	services := make([]string, 0, len(serviceSet))
	for s := range serviceSet {
		services = append(services, s)
	}

	rootSpan := spanMap[rootIDs[0]]

	return &TraceDetail{
		TraceID:         traceID,
		RootSpanID:      rootSpan.spanID,
		RootName:        rootSpan.name,
		RootOp:          rootSpan.op,
		StartTime:       earliestTS,
		TotalDurationMs: totalDurMs,
		Status:          overallStatus,
		ServiceCount:    len(services),
		SpanCount:       len(traceSpans),
		Services:        services,
		Spans:           traceSpans,
		SuspectIssues:   suspectIssues,
	}, nil
}
