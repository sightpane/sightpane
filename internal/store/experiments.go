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
	"math"
	"time"
)

var ErrExperimentNotFound = errors.New("experiment not found")

type ExperimentVariant struct {
	Key  string `json:"key"`  // e.g. "control", "variant_b"
	Name string `json:"name"` // e.g. "Control", "Treatment"
}

type Experiment struct {
	ID                    int64               `json:"id"`
	ProjectID             int64               `json:"project_id"`
	Name                  string              `json:"name"`
	Description           string              `json:"description"`
	FeatureFlagID         *int64              `json:"feature_flag_id"`
	FeatureFlagKey        string              `json:"feature_flag_key"`
	Status                string              `json:"status"` // 'draft', 'running', 'concluded'
	PrimaryMetricEvent    string              `json:"primary_metric_event"`
	SecondaryMetricEvents []string            `json:"secondary_metric_events"`
	Variants              []ExperimentVariant `json:"variants"`
	MinimumSampleSize     int                 `json:"minimum_sample_size"`
	WinnerVariant         *string             `json:"winner_variant"`
	StartedAt             *time.Time          `json:"started_at"`
	ConcludedAt           *time.Time          `json:"concluded_at"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
}

type VariantResult struct {
	Key                string     `json:"key"`
	Name               string     `json:"name"`
	Participants       int        `json:"participants"`
	Conversions        int        `json:"conversions"`
	ConversionRate     float64    `json:"conversion_rate"`
	ConfidenceInterval [2]float64 `json:"confidence_interval"`
	RelativeLift       float64    `json:"relative_lift"`
	ChanceToWin        float64    `json:"chance_to_win"`
	PValue             float64    `json:"p_value"`
}

type ExperimentResults struct {
	ExperimentID            int64           `json:"experiment_id"`
	Status                  string          `json:"status"`
	TotalParticipants       int             `json:"total_participants"`
	StatisticalSignificance float64         `json:"statistical_significance"`
	IsSignificant           bool            `json:"is_significant"`
	RecommendedAction       string          `json:"recommended_action"`
	Variants                []VariantResult `json:"variants"`
}

func (s *Store) CreateExperiment(projectID int64, name, description, flagKey, primaryMetric string, variants []ExperimentVariant, minSample int) (*Experiment, error) {
	if variants == nil {
		variants = []ExperimentVariant{
			{Key: "control", Name: "Control"},
			{Key: "test", Name: "Treatment"},
		}
	}
	if minSample <= 0 {
		minSample = 1000
	}

	// Try to resolve feature flag ID if key exists
	var flagID *int64
	flag, err := s.GetFeatureFlag(projectID, flagKey)
	if err == nil && flag != nil {
		flagID = &flag.ID
	}

	variantsBytes, err := json.Marshal(variants)
	if err != nil {
		return nil, fmt.Errorf("marshal variants: %w", err)
	}
	secMetricsBytes, _ := json.Marshal([]string{})

	now := time.Now()
	var exp Experiment
	exp.ProjectID = projectID
	exp.Name = name
	exp.Description = description
	exp.FeatureFlagID = flagID
	exp.FeatureFlagKey = flagKey
	exp.Status = "running"
	exp.PrimaryMetricEvent = primaryMetric
	exp.SecondaryMetricEvents = []string{}
	exp.Variants = variants
	exp.MinimumSampleSize = minSample
	exp.StartedAt = &now

	row := s.db.QueryRow(`
		INSERT INTO experiments (
			project_id, name, description, feature_flag_id, feature_flag_key,
			status, primary_metric_event, secondary_metric_events, variants,
			minimum_sample_size, started_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
		RETURNING id, created_at, updated_at
	`, projectID, name, description, flagID, flagKey, exp.Status, primaryMetric, secMetricsBytes, variantsBytes, minSample, exp.StartedAt)

	if err := row.Scan(&exp.ID, &exp.CreatedAt, &exp.UpdatedAt); err != nil {
		return nil, fmt.Errorf("insert experiment: %w", err)
	}

	return &exp, nil
}

func (s *Store) GetExperiment(projectID int64, id int64) (*Experiment, error) {
	var exp Experiment
	var secBytes, varBytes []byte

	row := s.db.QueryRow(`
		SELECT id, project_id, name, description, feature_flag_id, feature_flag_key,
		       status, primary_metric_event, secondary_metric_events, variants,
		       minimum_sample_size, winner_variant, started_at, concluded_at, created_at, updated_at
		FROM experiments
		WHERE project_id = $1 AND id = $2
	`, projectID, id)

	if err := row.Scan(
		&exp.ID, &exp.ProjectID, &exp.Name, &exp.Description, &exp.FeatureFlagID, &exp.FeatureFlagKey,
		&exp.Status, &exp.PrimaryMetricEvent, &secBytes, &varBytes,
		&exp.MinimumSampleSize, &exp.WinnerVariant, &exp.StartedAt, &exp.ConcludedAt, &exp.CreatedAt, &exp.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrExperimentNotFound
		}
		return nil, err
	}

	_ = json.Unmarshal(secBytes, &exp.SecondaryMetricEvents)
	_ = json.Unmarshal(varBytes, &exp.Variants)
	return &exp, nil
}

func (s *Store) ListExperiments(projectID int64) ([]*Experiment, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, name, description, feature_flag_id, feature_flag_key,
		       status, primary_metric_event, secondary_metric_events, variants,
		       minimum_sample_size, winner_variant, started_at, concluded_at, created_at, updated_at
		FROM experiments
		WHERE project_id = $1
		ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var exps []*Experiment
	for rows.Next() {
		var exp Experiment
		var secBytes, varBytes []byte
		if err := rows.Scan(
			&exp.ID, &exp.ProjectID, &exp.Name, &exp.Description, &exp.FeatureFlagID, &exp.FeatureFlagKey,
			&exp.Status, &exp.PrimaryMetricEvent, &secBytes, &varBytes,
			&exp.MinimumSampleSize, &exp.WinnerVariant, &exp.StartedAt, &exp.ConcludedAt, &exp.CreatedAt, &exp.UpdatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(secBytes, &exp.SecondaryMetricEvents)
		_ = json.Unmarshal(varBytes, &exp.Variants)
		exps = append(exps, &exp)
	}
	return exps, rows.Err()
}

func (s *Store) UpdateExperiment(projectID, id int64, name, description, status string, minSample int) (*Experiment, error) {
	var exp Experiment
	var secBytes, varBytes []byte

	row := s.db.QueryRow(`
		UPDATE experiments
		SET name = $1, description = $2, status = $3, minimum_sample_size = $4, updated_at = NOW()
		WHERE project_id = $5 AND id = $6
		RETURNING id, project_id, name, description, feature_flag_id, feature_flag_key,
		       status, primary_metric_event, secondary_metric_events, variants,
		       minimum_sample_size, winner_variant, started_at, concluded_at, created_at, updated_at
	`, name, description, status, minSample, projectID, id)

	if err := row.Scan(
		&exp.ID, &exp.ProjectID, &exp.Name, &exp.Description, &exp.FeatureFlagID, &exp.FeatureFlagKey,
		&exp.Status, &exp.PrimaryMetricEvent, &secBytes, &varBytes,
		&exp.MinimumSampleSize, &exp.WinnerVariant, &exp.StartedAt, &exp.ConcludedAt, &exp.CreatedAt, &exp.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrExperimentNotFound
		}
		return nil, err
	}

	_ = json.Unmarshal(secBytes, &exp.SecondaryMetricEvents)
	_ = json.Unmarshal(varBytes, &exp.Variants)
	return &exp, nil
}

func (s *Store) DeleteExperiment(projectID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM experiments WHERE project_id = $1 AND id = $2`, projectID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrExperimentNotFound
	}
	return nil
}

