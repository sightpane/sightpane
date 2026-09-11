// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrUptimeMonitorNotFound = errors.New("uptime monitor not found")

type UptimeMonitor struct {
	ID                 int64             `json:"id"`
	ProjectID          int64             `json:"project_id"`
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	Method             string            `json:"method"` // GET, HEAD, POST
	Headers            map[string]string `json:"headers"`
	ExpectedStatusCode int               `json:"expected_status_code"`
	IntervalSeconds    int               `json:"interval_seconds"`
	TimeoutSeconds     int               `json:"timeout_seconds"`
	Status             string            `json:"status"` // 'up', 'degraded', 'down'
	SSLCheckEnabled    bool              `json:"ssl_check_enabled"`
	SSLIssuer          string            `json:"ssl_issuer"`
	SSLExpiresAt       *time.Time        `json:"ssl_expires_at"`
	LastCheckedAt      *time.Time        `json:"last_checked_at"`
	UptimePercentage   float64           `json:"uptime_percentage"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

type UptimeCheck struct {
	ID             int64     `json:"id"`
	MonitorID      int64     `json:"monitor_id"`
	ProjectID      int64     `json:"project_id"`
	CheckedAt      time.Time `json:"checked_at"`
	StatusCode     *int      `json:"status_code"`
	ResponseTimeMs int       `json:"response_time_ms"`
	IsUp           bool      `json:"is_up"`
	ErrorMessage   string    `json:"error_message"`
}

type UptimeStats struct {
	TotalMonitors   int     `json:"total_monitors"`
	UpCount         int     `json:"up_count"`
	DegradedCount   int     `json:"degraded_count"`
	DownCount       int     `json:"down_count"`
	AvgUptimePct    float64 `json:"avg_uptime_percentage"`
}

type UptimeSSLInfo struct {
	Valid         bool       `json:"valid"`
	ExpiresAt     *time.Time `json:"expires_at"`
	DaysRemaining int        `json:"days_remaining"`
	Issuer        string     `json:"issuer"`
}

type UptimeDaySummary struct {
	Date      string  `json:"date"`
	Status    string  `json:"status"` // 'up', 'degraded', 'down', 'none'
	AvgMs     int     `json:"avg_ms"`
	UptimePct float64 `json:"uptime_pct"`
}

type UptimeHistoryDetail struct {
	Monitor               *UptimeMonitor      `json:"monitor"`
	Status                string              `json:"status"`
	UptimePercentage      float64             `json:"uptime_percentage"`
	CurrentResponseTimeMs int                 `json:"current_response_time_ms"`
	SSL                   *UptimeSSLInfo      `json:"ssl,omitempty"`
	History90d            []*UptimeDaySummary `json:"history_90d"`
	RecentChecks          []*UptimeCheck      `json:"recent_checks"`
}

func (s *Store) ListUptimeMonitors(projectID int64) ([]*UptimeMonitor, error) {
	ctx := context.Background()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, name, url, method, headers, expected_status_code,
		       interval_seconds, timeout_seconds, status, ssl_check_enabled,
		       ssl_issuer, ssl_expires_at, last_checked_at, uptime_percentage,
		       created_at, updated_at
		FROM uptime_monitors
		WHERE project_id = $1
		ORDER BY name ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list uptime monitors: %w", err)
	}
	defer rows.Close()

	var monitors []*UptimeMonitor
	for rows.Next() {
		var m UptimeMonitor
		var headersJSON []byte
		if err := rows.Scan(
			&m.ID, &m.ProjectID, &m.Name, &m.URL, &m.Method, &headersJSON,
			&m.ExpectedStatusCode, &m.IntervalSeconds, &m.TimeoutSeconds,
			&m.Status, &m.SSLCheckEnabled, &m.SSLIssuer, &m.SSLExpiresAt,
			&m.LastCheckedAt, &m.UptimePercentage, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan uptime monitor: %w", err)
		}
		if len(headersJSON) > 0 {
			_ = json.Unmarshal(headersJSON, &m.Headers)
		}
		if m.Headers == nil {
			m.Headers = map[string]string{}
		}
		monitors = append(monitors, &m)
	}
	return monitors, rows.Err()
}

func (s *Store) GetUptimeMonitor(projectID, monitorID int64) (*UptimeMonitor, error) {
	ctx := context.Background()
	var m UptimeMonitor
	var headersJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, name, url, method, headers, expected_status_code,
		       interval_seconds, timeout_seconds, status, ssl_check_enabled,
		       ssl_issuer, ssl_expires_at, last_checked_at, uptime_percentage,
		       created_at, updated_at
		FROM uptime_monitors
		WHERE project_id = $1 AND id = $2
	`, projectID, monitorID).Scan(
		&m.ID, &m.ProjectID, &m.Name, &m.URL, &m.Method, &headersJSON,
		&m.ExpectedStatusCode, &m.IntervalSeconds, &m.TimeoutSeconds,
		&m.Status, &m.SSLCheckEnabled, &m.SSLIssuer, &m.SSLExpiresAt,
		&m.LastCheckedAt, &m.UptimePercentage, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUptimeMonitorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get uptime monitor: %w", err)
	}
	if len(headersJSON) > 0 {
		_ = json.Unmarshal(headersJSON, &m.Headers)
	}
	if m.Headers == nil {
		m.Headers = map[string]string{}
	}
	return &m, nil
}

