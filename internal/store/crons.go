// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

var ErrCronMonitorNotFound = errors.New("cron monitor not found")

var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// ComputeNextExpected calculates the next execution time given a crontab schedule and timezone.
func ComputeNextExpected(scheduleStr, timezoneStr string, from time.Time) (time.Time, error) {
	sched, err := cronParser.Parse(scheduleStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron schedule %q: %w", scheduleStr, err)
	}
	loc := time.UTC
	if timezoneStr != "" {
		if l, err := time.LoadLocation(timezoneStr); err == nil {
			loc = l
		}
	}
	fromInLoc := from.In(loc)
	next := sched.Next(fromInLoc)
	return next.UTC(), nil
}

type CronMonitor struct {
	ID                 int64      `json:"id"`
	ProjectID          int64      `json:"project_id"`
	Slug               string     `json:"slug"`
	Name               string     `json:"name"`
	Schedule           string     `json:"schedule"`
	Timezone           string     `json:"timezone"`
	GracePeriodMinutes int        `json:"grace_period_minutes"`
	MaxRuntimeMinutes  int        `json:"max_runtime_minutes"`
	Status             string     `json:"status"` // 'ok', 'in_progress', 'error', 'missed'
	LastCheckinAt      *time.Time `json:"last_checkin_at"`
	NextExpectedAt     *time.Time `json:"next_expected_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type CronCheckin struct {
	ID         int64     `json:"id"`
	MonitorID  int64     `json:"monitor_id"`
	ProjectID  int64     `json:"project_id"`
	Status     string    `json:"status"` // 'in_progress', 'ok', 'error'
	DurationMs *int      `json:"duration_ms,omitempty"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

type CronStats struct {
	TotalMonitors   int `json:"total_monitors"`
	OkCount         int `json:"ok_count"`
	InProgressCount int `json:"in_progress_count"`
	ErrorCount      int `json:"error_count"`
	MissedCount     int `json:"missed_count"`
}

func (s *Store) ListCronMonitors(projectID int64) ([]*CronMonitor, error) {
	ctx := context.Background()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, slug, name, schedule, timezone, grace_period_minutes,
		       max_runtime_minutes, status, last_checkin_at, next_expected_at, created_at, updated_at
		FROM cron_monitors
		WHERE project_id = $1
		ORDER BY name ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list cron monitors: %w", err)
	}
	defer rows.Close()

	var monitors []*CronMonitor
	for rows.Next() {
		var m CronMonitor
		if err := rows.Scan(
			&m.ID, &m.ProjectID, &m.Slug, &m.Name, &m.Schedule, &m.Timezone,
			&m.GracePeriodMinutes, &m.MaxRuntimeMinutes, &m.Status,
			&m.LastCheckinAt, &m.NextExpectedAt, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan cron monitor: %w", err)
		}
		monitors = append(monitors, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return monitors, nil
}

func (s *Store) GetCronMonitor(projectID, monitorID int64) (*CronMonitor, error) {
	ctx := context.Background()
	var m CronMonitor
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, slug, name, schedule, timezone, grace_period_minutes,
		       max_runtime_minutes, status, last_checkin_at, next_expected_at, created_at, updated_at
		FROM cron_monitors
		WHERE project_id = $1 AND id = $2
	`, projectID, monitorID).Scan(
		&m.ID, &m.ProjectID, &m.Slug, &m.Name, &m.Schedule, &m.Timezone,
		&m.GracePeriodMinutes, &m.MaxRuntimeMinutes, &m.Status,
		&m.LastCheckinAt, &m.NextExpectedAt, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCronMonitorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get cron monitor: %w", err)
	}
	return &m, nil
}

func (s *Store) GetCronMonitorBySlug(projectID int64, slug string) (*CronMonitor, error) {
	ctx := context.Background()
	var m CronMonitor
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, slug, name, schedule, timezone, grace_period_minutes,
		       max_runtime_minutes, status, last_checkin_at, next_expected_at, created_at, updated_at
		FROM cron_monitors
		WHERE project_id = $1 AND slug = $2
	`, projectID, slug).Scan(
		&m.ID, &m.ProjectID, &m.Slug, &m.Name, &m.Schedule, &m.Timezone,
		&m.GracePeriodMinutes, &m.MaxRuntimeMinutes, &m.Status,
		&m.LastCheckinAt, &m.NextExpectedAt, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCronMonitorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get cron monitor by slug: %w", err)
	}
	return &m, nil
}

func (s *Store) CreateCronMonitor(m *CronMonitor) (*CronMonitor, error) {
	if m.GracePeriodMinutes <= 0 {
		m.GracePeriodMinutes = 15
	}
	if m.MaxRuntimeMinutes <= 0 {
		m.MaxRuntimeMinutes = 60
	}
	if m.Status == "" {
		m.Status = "ok"
	}
	if m.Timezone == "" {
		m.Timezone = "UTC"
	}

	next, err := ComputeNextExpected(m.Schedule, m.Timezone, time.Now())
	if err != nil {
		return nil, err
	}
	m.NextExpectedAt = &next

	ctx := context.Background()
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO cron_monitors (
			project_id, slug, name, schedule, timezone, grace_period_minutes,
			max_runtime_minutes, status, next_expected_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		RETURNING id, created_at, updated_at
	`, m.ProjectID, m.Slug, m.Name, m.Schedule, m.Timezone,
		m.GracePeriodMinutes, m.MaxRuntimeMinutes, m.Status, m.NextExpectedAt,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create cron monitor: %w", err)
	}
	return m, nil
}

func (s *Store) UpdateCronMonitor(m *CronMonitor) (*CronMonitor, error) {
	if m.GracePeriodMinutes <= 0 {
		m.GracePeriodMinutes = 15
	}
	if m.MaxRuntimeMinutes <= 0 {
		m.MaxRuntimeMinutes = 60
	}
	if m.Timezone == "" {
		m.Timezone = "UTC"
	}

	next, err := ComputeNextExpected(m.Schedule, m.Timezone, time.Now())
	if err != nil {
		return nil, err
	}
	m.NextExpectedAt = &next

	ctx := context.Background()
	res, err := s.db.ExecContext(ctx, `
		UPDATE cron_monitors
		SET name = $1, schedule = $2, timezone = $3, grace_period_minutes = $4,
		    max_runtime_minutes = $5, next_expected_at = $6, updated_at = NOW()
		WHERE project_id = $7 AND id = $8
	`, m.Name, m.Schedule, m.Timezone, m.GracePeriodMinutes, m.MaxRuntimeMinutes,
		m.NextExpectedAt, m.ProjectID, m.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("update cron monitor: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrCronMonitorNotFound
	}
	return m, nil
}

func (s *Store) DeleteCronMonitor(projectID, monitorID int64) error {
	ctx := context.Background()
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM cron_monitors
		WHERE project_id = $1 AND id = $2
	`, projectID, monitorID)
	if err != nil {
		return fmt.Errorf("delete cron monitor: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCronMonitorNotFound
	}
	return nil
}

func (s *Store) RecordCronCheckin(projectID int64, slug string, status string, durationMs *int, message string) (*CronCheckin, *CronMonitor, error) {
	monitor, err := s.GetCronMonitorBySlug(projectID, slug)
	if err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC()
	ctx := context.Background()

	var checkin CronCheckin
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO cron_checkins (monitor_id, project_id, status, duration_ms, message, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, monitor_id, project_id, status, duration_ms, message, created_at
	`, monitor.ID, projectID, status, durationMs, message, now).Scan(
		&checkin.ID, &checkin.MonitorID, &checkin.ProjectID, &checkin.Status,
		&checkin.DurationMs, &checkin.Message, &checkin.CreatedAt,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("record cron checkin: %w", err)
	}

	monitor.Status = status
	monitor.LastCheckinAt = &now
	if status == "ok" || status == "error" {
		if next, err := ComputeNextExpected(monitor.Schedule, monitor.Timezone, now); err == nil {
			monitor.NextExpectedAt = &next
		}
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE cron_monitors
		SET status = $1, last_checkin_at = $2, next_expected_at = $3, updated_at = $4
		WHERE id = $5
	`, monitor.Status, monitor.LastCheckinAt, monitor.NextExpectedAt, now, monitor.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("update monitor status on checkin: %w", err)
	}

	return &checkin, monitor, nil
}

func (s *Store) ListCronCheckins(projectID, monitorID int64, limit int) ([]*CronCheckin, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	ctx := context.Background()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, monitor_id, project_id, status, duration_ms, message, created_at
		FROM cron_checkins
		WHERE project_id = $1 AND monitor_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, projectID, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("list cron checkins: %w", err)
	}
	defer rows.Close()

	var checkins []*CronCheckin
	for rows.Next() {
		var c CronCheckin
		if err := rows.Scan(
			&c.ID, &c.MonitorID, &c.ProjectID, &c.Status,
			&c.DurationMs, &c.Message, &c.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan cron checkin: %w", err)
		}
		checkins = append(checkins, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return checkins, nil
}

func (s *Store) EvaluateCronDeadlines() ([]*CronMonitor, error) {
	ctx := context.Background()
	now := time.Now().UTC()

	var alerted []*CronMonitor

	// 1. Detect missed runs: where now > next_expected_at + grace_period_minutes
	missedRows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, slug, name, schedule, timezone, grace_period_minutes,
		       max_runtime_minutes, status, last_checkin_at, next_expected_at, created_at, updated_at
		FROM cron_monitors
		WHERE status != 'missed'
		  AND next_expected_at IS NOT NULL
		  AND $1 > next_expected_at + (grace_period_minutes * INTERVAL '1 minute')
	`, now)
	if err == nil {
		defer missedRows.Close()
		for missedRows.Next() {
			var m CronMonitor
			if err := missedRows.Scan(
				&m.ID, &m.ProjectID, &m.Slug, &m.Name, &m.Schedule, &m.Timezone,
				&m.GracePeriodMinutes, &m.MaxRuntimeMinutes, &m.Status,
				&m.LastCheckinAt, &m.NextExpectedAt, &m.CreatedAt, &m.UpdatedAt,
			); err == nil {
				m.Status = "missed"
				if next, err := ComputeNextExpected(m.Schedule, m.Timezone, now); err == nil {
					m.NextExpectedAt = &next
				}
				// Mark monitor as missed
				s.db.ExecContext(ctx, `
					UPDATE cron_monitors
					SET status = 'missed', next_expected_at = $1, updated_at = $2
					WHERE id = $3
				`, m.NextExpectedAt, now, m.ID)

				// Record checkin indicating missed run
				s.db.ExecContext(ctx, `
					INSERT INTO cron_checkins (monitor_id, project_id, status, message, created_at)
					VALUES ($1, $2, 'error', 'Missed check-in deadline', $3)
				`, m.ID, m.ProjectID, now)

				alerted = append(alerted, &m)
			}
		}
	}

	// 2. Detect timeouts: where status == 'in_progress' and now > last_checkin_at + max_runtime_minutes
	timeoutRows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, slug, name, schedule, timezone, grace_period_minutes,
		       max_runtime_minutes, status, last_checkin_at, next_expected_at, created_at, updated_at
		FROM cron_monitors
		WHERE status = 'in_progress'
		  AND last_checkin_at IS NOT NULL
		  AND $1 > last_checkin_at + (max_runtime_minutes * INTERVAL '1 minute')
	`, now)
	if err == nil {
		defer timeoutRows.Close()
		for timeoutRows.Next() {
			var m CronMonitor
			if err := timeoutRows.Scan(
				&m.ID, &m.ProjectID, &m.Slug, &m.Name, &m.Schedule, &m.Timezone,
				&m.GracePeriodMinutes, &m.MaxRuntimeMinutes, &m.Status,
				&m.LastCheckinAt, &m.NextExpectedAt, &m.CreatedAt, &m.UpdatedAt,
			); err == nil {
				m.Status = "error"
				if next, err := ComputeNextExpected(m.Schedule, m.Timezone, now); err == nil {
					m.NextExpectedAt = &next
				}
				// Mark monitor as error
				s.db.ExecContext(ctx, `
					UPDATE cron_monitors
					SET status = 'error', next_expected_at = $1, updated_at = $2
					WHERE id = $3
				`, m.NextExpectedAt, now, m.ID)

				// Record checkin indicating timeout
				s.db.ExecContext(ctx, `
					INSERT INTO cron_checkins (monitor_id, project_id, status, message, created_at)
					VALUES ($1, $2, 'error', 'Job execution timed out', $3)
				`, m.ID, m.ProjectID, now)

				alerted = append(alerted, &m)
			}
		}
	}

	return alerted, nil
}

func (s *Store) GetCronStats(projectID int64) (*CronStats, error) {
	ctx := context.Background()
	var stats CronStats
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM cron_monitors
		WHERE project_id = $1
		GROUP BY status
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("get cron stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err == nil {
			stats.TotalMonitors += count
			switch status {
			case "ok":
				stats.OkCount = count
			case "in_progress":
				stats.InProgressCount = count
			case "error":
				stats.ErrorCount = count
			case "missed":
				stats.MissedCount = count
			}
		}
	}
	return &stats, nil
}
