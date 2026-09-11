// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"database/sql"
	"fmt"
	"sort"
	"time"
)

type RetentionPeriodActivity struct {
	PeriodIndex int     `json:"period_index"`
	Count       int     `json:"count"`
	Percentage  float64 `json:"percentage"`
}

type RetentionCohortBucket struct {
	Date       string                    `json:"date"`
	TotalUsers int                       `json:"total_users"`
	Activity   []RetentionPeriodActivity `json:"activity"`
}

type RetentionResult struct {
	Period      string                  `json:"period"`
	StartEvent  string                  `json:"start_event"`
	ReturnEvent string                  `json:"return_event"`
	Cohorts     []RetentionCohortBucket `json:"cohorts"`
}

func (s *Store) CalculateRetentionMatrix(
	projectID int64,
	period string,
	days int,
	startEvent, returnEvent string,
	cohortID *int64,
) (*RetentionResult, error) {
	if period != "week" {
		period = "day"
	}
	if days <= 0 || days > 90 {
		days = 30
	}
	if startEvent == "" {
		startEvent = "session_start"
	}
	if returnEvent == "" {
		returnEvent = "session_start"
	}

	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)

	// 1. Optional cohort filter
	var cohortUserMap map[string]bool
	if cohortID != nil && *cohortID > 0 {
		cohortUserMap = make(map[string]bool)
		rows, err := s.db.Query(`SELECT user_id FROM cohort_members WHERE cohort_id = $1`, *cohortID)
		if err != nil {
			return nil, fmt.Errorf("query cohort members: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var uid string
			if err := rows.Scan(&uid); err == nil {
				cohortUserMap[uid] = true
			}
		}
	}

	// 2. Fetch start events: (user_id, ts)
	userFirstStart := make(map[string]time.Time)
	processStartRows := func(rows *sql.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var uid string
			var ts time.Time
			if err := rows.Scan(&uid, &ts); err != nil {
				return err
			}
			if uid == "" {
				continue
			}
			if cohortUserMap != nil && !cohortUserMap[uid] {
				continue
			}
			if prev, ok := userFirstStart[uid]; !ok || ts.Before(prev) {
				userFirstStart[uid] = ts
			}
		}
		return rows.Err()
	}

	if startEvent == "session_start" {
		rows, err := s.db.Query(`
			SELECT user_id, started_at
			FROM sessions
			WHERE project_id = $1 AND started_at >= $2 AND user_id != ''
			ORDER BY started_at ASC
		`, projectID, since)
		if err != nil {
			return nil, fmt.Errorf("query start sessions: %w", err)
		}
		if err := processStartRows(rows); err != nil {
			return nil, err
		}
	} else {
		rows, err := s.db.Query(`
			SELECT s.user_id, i.ts
			FROM items i
			JOIN sessions s ON s.id = i.session_id AND s.project_id = i.project_id
			WHERE i.project_id = $1 AND i.type = 'event' AND i.name = $2 AND i.ts >= $3 AND s.user_id != ''
			ORDER BY i.ts ASC
		`, projectID, startEvent, since)
		if err != nil {
			return nil, fmt.Errorf("query start events: %w", err)
		}
		if err := processStartRows(rows); err != nil {
			return nil, err
		}
	}

	if len(userFirstStart) == 0 {
		return &RetentionResult{
			Period:      period,
			StartEvent:  startEvent,
			ReturnEvent: returnEvent,
			Cohorts:     []RetentionCohortBucket{},
		}, nil
	}

	// 3. Fetch return events: (user_id, ts)
	type userEvent struct {
		userID string
		ts     time.Time
	}
	var returnEvents []userEvent

	processReturnRows := func(rows *sql.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var uid string
			var ts time.Time
			if err := rows.Scan(&uid, &ts); err != nil {
				return err
			}
			if uid == "" {
				continue
			}
			if _, ok := userFirstStart[uid]; !ok {
				continue
			}
			returnEvents = append(returnEvents, userEvent{userID: uid, ts: ts})
		}
		return rows.Err()
	}

	if returnEvent == "session_start" {
		rows, err := s.db.Query(`
			SELECT user_id, started_at
			FROM sessions
			WHERE project_id = $1 AND started_at >= $2 AND user_id != ''
			ORDER BY started_at ASC
		`, projectID, since)
		if err != nil {
			return nil, fmt.Errorf("query return sessions: %w", err)
		}
		if err := processReturnRows(rows); err != nil {
			return nil, err
		}
	} else {
		rows, err := s.db.Query(`
			SELECT s.user_id, i.ts
			FROM items i
			JOIN sessions s ON s.id = i.session_id AND s.project_id = i.project_id
			WHERE i.project_id = $1 AND i.type = 'event' AND i.name = $2 AND i.ts >= $3 AND s.user_id != ''
			ORDER BY i.ts ASC
		`, projectID, returnEvent, since)
		if err != nil {
			return nil, fmt.Errorf("query return events: %w", err)
		}
		if err := processReturnRows(rows); err != nil {
			return nil, err
		}
	}

	// 4. Build buckets
	// Helper to format date bucket key
	bucketKey := func(t time.Time) (string, time.Time) {
		t = t.UTC()
		if period == "week" {
			// Align to Monday of that week
			weekday := int(t.Weekday())
			if weekday == 0 {
				weekday = 7
			}
			aligned := time.Date(t.Year(), t.Month(), t.Day()-weekday+1, 0, 0, 0, 0, time.UTC)
			return aligned.Format("2006-01-02"), aligned
		}
		aligned := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return aligned.Format("2006-01-02"), aligned
	}

	// Map bucketKey -> list of users in this cohort
	cohortUsers := make(map[string]map[string]bool)
	cohortDates := make(map[string]time.Time)
	for uid, firstTs := range userFirstStart {
		bKey, bDate := bucketKey(firstTs)
		if cohortUsers[bKey] == nil {
			cohortUsers[bKey] = make(map[string]bool)
			cohortDates[bKey] = bDate
		}
		cohortUsers[bKey][uid] = true
	}

	// Map (bKey, periodIndex) -> set of returned users
	cohortPeriodUsers := make(map[string]map[int]map[string]bool)
	for bKey := range cohortUsers {
		cohortPeriodUsers[bKey] = make(map[int]map[string]bool)
	}

	for _, ev := range returnEvents {
		firstTs, ok := userFirstStart[ev.userID]
		if !ok || ev.ts.Before(firstTs) {
			continue
		}
		bKey, bDate := bucketKey(firstTs)

		var pIndex int
		if period == "week" {
			diffDays := int(ev.ts.Sub(bDate).Hours() / 24)
			pIndex = diffDays / 7
		} else {
			diffHours := int(ev.ts.Sub(bDate).Hours())
			pIndex = diffHours / 24
		}

		if pIndex >= 0 {
			if cohortPeriodUsers[bKey][pIndex] == nil {
				cohortPeriodUsers[bKey][pIndex] = make(map[string]bool)
			}
			cohortPeriodUsers[bKey][pIndex][ev.userID] = true
		}
	}

	// Assemble final result sorted by bucket date
	var sortedKeys []string
	for k := range cohortUsers {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	maxPeriod := 7
	if period == "day" {
		maxPeriod = 14
	}

	var resultCohorts []RetentionCohortBucket
	for _, bKey := range sortedKeys {
		users := cohortUsers[bKey]
		total := len(users)
		if total == 0 {
			continue
		}

		var activity []RetentionPeriodActivity
		for p := 0; p <= maxPeriod; p++ {
			// Only include periods that have elapsed or have activity
			pUsers := cohortPeriodUsers[bKey][p]
			cnt := len(pUsers)
			// Period 0 should at least include all users since they started in period 0
			if p == 0 && cnt < total {
				cnt = total
			}
			pct := 0.0
			if total > 0 {
				pct = float64(cnt) / float64(total) * 100.0
			}
			activity = append(activity, RetentionPeriodActivity{
				PeriodIndex: p,
				Count:       cnt,
				Percentage:  pct,
			})
		}

		resultCohorts = append(resultCohorts, RetentionCohortBucket{
			Date:       bKey,
			TotalUsers: total,
			Activity:   activity,
		})
	}

	return &RetentionResult{
		Period:      period,
		StartEvent:  startEvent,
		ReturnEvent: returnEvent,
		Cohorts:     resultCohorts,
	}, nil
}
