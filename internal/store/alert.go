// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"sightpane/internal/apierr"
)

type AlertChannel struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // email, slack, webhook
	Target    string `json:"target"`
	Secret    string `json:"secret,omitempty"`
	CreatedAt string `json:"created_at"`
}

type AlertRule struct {
	ID         int64           `json:"id"`
	ProjectID  int64           `json:"project_id"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"` // new_issue, regression, rate_spike, session_crash_free
	Params     json.RawMessage `json:"params"`
	ChannelIDs []int64         `json:"channel_ids"`
	Enabled    bool            `json:"enabled"`
	CreatedAt  string          `json:"created_at"`
}

type AlertDelivery struct {
	ID          int64  `json:"id"`
	RuleID      int64  `json:"rule_id"`
	ChannelID   int64  `json:"channel_id"`
	IssueID     *int64 `json:"issue_id"`
	Fingerprint string `json:"fingerprint"`
	SentAt      string `json:"sent_at"`
	Status      string `json:"status"` // success, failed
	Error       string `json:"error"`
}

type IssueEvent struct {
	ProjectID   int64     `json:"project_id"`
	IssueID     int64     `json:"issue_id"`
	Fingerprint string    `json:"fingerprint"`
	Title       string    `json:"title"`
	Exception   string    `json:"exception"`
	Kind        string    `json:"kind"` // new_issue, regression
	Count       int       `json:"count"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	Route       string    `json:"route"`
	Browser     string    `json:"browser"`
}

var (
	ErrAlertChannelNotFound = apierr.New(404, apierr.CodeAlertChannelNotFound, "alert channel not found")
	ErrAlertRuleNotFound    = apierr.New(404, apierr.CodeAlertRuleNotFound, "alert rule not found")
)

func validateChannelKind(kind string) bool {
	return kind == "email" || kind == "slack" || kind == "webhook"
}

func validateRuleKind(kind string) bool {
	return kind == "new_issue" || kind == "regression" || kind == "rate_spike" || kind == "session_crash_free"
}

func (s *Store) ListAlertChannels(projectID int64) ([]AlertChannel, error) {
	rows, err := s.db.Query(`SELECT id, project_id, name, kind, target, secret, created_at
		FROM alert_channels WHERE project_id=$1 ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertChannel{}
	for rows.Next() {
		var c AlertChannel
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Kind, &c.Target, &c.Secret, tsCol{&c.CreatedAt}); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetAlertChannel(projectID, id int64) (*AlertChannel, error) {
	var c AlertChannel
	err := s.db.QueryRow(`SELECT id, project_id, name, kind, target, secret, created_at
		FROM alert_channels WHERE project_id=$1 AND id=$2`, projectID, id).
		Scan(&c.ID, &c.ProjectID, &c.Name, &c.Kind, &c.Target, &c.Secret, tsCol{&c.CreatedAt})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAlertChannelNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) CreateAlertChannel(projectID int64, name, kind, target, secret string) (*AlertChannel, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !validateChannelKind(kind) {
		return nil, apierr.New(400, apierr.CodeAlertChannelInvalid, "kind must be email, slack, or webhook")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, apierr.New(400, apierr.CodeAlertChannelInvalid, "target is required")
	}
	if name == "" {
		name = target
	}
	now := time.Now().UTC()
	var id int64
	err := s.db.QueryRow(`INSERT INTO alert_channels(project_id, name, kind, target, secret, created_at)
		VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, projectID, name, kind, target, secret, now).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &AlertChannel{
		ID:        id,
		ProjectID: projectID,
		Name:      name,
		Kind:      kind,
		Target:    target,
		Secret:    secret,
		CreatedAt: now.Format(time.RFC3339Nano),
	}, nil
}

func (s *Store) UpdateAlertChannel(projectID, id int64, name, kind, target, secret string) (*AlertChannel, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !validateChannelKind(kind) {
		return nil, apierr.New(400, apierr.CodeAlertChannelInvalid, "kind must be email, slack, or webhook")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, apierr.New(400, apierr.CodeAlertChannelInvalid, "target is required")
	}
	if name == "" {
		name = target
	}
	var c AlertChannel
	err := s.db.QueryRow(`UPDATE alert_channels SET name=$1, kind=$2, target=$3, secret=$4
		WHERE project_id=$5 AND id=$6 RETURNING id, project_id, name, kind, target, secret, created_at`,
		name, kind, target, secret, projectID, id).
		Scan(&c.ID, &c.ProjectID, &c.Name, &c.Kind, &c.Target, &c.Secret, tsCol{&c.CreatedAt})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAlertChannelNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) DeleteAlertChannel(projectID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM alert_channels WHERE project_id=$1 AND id=$2`, projectID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAlertChannelNotFound
	}
	return nil
}

func (s *Store) ListAlertRules(projectID int64) ([]AlertRule, error) {
	rows, err := s.db.Query(`SELECT id, project_id, name, kind, params_json, channel_ids_json, enabled, created_at
		FROM alert_rules WHERE project_id=$1 ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertRule{}
	for rows.Next() {
		var r AlertRule
		var paramsStr, channelsStr string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Name, &r.Kind, &paramsStr, &channelsStr, &r.Enabled, tsCol{&r.CreatedAt}); err != nil {
			return nil, err
		}
		r.Params = json.RawMessage(paramsStr)
		r.ChannelIDs = []int64{}
		_ = json.Unmarshal([]byte(channelsStr), &r.ChannelIDs)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetAlertRule(projectID, id int64) (*AlertRule, error) {
	var r AlertRule
	var paramsStr, channelsStr string
	err := s.db.QueryRow(`SELECT id, project_id, name, kind, params_json, channel_ids_json, enabled, created_at
		FROM alert_rules WHERE project_id=$1 AND id=$2`, projectID, id).
		Scan(&r.ID, &r.ProjectID, &r.Name, &r.Kind, &paramsStr, &channelsStr, &r.Enabled, tsCol{&r.CreatedAt})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAlertRuleNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Params = json.RawMessage(paramsStr)
	r.ChannelIDs = []int64{}
	_ = json.Unmarshal([]byte(channelsStr), &r.ChannelIDs)
	return &r, nil
}

func (s *Store) CreateAlertRule(projectID int64, name, kind string, params json.RawMessage, channelIDs []int64, enabled bool) (*AlertRule, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !validateRuleKind(kind) {
		return nil, apierr.New(400, apierr.CodeAlertRuleInvalid, "kind must be new_issue, regression, rate_spike, or session_crash_free")
	}
	if name == "" {
		name = kind
	}
	if len(params) == 0 || string(params) == "null" {
		params = json.RawMessage("{}")
	}
	if channelIDs == nil {
		channelIDs = []int64{}
	}
	chBytes, _ := json.Marshal(channelIDs)
	now := time.Now().UTC()
	var id int64
	err := s.db.QueryRow(`INSERT INTO alert_rules(project_id, name, kind, params_json, channel_ids_json, enabled, created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, projectID, name, kind, string(params), string(chBytes), enabled, now).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &AlertRule{
		ID:         id,
		ProjectID:  projectID,
		Name:       name,
		Kind:       kind,
		Params:     params,
		ChannelIDs: channelIDs,
		Enabled:    enabled,
		CreatedAt:  now.Format(time.RFC3339Nano),
	}, nil
}

func (s *Store) UpdateAlertRule(projectID, id int64, name, kind string, params json.RawMessage, channelIDs []int64, enabled bool) (*AlertRule, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !validateRuleKind(kind) {
		return nil, apierr.New(400, apierr.CodeAlertRuleInvalid, "kind must be new_issue, regression, rate_spike, or session_crash_free")
	}
	if name == "" {
		name = kind
	}
	if len(params) == 0 || string(params) == "null" {
		params = json.RawMessage("{}")
	}
	if channelIDs == nil {
		channelIDs = []int64{}
	}
	chBytes, _ := json.Marshal(channelIDs)
	var r AlertRule
	var paramsStr, channelsStr string
	err := s.db.QueryRow(`UPDATE alert_rules SET name=$1, kind=$2, params_json=$3, channel_ids_json=$4, enabled=$5
		WHERE project_id=$6 AND id=$7 RETURNING id, project_id, name, kind, params_json, channel_ids_json, enabled, created_at`,
		name, kind, string(params), string(chBytes), enabled, projectID, id).
		Scan(&r.ID, &r.ProjectID, &r.Name, &r.Kind, &paramsStr, &channelsStr, &r.Enabled, tsCol{&r.CreatedAt})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAlertRuleNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Params = json.RawMessage(paramsStr)
	r.ChannelIDs = []int64{}
	_ = json.Unmarshal([]byte(channelsStr), &r.ChannelIDs)
	return &r, nil
}

func (s *Store) DeleteAlertRule(projectID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM alert_rules WHERE project_id=$1 AND id=$2`, projectID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAlertRuleNotFound
	}
	return nil
}

func (s *Store) RecordAlertDelivery(ruleID, channelID int64, issueID *int64, fingerprint, status, errStr string) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO alert_deliveries(rule_id, channel_id, issue_id, fingerprint, sent_at, status, error)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, ruleID, channelID, issueID, fingerprint, now, status, errStr)
	return err
}

func (s *Store) HasDeliveredAlert(ruleID, issueID int64) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM alert_deliveries
		WHERE rule_id=$1 AND issue_id=$2 AND status='success'`, ruleID, issueID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) LastAlertDelivery(ruleID int64) (time.Time, error) {
	var sentAt time.Time
	err := s.db.QueryRow(`SELECT sent_at FROM alert_deliveries
		WHERE rule_id=$1 AND status='success' ORDER BY sent_at DESC LIMIT 1`, ruleID).Scan(&sentAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return sentAt, err
}

// EvaluateRateMetrics calculates errors, sessions and crashFreeRatio for a project in the last windowMinutes.
func (s *Store) EvaluateRateMetrics(projectID int64, windowMinutes int) (int, int, float64, error) {
	if windowMinutes <= 0 {
		windowMinutes = 5
	}
	from := time.Now().UTC().Add(-time.Duration(windowMinutes) * time.Minute)

	var sessions, crashFreeSessions int
	err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN error_count=0 THEN 1 ELSE 0 END),0)
		FROM sessions WHERE project_id=$1 AND started_at>=$2`, projectID, from).Scan(&sessions, &crashFreeSessions)
	if err != nil {
		return 0, 0, 0, err
	}

	var errorsCount int
	err = s.db.QueryRow(`SELECT COUNT(*) FROM items
		WHERE project_id=$1 AND type='error' AND ts>=$2`, projectID, from).Scan(&errorsCount)
	if err != nil {
		return 0, 0, 0, err
	}

	var crashFree float64 = 1.0
	if sessions > 0 {
		crashFree = float64(crashFreeSessions) / float64(sessions)
	}
	return errorsCount, sessions, crashFree, nil
}