func (s *Store) ConcludeExperimentWinner(projectID, id int64, winnerVariant string) (*Experiment, error) {
	exp, err := s.GetExperiment(projectID, id)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now()
	_, err = tx.Exec(`
		UPDATE experiments
		SET status = 'concluded', winner_variant = $1, concluded_at = $2, updated_at = NOW()
		WHERE project_id = $3 AND id = $4
	`, winnerVariant, now, projectID, id)
	if err != nil {
		return nil, err
	}

	// Update linked feature flag rollout to 100% for the winning variant
	if exp.FeatureFlagKey != "" {
		flag, _ := s.GetFeatureFlag(projectID, exp.FeatureFlagKey)
		if flag != nil {
			newVariants := make([]FlagVariant, len(flag.Variants))
			for i, v := range flag.Variants {
				roll := 0
				if v.Key == winnerVariant {
					roll = 100
				}
				newVariants[i] = FlagVariant{
					Key:     v.Key,
					Rollout: roll,
				}
			}
			varBytes, err := json.Marshal(newVariants)
			if err != nil {
				return nil, err
			}
			_, err = tx.Exec(`
				UPDATE feature_flags
				SET enabled = true, rollout_percentage = 100, variants = $1, updated_at = NOW()
				WHERE project_id = $2 AND key = $3
			`, varBytes, projectID, exp.FeatureFlagKey)
			if err != nil {
				return nil, err
			}
		} else {
			_, err = tx.Exec(`
				UPDATE feature_flags
				SET enabled = true, rollout_percentage = 100, updated_at = NOW()
				WHERE project_id = $1 AND key = $2
			`, projectID, exp.FeatureFlagKey)
			if err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	exp.Status = "concluded"
	exp.WinnerVariant = &winnerVariant
	exp.ConcludedAt = &now
	return exp, nil
}

// CalculateExperimentResults computes variant participation, conversion rate, and statistical significance.
func (s *Store) CalculateExperimentResults(ctx context.Context, projectID, id int64, days int) (*ExperimentResults, error) {
	exp, err := s.GetExperiment(projectID, id)
	if err != nil {
		return nil, err
	}

	if days <= 0 {
		days = 14
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	if exp.StartedAt != nil && exp.StartedAt.After(since) {
		since = *exp.StartedAt
	}

	// Fetch linked flag
	flag, _ := s.GetFeatureFlag(projectID, exp.FeatureFlagKey)
	if flag == nil {
		// Mock a flag with the experiment's variants if none exists
		var flagVariants []FlagVariant
		for _, v := range exp.Variants {
			flagVariants = append(flagVariants, FlagVariant{Key: v.Key, Rollout: 100 / len(exp.Variants)})
		}
		flag = &FeatureFlag{
			Key:               exp.FeatureFlagKey,
			Enabled:           true,
			RolloutPercentage: 100,
			Variants:          flagVariants,
		}
	}

	// 1. Find distinct participants in the window
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT COALESCE(NULLIF(user_id, ''), id) as distinct_id
		FROM sessions
		WHERE project_id = $1 AND started_at >= $2
	`, projectID, since)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	var allUsers []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan experiment session user: %w", err)
		}
		if uid != "" {
			allUsers = append(allUsers, uid)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate experiment session users: %w", err)
	}

	// 2. Find converted users in the window
	cRows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT COALESCE(NULLIF(s.user_id, ''), s.id) as distinct_id
		FROM items i
		JOIN sessions s ON i.session_id = s.id
		WHERE i.project_id = $1 AND i.type = 'event' AND i.name = $2 AND i.ts >= $3
	`, projectID, exp.PrimaryMetricEvent, since)
	if err != nil {
		return nil, fmt.Errorf("query conversions: %w", err)
	}
	defer cRows.Close()

	convertedSet := make(map[string]bool)
	for cRows.Next() {
		var uid string
		if err := cRows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan experiment conversion user: %w", err)
		}
		if uid != "" {
			convertedSet[uid] = true
		}
	}
	if err := cRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate experiment conversion users: %w", err)
	}

	// 3. Bucket participants and conversions into variants
	variantParticipants := make(map[string]int)
	variantConversions := make(map[string]int)

	for _, v := range exp.Variants {
		variantParticipants[v.Key] = 0
		variantConversions[v.Key] = 0
	}

	for _, uid := range allUsers {
		val, active := EvaluateFlag(flag, uid, nil)
		var vKey string
		if active {
			if str, ok := val.(string); ok && str != "" {
				vKey = str
			} else {
				vKey = exp.Variants[0].Key
			}
		} else {
			vKey = exp.Variants[0].Key
		}

		if _, exists := variantParticipants[vKey]; !exists {
			vKey = exp.Variants[0].Key
		}

		variantParticipants[vKey]++
		if convertedSet[uid] {
			variantConversions[vKey]++
		}
	}

	// 4. Calculate statistical metrics
	results := &ExperimentResults{
		ExperimentID:      exp.ID,
		Status:            exp.Status,
		TotalParticipants: len(allUsers),
		Variants:          make([]VariantResult, 0, len(exp.Variants)),
	}

	var controlRate float64
	var controlPart, controlConv int

	for i, v := range exp.Variants {
		n := variantParticipants[v.Key]
		c := variantConversions[v.Key]
		var rate float64
		if n > 0 {
			rate = float64(c) / float64(n)
		}

		ci := calculateConfidenceInterval(rate, n)

		vr := VariantResult{
			Key:                v.Key,
			Name:               v.Name,
			Participants:       n,
			Conversions:        c,
			ConversionRate:     rate,
			ConfidenceInterval: ci,
		}

		if i == 0 {
			controlRate = rate
			controlPart = n
			controlConv = c
			vr.RelativeLift = 0.0
			vr.ChanceToWin = 0.5
			vr.PValue = 1.0
		} else {
			// Compare treatment against control
			lift := 0.0
			if controlRate > 0 {
				lift = (rate - controlRate) / controlRate
			}
			pValue, chanceToWin := calculateSignificance(controlConv, controlPart, c, n)
			vr.RelativeLift = lift
			vr.ChanceToWin = chanceToWin
			vr.PValue = pValue

			if 1.0-pValue > results.StatisticalSignificance {
				results.StatisticalSignificance = 1.0 - pValue
			}
		}

		results.Variants = append(results.Variants, vr)
	}

	if results.StatisticalSignificance >= 0.95 {
		results.IsSignificant = true
		// Pick variant with highest conversion rate
		var bestKey string
		var bestRate = -1.0
		for _, vr := range results.Variants {
			if vr.ConversionRate > bestRate {
				bestRate = vr.ConversionRate
				bestKey = vr.Key
			}
		}
		results.RecommendedAction = bestKey + "_winning"
	} else if results.TotalParticipants < exp.MinimumSampleSize {
		results.RecommendedAction = "needs_more_data"
	} else {
		results.RecommendedAction = "inconclusive"
	}

	return results, nil
}

