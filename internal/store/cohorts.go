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

var ErrCohortNotFound = errors.New("cohort not found")

type CohortRule struct {
	Event      string `json:"event"`       // e.g. "session_start" or custom event name
	Operator   string `json:"operator"`    // "gte", "lte", "eq", "gt"
	Count      int    `json:"count"`
	WindowDays int    `json:"window_days"` // e.g. 14, 30
}

type Cohort struct {
	ID               int64        `json:"id"`
	ProjectID        int64        `json:"project_id"`
	Name             string       `json:"name"`
	Description      string       `json:"description"`
	IsStatic         bool         `json:"is_static"`
	Rules            []CohortRule `json:"rules"`
	UserCount        int          `json:"user_count"`
	LastCalculatedAt *time.Time   `json:"last_calculated_at"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
}

func (s *Store) CreateCohort(projectID int64, name, description string, isStatic bool, rules []CohortRule, initialMembers []string) (*Cohort, error) {
	if rules == nil {
		rules = []CohortRule{}
	}
	rulesBytes, err := json.Marshal(rules)
	if err != nil {
		return nil, fmt.Errorf("marshal cohort rules: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var c Cohort
	c.ProjectID = projectID
	c.Name = name
	c.Description = description
	c.IsStatic = isStatic
	c.Rules = rules

	row := tx.QueryRow(`
		INSERT INTO cohorts (project_id, name, description, is_static, rules, user_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		RETURNING id, user_count, last_calculated_at, created_at, updated_at
	`, projectID, name, description, isStatic, rulesBytes, len(initialMembers))

	if err := row.Scan(&c.ID, &c.UserCount, &c.LastCalculatedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("insert cohort: %w", err)
	}

	if isStatic && len(initialMembers) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO cohort_members (cohort_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`)
		if err != nil {
			return nil, err
		}
		defer stmt.Close()
		for _, uid := range initialMembers {
			if uid != "" {
				if _, err := stmt.Exec(c.ID, uid); err != nil {
					return nil, err
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	if !isStatic && len(rules) > 0 {
		_ = s.RefreshCohortMembers(projectID, c.ID)
		return s.GetCohort(projectID, c.ID)
	}

	return &c, nil
}

func (s *Store) GetCohort(projectID, cohortID int64) (*Cohort, error) {
	row := s.db.QueryRow(`
		SELECT id, project_id, name, description, is_static, rules, user_count, last_calculated_at, created_at, updated_at
		FROM cohorts
		WHERE project_id = $1 AND id = $2
	`, projectID, cohortID)

	var c Cohort
	var rulesBytes []byte
	if err := row.Scan(
		&c.ID,
		&c.ProjectID,
		&c.Name,
		&c.Description,
		&c.IsStatic,
		&rulesBytes,
		&c.UserCount,
		&c.LastCalculatedAt,
		&c.CreatedAt,
		&c.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCohortNotFound
		}
		return nil, fmt.Errorf("get cohort: %w", err)
	}

	c.Rules = []CohortRule{}
	if len(rulesBytes) > 0 {
		_ = json.Unmarshal(rulesBytes, &c.Rules)
	}
	return &c, nil
}

func (s *Store) ListCohorts(projectID int64) ([]*Cohort, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, name, description, is_static, rules, user_count, last_calculated_at, created_at, updated_at
		FROM cohorts
		WHERE project_id = $1
		ORDER BY name ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list cohorts: %w", err)
	}
	defer rows.Close()

	var cohorts []*Cohort
	for rows.Next() {
		var c Cohort
		var rulesBytes []byte
		if err := rows.Scan(
			&c.ID,
			&c.ProjectID,
			&c.Name,
			&c.Description,
			&c.IsStatic,
			&rulesBytes,
			&c.UserCount,
			&c.LastCalculatedAt,
			&c.CreatedAt,
			&c.UpdatedAt,
		); err != nil {
			return nil, err
		}
		c.Rules = []CohortRule{}
		if len(rulesBytes) > 0 {
			_ = json.Unmarshal(rulesBytes, &c.Rules)
		}
		cohorts = append(cohorts, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cohorts, nil
}

func (s *Store) UpdateCohort(projectID, cohortID int64, name, description string, rules []CohortRule) (*Cohort, error) {
	if rules == nil {
		rules = []CohortRule{}
	}
	rulesBytes, err := json.Marshal(rules)
	if err != nil {
		return nil, fmt.Errorf("marshal cohort rules: %w", err)
	}

	res, err := s.db.Exec(`
		UPDATE cohorts
		SET name = $1, description = $2, rules = $3, updated_at = NOW()
		WHERE project_id = $4 AND id = $5
	`, name, description, rulesBytes, projectID, cohortID)
	if err != nil {
		return nil, fmt.Errorf("update cohort: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrCohortNotFound
	}

	c, err := s.GetCohort(projectID, cohortID)
	if err != nil {
		return nil, err
	}
	if !c.IsStatic {
		_ = s.RefreshCohortMembers(projectID, cohortID)
		return s.GetCohort(projectID, cohortID)
	}
	return c, nil
}

func (s *Store) DeleteCohort(projectID, cohortID int64) error {
	res, err := s.db.Exec(`DELETE FROM cohorts WHERE project_id = $1 AND id = $2`, projectID, cohortID)
	if err != nil {
		return fmt.Errorf("delete cohort: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCohortNotFound
	}
	return nil
}

func (s *Store) ListCohortMembers(projectID, cohortID int64, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Verify cohort exists
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM cohorts WHERE project_id = $1 AND id = $2)`, projectID, cohortID).Scan(&exists); err != nil || !exists {
		return nil, ErrCohortNotFound
	}

	rows, err := s.db.Query(`
		SELECT user_id
		FROM cohort_members
		WHERE cohort_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, cohortID, limit)
	if err != nil {
		return nil, fmt.Errorf("list cohort members: %w", err)
	}
	defer rows.Close()

	var members []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err == nil {
			members = append(members, uid)
		}
	}
	return members, nil
}

// RefreshCohortMembers evaluates dynamic cohort rules and updates cohort_members.
func (s *Store) RefreshCohortMembers(projectID, cohortID int64) error {
	cohort, err := s.GetCohort(projectID, cohortID)
	if err != nil {
		return err
	}
	if cohort.IsStatic || len(cohort.Rules) == 0 {
		return nil
	}

	// For each rule, evaluate matching users.
	// Users that satisfy all rules (AND intersection) are placed in cohort.
	matchingUsers := make(map[string]bool)
	firstRule := true

	for _, rule := range cohort.Rules {
		windowDays := rule.WindowDays
		if windowDays <= 0 {
			windowDays = 30
		}
		cutoff := time.Now().UTC().AddDate(0, 0, -windowDays)

		ruleUsers := make(map[string]bool)

		if rule.Event == "" || rule.Event == "session_start" {
			// Count sessions per user
			rows, err := s.db.Query(`
				SELECT user_id, COUNT(*) as cnt
				FROM sessions
				WHERE project_id = $1 AND started_at >= $2 AND user_id != ''
				GROUP BY user_id
			`, projectID, cutoff)
			if err != nil {
				return fmt.Errorf("evaluate session rule: %w", err)
			}
			defer rows.Close()

			for rows.Next() {
				var uid string
				var cnt int
				if err := rows.Scan(&uid, &cnt); err == nil {
					if evalCount(cnt, rule.Operator, rule.Count) {
						ruleUsers[uid] = true
					}
				}
			}
		} else {
			// Count events per user from items
			// Items table has session_id, join with sessions to get user_id
			rows, err := s.db.Query(`
				SELECT s.user_id, COUNT(*) as cnt
				FROM items i
				JOIN sessions s ON s.id = i.session_id AND s.project_id = i.project_id
				WHERE i.project_id = $1 AND i.type = 'event' AND i.name = $2 AND i.ts >= $3 AND s.user_id != ''
				GROUP BY s.user_id
			`, projectID, rule.Event, cutoff)
			if err != nil {
				return fmt.Errorf("evaluate event rule: %w", err)
			}
			defer rows.Close()

			for rows.Next() {
				var uid string
				var cnt int
				if err := rows.Scan(&uid, &cnt); err == nil {
					if evalCount(cnt, rule.Operator, rule.Count) {
						ruleUsers[uid] = true
					}
				}
			}
		}

		if firstRule {
			matchingUsers = ruleUsers
			firstRule = false
		} else {
			// Intersect
			for uid := range matchingUsers {
				if !ruleUsers[uid] {
					delete(matchingUsers, uid)
				}
			}
		}
	}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM cohort_members WHERE cohort_id = $1`, cohortID); err != nil {
		return err
	}

	if len(matchingUsers) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO cohort_members (cohort_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for uid := range matchingUsers {
			if _, err := stmt.Exec(cohortID, uid); err != nil {
				return err
			}
		}
	}

	now := time.Now().UTC()
	if _, err := tx.Exec(`
		UPDATE cohorts
		SET user_count = $1, last_calculated_at = $2, updated_at = NOW()
		WHERE id = $3
	`, len(matchingUsers), now, cohortID); err != nil {
		return err
	}

	return tx.Commit()
}

func evalCount(actual int, op string, target int) bool {
	switch op {
	case "gte":
		return actual >= target
	case "gt":
		return actual > target
	case "lte":
		return actual <= target
	case "lt":
		return actual < target
	case "eq":
		return actual == target
	default:
		return actual >= target
	}
}