func (s *Store) CreateUptimeMonitor(
	projectID int64,
	name, url, method string,
	headers map[string]string,
	expectedStatusCode, intervalSeconds, timeoutSeconds int,
	sslCheckEnabled bool,
) (*UptimeMonitor, error) {
	ctx := context.Background()
	if method == "" {
		method = "GET"
	}
	if expectedStatusCode <= 0 {
		expectedStatusCode = 200
	}
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 10
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return nil, fmt.Errorf("marshal headers: %w", err)
	}

	var m UptimeMonitor
	var retHeaders []byte
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO uptime_monitors (
			project_id, name, url, method, headers, expected_status_code,
			interval_seconds, timeout_seconds, ssl_check_enabled
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, project_id, name, url, method, headers, expected_status_code,
		          interval_seconds, timeout_seconds, status, ssl_check_enabled,
		          ssl_issuer, ssl_expires_at, last_checked_at, uptime_percentage,
		          created_at, updated_at
	`, projectID, name, url, method, headersJSON, expectedStatusCode, intervalSeconds, timeoutSeconds, sslCheckEnabled).Scan(
		&m.ID, &m.ProjectID, &m.Name, &m.URL, &m.Method, &retHeaders,
		&m.ExpectedStatusCode, &m.IntervalSeconds, &m.TimeoutSeconds,
		&m.Status, &m.SSLCheckEnabled, &m.SSLIssuer, &m.SSLExpiresAt,
		&m.LastCheckedAt, &m.UptimePercentage, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create uptime monitor: %w", err)
	}
	if len(retHeaders) > 0 {
		_ = json.Unmarshal(retHeaders, &m.Headers)
	}
	if m.Headers == nil {
		m.Headers = map[string]string{}
	}
	return &m, nil
}

func (s *Store) UpdateUptimeMonitor(
	projectID, monitorID int64,
	name, url, method *string,
	headers map[string]string,
	expectedStatusCode, intervalSeconds, timeoutSeconds *int,
	sslCheckEnabled *bool,
) (*UptimeMonitor, error) {
	current, err := s.GetUptimeMonitor(projectID, monitorID)
	if err != nil {
		return nil, err
	}

	if name != nil {
		current.Name = *name
	}
	if url != nil {
		current.URL = *url
	}
	if method != nil && *method != "" {
		current.Method = *method
	}
	if headers != nil {
		current.Headers = headers
	}
	if expectedStatusCode != nil && *expectedStatusCode > 0 {
		current.ExpectedStatusCode = *expectedStatusCode
	}
	if intervalSeconds != nil && *intervalSeconds > 0 {
		current.IntervalSeconds = *intervalSeconds
	}
	if timeoutSeconds != nil && *timeoutSeconds > 0 {
		current.TimeoutSeconds = *timeoutSeconds
	}
	if sslCheckEnabled != nil {
		current.SSLCheckEnabled = *sslCheckEnabled
	}

	headersJSON, err := json.Marshal(current.Headers)
	if err != nil {
		return nil, fmt.Errorf("marshal headers: %w", err)
	}

	ctx := context.Background()
	var m UptimeMonitor
	var retHeaders []byte
	err = s.db.QueryRowContext(ctx, `
		UPDATE uptime_monitors
		SET name = $1, url = $2, method = $3, headers = $4,
		    expected_status_code = $5, interval_seconds = $6,
		    timeout_seconds = $7, ssl_check_enabled = $8,
		    updated_at = NOW()
		WHERE project_id = $9 AND id = $10
		RETURNING id, project_id, name, url, method, headers, expected_status_code,
		          interval_seconds, timeout_seconds, status, ssl_check_enabled,
		          ssl_issuer, ssl_expires_at, last_checked_at, uptime_percentage,
		          created_at, updated_at
	`, current.Name, current.URL, current.Method, headersJSON,
		current.ExpectedStatusCode, current.IntervalSeconds,
		current.TimeoutSeconds, current.SSLCheckEnabled,
		projectID, monitorID).Scan(
		&m.ID, &m.ProjectID, &m.Name, &m.URL, &m.Method, &retHeaders,
		&m.ExpectedStatusCode, &m.IntervalSeconds, &m.TimeoutSeconds,
		&m.Status, &m.SSLCheckEnabled, &m.SSLIssuer, &m.SSLExpiresAt,
		&m.LastCheckedAt, &m.UptimePercentage, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUptimeMonitorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update uptime monitor: %w", err)
	}
	if len(retHeaders) > 0 {
		_ = json.Unmarshal(retHeaders, &m.Headers)
	}
	if m.Headers == nil {
		m.Headers = map[string]string{}
	}
	return &m, nil
}

func (s *Store) DeleteUptimeMonitor(projectID, monitorID int64) error {
	ctx := context.Background()
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM uptime_monitors
		WHERE project_id = $1 AND id = $2
	`, projectID, monitorID)
	if err != nil {
		return fmt.Errorf("delete uptime monitor: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return ErrUptimeMonitorNotFound
	}
	return nil
}

