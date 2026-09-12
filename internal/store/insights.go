// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrInsightNotFound = errors.New("insight not found")

var safeIdentifierRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

type InsightEvent struct {
	Name     string `json:"name"`
	Math     string `json:"math"`               // count, unique_users, avg, sum, min, max, p90
	Property string `json:"property,omitempty"` // for avg, sum, min, max, p90
}

type InsightFilter struct {
	Property string `json:"property"`
	Operator string `json:"operator"` // exact, is_not, contains, greater_than, less_than
	Value    string `json:"value"`
}

type InsightQuery struct {
	DateRange string          `json:"date_range"` // 24h, 7d, 14d, 30d, 90d
	Interval  string          `json:"interval"`   // hour, day, week
	Events    []InsightEvent  `json:"events"`
	Breakdown string          `json:"breakdown,omitempty"`
	Filters   []InsightFilter `json:"filters,omitempty"`
}

type InsightDataPoint struct {
	Time  string  `json:"time"`
	Value float64 `json:"value"`
}

type InsightSeries struct {
	Label           string             `json:"label"`
	Data            []InsightDataPoint `json:"data"`
	AggregatedValue float64            `json:"aggregated_value"`
}

type InsightQueryResult struct {
	Series   []InsightSeries `json:"series"`
	Cached   bool            `json:"cached"`
	Executed time.Time       `json:"executed_at"`
}

