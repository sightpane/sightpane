// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
)

type IssueComment struct {
	ID        int64     `json:"id"`
	IssueID   int64     `json:"issue_id"`
	UserID    int64     `json:"user_id"`
	UserEmail string    `json:"user_email"`
	UserName  string    `json:"user_name"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type ProjectFingerprintRule struct {
	ID               int64     `json:"id"`
	ProjectID        int64     `json:"project_id"`
	ExceptionMatch   string    `json:"exception_match"`
	MessageGlob      string    `json:"message_glob"`
	StackContains    string    `json:"stack_contains"`
	Action           string    `json:"action"` // "ignore" or "group_as"
	GroupFingerprint string    `json:"group_fingerprint"`
	Priority         int       `json:"priority"`
	CreatedAt        time.Time `json:"created_at"`
}

func MatchFingerprintRule(rules []ProjectFingerprintRule, exception, message, stack string) *ProjectFingerprintRule {
	for i := range rules {
		r := &rules[i]
		if r.ExceptionMatch != "" && !strings.Contains(strings.ToLower(exception), strings.ToLower(r.ExceptionMatch)) {
			continue
		}
		if r.MessageGlob != "" {
			matched, err := filepath.Match(strings.ToLower(r.MessageGlob), strings.ToLower(message))
			if err != nil || !matched {
				if !strings.Contains(strings.ToLower(message), strings.ToLower(r.MessageGlob)) {
					continue
				}
			}
		}
		if r.StackContains != "" && !strings.Contains(strings.ToLower(stack), strings.ToLower(r.StackContains)) {
			continue
		}
		return r
	}
	return nil
}

func (s *Store) AssignIssue(id int64, assigneeUserID *int64) error {
	_, err := s.db.Exec(`UPDATE issues SET assignee_user_id=$1 WHERE id=$2`, assigneeUserID, id)
	return err
}

func (s *Store) SetIssueStatus(id int64, status string) error {
	status = strings.ToLower(status)
	if status != "open" && status != "resolved" && status != "ignored" && status != "snoozed" {
		return errors.New("invalid issue status")
	}
	resolved := status == "resolved"
	_, err := s.db.Exec(`UPDATE issues SET status=$1, resolved=$2 WHERE id=$3`, status, resolved, id)
	return err
}

func (s *Store) SnoozeIssue(id int64, until *time.Time, countThreshold int) error {
	_, err := s.db.Exec(`UPDATE issues SET status='snoozed', resolved=false, snooze_until=$1, snooze_count_threshold=$2, snooze_start_count=count WHERE id=$3`, until, countThreshold, id)
	return err
}

func (s *Store) MergeIssue(sourceID, targetID int64) error {
	if sourceID == targetID {
		return errors.New("cannot merge issue into itself")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sourceCount int
	if err := tx.QueryRow(`SELECT count FROM issues WHERE id=$1`, sourceID).Scan(&sourceCount); err != nil {
		return err
	}

	if _, err := tx.Exec(`UPDATE issues SET merged_into=$1, status='ignored' WHERE id=$2`, targetID, sourceID); err != nil {
		return err
	}

	if _, err := tx.Exec(`UPDATE issues SET count=count+$1 WHERE id=$2`, sourceCount, targetID); err != nil {
		return err
	}

	if _, err := tx.Exec(`UPDATE items SET issue_id=$1 WHERE issue_id=$2`, targetID, sourceID); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) AddIssueComment(issueID, userID int64, body string) (*IssueComment, error) {
	var c IssueComment
	c.IssueID = issueID
	c.UserID = userID
	c.Body = strings.TrimSpace(body)
	if c.Body == "" {
		return nil, errors.New("comment body cannot be empty")
	}
	err := s.db.QueryRow(`
		INSERT INTO issue_comments(issue_id, user_id, body)
		VALUES($1, $2, $3)
		RETURNING id, created_at`,
		issueID, userID, c.Body,
	).Scan(&c.ID, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	_ = s.db.QueryRow(`SELECT email, name FROM users WHERE id=$1`, userID).Scan(&c.UserEmail, &c.UserName)
	return &c, nil
}

func (s *Store) ListIssueComments(issueID int64) ([]IssueComment, error) {
	rows, err := s.db.Query(`
		SELECT c.id, c.issue_id, c.user_id, u.email, u.name, c.body, c.created_at
		FROM issue_comments c
		JOIN users u ON u.id = c.user_id
		WHERE c.issue_id = $1
		ORDER BY c.created_at ASC`,
		issueID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IssueComment
	for rows.Next() {
		var c IssueComment
		if err := rows.Scan(&c.ID, &c.IssueID, &c.UserID, &c.UserEmail, &c.UserName, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateFingerprintRule(r ProjectFingerprintRule) (*ProjectFingerprintRule, error) {
	r.Action = strings.ToLower(r.Action)
	if r.Action != "ignore" && r.Action != "group_as" {
		return nil, errors.New("invalid rule action: must be ignore or group_as")
	}
	err := s.db.QueryRow(`
		INSERT INTO project_fingerprint_rules(project_id, exception_match, message_glob, stack_contains, action, group_fingerprint, priority)
		VALUES($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`,
		r.ProjectID, r.ExceptionMatch, r.MessageGlob, r.StackContains, r.Action, r.GroupFingerprint, r.Priority,
	).Scan(&r.ID, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) ListFingerprintRules(projectID int64) ([]ProjectFingerprintRule, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, exception_match, message_glob, stack_contains, action, group_fingerprint, priority, created_at
		FROM project_fingerprint_rules
		WHERE project_id = $1
		ORDER BY priority DESC, id ASC`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectFingerprintRule
	for rows.Next() {
		var r ProjectFingerprintRule
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.ExceptionMatch, &r.MessageGlob, &r.StackContains, &r.Action, &r.GroupFingerprint, &r.Priority, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteFingerprintRule(projectID, ruleID int64) error {
	_, err := s.db.Exec(`DELETE FROM project_fingerprint_rules WHERE id=$1 AND project_id=$2`, ruleID, projectID)
	return err
}
