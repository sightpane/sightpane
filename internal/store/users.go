// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"
)

type UserSummary struct {
	UserID            string          `json:"user_id"`
	Email             string          `json:"email,omitempty"`
	Name              string          `json:"name,omitempty"`
	UserJSON          json.RawMessage `json:"user_json,omitempty"`
	SessionCount      int             `json:"session_count"`
	TotalDurationSec  float64         `json:"total_duration_sec"`
	AvgDurationSec    float64         `json:"avg_duration_sec"`
	ErrorCount        int             `json:"error_count"`
	ErrorSessionCount int             `json:"error_session_count"`
	FirstSeen         string          `json:"first_seen"`
	LastSeen          string          `json:"last_seen"`
	LastPlatform      string          `json:"last_platform"`
	LastBrowser       string          `json:"last_browser"`
	LastIP            string          `json:"last_ip"`
}

type UserDailyStat struct {
	Day            string  `json:"day"`
	ActiveUsers    int     `json:"active_users"`
	ErrorUsers     int     `json:"error_users"`
	AvgDurationSec float64 `json:"avg_duration_sec"`
}

type ProjectUsersResponse struct {
	TotalUsers      int             `json:"total_users"`
	ActiveUsers     int             `json:"active_users"`
	AvgDurationSec  float64         `json:"avg_duration_sec"`
	SessionsPerUser float64         `json:"sessions_per_user"`
	ErrorUserCount  int             `json:"error_user_count"`
	Daily           []UserDailyStat `json:"daily"`
	Users           []UserSummary   `json:"users"`
}

func (s *Store) ListProjectUsers(ctx context.Context, projectID int64, days int, query string) (*ProjectUsersResponse, error) {
	if days <= 0 || days > 90 {
		days = 14
	}
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)

	resp := &ProjectUsersResponse{
		Daily: []UserDailyStat{},
		Users: []UserSummary{},
	}

	// 1. Overall stats across all identified users in project
	var totalSessions int
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(DISTINCT user_id) FILTER (WHERE user_id != ''),
			COUNT(DISTINCT user_id) FILTER (WHERE user_id != '' AND started_at >= $2),
			COALESCE(AVG(GREATEST(0, EXTRACT(EPOCH FROM (COALESCE(ended_at, last_seen_at) - started_at)))), 0),
			COUNT(*),
			COUNT(DISTINCT user_id) FILTER (WHERE user_id != '' AND error_count > 0 AND started_at >= $2)
		FROM sessions
		WHERE project_id = $1
	`, projectID, since).Scan(
		&resp.TotalUsers,
		&resp.ActiveUsers,
		&resp.AvgDurationSec,
		&totalSessions,
		&resp.ErrorUserCount,
	)
	if err != nil {
		return nil, err
	}
	resp.AvgDurationSec = math.Round(resp.AvgDurationSec*10) / 10
	if resp.TotalUsers > 0 {
		resp.SessionsPerUser = math.Round((float64(totalSessions)/float64(resp.TotalUsers))*10) / 10
	}

	// 2. Daily active users and avg duration series for the time window
	dailyMap := make(map[string]*UserDailyStat, days)
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -(days - 1 - i)).Format("2006-01-02")
		st := &UserDailyStat{Day: d}
		dailyMap[d] = st
		resp.Daily = append(resp.Daily, *st)
	}

	dailyRows, err := s.db.QueryContext(ctx, `
		SELECT
			TO_CHAR(started_at, 'YYYY-MM-DD') AS day,
			COUNT(DISTINCT CASE WHEN user_id != '' THEN user_id ELSE visitor_key END) AS active_users,
			COUNT(DISTINCT CASE WHEN error_count > 0 THEN (CASE WHEN user_id != '' THEN user_id ELSE visitor_key END) END) AS error_users,
			COALESCE(AVG(GREATEST(0, EXTRACT(EPOCH FROM (COALESCE(ended_at, last_seen_at) - started_at)))), 0) AS avg_duration_sec
		FROM sessions
		WHERE project_id = $1 AND started_at >= $2
		GROUP BY 1
		ORDER BY 1
	`, projectID, since)
	if err == nil {
		defer dailyRows.Close()
		for dailyRows.Next() {
			var day string
			var activeUsers, errorUsers int
			var avgDur float64
			if err := dailyRows.Scan(&day, &activeUsers, &errorUsers, &avgDur); err == nil {
				for i := range resp.Daily {
					if resp.Daily[i].Day == day {
						resp.Daily[i].ActiveUsers = activeUsers
						resp.Daily[i].ErrorUsers = errorUsers
						resp.Daily[i].AvgDurationSec = math.Round(avgDur*10) / 10
						break
					}
				}
			}
		}
	}

	// 3. User directory list
	q := `
		SELECT
			s.user_id,
			COALESCE((array_agg(s.user_json ORDER BY s.last_seen_at DESC))[1], '{}'),
			COUNT(*),
			COALESCE(SUM(GREATEST(0, EXTRACT(EPOCH FROM (COALESCE(s.ended_at, s.last_seen_at) - s.started_at)))), 0),
			COALESCE(AVG(GREATEST(0, EXTRACT(EPOCH FROM (COALESCE(s.ended_at, s.last_seen_at) - s.started_at)))), 0),
			COALESCE(SUM(s.error_count), 0),
			COUNT(CASE WHEN s.error_count > 0 THEN 1 END),
			MIN(s.started_at),
			MAX(s.last_seen_at),
			COALESCE((array_agg(s.platform ORDER BY s.last_seen_at DESC))[1], ''),
			COALESCE((array_agg(s.browser ORDER BY s.last_seen_at DESC))[1], ''),
			COALESCE((array_agg(s.ip ORDER BY s.last_seen_at DESC))[1], '')
		FROM sessions s
		WHERE s.project_id = $1 AND s.user_id != ''
	`
	args := []any{projectID}
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery != "" {
		args = append(args, "%"+trimmedQuery+"%")
		q += ` AND (s.user_id ILIKE $2 OR s.user_json::text ILIKE $2)`
	}
	q += `
		GROUP BY s.user_id
		ORDER BY MAX(s.last_seen_at) DESC
		LIMIT 200
	`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var u UserSummary
		var userRaw string
		if err := rows.Scan(
			&u.UserID,
			&userRaw,
			&u.SessionCount,
			&u.TotalDurationSec,
			&u.AvgDurationSec,
			&u.ErrorCount,
			&u.ErrorSessionCount,
			tsCol{&u.FirstSeen},
			tsCol{&u.LastSeen},
			&u.LastPlatform,
			&u.LastBrowser,
			&u.LastIP,
		); err != nil {
			return nil, err
		}

		u.TotalDurationSec = math.Round(u.TotalDurationSec*10) / 10
		u.AvgDurationSec = math.Round(u.AvgDurationSec*10) / 10
		u.UserJSON = json.RawMessage(userRaw)

		// Parse name and email from user_json if present
		var userMap map[string]any
		if err := json.Unmarshal([]byte(userRaw), &userMap); err == nil {
			if emailVal, ok := userMap["email"].(string); ok {
				u.Email = emailVal
			}
			if nameVal, ok := userMap["name"].(string); ok {
				u.Name = nameVal
			}
		}
		if u.Email == "" && strings.Contains(u.UserID, "@") {
			u.Email = u.UserID
		}

		resp.Users = append(resp.Users, u)
	}

	return resp, nil
}