func calculateConfidenceInterval(p float64, n int) [2]float64 {
	if n <= 0 || p <= 0 || p >= 1 {
		return [2]float64{p, p}
	}
	margin := 1.96 * math.Sqrt((p*(1.0-p))/float64(n))
	low := math.Max(0.0, p-margin)
	high := math.Min(1.0, p+margin)
	return [2]float64{low, high}
}

// calculateSignificance computes p-value and Bayesian chance-to-win using standard normal approximation.
func calculateSignificance(c1, n1, c2, n2 int) (pValue, chanceToWin float64) {
	if n1 <= 0 || n2 <= 0 {
		return 1.0, 0.5
	}

	p1 := float64(c1) / float64(n1)
	p2 := float64(c2) / float64(n2)

	// Pooled proportion for null hypothesis
	pPooled := float64(c1+c2) / float64(n1+n2)
	if pPooled <= 0 || pPooled >= 1 {
		return 1.0, 0.5
	}

	se := math.Sqrt(pPooled * (1.0 - pPooled) * (1.0/float64(n1) + 1.0/float64(n2)))
	if se == 0 {
		return 1.0, 0.5
	}

	z := (p2 - p1) / se
	// Two-tailed p-value
	pValue = 2.0 * (1.0 - normalCDF(math.Abs(z)))
	if pValue < 0.0001 {
		pValue = 0.0001
	} else if pValue > 1.0 {
		pValue = 1.0
	}

	// Bayesian chance to win: P(Treatment > Control)
	seDiff := math.Sqrt((p1*(1.0-p1))/float64(n1) + (p2*(1.0-p2))/float64(n2))
	if seDiff > 0 {
		zDiff := (p2 - p1) / seDiff
		chanceToWin = normalCDF(zDiff)
	} else {
		chanceToWin = 0.5
	}

	return pValue, chanceToWin
}

// normalCDF calculates the standard normal cumulative distribution function using math.Erf.
func normalCDF(x float64) float64 {
	return 0.5 * (1.0 + math.Erf(x/math.Sqrt2))
}