type Insight struct {
	ID          string       `json:"id"`
	ProjectID   int64        `json:"project_id"`
	DashboardID *string      `json:"dashboard_id,omitempty"`
	Name        string       `json:"name"`
	ChartType   string       `json:"chart_type"` // line, bar, area, number, donut, table
	Query       InsightQuery `json:"query"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// In-memory query cache with 60s TTL
type insightQueryCache struct {
	sync.RWMutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	result    InsightQueryResult
	expiresAt time.Time
}

var globalInsightCache = &insightQueryCache{
	entries: make(map[string]cacheEntry),
}

func (c *insightQueryCache) get(key string) (*InsightQueryResult, bool) {
	c.RLock()
	defer c.RUnlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	res := entry.result
	res.Cached = true
	return &res, true
}

func (c *insightQueryCache) set(key string, result InsightQueryResult, ttl time.Duration) {
	c.Lock()
	defer c.Unlock()
	// Periodic simple sweep if large
	if len(c.entries) > 2000 {
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
	c.entries[key] = cacheEntry{
		result:    result,
		expiresAt: time.Now().Add(ttl),
	}
}

// CreateInsight inserts a new insight.
func (s *Store) CreateInsight(projectID int64, dashboardID *string, name, chartType string, query InsightQuery) (*Insight, error) {
	if chartType == "" {
		chartType = "line"
	}
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	var ins Insight
	var rawQuery []byte
	var dashID sql.NullString
	if dashboardID != nil && *dashboardID != "" {
		dashID = sql.NullString{String: *dashboardID, Valid: true}
	}

	row := s.db.QueryRow(`
		INSERT INTO insights (project_id, dashboard_id, name, chart_type, query)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, project_id, dashboard_id, name, chart_type, query, created_at, updated_at
	`, projectID, dashID, name, chartType, queryJSON)

	var retrievedDashID sql.NullString
	if err := row.Scan(&ins.ID, &ins.ProjectID, &retrievedDashID, &ins.Name, &ins.ChartType, &rawQuery, &ins.CreatedAt, &ins.UpdatedAt); err != nil {
		return nil, fmt.Errorf("insert insight: %w", err)
	}

	if retrievedDashID.Valid {
		v := retrievedDashID.String
		ins.DashboardID = &v
	}
	if len(rawQuery) > 0 {
		_ = json.Unmarshal(rawQuery, &ins.Query)
	}

	return &ins, nil
}

// GetInsight retrieves an insight by ID.
func (s *Store) GetInsight(projectID int64, id string) (*Insight, error) {
	var ins Insight
	var rawQuery []byte
	var dashID sql.NullString

	err := s.db.QueryRow(`
		SELECT id, project_id, dashboard_id, name, chart_type, query, created_at, updated_at
		FROM insights
		WHERE project_id = $1 AND id = $2
	`, projectID, id).Scan(&ins.ID, &ins.ProjectID, &dashID, &ins.Name, &ins.ChartType, &rawQuery, &ins.CreatedAt, &ins.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInsightNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query insight: %w", err)
	}

	if dashID.Valid {
		v := dashID.String
		ins.DashboardID = &v
	}
	if len(rawQuery) > 0 {
		_ = json.Unmarshal(rawQuery, &ins.Query)
	}

	return &ins, nil
}

// ListInsights lists all insights for a project or for a specific dashboard.
func (s *Store) ListInsights(projectID int64, dashboardID *string) ([]Insight, error) {
	var query strings.Builder
	query.WriteString(`
		SELECT id, project_id, dashboard_id, name, chart_type, query, created_at, updated_at
		FROM insights
		WHERE project_id = $1
	`)
	args := []any{projectID}

	if dashboardID != nil && *dashboardID != "" {
		args = append(args, *dashboardID)
		query.WriteString(fmt.Sprintf(` AND dashboard_id = $%d`, len(args)))
	}

	query.WriteString(` ORDER BY created_at DESC`)

	rows, err := s.db.Query(query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list insights: %w", err)
	}
	defer rows.Close()

	var list []Insight
	for rows.Next() {
		var ins Insight
		var rawQuery []byte
		var dashID sql.NullString
		if err := rows.Scan(&ins.ID, &ins.ProjectID, &dashID, &ins.Name, &ins.ChartType, &rawQuery, &ins.CreatedAt, &ins.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan insight: %w", err)
		}
		if dashID.Valid {
			v := dashID.String
			ins.DashboardID = &v
		}
		if len(rawQuery) > 0 {
			_ = json.Unmarshal(rawQuery, &ins.Query)
		}
		list = append(list, ins)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Insight{}
	}
	return list, nil
}

// UpdateInsight updates an insight.
func (s *Store) UpdateInsight(projectID int64, id string, dashboardID *string, name, chartType string, query InsightQuery) (*Insight, error) {
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	var dashID sql.NullString
	if dashboardID != nil && *dashboardID != "" {
		dashID = sql.NullString{String: *dashboardID, Valid: true}
	}

	var ins Insight
	var rawQuery []byte
	var retrievedDashID sql.NullString
	err = s.db.QueryRow(`
		UPDATE insights
		SET dashboard_id = $1, name = $2, chart_type = $3, query = $4, updated_at = NOW()
		WHERE project_id = $5 AND id = $6
		RETURNING id, project_id, dashboard_id, name, chart_type, query, created_at, updated_at
	`, dashID, name, chartType, queryJSON, projectID, id).Scan(
		&ins.ID, &ins.ProjectID, &retrievedDashID, &ins.Name, &ins.ChartType, &rawQuery, &ins.CreatedAt, &ins.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInsightNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update insight: %w", err)
	}

	if retrievedDashID.Valid {
		v := retrievedDashID.String
		ins.DashboardID = &v
	}
	if len(rawQuery) > 0 {
		_ = json.Unmarshal(rawQuery, &ins.Query)
	}

	return &ins, nil
}

// DeleteInsight removes an insight.
func (s *Store) DeleteInsight(projectID int64, id string) error {
	res, err := s.db.Exec(`DELETE FROM insights WHERE project_id = $1 AND id = $2`, projectID, id)
	if err != nil {
		return fmt.Errorf("delete insight: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInsightNotFound
	}
	return nil
}

// ExecuteInsightQuery runs an analytical query over items and sessions with 60s TTL caching.
func (s *Store) ExecuteInsightQuery(ctx context.Context, projectID int64, q InsightQuery) (*InsightQueryResult, error) {
	// Build cache key
	qBytes, _ := json.Marshal(q)
	h := sha256.Sum256(append(fmt.Appendf(nil, "p:%d:", projectID), qBytes...))
	cacheKey := hex.EncodeToString(h[:])

	if cached, ok := globalInsightCache.get(cacheKey); ok {
		return cached, nil
	}

	now := time.Now().UTC()
	var cutoff time.Time
	interval := strings.ToLower(q.Interval)

	switch strings.ToLower(q.DateRange) {
	case "24h", "1d":
		cutoff = now.Add(-24 * time.Hour)
		if interval == "" {
			interval = "hour"
		}
	case "7d":
		cutoff = now.AddDate(0, 0, -7)
		if interval == "" {
			interval = "day"
		}
	case "30d":
		cutoff = now.AddDate(0, 0, -30)
		if interval == "" {
			interval = "day"
		}
	case "90d":
		cutoff = now.AddDate(0, 0, -90)
		if interval == "" {
			interval = "week"
		}
	default: // "14d"
		cutoff = now.AddDate(0, 0, -14)
		if interval == "" {
			interval = "day"
		}
	}

	if interval != "hour" && interval != "week" && interval != "month" {
		interval = "day"
	}

	// Determine aggregation
	mathFn := "count"
	propName := ""
	eventName := ""
	if len(q.Events) > 0 {
		mathFn = strings.ToLower(q.Events[0].Math)
		propName = q.Events[0].Property
		eventName = q.Events[0].Name
	}

	var aggExpr string
	switch mathFn {
	case "unique_users":
		aggExpr = "COUNT(DISTINCT NULLIF(COALESCE(sessions.user_id, items.session_id), ''))::float8"
	case "sum":
		if propName != "" && safeIdentifierRegex.MatchString(propName) {
			aggExpr = fmt.Sprintf("COALESCE(SUM((items.body_json::jsonb ->> '%s')::numeric), 0)::float8", propName)
		} else {
			aggExpr = "COUNT(*)::float8"
		}
	case "avg":
		if propName != "" && safeIdentifierRegex.MatchString(propName) {
			aggExpr = fmt.Sprintf("COALESCE(AVG((items.body_json::jsonb ->> '%s')::numeric), 0)::float8", propName)
		} else {
			aggExpr = "COUNT(*)::float8"
		}
	case "min":
		if propName != "" && safeIdentifierRegex.MatchString(propName) {
			aggExpr = fmt.Sprintf("COALESCE(MIN((items.body_json::jsonb ->> '%s')::numeric), 0)::float8", propName)
		} else {
			aggExpr = "COUNT(*)::float8"
		}
	case "max":
		if propName != "" && safeIdentifierRegex.MatchString(propName) {
			aggExpr = fmt.Sprintf("COALESCE(MAX((items.body_json::jsonb ->> '%s')::numeric), 0)::float8", propName)
		} else {
			aggExpr = "COUNT(*)::float8"
		}
	case "p90":
		if propName != "" && safeIdentifierRegex.MatchString(propName) {
			aggExpr = fmt.Sprintf("COALESCE(percentile_cont(0.90) within group (order by (items.body_json::jsonb ->> '%s')::numeric), 0)::float8", propName)
		} else {
			aggExpr = "COUNT(*)::float8"
		}
	default:
		aggExpr = "COUNT(*)::float8"
	}

	// Breakdown expr
	breakdownCol := "''"
	hasBreakdown := false
	if q.Breakdown != "" && safeIdentifierRegex.MatchString(q.Breakdown) {
		hasBreakdown = true
		switch strings.ToLower(q.Breakdown) {
		case "platform":
			breakdownCol = "COALESCE(NULLIF(sessions.platform, ''), 'other')"
		case "browser":
			breakdownCol = "COALESCE(NULLIF(sessions.browser, ''), 'other')"
		case "release":
			breakdownCol = "COALESCE(NULLIF(sessions.release, ''), 'other')"
		default:
			breakdownCol = fmt.Sprintf("COALESCE(NULLIF(items.body_json::jsonb ->> '%s', ''), 'other')", q.Breakdown)
		}
	}

	args := []any{projectID, cutoff}
	var whereClauses []string
	whereClauses = append(whereClauses, "items.project_id = $1", "items.ts >= $2", "items.type = 'event'")

	if eventName != "" && eventName != "$all" {
		args = append(args, eventName)
		whereClauses = append(whereClauses, fmt.Sprintf("items.name = $%d", len(args)))
	}

	for _, f := range q.Filters {
		if !safeIdentifierRegex.MatchString(f.Property) {
			continue
		}
		var fieldExpr string
		switch strings.ToLower(f.Property) {
		case "platform":
			fieldExpr = "sessions.platform"
		case "browser":
			fieldExpr = "sessions.browser"
		case "release":
			fieldExpr = "sessions.release"
		default:
			fieldExpr = fmt.Sprintf("items.body_json::jsonb ->> '%s'", f.Property)
		}

		args = append(args, f.Value)
		idx := len(args)
		switch f.Operator {
		case "is_not":
			whereClauses = append(whereClauses, fmt.Sprintf("%s != $%d", fieldExpr, idx))
		case "contains":
			whereClauses = append(whereClauses, fmt.Sprintf("%s ILIKE ('%%' || $%d || '%%')", fieldExpr, idx))
		case "greater_than":
			whereClauses = append(whereClauses, fmt.Sprintf("(%s)::numeric > ($%d)::numeric", fieldExpr, idx))
		case "less_than":
			whereClauses = append(whereClauses, fmt.Sprintf("(%s)::numeric < ($%d)::numeric", fieldExpr, idx))
		default: // exact
			whereClauses = append(whereClauses, fmt.Sprintf("%s = $%d", fieldExpr, idx))
		}
	}

	sqlQuery := fmt.Sprintf(`
		SELECT date_trunc('%s', items.ts) AS bucket,
		       %s AS group_key,
		       %s AS val
		FROM items
		LEFT JOIN sessions ON sessions.id = items.session_id AND sessions.project_id = items.project_id
		WHERE %s
		GROUP BY bucket, group_key
		ORDER BY bucket ASC, val DESC
	`, interval, breakdownCol, aggExpr, strings.Join(whereClauses, " AND "))

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("execute insight query: %w", err)
	}
	defer rows.Close()

	seriesMap := make(map[string][]InsightDataPoint)
	seriesAgg := make(map[string]float64)

	for rows.Next() {
		var bucket time.Time
		var groupKey string
		var val float64
		if err := rows.Scan(&bucket, &groupKey, &val); err != nil {
			return nil, fmt.Errorf("scan query row: %w", err)
		}

		label := groupKey
		if !hasBreakdown || label == "" {
			if eventName != "" {
				label = eventName
			} else {
				label = "All Events"
			}
		}

		timeStr := bucket.Format("2006-01-02 15:04:05")
		if interval == "day" {
			timeStr = bucket.Format("2006-01-02")
		}

		seriesMap[label] = append(seriesMap[label], InsightDataPoint{
			Time:  timeStr,
			Value: val,
		})
		seriesAgg[label] += val
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var seriesList []InsightSeries
	for label, points := range seriesMap {
		seriesList = append(seriesList, InsightSeries{
			Label:           label,
			Data:            points,
			AggregatedValue: seriesAgg[label],
		})
	}

	sort.Slice(seriesList, func(i, j int) bool {
		return seriesList[i].AggregatedValue > seriesList[j].AggregatedValue
	})

	// Limit to top 10 series if large breakdown
	if len(seriesList) > 10 {
		seriesList = seriesList[:10]
	}

	res := InsightQueryResult{
		Series:   seriesList,
		Cached:   false,
		Executed: now,
	}

	// Cache result for 60s
	globalInsightCache.set(cacheKey, res, 60*time.Second)

	return &res, nil
}
