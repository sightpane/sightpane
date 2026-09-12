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
	"sort"
	"strings"
	"time"
)

type ProfileRecord struct {
	ID              string          `json:"id"`
	ProjectID       int64           `json:"project_id"`
	TransactionName string          `json:"transaction_name"`
	SessionID       string          `json:"session_id,omitempty"`
	TraceID         string          `json:"trace_id,omitempty"`
	DurationMs      float64         `json:"duration_ms"`
	CPUTimeMs       float64         `json:"cpu_time_ms"`
	ThreadName      string          `json:"thread_name"`
	Platform        string          `json:"platform"`
	ProfileData     json.RawMessage `json:"profile_data,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

type ProfileSummary struct {
	ID              string    `json:"id"`
	TransactionName string    `json:"transaction_name"`
	SessionID       string    `json:"session_id,omitempty"`
	TraceID         string    `json:"trace_id,omitempty"`
	DurationMs      float64   `json:"duration_ms"`
	CPUTimeMs       float64   `json:"cpu_time_ms"`
	ThreadName      string    `json:"thread_name"`
	Platform        string    `json:"platform"`
	CreatedAt       time.Time `json:"created_at"`
}

type ProfileFrame struct {
	Name string `json:"name"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
}

type ProfileSample struct {
	ElapsedMs float64 `json:"elapsed_ms"`
	StackID   []int   `json:"stack_id"`
}

type ProfileDataPayload struct {
	Shared struct {
		Frames []ProfileFrame `json:"frames"`
	} `json:"shared"`
	Samples []ProfileSample `json:"samples"`
}

type SlowFunction struct {
	Name        string  `json:"name"`
	File        string  `json:"file"`
	TotalTimeMs float64 `json:"total_time_ms"`
	SelfTimeMs  float64 `json:"self_time_ms"`
	CallCount   int     `json:"call_count"`
}

func (s *Store) InsertProfile(ctx context.Context, p *ProfileRecord) error {
	if p.ThreadName == "" {
		p.ThreadName = "main"
	}
	if len(p.ProfileData) == 0 {
		p.ProfileData = json.RawMessage("{}")
	}

	var sessID any
	if p.SessionID != "" {
		sessID = p.SessionID
	}

	const q = `
		INSERT INTO profiles (
			project_id, transaction_name, session_id, trace_id,
			duration_ms, cpu_time_ms, thread_name, platform,
			profile_data, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($10, NOW()))
		RETURNING id, created_at
	`
	var createdAt sql.NullTime
	if !p.CreatedAt.IsZero() {
		createdAt.Valid = true
		createdAt.Time = p.CreatedAt
	}

	return s.db.QueryRowContext(
		ctx, q,
		p.ProjectID, p.TransactionName, sessID, p.TraceID,
		p.DurationMs, p.CPUTimeMs, p.ThreadName, p.Platform,
		p.ProfileData, createdAt,
	).Scan(&p.ID, &p.CreatedAt)
}

func (s *Store) ListProfiles(
	ctx context.Context,
	projectID int64,
	txName string,
	days int,
	limit int,
) ([]ProfileSummary, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	var sb strings.Builder
	sb.WriteString(`
		SELECT id, transaction_name, COALESCE(session_id::text, ''), trace_id,
		       duration_ms, cpu_time_ms, thread_name, platform, created_at
		FROM profiles
		WHERE project_id = $1
		  AND created_at >= $2
	`)
	args := []any{projectID, cutoff}
	argIdx := 3

	if txName != "" {
		sb.WriteString(fmt.Sprintf(" AND transaction_name = $%d", argIdx))
		args = append(args, txName)
		argIdx++
	}

	sb.WriteString(fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", argIdx))
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)

	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer rows.Close()

	var list []ProfileSummary
	for rows.Next() {
		var item ProfileSummary
		if err := rows.Scan(
			&item.ID, &item.TransactionName, &item.SessionID, &item.TraceID,
			&item.DurationMs, &item.CPUTimeMs, &item.ThreadName, &item.Platform,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan profile summary: %w", err)
		}
		list = append(list, item)
	}
	if list == nil {
		list = []ProfileSummary{}
	}
	return list, rows.Err()
}

