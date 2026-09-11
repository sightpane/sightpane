// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrFeatureFlagNotFound = errors.New("feature flag not found")

type FlagFilter struct {
	Property string `json:"property"` // e.g. "role", "app_version", "distinct_id"
	Operator string `json:"operator"` // "exact", "not_exact", "contains", "regex", "gt", "gte", "lt", "lte", "in"
	Value    any    `json:"value"`
}

type FlagVariant struct {
	Key     string `json:"key"`     // e.g. "control", "variant_a"
	Rollout int    `json:"rollout"` // e.g. 50 (percentage of variant split)
}

type FeatureFlag struct {
	ID                int64         `json:"id"`
	ProjectID         int64         `json:"project_id"`
	Key               string        `json:"key"`
	Name              string        `json:"name"`
	Description       string        `json:"description"`
	Enabled           bool          `json:"enabled"`
	RolloutPercentage int           `json:"rollout_percentage"`
	Filters           []FlagFilter  `json:"filters"`
	Variants          []FlagVariant `json:"variants"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

func (s *Store) CreateFeatureFlag(projectID int64, key, name, description string, enabled bool, rolloutPercentage int, filters []FlagFilter, variants []FlagVariant) (*FeatureFlag, error) {
	if filters == nil {
		filters = []FlagFilter{}
	}
	if variants == nil {
		variants = []FlagVariant{}
	}
	if rolloutPercentage < 0 {
		rolloutPercentage = 0
	} else if rolloutPercentage > 100 {
		rolloutPercentage = 100
	}

	filtersBytes, err := json.Marshal(filters)
	if err != nil {
		return nil, fmt.Errorf("marshal filters: %w", err)
	}
	variantsBytes, err := json.Marshal(variants)
	if err != nil {
		return nil, fmt.Errorf("marshal variants: %w", err)
	}

	var f FeatureFlag
	f.ProjectID = projectID
	f.Key = key
	f.Name = name
	f.Description = description
	f.Enabled = enabled
	f.RolloutPercentage = rolloutPercentage
	f.Filters = filters
	f.Variants = variants

	row := s.db.QueryRow(`
		INSERT INTO feature_flags (project_id, key, name, description, enabled, rollout_percentage, filters, variants, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		RETURNING id, created_at, updated_at
	`, projectID, key, name, description, enabled, rolloutPercentage, filtersBytes, variantsBytes)

	if err := row.Scan(&f.ID, &f.CreatedAt, &f.UpdatedAt); err != nil {
		return nil, fmt.Errorf("insert feature flag: %w", err)
	}
	return &f, nil
}

func (s *Store) GetFeatureFlag(projectID int64, key string) (*FeatureFlag, error) {
	var f FeatureFlag
	var filtersBytes, variantsBytes []byte

	row := s.db.QueryRow(`
		SELECT id, project_id, key, name, description, enabled, rollout_percentage, filters, variants, created_at, updated_at
		FROM feature_flags
		WHERE project_id = $1 AND key = $2
	`, projectID, key)

	if err := row.Scan(&f.ID, &f.ProjectID, &f.Key, &f.Name, &f.Description, &f.Enabled, &f.RolloutPercentage, &filtersBytes, &variantsBytes, &f.CreatedAt, &f.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFeatureFlagNotFound
		}
		return nil, err
	}

	_ = json.Unmarshal(filtersBytes, &f.Filters)
	_ = json.Unmarshal(variantsBytes, &f.Variants)
	return &f, nil
}

func (s *Store) GetFeatureFlagByID(projectID int64, id int64) (*FeatureFlag, error) {
	var f FeatureFlag
	var filtersBytes, variantsBytes []byte

	row := s.db.QueryRow(`
		SELECT id, project_id, key, name, description, enabled, rollout_percentage, filters, variants, created_at, updated_at
		FROM feature_flags
		WHERE project_id = $1 AND id = $2
	`, projectID, id)

	if err := row.Scan(&f.ID, &f.ProjectID, &f.Key, &f.Name, &f.Description, &f.Enabled, &f.RolloutPercentage, &filtersBytes, &variantsBytes, &f.CreatedAt, &f.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFeatureFlagNotFound
		}
		return nil, err
	}

	_ = json.Unmarshal(filtersBytes, &f.Filters)
	_ = json.Unmarshal(variantsBytes, &f.Variants)
	return &f, nil
}

func (s *Store) ListFeatureFlags(projectID int64) ([]*FeatureFlag, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, key, name, description, enabled, rollout_percentage, filters, variants, created_at, updated_at
		FROM feature_flags
		WHERE project_id = $1
		ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var flags []*FeatureFlag
	for rows.Next() {
		var f FeatureFlag
		var filtersBytes, variantsBytes []byte
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.Key, &f.Name, &f.Description, &f.Enabled, &f.RolloutPercentage, &filtersBytes, &variantsBytes, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(filtersBytes, &f.Filters)
		_ = json.Unmarshal(variantsBytes, &f.Variants)
		flags = append(flags, &f)
	}
	return flags, rows.Err()
}

func (s *Store) UpdateFeatureFlag(projectID int64, id int64, name, description string, enabled bool, rolloutPercentage int, filters []FlagFilter, variants []FlagVariant) (*FeatureFlag, error) {
	if filters == nil {
		filters = []FlagFilter{}
	}
	if variants == nil {
		variants = []FlagVariant{}
	}
	if rolloutPercentage < 0 {
		rolloutPercentage = 0
	} else if rolloutPercentage > 100 {
		rolloutPercentage = 100
	}

	filtersBytes, err := json.Marshal(filters)
	if err != nil {
		return nil, fmt.Errorf("marshal filters: %w", err)
	}
	variantsBytes, err := json.Marshal(variants)
	if err != nil {
		return nil, fmt.Errorf("marshal variants: %w", err)
	}

	var f FeatureFlag
	row := s.db.QueryRow(`
		UPDATE feature_flags
		SET name = $1, description = $2, enabled = $3, rollout_percentage = $4, filters = $5, variants = $6, updated_at = NOW()
		WHERE project_id = $7 AND id = $8
		RETURNING id, project_id, key, name, description, enabled, rollout_percentage, filters, variants, created_at, updated_at
	`, name, description, enabled, rolloutPercentage, filtersBytes, variantsBytes, projectID, id)

	var retFilters, retVariants []byte
	if err := row.Scan(&f.ID, &f.ProjectID, &f.Key, &f.Name, &f.Description, &f.Enabled, &f.RolloutPercentage, &retFilters, &retVariants, &f.CreatedAt, &f.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFeatureFlagNotFound
		}
		return nil, err
	}

	_ = json.Unmarshal(retFilters, &f.Filters)
	_ = json.Unmarshal(retVariants, &f.Variants)
	return &f, nil
}

func (s *Store) DeleteFeatureFlag(projectID int64, id int64) error {
	res, err := s.db.Exec(`DELETE FROM feature_flags WHERE project_id = $1 AND id = $2`, projectID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFeatureFlagNotFound
	}
	return nil
}

// EvaluateFlag evaluates a single flag against distinctID and user properties deterministically.
func EvaluateFlag(flag *FeatureFlag, distinctID string, properties map[string]any) (any, bool) {
	if !flag.Enabled {
		return false, false
	}

	// 1. Property filtering rules
	if len(flag.Filters) > 0 {
		for _, filter := range flag.Filters {
			if !matchesFilter(filter, distinctID, properties) {
				return false, false
			}
		}
	}

	// 2. Rollout percentage check
	if flag.RolloutPercentage <= 0 {
		return false, false
	}

	var hashVal int
	if distinctID != "" {
		hashVal = hashDistinctID(flag.Key, distinctID)
	} else {
		hashVal = 0 // If no distinctID, deterministic 0
	}

	if hashVal >= flag.RolloutPercentage {
		return false, false
	}

	// 3. Multivariant distribution
	if len(flag.Variants) > 0 {
		totalRollout := 0
		for _, v := range flag.Variants {
			totalRollout += v.Rollout
		}
		if totalRollout > 0 {
			varHash := hashDistinctID(flag.Key+":variant", distinctID)
			variantTarget := (varHash * totalRollout) / 100
			cumulative := 0
			for _, v := range flag.Variants {
				cumulative += v.Rollout
				if variantTarget < cumulative {
					return v.Key, true
				}
			}
			return flag.Variants[len(flag.Variants)-1].Key, true
		}
	}

	return true, true
}

func hashDistinctID(key, distinctID string) int {
	h := sha256.Sum256([]byte(key + ":" + distinctID))
	val := binary.BigEndian.Uint32(h[:4])
	return int(val % 100)
}

func matchesFilter(f FlagFilter, distinctID string, properties map[string]any) bool {
	var val any
	if f.Property == "distinct_id" || f.Property == "id" {
		val = distinctID
	} else if properties != nil {
		val = properties[f.Property]
	}

	if val == nil {
		return false
	}

	strVal := fmt.Sprintf("%v", val)
	targetStr := fmt.Sprintf("%v", f.Value)

	switch strings.ToLower(f.Operator) {
	case "exact", "eq", "==":
		return strVal == targetStr
	case "not_exact", "neq", "!=":
		return strVal != targetStr
	case "contains":
		return strings.Contains(strVal, targetStr)
	case "not_contains":
		return !strings.Contains(strVal, targetStr)
	case "regex":
		re, err := regexp.Compile(targetStr)
		if err != nil {
			return false
		}
		return re.MatchString(strVal)
	case "gt", "gte", "lt", "lte":
		numVal, err1 := toFloat(val)
		targetNum, err2 := toFloat(f.Value)
		if err1 != nil || err2 != nil {
			return false
		}
		switch f.Operator {
		case "gt":
			return numVal > targetNum
		case "gte":
			return numVal >= targetNum
		case "lt":
			return numVal < targetNum
		case "lte":
			return numVal <= targetNum
		}
	case "in":
		switch t := f.Value.(type) {
		case []any:
			for _, item := range t {
				if fmt.Sprintf("%v", item) == strVal {
					return true
				}
			}
		case []string:
			for _, item := range t {
				if item == strVal {
					return true
				}
			}
		}
	}
	return false
}

func toFloat(val any) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return strconv.ParseFloat(fmt.Sprintf("%v", v), 64)
	}
}

// EvaluateProjectFlags returns all evaluated feature flags for a project.
func (s *Store) EvaluateProjectFlags(projectID int64, distinctID string, properties map[string]any) (map[string]any, error) {
	flags, err := s.ListFeatureFlags(projectID)
	if err != nil {
		return nil, err
	}

	result := make(map[string]any, len(flags))
	for _, flag := range flags {
		val, _ := EvaluateFlag(flag, distinctID, properties)
		result[flag.Key] = val
	}
	return result, nil
}
