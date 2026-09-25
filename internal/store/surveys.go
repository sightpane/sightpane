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

var ErrSurveyNotFound = errors.New("survey not found")

type SurveyTargeting struct {
	URLPattern   string  `json:"url_pattern,omitempty"`
	EventTrigger string  `json:"event_trigger,omitempty"`
	SampleRate   float64 `json:"sample_rate,omitempty"`
}

type Survey struct {
	ID          int64           `json:"id"`
	ProjectID   int64           `json:"project_id"`
	Name        string          `json:"name"`
	Type        string          `json:"type"` // 'nps', 'csat', 'rating', 'open_text', 'single_choice'
	Question    string          `json:"question"`
	Description string          `json:"description"`
	Choices     []string        `json:"choices"`
	Targeting   SurveyTargeting `json:"targeting"`
	Active      bool            `json:"active"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type SurveyResponse struct {
	ID           int64     `json:"id"`
	SurveyID     int64     `json:"survey_id"`
	ProjectID    int64     `json:"project_id"`
	SessionID    *string   `json:"session_id"`
	UserID       string    `json:"user_id"`
	Score        *int      `json:"score"`
	ResponseText string    `json:"response_text"`
	CreatedAt    time.Time `json:"created_at"`
}

type ScoreBucket struct {
	Score int `json:"score"`
	Count int `json:"count"`
}

type SurveyResults struct {
	SurveyID         int64          `json:"survey_id"`
	Type             string         `json:"type"`
	TotalResponses   int            `json:"total_responses"`
	NPSScore         *float64       `json:"nps_score,omitempty"`
	PromotersCount   int            `json:"promoters_count"`
	PassivesCount    int            `json:"passives_count"`
	DetractorsCount  int            `json:"detractors_count"`
	AverageScore     *float64       `json:"average_score,omitempty"`
	SatisfactionRate *float64       `json:"satisfaction_rate,omitempty"`
	Distribution     []ScoreBucket  `json:"distribution"`
	ChoiceCounts     map[string]int `json:"choice_counts,omitempty"`
}

func (s *Store) ListSurveys(projectID int64) ([]*Survey, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, name, type, question, description, choices, targeting, active, created_at, updated_at
		FROM surveys
		WHERE project_id = $1
		ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list surveys: %w", err)
	}
	defer rows.Close()

	var result []*Survey
	for rows.Next() {
		var s Survey
		var choicesJSON, targetingJSON []byte
		if err := rows.Scan(
			&s.ID, &s.ProjectID, &s.Name, &s.Type, &s.Question, &s.Description,
			&choicesJSON, &targetingJSON, &s.Active, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan survey: %w", err)
		}
		if len(choicesJSON) > 0 {
			_ = json.Unmarshal(choicesJSON, &s.Choices)
		}
		if s.Choices == nil {
			s.Choices = []string{}
		}
		if len(targetingJSON) > 0 {
			_ = json.Unmarshal(targetingJSON, &s.Targeting)
		}
		result = append(result, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		result = []*Survey{}
	}
	return result, nil
}

func (s *Store) GetSurvey(projectID, surveyID int64) (*Survey, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sur Survey
	var choicesJSON, targetingJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, name, type, question, description, choices, targeting, active, created_at, updated_at
		FROM surveys
		WHERE project_id = $1 AND id = $2
	`, projectID, surveyID).Scan(
		&sur.ID, &sur.ProjectID, &sur.Name, &sur.Type, &sur.Question, &sur.Description,
		&choicesJSON, &targetingJSON, &sur.Active, &sur.CreatedAt, &sur.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSurveyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get survey: %w", err)
	}
	if len(choicesJSON) > 0 {
		_ = json.Unmarshal(choicesJSON, &sur.Choices)
	}
	if sur.Choices == nil {
		sur.Choices = []string{}
	}
	if len(targetingJSON) > 0 {
		_ = json.Unmarshal(targetingJSON, &sur.Targeting)
	}
	return &sur, nil
}

