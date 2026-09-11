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
	"time"
)

var ErrDashboardNotFound = errors.New("dashboard not found")

type DashboardTile struct {
	InsightID string `json:"insight_id"`
	Col       int    `json:"col"`
	Row       int    `json:"row"`
	W         int    `json:"w"`
	H         int    `json:"h"`
}

type Dashboard struct {
	ID          string          `json:"id"`
	ProjectID   int64           `json:"project_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"is_default"`
	Layout      []DashboardTile `json:"layout"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// CreateDashboard inserts a new dashboard.
func (s *Store) CreateDashboard(projectID int64, name, description string, isDefault bool, layout []DashboardTile) (*Dashboard, error) {
	if layout == nil {
		layout = []DashboardTile{}
	}
	layoutJSON, err := json.Marshal(layout)
	if err != nil {
		return nil, fmt.Errorf("marshal layout: %w", err)
	}

	if isDefault {
		_, _ = s.db.Exec(`UPDATE dashboards SET is_default = false WHERE project_id = $1`, projectID)
	}

	var d Dashboard
	var rawLayout []byte
	row := s.db.QueryRow(`
		INSERT INTO dashboards (project_id, name, description, is_default, layout)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, project_id, name, description, is_default, layout, created_at, updated_at
	`, projectID, name, description, isDefault, layoutJSON)

	if err := row.Scan(&d.ID, &d.ProjectID, &d.Name, &d.Description, &d.IsDefault, &rawLayout, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, fmt.Errorf("insert dashboard: %w", err)
	}

	if len(rawLayout) > 0 {
		_ = json.Unmarshal(rawLayout, &d.Layout)
	}
	if d.Layout == nil {
		d.Layout = []DashboardTile{}
	}

	return &d, nil
}

// GetDashboard fetches a single dashboard by ID.
func (s *Store) GetDashboard(projectID int64, id string) (*Dashboard, error) {
	var d Dashboard
	var rawLayout []byte
	err := s.db.QueryRow(`
		SELECT id, project_id, name, description, is_default, layout, created_at, updated_at
		FROM dashboards
		WHERE project_id = $1 AND id = $2
	`, projectID, id).Scan(&d.ID, &d.ProjectID, &d.Name, &d.Description, &d.IsDefault, &rawLayout, &d.CreatedAt, &d.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDashboardNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query dashboard: %w", err)
	}

	if len(rawLayout) > 0 {
		_ = json.Unmarshal(rawLayout, &d.Layout)
	}
	if d.Layout == nil {
		d.Layout = []DashboardTile{}
	}

	return &d, nil
}

// ListDashboards lists all dashboards for a project, with default dashboard first, then ordered by name.
func (s *Store) ListDashboards(projectID int64) ([]Dashboard, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, name, description, is_default, layout, created_at, updated_at
		FROM dashboards
		WHERE project_id = $1
		ORDER BY is_default DESC, created_at DESC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list dashboards: %w", err)
	}
	defer rows.Close()

	var list []Dashboard
	for rows.Next() {
		var d Dashboard
		var rawLayout []byte
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Name, &d.Description, &d.IsDefault, &rawLayout, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan dashboard: %w", err)
		}
		if len(rawLayout) > 0 {
			_ = json.Unmarshal(rawLayout, &d.Layout)
		}
		if d.Layout == nil {
			d.Layout = []DashboardTile{}
		}
		list = append(list, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Dashboard{}
	}
	return list, nil
}

// UpdateDashboard updates dashboard metadata and tile layout.
func (s *Store) UpdateDashboard(projectID int64, id, name, description string, isDefault bool, layout []DashboardTile) (*Dashboard, error) {
	if layout == nil {
		layout = []DashboardTile{}
	}
	layoutJSON, err := json.Marshal(layout)
	if err != nil {
		return nil, fmt.Errorf("marshal layout: %w", err)
	}

	if isDefault {
		_, _ = s.db.Exec(`UPDATE dashboards SET is_default = false WHERE project_id = $1 AND id != $2`, projectID, id)
	}

	var d Dashboard
	var rawLayout []byte
	err = s.db.QueryRow(`
		UPDATE dashboards
		SET name = $1, description = $2, is_default = $3, layout = $4, updated_at = NOW()
		WHERE project_id = $5 AND id = $6
		RETURNING id, project_id, name, description, is_default, layout, created_at, updated_at
	`, name, description, isDefault, layoutJSON, projectID, id).Scan(
		&d.ID, &d.ProjectID, &d.Name, &d.Description, &d.IsDefault, &rawLayout, &d.CreatedAt, &d.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDashboardNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update dashboard: %w", err)
	}

	if len(rawLayout) > 0 {
		_ = json.Unmarshal(rawLayout, &d.Layout)
	}
	if d.Layout == nil {
		d.Layout = []DashboardTile{}
	}

	return &d, nil
}

// DeleteDashboard removes a dashboard.
func (s *Store) DeleteDashboard(projectID int64, id string) error {
	res, err := s.db.Exec(`DELETE FROM dashboards WHERE project_id = $1 AND id = $2`, projectID, id)
	if err != nil {
		return fmt.Errorf("delete dashboard: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrDashboardNotFound
	}
	return nil
}

// SetDefaultDashboard designates a dashboard as the project default.
func (s *Store) SetDefaultDashboard(projectID int64, id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE dashboards SET is_default = false WHERE project_id = $1`, projectID); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE dashboards SET is_default = true, updated_at = NOW() WHERE project_id = $1 AND id = $2`, projectID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrDashboardNotFound
	}
	return tx.Commit()
}
