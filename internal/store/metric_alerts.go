// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"sightpane/internal/apierr"
)

var (
	ErrMetricAlertRuleNotFound = apierr.New(404, "METRIC_ALERT_RULE_NOT_FOUND", "Metric alert rule not found")
)

type MetricAlertRule struct {
	ID                 int64     `json:"id"`
	ProjectID          int64     `json:"project_id"`
	Name               string    `json:"name"`
	MetricType         string    `json:"metric_type"` // error_count, error_rate, transaction_duration_p95, unhandled_crash_count
	TargetFilter       string    `json:"target_filter"`
	ComparisonOperator string    `json:"comparison_operator"` // gt, gte, lt, spike_multiplier
	CriticalThreshold  float64   `json:"critical_threshold"`
	WarningThreshold   *float64  `json:"warning_threshold,omitempty"`
	WindowMinutes      int       `json:"window_minutes"` // 1, 5, 10, 15, 30, 60
	ChannelIDs         []int64   `json:"channel_ids"`
	IsActive           bool      `json:"is_active"`
	CurrentStatus      string    `json:"current_status"` // ok, warning, firing
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type MetricAlertIncident struct {
	ID          int64      `json:"id"`
	RuleID      int64      `json:"rule_id"`
	ProjectID   int64      `json:"project_id"`
	Status      string     `json:"status"` // firing, resolved
	TriggeredAt time.Time  `json:"triggered_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	PeakValue   float64    `json:"peak_value"`
	Summary     string     `json:"summary"`
	RuleName    string     `json:"rule_name,omitempty"`
}

type MetricHistoryPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

func (s *Store) ListMetricAlertRules(projectID int64) ([]MetricAlertRule, error) {
	rows, err := s.db.Query(`SELECT id, project_id, name, metric_type, target_filter, comparison_operator,
		critical_threshold, warning_threshold, window_minutes, channel_ids, is_active, current_status, created_at, updated_at
		FROM metric_alert_rules WHERE project_id=$1 ORDER BY id ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []MetricAlertRule
	for rows.Next() {
		var r MetricAlertRule
		var channelsRaw []byte
		var warnThresh sql.NullFloat64
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Name, &r.MetricType, &r.TargetFilter, &r.ComparisonOperator,
			&r.CriticalThreshold, &warnThresh, &r.WindowMinutes, &channelsRaw, &r.IsActive, &r.CurrentStatus,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if warnThresh.Valid {
			r.WarningThreshold = &warnThresh.Float64
		}
		r.ChannelIDs = []int64{}
		if len(channelsRaw) > 0 {
			_ = json.Unmarshal(channelsRaw, &r.ChannelIDs)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func (s *Store) ListAllActiveMetricAlertRules() ([]MetricAlertRule, error) {
	rows, err := s.db.Query(`SELECT id, project_id, name, metric_type, target_filter, comparison_operator,
		critical_threshold, warning_threshold, window_minutes, channel_ids, is_active, current_status, created_at, updated_at
		FROM metric_alert_rules WHERE is_active=true ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []MetricAlertRule
	for rows.Next() {
		var r MetricAlertRule
		var channelsRaw []byte
		var warnThresh sql.NullFloat64
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Name, &r.MetricType, &r.TargetFilter, &r.ComparisonOperator,
			&r.CriticalThreshold, &warnThresh, &r.WindowMinutes, &channelsRaw, &r.IsActive, &r.CurrentStatus,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if warnThresh.Valid {
			r.WarningThreshold = &warnThresh.Float64
		}
		r.ChannelIDs = []int64{}
		if len(channelsRaw) > 0 {
			_ = json.Unmarshal(channelsRaw, &r.ChannelIDs)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func (s *Store) GetMetricAlertRule(projectID, ruleID int64) (*MetricAlertRule, error) {
	var r MetricAlertRule
	var channelsRaw []byte
	var warnThresh sql.NullFloat64
	err := s.db.QueryRow(`SELECT id, project_id, name, metric_type, target_filter, comparison_operator,
		critical_threshold, warning_threshold, window_minutes, channel_ids, is_active, current_status, created_at, updated_at
		FROM metric_alert_rules WHERE project_id=$1 AND id=$2`, projectID, ruleID).
		Scan(&r.ID, &r.ProjectID, &r.Name, &r.MetricType, &r.TargetFilter, &r.ComparisonOperator,
			&r.CriticalThreshold, &warnThresh, &r.WindowMinutes, &channelsRaw, &r.IsActive, &r.CurrentStatus,
			&r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMetricAlertRuleNotFound
	}
	if err != nil {
		return nil, err
	}
	if warnThresh.Valid {
		r.WarningThreshold = &warnThresh.Float64
	}
	r.ChannelIDs = []int64{}
	if len(channelsRaw) > 0 {
		_ = json.Unmarshal(channelsRaw, &r.ChannelIDs)
	}
	return &r, nil
}

func (s *Store) CreateMetricAlertRule(r *MetricAlertRule) (*MetricAlertRule, error) {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return nil, apierr.New(400, "INVALID_RULE_NAME", "Rule name is required")
	}
	metricType := strings.TrimSpace(r.MetricType)
	switch metricType {
	case "error_count", "error_rate", "transaction_duration_p95", "unhandled_crash_count":
	default:
		return nil, apierr.New(400, "INVALID_METRIC_TYPE", "metric_type must be error_count, error_rate, transaction_duration_p95, or unhandled_crash_count")
	}
	compOp := strings.TrimSpace(r.ComparisonOperator)
	switch compOp {
	case "gt", "gte", "lt", "spike_multiplier":
	default:
		return nil, apierr.New(400, "INVALID_OPERATOR", "comparison_operator must be gt, gte, lt, or spike_multiplier")
	}
	if r.WindowMinutes <= 0 {
		r.WindowMinutes = 5
	}
	if r.ChannelIDs == nil {
		r.ChannelIDs = []int64{}
	}
	chBytes, _ := json.Marshal(r.ChannelIDs)

	now := time.Now().UTC()
	var warnVal any = nil
	if r.WarningThreshold != nil {
		warnVal = *r.WarningThreshold
	}

	var id int64
	err := s.db.QueryRow(`INSERT INTO metric_alert_rules(
		project_id, name, metric_type, target_filter, comparison_operator,
		critical_threshold, warning_threshold, window_minutes, channel_ids, is_active, current_status, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'ok',$11,$11) RETURNING id`,
		r.ProjectID, name, metricType, strings.TrimSpace(r.TargetFilter), compOp,
		r.CriticalThreshold, warnVal, r.WindowMinutes, chBytes, r.IsActive, now).Scan(&id)
	if err != nil {
		return nil, err
	}
	r.ID = id
	r.CurrentStatus = "ok"
	r.CreatedAt = now
	r.UpdatedAt = now
	return r, nil
}

func (s *Store) UpdateMetricAlertRule(r *MetricAlertRule) (*MetricAlertRule, error) {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return nil, apierr.New(400, "INVALID_RULE_NAME", "Rule name is required")
	}
	metricType := strings.TrimSpace(r.MetricType)
	switch metricType {
	case "error_count", "error_rate", "transaction_duration_p95", "unhandled_crash_count":
	default:
		return nil, apierr.New(400, "INVALID_METRIC_TYPE", "metric_type must be error_count, error_rate, transaction_duration_p95, or unhandled_crash_count")
	}
	compOp := strings.TrimSpace(r.ComparisonOperator)
	switch compOp {
	case "gt", "gte", "lt", "spike_multiplier":
	default:
		return nil, apierr.New(400, "INVALID_OPERATOR", "comparison_operator must be gt, gte, lt, or spike_multiplier")
	}
	if r.WindowMinutes <= 0 {
		r.WindowMinutes = 5
	}
	if r.ChannelIDs == nil {
		r.ChannelIDs = []int64{}
	}
	chBytes, _ := json.Marshal(r.ChannelIDs)

	now := time.Now().UTC()
	var warnVal any = nil
	if r.WarningThreshold != nil {
		warnVal = *r.WarningThreshold
	}

	res, err := s.db.Exec(`UPDATE metric_alert_rules SET
		name=$1, metric_type=$2, target_filter=$3, comparison_operator=$4,
		critical_threshold=$5, warning_threshold=$6, window_minutes=$7, channel_ids=$8, is_active=$9, updated_at=$10
		WHERE project_id=$11 AND id=$12`,
		name, metricType, strings.TrimSpace(r.TargetFilter), compOp,
		r.CriticalThreshold, warnVal, r.WindowMinutes, chBytes, r.IsActive, now, r.ProjectID, r.ID)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrMetricAlertRuleNotFound
	}
	r.UpdatedAt = now
	return r, nil
}

func (s *Store) DeleteMetricAlertRule(projectID, ruleID int64) error {
	res, err := s.db.Exec(`DELETE FROM metric_alert_rules WHERE project_id=$1 AND id=$2`, projectID, ruleID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrMetricAlertRuleNotFound
	}
	return nil
}

func (s *Store) UpdateMetricRuleStatus(ruleID int64, status string) error {
	_, err := s.db.Exec(`UPDATE metric_alert_rules SET current_status=$1, updated_at=NOW() WHERE id=$2`, status, ruleID)
	return err
}

// Incident operations

func (s *Store) ListMetricAlertIncidents(projectID int64, ruleID *int64, limit int) ([]MetricAlertIncident, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT i.id, i.rule_id, i.project_id, i.status, i.triggered_at, i.resolved_at, i.peak_value, i.summary, r.name
		FROM metric_alert_incidents i
		JOIN metric_alert_rules r ON r.id = i.rule_id
		WHERE i.project_id=$1`
	args := []any{projectID}
	if ruleID != nil {
		query += " AND i.rule_id=$2"
		args = append(args, *ruleID)
	}
	query += fmt.Sprintf(" ORDER BY i.triggered_at DESC LIMIT %d", limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var incidents []MetricAlertIncident
	for rows.Next() {
		var inc MetricAlertIncident
		var resolvedAt sql.NullTime
		if err := rows.Scan(&inc.ID, &inc.RuleID, &inc.ProjectID, &inc.Status, &inc.TriggeredAt, &resolvedAt,
			&inc.PeakValue, &inc.Summary, &inc.RuleName); err != nil {
			return nil, err
		}
		if resolvedAt.Valid {
			inc.ResolvedAt = &resolvedAt.Time
		}
		incidents = append(incidents, inc)
	}
	return incidents, rows.Err()
}

func (s *Store) GetActiveIncidentForRule(ruleID int64) (*MetricAlertIncident, error) {
	var inc MetricAlertIncident
	var resolvedAt sql.NullTime
	err := s.db.QueryRow(`SELECT id, rule_id, project_id, status, triggered_at, resolved_at, peak_value, summary
		FROM metric_alert_incidents WHERE rule_id=$1 AND status='firing' ORDER BY triggered_at DESC LIMIT 1`, ruleID).
		Scan(&inc.ID, &inc.RuleID, &inc.ProjectID, &inc.Status, &inc.TriggeredAt, &resolvedAt, &inc.PeakValue, &inc.Summary)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if resolvedAt.Valid {
		inc.ResolvedAt = &resolvedAt.Time
	}
	return &inc, nil
}

func (s *Store) CreateMetricIncident(ruleID, projectID int64, peakValue float64, summary string) (*MetricAlertIncident, error) {
	now := time.Now().UTC()
	var id int64
	err := s.db.QueryRow(`INSERT INTO metric_alert_incidents(rule_id, project_id, status, triggered_at, peak_value, summary)
		VALUES ($1,$2,'firing',$3,$4,$5) RETURNING id`, ruleID, projectID, now, peakValue, summary).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &MetricAlertIncident{
		ID:          id,
		RuleID:      ruleID,
		ProjectID:   projectID,
		Status:      "firing",
		TriggeredAt: now,
		PeakValue:   peakValue,
		Summary:     summary,
	}, nil
}

func (s *Store) UpdateIncidentPeak(incidentID int64, peakValue float64) error {
	_, err := s.db.Exec(`UPDATE metric_alert_incidents SET peak_value=GREATEST(peak_value, $1) WHERE id=$2`, peakValue, incidentID)
	return err
}

func (s *Store) ResolveActiveIncident(incidentID int64, peakValue float64) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`UPDATE metric_alert_incidents SET status='resolved', resolved_at=$1, peak_value=GREATEST(peak_value, $2)
		WHERE id=$3 AND status='firing'`, now, peakValue, incidentID)
	return err
}

// Metric evaluation queries

func (s *Store) EvaluateMetricValue(projectID int64, metricType, targetFilter string, windowMinutes int) (float64, int, error) {
	if windowMinutes <= 0 {
		windowMinutes = 5
	}
	from := time.Now().UTC().Add(-time.Duration(windowMinutes) * time.Minute)

	switch metricType {
	case "error_count":
		query := `SELECT COUNT(*) FROM items WHERE project_id=$1 AND type='error' AND ts>=$2`
		args := []any{projectID, from}
		if targetFilter != "" {
			if strings.HasPrefix(targetFilter, "route:") {
				r := strings.TrimPrefix(targetFilter, "route:")
				query += ` AND session_id IN (SELECT id FROM sessions WHERE project_id=$1 AND current_route=$3)`
				args = append(args, r)
			} else if strings.HasPrefix(targetFilter, "platform:") {
				p := strings.TrimPrefix(targetFilter, "platform:")
				query += ` AND session_id IN (SELECT id FROM sessions WHERE project_id=$1 AND platform=$3)`
				args = append(args, p)
			} else {
				query += ` AND (name ILIKE $3 OR body_json ILIKE $3)`
				args = append(args, "%"+targetFilter+"%")
			}
		}
		var count int
		if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
			return 0, 0, err
		}
		return float64(count), count, nil

	case "unhandled_crash_count":
		query := `SELECT COUNT(*) FROM items WHERE project_id=$1 AND (type='unhandled_crash' OR (type='error' AND body_json ILIKE '%"unhandled":true%')) AND ts>=$2`
		args := []any{projectID, from}
		if targetFilter != "" {
			if strings.HasPrefix(targetFilter, "platform:") {
				p := strings.TrimPrefix(targetFilter, "platform:")
				query += ` AND session_id IN (SELECT id FROM sessions WHERE project_id=$1 AND platform=$3)`
				args = append(args, p)
			} else {
				query += ` AND (name ILIKE $3 OR body_json ILIKE $3)`
				args = append(args, "%"+targetFilter+"%")
			}
		}
		var count int
		if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
			return 0, 0, err
		}
		return float64(count), count, nil

	case "error_rate":
		// errors / sessions * 100%
		var sessCount int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE project_id=$1 AND started_at>=$2`, projectID, from).Scan(&sessCount); err != nil {
			return 0, 0, err
		}
		var errCount int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM items WHERE project_id=$1 AND type='error' AND ts>=$2`, projectID, from).Scan(&errCount); err != nil {
			return 0, 0, err
		}
		if sessCount == 0 {
			return 0.0, 0, nil
		}
		rate := (float64(errCount) / float64(sessCount)) * 100.0
		return rate, sessCount, nil

	case "transaction_duration_p95":
		query := `SELECT COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0)
			FROM spans WHERE project_id=$1 AND ts>=$2`
		args := []any{projectID, from}
		if targetFilter != "" {
			if strings.HasPrefix(targetFilter, "op:") {
				op := strings.TrimPrefix(targetFilter, "op:")
				query += ` AND op=$3`
				args = append(args, op)
			} else {
				query += ` AND (name ILIKE $3 OR op ILIKE $3)`
				args = append(args, "%"+targetFilter+"%")
			}
		}
		var p95 float64
		if err := s.db.QueryRow(query, args...).Scan(&p95); err != nil {
			return 0, 0, err
		}
		return p95, 1, nil

	default:
		return 0, 0, fmt.Errorf("unknown metric type: %s", metricType)
	}
}

// EvaluateMetricBaseline calculates the historical baseline for this window, comparing to t-7d and t-14d.
func (s *Store) EvaluateMetricBaseline(projectID int64, metricType, targetFilter string, windowMinutes int) (float64, error) {
	if windowMinutes <= 0 {
		windowMinutes = 5
	}
	// Historical windows: exactly 7 days ago and 14 days ago
	dur := time.Duration(windowMinutes) * time.Minute
	t7Start := time.Now().UTC().Add(-7 * 24 * time.Hour)
	t7End := t7Start.Add(dur)

	t14Start := time.Now().UTC().Add(-14 * 24 * time.Hour)
	t14End := t14Start.Add(dur)

	val7, err := s.evaluateMetricRange(projectID, metricType, targetFilter, t7Start, t7End)
	if err != nil {
		return 0, err
	}
	val14, err := s.evaluateMetricRange(projectID, metricType, targetFilter, t14Start, t14End)
	if err != nil {
		return 0, err
	}

	// If historical is zero, fallback to trailing 24h average per window
	if val7 == 0 && val14 == 0 {
		trailStart := time.Now().UTC().Add(-24 * time.Hour)
		trailEnd := time.Now().UTC().Add(-dur)
		total24h, _ := s.evaluateMetricRange(projectID, metricType, targetFilter, trailStart, trailEnd)
		numWindows := float64(24*60) / float64(windowMinutes)
		if numWindows > 0 {
			return total24h / numWindows, nil
		}
		return 0, nil
	}

	if val7 > 0 && val14 > 0 {
		return (val7 + val14) / 2.0, nil
	}
	if val7 > 0 {
		return val7, nil
	}
	return val14, nil
}

func (s *Store) evaluateMetricRange(projectID int64, metricType, targetFilter string, start, end time.Time) (float64, error) {
	switch metricType {
	case "error_count":
		var count int
		err := s.db.QueryRow(`SELECT COUNT(*) FROM items WHERE project_id=$1 AND type='error' AND ts>=$2 AND ts<$3`,
			projectID, start, end).Scan(&count)
		return float64(count), err
	case "unhandled_crash_count":
		var count int
		err := s.db.QueryRow(`SELECT COUNT(*) FROM items WHERE project_id=$1 AND (type='unhandled_crash' OR (type='error' AND body_json ILIKE '%"unhandled":true%')) AND ts>=$2 AND ts<$3`,
			projectID, start, end).Scan(&count)
		return float64(count), err
	case "error_rate":
		var sess, errs int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE project_id=$1 AND started_at>=$2 AND started_at<$3`,
			projectID, start, end).Scan(&sess)
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM items WHERE project_id=$1 AND type='error' AND ts>=$2 AND ts<$3`,
			projectID, start, end).Scan(&errs)
		if sess == 0 {
			return 0, nil
		}
		return (float64(errs) / float64(sess)) * 100.0, nil
	case "transaction_duration_p95":
		var p95 float64
		err := s.db.QueryRow(`SELECT COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0)
			FROM spans WHERE project_id=$1 AND ts>=$2 AND ts<$3`, projectID, start, end).Scan(&p95)
		return p95, err
	default:
		return 0, nil
	}
}

// GetMetricHistoryPreview provides metric points over the specified days for visual preview
func (s *Store) GetMetricHistoryPreview(projectID int64, metricType, targetFilter string, windowMinutes, days int) ([]MetricHistoryPoint, error) {
	if days <= 0 {
		days = 7
	}
	if windowMinutes <= 0 {
		windowMinutes = 60 // 1-hour buckets for preview
	}
	now := time.Now().UTC()
	start := now.Add(-time.Duration(days) * 24 * time.Hour)

	bucketDur := time.Duration(windowMinutes) * time.Minute
	numBuckets := int(now.Sub(start) / bucketDur)
	if numBuckets > 168 { // Cap at 168 points (7 days of hourly buckets)
		bucketDur = now.Sub(start) / 168
		numBuckets = 168
	}

	points := make([]MetricHistoryPoint, 0, numBuckets)
	for t := start; t.Before(now); t = t.Add(bucketDur) {
		bEnd := t.Add(bucketDur)
		if bEnd.After(now) {
			bEnd = now
		}
		val, _ := s.evaluateMetricRange(projectID, metricType, targetFilter, t, bEnd)
		points = append(points, MetricHistoryPoint{
			Timestamp: t,
			Value:     val,
		})
	}
	return points, nil
}