func (s *Store) GetProfile(ctx context.Context, projectID int64, id string) (*ProfileRecord, error) {
	const q = `
		SELECT id, project_id, transaction_name, COALESCE(session_id::text, ''), trace_id,
		       duration_ms, cpu_time_ms, thread_name, platform, profile_data, created_at
		FROM profiles
		WHERE project_id = $1 AND id = $2
	`
	var p ProfileRecord
	err := s.db.QueryRowContext(ctx, q, projectID, id).Scan(
		&p.ID, &p.ProjectID, &p.TransactionName, &p.SessionID, &p.TraceID,
		&p.DurationMs, &p.CPUTimeMs, &p.ThreadName, &p.Platform, &p.ProfileData, &p.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("profile not found: %w", err)
		}
		return nil, fmt.Errorf("get profile: %w", err)
	}
	return &p, nil
}

func (s *Store) GetTopSlowFunctions(
	ctx context.Context,
	projectID int64,
	txName string,
	days int,
	limit int,
) ([]SlowFunction, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	var sb strings.Builder
	sb.WriteString(`
		SELECT profile_data
		FROM profiles
		WHERE project_id = $1
		  AND created_at >= $2
	`)
	args := []any{projectID, cutoff}
	argIdx := 3

	if txName != "" {
		sb.WriteString(fmt.Sprintf(" AND transaction_name = $%d", argIdx))
		args = append(args, txName)
		argIdx++
	}

	sb.WriteString(fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", argIdx))
	args = append(args, 25) // analyze up to 25 latest profiles for aggregated function stats

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query profiles for slow functions: %w", err)
	}
	defer rows.Close()

	type funcAgg struct {
		name      string
		file      string
		selfTime  float64
		totalTime float64
		calls     int
	}
	aggMap := make(map[string]*funcAgg)

	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var payload ProfileDataPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}
		frames := payload.Shared.Frames
		if len(frames) == 0 || len(payload.Samples) == 0 {
			continue
		}

		var prevElapsed float64
		for i, sample := range payload.Samples {
			var dt float64
			if i == 0 {
				dt = sample.ElapsedMs
				if dt <= 0 {
					dt = 10.0 // default sampling tick ~10ms
				}
			} else {
				dt = sample.ElapsedMs - prevElapsed
				if dt <= 0 {
					dt = 10.0
				}
			}
			prevElapsed = sample.ElapsedMs

			// All frames in stack get totalTime
			seenInSample := make(map[int]bool)
			for _, frameIdx := range sample.StackID {
				if frameIdx < 0 || frameIdx >= len(frames) {
					continue
				}
				frame := frames[frameIdx]
				key := frame.Name + "@" + frame.File
				entry, ok := aggMap[key]
				if !ok {
					entry = &funcAgg{name: frame.Name, file: frame.File}
					aggMap[key] = entry
				}
				entry.totalTime += dt
				if !seenInSample[frameIdx] {
					entry.calls++
					seenInSample[frameIdx] = true
				}
			}

			// Top frame gets selfTime
			if len(sample.StackID) > 0 {
				topIdx := sample.StackID[len(sample.StackID)-1]
				if topIdx >= 0 && topIdx < len(frames) {
					frame := frames[topIdx]
					key := frame.Name + "@" + frame.File
					if entry, ok := aggMap[key]; ok {
						entry.selfTime += dt
					}
				}
			}
		}
	}

	result := make([]SlowFunction, 0, len(aggMap))
	for _, entry := range aggMap {
		result = append(result, SlowFunction{
			Name:        entry.name,
			File:        entry.file,
			TotalTimeMs: entry.totalTime,
			SelfTimeMs:  entry.selfTime,
			CallCount:   entry.calls,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flamegraph samples: %w", err)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].SelfTimeMs == result[j].SelfTimeMs {
			return result[i].TotalTimeMs > result[j].TotalTimeMs
		}
		return result[i].SelfTimeMs > result[j].SelfTimeMs
	})

	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}
