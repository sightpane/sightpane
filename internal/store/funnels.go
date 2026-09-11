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
	"sort"
	"time"
)

var ErrFunnelNotFound = errors.New("funnel not found")

type FunnelStep struct {
	Name string `json:"name"`
}

type Funnel struct {
	ID                      int64        `json:"id"`
	ProjectID               int64        `json:"project_id"`
	Name                    string       `json:"name"`
	Description             string       `json:"description"`
	Steps                   []FunnelStep `json:"steps"`
	ConversionWindowSeconds int          `json:"conversion_window_seconds"`
	CreatedAt               time.Time    `json:"created_at"`
	UpdatedAt               time.Time    `json:"updated_at"`
}

type FunnelStepResult struct {
	StepIndex      int     `json:"step_index"`
	Name           string  `json:"name"`
	Count          int     `json:"count"`
	ConversionRate float64 `json:"conversion_rate"`
	DropOffCount   int     `json:"drop_off_count"`
	DropOffRate    float64 `json:"drop_off_rate"`
}

type FunnelResult struct {
	TotalEntered                int                `json:"total_entered"`
	TotalConverted              int                `json:"total_converted"`
	OverallConversionRate       float64            `json:"overall_conversion_rate"`
	MedianConversionTimeSeconds int                `json:"median_conversion_time_seconds"`
	Steps                       []FunnelStepResult `json:"steps"`
}