func (s *Store) CreateSurvey(sur *Survey) (*Survey, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if sur.Choices == nil {
		sur.Choices = []string{}
	}
	choicesJSON, err := json.Marshal(sur.Choices)
	if err != nil {
		return nil, err
	}
	targetingJSON, err := json.Marshal(sur.Targeting)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO surveys (project_id, name, type, question, description, choices, targeting, active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at, updated_at
	`, sur.ProjectID, sur.Name, sur.Type, sur.Question, sur.Description, choicesJSON, targetingJSON, sur.Active, now, now).Scan(
		&sur.ID, &sur.CreatedAt, &sur.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create survey: %w", err)
	}
	return sur, nil
}

func (s *Store) UpdateSurvey(sur *Survey) (*Survey, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if sur.Choices == nil {
		sur.Choices = []string{}
	}
	choicesJSON, err := json.Marshal(sur.Choices)
	if err != nil {
		return nil, err
	}
	targetingJSON, err := json.Marshal(sur.Targeting)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	res, err := s.db.ExecContext(ctx, `
		UPDATE surveys
		SET name = $1, type = $2, question = $3, description = $4, choices = $5, targeting = $6, active = $7, updated_at = $8
		WHERE project_id = $9 AND id = $10
	`, sur.Name, sur.Type, sur.Question, sur.Description, choicesJSON, targetingJSON, sur.Active, now, sur.ProjectID, sur.ID)
	if err != nil {
		return nil, fmt.Errorf("update survey: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrSurveyNotFound
	}
	sur.UpdatedAt = now
	return sur, nil
}

func (s *Store) DeleteSurvey(projectID, surveyID int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := s.db.ExecContext(ctx, `
		DELETE FROM surveys
		WHERE project_id = $1 AND id = $2
	`, projectID, surveyID)
	if err != nil {
		return fmt.Errorf("delete survey: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSurveyNotFound
	}
	return nil
}

func (s *Store) GetActiveSurveysForClient(projectID int64) ([]*Survey, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, name, type, question, description, choices, targeting, active, created_at, updated_at
		FROM surveys
		WHERE project_id = $1 AND active = true
		ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("active surveys: %w", err)
	}
	defer rows.Close()

	var result []*Survey
	for rows.Next() {
		var s Survey
		var choicesJSON, targetingJSON []byte
		if err := rows.Scan(
			&s.ID, &s.ProjectID, &s.Name, &s.Type, &s.Question, &s.Description,
			&choicesJSON, &targetingJSON, &s.Active, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan active survey: %w", err)
		}
		if len(choicesJSON) > 0 {
			_ = json.Unmarshal(choicesJSON, &s.Choices)
		}
		if s.Choices == nil {
			s.Choices = []string{}
		}
		if len(targetingJSON) > 0 {
			_ = json.Unmarshal(targetingJSON, &s.Targeting)
		}
		result = append(result, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		result = []*Survey{}
	}
	return result, nil
}

func (s *Store) SubmitSurveyResponse(resp *SurveyResponse) (*SurveyResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The SDK can show a survey before its first envelope has created the
	// session row, so the answer links to the session only when that row is
	// already there — in this project — and is kept unlinked otherwise rather
	// than refused by the foreign key.
	now := time.Now()
	var session sql.NullString
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO survey_responses (survey_id, project_id, session_id, user_id, score, response_text, created_at)
		VALUES ($1, $2, (SELECT id FROM sessions WHERE id = $3 AND project_id = $2), $4, $5, $6, $7)
		RETURNING id, created_at, session_id
	`, resp.SurveyID, resp.ProjectID, resp.SessionID, resp.UserID, resp.Score, resp.ResponseText, now).Scan(
		&resp.ID, &resp.CreatedAt, &session,
	)
	if err != nil {
		return nil, fmt.Errorf("submit survey response: %w", err)
	}
	resp.SessionID = nil
	if session.Valid {
		resp.SessionID = &session.String
	}
	return resp, nil
}

func (s *Store) ListSurveyResponses(projectID, surveyID int64, limit int) ([]*SurveyResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if limit <= 0 || limit > 500 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, survey_id, project_id, session_id, user_id, score, response_text, created_at
		FROM survey_responses
		WHERE project_id = $1 AND survey_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, projectID, surveyID, limit)
	if err != nil {
		return nil, fmt.Errorf("list survey responses: %w", err)
	}
	defer rows.Close()

	var result []*SurveyResponse
	for rows.Next() {
		var r SurveyResponse
		if err := rows.Scan(
			&r.ID, &r.SurveyID, &r.ProjectID, &r.SessionID, &r.UserID, &r.Score, &r.ResponseText, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan survey response: %w", err)
		}
		result = append(result, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		result = []*SurveyResponse{}
	}
	return result, nil
}