func (s *Store) RecordUptimeCheck(
	monitorID, projectID int64,
	statusCode *int,
	responseTimeMs int,
	isUp bool,
	errorMessage string,
	sslIssuer string,
	sslExpiresAt *time.Time,
) error {
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Insert check
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO uptime_checks (
			monitor_id, project_id, checked_at, status_code,
			response_time_ms, is_up, error_message
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, monitorID, projectID, now, statusCode, responseTimeMs, isUp, errorMessage)
	if err != nil {
		return fmt.Errorf("insert uptime check: %w", err)
	}

	// 2. Compute uptime percentage over past 30 days
	var uptimePct float64 = 100.0
	var totalChecks, upChecks int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(CASE WHEN is_up THEN 1 END)
		FROM uptime_checks
		WHERE monitor_id = $1 AND checked_at >= NOW() - INTERVAL '30 days'
	`, monitorID).Scan(&totalChecks, &upChecks)
	if totalChecks > 0 {
		uptimePct = float64(upChecks) / float64(totalChecks) * 100.0
	}

	status := "up"
	if !isUp {
		status = "down"
	}

	// 3. Update monitor record
	_, err = s.db.ExecContext(ctx, `
		UPDATE uptime_monitors
		SET last_checked_at = $1,
		    status = $2,
		    ssl_issuer = COALESCE(NULLIF($3, ''), ssl_issuer),
		    ssl_expires_at = COALESCE($4, ssl_expires_at),
		    uptime_percentage = $5,
		    updated_at = NOW()
		WHERE id = $6
	`, now, status, sslIssuer, sslExpiresAt, uptimePct, monitorID)
	if err != nil {
		return fmt.Errorf("update monitor status: %w", err)
	}

	return nil
}

func (s *Store) GetUptimeStats(projectID int64) (*UptimeStats, error) {
	ctx := context.Background()
	var stats UptimeStats
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(CASE WHEN status = 'up' THEN 1 END),
		       COUNT(CASE WHEN status = 'degraded' THEN 1 END),
		       COUNT(CASE WHEN status = 'down' THEN 1 END),
		       COALESCE(AVG(uptime_percentage), 100.0)
		FROM uptime_monitors
		WHERE project_id = $1
	`, projectID).Scan(
		&stats.TotalMonitors,
		&stats.UpCount,
		&stats.DegradedCount,
		&stats.DownCount,
		&stats.AvgUptimePct,
	)
	if err != nil {
		return nil, fmt.Errorf("get uptime stats: %w", err)
	}
	return &stats, nil
}