func (s *Store) CreateFunnel(projectID int64, name, description string, steps []FunnelStep, windowSeconds int) (*Funnel, error) {
	if windowSeconds <= 0 {
		windowSeconds = 86400
	}
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return nil, fmt.Errorf("marshal steps: %w", err)
	}

	now := time.Now().UTC()
	var id int64
	err = s.db.QueryRow(`
		INSERT INTO funnels(project_id, name, description, steps_json, conversion_window_seconds, created_at, updated_at)
		VALUES($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		projectID, name, description, string(stepsJSON), windowSeconds, now, now,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert funnel: %w", err)
	}

	return &Funnel{
		ID:                      id,
		ProjectID:               projectID,
		Name:                    name,
		Description:             description,
		Steps:                   steps,
		ConversionWindowSeconds: windowSeconds,
		CreatedAt:               now,
		UpdatedAt:               now,
	}, nil
}

func (s *Store) GetFunnel(projectID, funnelID int64) (*Funnel, error) {
	var f Funnel
	var stepsJSON string
	err := s.db.QueryRow(`
		SELECT id, project_id, name, description, steps_json, conversion_window_seconds, created_at, updated_at
		FROM funnels
		WHERE project_id = $1 AND id = $2`,
		projectID, funnelID,
	).Scan(&f.ID, &f.ProjectID, &f.Name, &f.Description, &stepsJSON, &f.ConversionWindowSeconds, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFunnelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query funnel: %w", err)
	}

	if err := json.Unmarshal([]byte(stepsJSON), &f.Steps); err != nil {
		f.Steps = []FunnelStep{}
	}
	return &f, nil
}

func (s *Store) ListFunnels(projectID int64) ([]*Funnel, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, name, description, steps_json, conversion_window_seconds, created_at, updated_at
		FROM funnels
		WHERE project_id = $1
		ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list funnels: %w", err)
	}
	defer rows.Close()

	var list []*Funnel
	for rows.Next() {
		var f Funnel
		var stepsJSON string
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.Name, &f.Description, &stepsJSON, &f.ConversionWindowSeconds, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan funnel: %w", err)
		}
		if err := json.Unmarshal([]byte(stepsJSON), &f.Steps); err != nil {
			f.Steps = []FunnelStep{}
		}
		list = append(list, &f)
	}
	return list, rows.Err()
}

func (s *Store) UpdateFunnel(projectID, funnelID int64, name, description string, steps []FunnelStep, windowSeconds int) (*Funnel, error) {
	if windowSeconds <= 0 {
		windowSeconds = 86400
	}
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return nil, fmt.Errorf("marshal steps: %w", err)
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(`
		UPDATE funnels
		SET name = $1, description = $2, steps_json = $3, conversion_window_seconds = $4, updated_at = $5
		WHERE project_id = $6 AND id = $7`,
		name, description, string(stepsJSON), windowSeconds, now, projectID, funnelID,
	)
	if err != nil {
		return nil, fmt.Errorf("update funnel: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrFunnelNotFound
	}
	return s.GetFunnel(projectID, funnelID)
}

func (s *Store) DeleteFunnel(projectID, funnelID int64) error {
	res, err := s.db.Exec(`DELETE FROM funnels WHERE project_id = $1 AND id = $2`, projectID, funnelID)
	if err != nil {
		return fmt.Errorf("delete funnel: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFunnelNotFound
	}
	return nil
}

type eventOccurrence struct {
	sessionID string
	name      string
	ts        time.Time
}

func (s *Store) CalculateFunnelResults(projectID, funnelID int64, days int) (*FunnelResult, error) {
	f, err := s.GetFunnel(projectID, funnelID)
	if err != nil {
		return nil, err
	}
	if len(f.Steps) == 0 {
		return &FunnelResult{Steps: []FunnelStepResult{}}, nil
	}

	if days <= 0 {
		days = 14
	}
	since := time.Now().UTC().AddDate(0, 0, -days)

	// Fetch event names needed
	stepNames := make([]string, len(f.Steps))
	nameMap := make(map[string]bool)
	for i, st := range f.Steps {
		stepNames[i] = st.Name
		nameMap[st.Name] = true
	}

	// Fetch items of type 'event' within time range
	rows, err := s.db.Query(`
		SELECT session_id, name, ts
		FROM items
		WHERE project_id = $1 AND type = 'event' AND ts >= $2
		ORDER BY session_id, ts ASC`,
		projectID, since,
	)
	if err != nil {
		return nil, fmt.Errorf("query funnel events: %w", err)
	}
	defer rows.Close()

	// Group events by session
	sessionEvents := make(map[string][]eventOccurrence)
	for rows.Next() {
		var ev eventOccurrence
		if err := rows.Scan(&ev.sessionID, &ev.name, &ev.ts); err != nil {
			return nil, fmt.Errorf("scan funnel event: %w", err)
		}
		if nameMap[ev.name] {
			sessionEvents[ev.sessionID] = append(sessionEvents[ev.sessionID], ev)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Calculate progression for each session
	stepCounts := make([]int, len(f.Steps))
	windowDuration := time.Duration(f.ConversionWindowSeconds) * time.Second
	var conversionDurations []int

	for _, events := range sessionEvents {
		reachedStep := -1
		var t0 time.Time
		var lastTime time.Time

		// Check sequence
		currentStep := 0
		for _, ev := range events {
			if currentStep >= len(f.Steps) {
				break
			}
			targetName := f.Steps[currentStep].Name
			if ev.name == targetName {
				if currentStep == 0 {
					t0 = ev.ts
					lastTime = ev.ts
					reachedStep = 0
					currentStep++
				} else {
					// Check within conversion window from t0 and after lastTime
					if (ev.ts.After(lastTime) || ev.ts.Equal(lastTime)) && ev.ts.Sub(t0) <= windowDuration {
						lastTime = ev.ts
						reachedStep = currentStep
						currentStep++
					}
				}
			}
		}

		if reachedStep >= 0 {
			for s := 0; s <= reachedStep; s++ {
				stepCounts[s]++
			}
			if reachedStep == len(f.Steps)-1 {
				conversionDurations = append(conversionDurations, int(lastTime.Sub(t0).Seconds()))
			}
		}
	}

	totalEntered := stepCounts[0]
	totalConverted := 0
	if len(stepCounts) > 0 {
		totalConverted = stepCounts[len(stepCounts)-1]
	}

	overallRate := 0.0
	if totalEntered > 0 {
		overallRate = float64(totalConverted) / float64(totalEntered)
	}

	medianDuration := 0
	if len(conversionDurations) > 0 {
		sort.Ints(conversionDurations)
		medianDuration = conversionDurations[len(conversionDurations)/2]
	}

	stepResults := make([]FunnelStepResult, len(f.Steps))
	for i, st := range f.Steps {
		count := stepCounts[i]
		dropOffCount := 0
		if i < len(f.Steps)-1 {
			dropOffCount = count - stepCounts[i+1]
		}

		convRate := 0.0
		if totalEntered > 0 {
			convRate = float64(count) / float64(totalEntered)
		}

		dropRate := 0.0
		if count > 0 {
			dropRate = float64(dropOffCount) / float64(count)
		}

		stepResults[i] = FunnelStepResult{
			StepIndex:      i,
			Name:           st.Name,
			Count:          count,
			ConversionRate: convRate,
			DropOffCount:   dropOffCount,
			DropOffRate:    dropRate,
		}
	}

	return &FunnelResult{
		TotalEntered:                totalEntered,
		TotalConverted:              totalConverted,
		OverallConversionRate:       overallRate,
		MedianConversionTimeSeconds: medianDuration,
		Steps:                       stepResults,
	}, nil
}

func (s *Store) GetFunnelDropoffs(projectID, funnelID int64, stepIndex int, days int, limit int) ([]string, error) {
	f, err := s.GetFunnel(projectID, funnelID)
	if err != nil {
		return nil, err
	}
	if stepIndex < 0 || stepIndex >= len(f.Steps)-1 {
		return []string{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if days <= 0 {
		days = 14
	}
	since := time.Now().UTC().AddDate(0, 0, -days)

	nameMap := make(map[string]bool)
	for _, st := range f.Steps {
		nameMap[st.Name] = true
	}

	rows, err := s.db.Query(`
		SELECT session_id, name, ts
		FROM items
		WHERE project_id = $1 AND type = 'event' AND ts >= $2
		ORDER BY session_id, ts ASC`,
		projectID, since,
	)
	if err != nil {
		return nil, fmt.Errorf("query dropoff events: %w", err)
	}
	defer rows.Close()

	sessionEvents := make(map[string][]eventOccurrence)
	for rows.Next() {
		var ev eventOccurrence
		if err := rows.Scan(&ev.sessionID, &ev.name, &ev.ts); err != nil {
			return nil, fmt.Errorf("scan dropoff event: %w", err)
		}
		if nameMap[ev.name] {
			sessionEvents[ev.sessionID] = append(sessionEvents[ev.sessionID], ev)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	windowDuration := time.Duration(f.ConversionWindowSeconds) * time.Second
	var dropoffSessionIDs []string

	for sessionID, events := range sessionEvents {
		reachedStep := -1
		var t0 time.Time
		var lastTime time.Time
		currentStep := 0

		for _, ev := range events {
			if currentStep >= len(f.Steps) {
				break
			}
			targetName := f.Steps[currentStep].Name
			if ev.name == targetName {
				if currentStep == 0 {
					t0 = ev.ts
					lastTime = ev.ts
					reachedStep = 0
					currentStep++
				} else {
					if (ev.ts.After(lastTime) || ev.ts.Equal(lastTime)) && ev.ts.Sub(t0) <= windowDuration {
						lastTime = ev.ts
						reachedStep = currentStep
						currentStep++
					}
				}
			}
		}

		// If the session reached exactly stepIndex, and did not reach stepIndex + 1
		if reachedStep == stepIndex {
			dropoffSessionIDs = append(dropoffSessionIDs, sessionID)
			if len(dropoffSessionIDs) >= limit {
				break
			}
		}
	}

	return dropoffSessionIDs, nil
}