func (s *Store) GetSurveyResults(projectID, surveyID int64) (*SurveyResults, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	survey, err := s.GetSurvey(projectID, surveyID)
	if err != nil {
		return nil, err
	}

	results := &SurveyResults{
		SurveyID:     surveyID,
		Type:         survey.Type,
		Distribution: []ScoreBucket{},
		ChoiceCounts: make(map[string]int),
	}

	// Calculate total count
	var totalResponses int
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM survey_responses
		WHERE project_id = $1 AND survey_id = $2
	`, projectID, surveyID).Scan(&totalResponses)
	if err != nil {
		return nil, fmt.Errorf("count survey responses: %w", err)
	}
	results.TotalResponses = totalResponses

	if totalResponses == 0 {
		return results, nil
	}

	switch survey.Type {
	case "nps":
		// Scores 0-10
		var promoters, passives, detractors int
		err = s.db.QueryRowContext(ctx, `
			SELECT
				COUNT(*) FILTER (WHERE score >= 9) as promoters,
				COUNT(*) FILTER (WHERE score >= 7 AND score <= 8) as passives,
				COUNT(*) FILTER (WHERE score >= 0 AND score <= 6) as detractors
			FROM survey_responses
			WHERE project_id = $1 AND survey_id = $2 AND score IS NOT NULL
		`, projectID, surveyID).Scan(&promoters, &passives, &detractors)
		if err != nil {
			return nil, fmt.Errorf("nps metrics: %w", err)
		}

		results.PromotersCount = promoters
		results.PassivesCount = passives
		results.DetractorsCount = detractors

		scoredTotal := promoters + passives + detractors
		if scoredTotal > 0 {
			nps := (float64(promoters-detractors) / float64(scoredTotal)) * 100.0
			results.NPSScore = &nps
		}

		// Bucket breakdown 0 to 10
		bucketMap := make(map[int]int)
		for i := 0; i <= 10; i++ {
			bucketMap[i] = 0
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT score, COUNT(*)
			FROM survey_responses
			WHERE project_id = $1 AND survey_id = $2 AND score IS NOT NULL
			GROUP BY score
			ORDER BY score
		`, projectID, surveyID)
		if err != nil {
			return nil, fmt.Errorf("nps distribution: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sc, cnt int
			if err := rows.Scan(&sc, &cnt); err != nil {
				return nil, fmt.Errorf("scan nps bucket: %w", err)
			}
			bucketMap[sc] = cnt
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate nps buckets: %w", err)
		}
		for i := 0; i <= 10; i++ {
			results.Distribution = append(results.Distribution, ScoreBucket{Score: i, Count: bucketMap[i]})
		}

	case "csat", "rating":
		// Scores 1-5
		var avgScore float64
		var satisfiedCount, totalScored int
		err = s.db.QueryRowContext(ctx, `
			SELECT
				COALESCE(AVG(score), 0),
				COUNT(*) FILTER (WHERE score >= 4),
				COUNT(*)
			FROM survey_responses
			WHERE project_id = $1 AND survey_id = $2 AND score IS NOT NULL
		`, projectID, surveyID).Scan(&avgScore, &satisfiedCount, &totalScored)
		if err != nil {
			return nil, fmt.Errorf("csat metrics: %w", err)
		}

		results.AverageScore = &avgScore
		if totalScored > 0 {
			satRate := (float64(satisfiedCount) / float64(totalScored)) * 100.0
			results.SatisfactionRate = &satRate
		}

		bucketMap := make(map[int]int)
		for i := 1; i <= 5; i++ {
			bucketMap[i] = 0
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT score, COUNT(*)
			FROM survey_responses
			WHERE project_id = $1 AND survey_id = $2 AND score IS NOT NULL
			GROUP BY score
			ORDER BY score
		`, projectID, surveyID)
		if err != nil {
			return nil, fmt.Errorf("csat distribution: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sc, cnt int
			if err := rows.Scan(&sc, &cnt); err != nil {
				return nil, fmt.Errorf("scan csat bucket: %w", err)
			}
			bucketMap[sc] = cnt
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate csat buckets: %w", err)
		}
		for i := 1; i <= 5; i++ {
			results.Distribution = append(results.Distribution, ScoreBucket{Score: i, Count: bucketMap[i]})
		}

	case "single_choice":
		for _, ch := range survey.Choices {
			results.ChoiceCounts[ch] = 0
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT response_text, COUNT(*)
			FROM survey_responses
			WHERE project_id = $1 AND survey_id = $2 AND response_text != ''
			GROUP BY response_text
		`, projectID, surveyID)
		if err != nil {
			return nil, fmt.Errorf("single choice counts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var txt string
			var cnt int
			if err := rows.Scan(&txt, &cnt); err != nil {
				return nil, fmt.Errorf("scan choice count: %w", err)
			}
			results.ChoiceCounts[txt] = cnt
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate choice counts: %w", err)
		}
	}

	return results, nil
}