func (s *Store) GetUptimeHistory(projectID, monitorID int64, days int) (*UptimeHistoryDetail, error) {
	if days <= 0 {
		days = 90
	}
	monitor, err := s.GetUptimeMonitor(projectID, monitorID)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	// 1. Daily aggregation
	rows, err := s.db.QueryContext(ctx, `
		SELECT TO_CHAR(checked_at, 'YYYY-MM-DD') AS day,
		       COUNT(*) AS total_count,
		       COUNT(CASE WHEN is_up THEN 1 END) AS up_count,
		       COALESCE(AVG(response_time_ms), 0)::int AS avg_ms
		FROM uptime_checks
		WHERE monitor_id = $1 AND checked_at >= NOW() - ($2 * INTERVAL '1 day')
		GROUP BY day
		ORDER BY day ASC
	`, monitorID, days)
	if err != nil {
		return nil, fmt.Errorf("query daily history: %w", err)
	}
	defer rows.Close()

	dailyMap := map[string]*UptimeDaySummary{}
	for rows.Next() {
		var day string
		var total, up, avgMs int
		if err := rows.Scan(&day, &total, &up, &avgMs); err != nil {
			return nil, fmt.Errorf("scan daily history: %w", err)
		}
		status := "up"
		var pct float64 = 100.0
		if total > 0 {
			pct = float64(up) / float64(total) * 100.0
			if up == 0 {
				status = "down"
			} else if up < total {
				status = "degraded"
			}
		}
		dailyMap[day] = &UptimeDaySummary{
			Date:      day,
			Status:    status,
			AvgMs:     avgMs,
			UptimePct: pct,
		}
	}

	// Fill contiguous days for the requested window
	var history90d []*UptimeDaySummary
	now := time.Now().UTC()
	for i := days - 1; i >= 0; i-- {
		dayStr := now.AddDate(0, 0, -i).Format("2006-01-02")
		if summary, exists := dailyMap[dayStr]; exists {
			history90d = append(history90d, summary)
		} else {
			history90d = append(history90d, &UptimeDaySummary{
				Date:      dayStr,
				Status:    "none",
				AvgMs:     0,
				UptimePct: 100.0,
			})
		}
	}

	// 2. Recent checks (limit 50)
	checkRows, err := s.db.QueryContext(ctx, `
		SELECT id, monitor_id, project_id, checked_at, status_code,
		       response_time_ms, is_up, error_message
		FROM uptime_checks
		WHERE monitor_id = $1
		ORDER BY checked_at DESC
		LIMIT 50
	`, monitorID)
	if err != nil {
		return nil, fmt.Errorf("query recent checks: %w", err)
	}
	defer checkRows.Close()

	var recentChecks []*UptimeCheck
	currentMs := 0
	for checkRows.Next() {
		var c UptimeCheck
		if err := checkRows.Scan(
			&c.ID, &c.MonitorID, &c.ProjectID, &c.CheckedAt, &c.StatusCode,
			&c.ResponseTimeMs, &c.IsUp, &c.ErrorMessage,
		); err != nil {
			return nil, fmt.Errorf("scan check: %w", err)
		}
		if len(recentChecks) == 0 {
			currentMs = c.ResponseTimeMs
		}
		recentChecks = append(recentChecks, &c)
	}

	// 3. SSL Info
	var sslInfo *UptimeSSLInfo
	if monitor.SSLCheckEnabled && monitor.SSLExpiresAt != nil {
		daysRemaining := int(time.Until(*monitor.SSLExpiresAt).Hours() / 24)
		valid := time.Now().Before(*monitor.SSLExpiresAt)
		sslInfo = &UptimeSSLInfo{
			Valid:         valid,
			ExpiresAt:     monitor.SSLExpiresAt,
			DaysRemaining: daysRemaining,
			Issuer:        monitor.SSLIssuer,
		}
	}

	return &UptimeHistoryDetail{
		Monitor:               monitor,
		Status:                monitor.Status,
		UptimePercentage:      monitor.UptimePercentage,
		CurrentResponseTimeMs: currentMs,
		SSL:                   sslInfo,
		History90d:            history90d,
		RecentChecks:          recentChecks,
	}, nil
}

func (s *Store) GetMonitorsDueForCheck() ([]*UptimeMonitor, error) {
	ctx := context.Background()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, name, url, method, headers, expected_status_code,
		       interval_seconds, timeout_seconds, status, ssl_check_enabled,
		       ssl_issuer, ssl_expires_at, last_checked_at, uptime_percentage,
		       created_at, updated_at
		FROM uptime_monitors
		WHERE last_checked_at IS NULL
		   OR last_checked_at + (interval_seconds * INTERVAL '1 second') <= NOW()
	`)
	if err != nil {
		return nil, fmt.Errorf("monitors due for check: %w", err)
	}
	defer rows.Close()

	var monitors []*UptimeMonitor
	for rows.Next() {
		var m UptimeMonitor
		var headersJSON []byte
		if err := rows.Scan(
			&m.ID, &m.ProjectID, &m.Name, &m.URL, &m.Method, &headersJSON,
			&m.ExpectedStatusCode, &m.IntervalSeconds, &m.TimeoutSeconds,
			&m.Status, &m.SSLCheckEnabled, &m.SSLIssuer, &m.SSLExpiresAt,
			&m.LastCheckedAt, &m.UptimePercentage, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan monitor due: %w", err)
		}
		if len(headersJSON) > 0 {
			_ = json.Unmarshal(headersJSON, &m.Headers)
		}
		if m.Headers == nil {
			m.Headers = map[string]string{}
		}
		monitors = append(monitors, &m)
	}
	return monitors, rows.Err()
}
