// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"sightpane/internal/blob"
)

type Project struct {
	ID                  int64  `json:"id"`
	Name                string `json:"name"`
	APIKey              string `json:"api_key"`
	Platform            string `json:"platform"`
	CreatedBy           *int64 `json:"created_by"`
	CreatedAt           string `json:"created_at"`
	RetentionDays       int    `json:"retention_days"`
	QuotaItemsPerMinute int    `json:"quota_items_per_minute"`
	Role                string `json:"role,omitempty"`
	// Summary counters, filled in for the list view so it needs no extra request
	// per project.
	Sessions24h int `json:"sessions_24h"`
	Errors24h   int `json:"errors_24h"`
	OpenIssues  int `json:"open_issues"`
}

const projectCols = `id, name, api_key, platform, created_by, created_at, retention_days, quota_items_per_minute`

func scanProject(sc interface{ Scan(...any) error }) (*Project, error) {
	p := &Project{}
	if err := sc.Scan(&p.ID, &p.Name, &p.APIKey, &p.Platform, &p.CreatedBy, tsCol{&p.CreatedAt}, &p.RetentionDays, &p.QuotaItemsPerMinute); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// EnsureProject returns the project holding [key] and creates it when there is
// none. Startup calls it so a fresh install already has a project and a key to
// paste into Hog.init, and a restart does not create a second one.
func (s *Store) EnsureProject(name, key string) (*Project, error) {
	if p, err := s.ProjectByKey(key); err == nil {
		return p, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.CreateProject(name, "flutter", key, nil)
}

// CreateProject generates an API key when [key] is empty. When [owner] is given
// they are added as the owner member, so a project made from the dashboard is
// never left with nobody who can administer it.
func (s *Store) CreateProject(name, platform, key string, owner *int64) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrProjectNameRequired
	}
	if key == "" {
		key = randomKey()
	}
	if platform == "" {
		platform = "flutter"
	}
	now := time.Now().UTC()
	// RETURNING rather than LastInsertId, which pgx does not implement.
	var id int64
	if err := s.db.QueryRow(`INSERT INTO projects(name, api_key, platform, created_by, created_at) VALUES($1,$2,$3,$4,$5) RETURNING id`,
		name, key, platform, owner, now).Scan(&id); err != nil {
		return nil, err
	}
	if owner != nil {
		if err := s.AddMember(id, *owner, "owner"); err != nil {
			return nil, err
		}
	}
	return &Project{ID: id, Name: name, APIKey: key, Platform: platform, CreatedBy: owner, CreatedAt: now.Format(time.RFC3339Nano), RetentionDays: 30, QuotaItemsPerMinute: 0}, nil
}

func (s *Store) ProjectByKey(key string) (*Project, error) {
	return scanProject(s.db.QueryRow(`SELECT `+projectCols+` FROM projects WHERE api_key=$1`, key))
}

func (s *Store) ProjectByID(id int64) (*Project, error) {
	p, err := scanProject(s.db.QueryRow(`SELECT `+projectCols+` FROM projects WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	s.fillProjectCounters(p)
	return p, nil
}

func (s *Store) ListAllProjects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT ` + projectCols + ` FROM projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) fillProjectCounters(p *Project) {
	// A rolling 24 hours, not the last calendar day, so this counts raw items:
	// the daily aggregate cannot answer a window that starts inside a bucket.
	since := time.Now().UTC().Add(-24 * time.Hour)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE project_id=$1 AND started_at>=$2`, p.ID, since).Scan(&p.Sessions24h)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM items WHERE project_id=$1 AND type='error' AND ts>=$2`, p.ID, since).Scan(&p.Errors24h)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM issues WHERE project_id=$1 AND NOT resolved`, p.ID).Scan(&p.OpenIssues)
}

// ListProjectsForUser returns only the projects the user is a member of, each
// with the role they hold there, which is what the dashboard uses to decide
// which owner-only actions to show.
func (s *Store) ListProjectsForUser(userID int64) ([]Project, error) {
	rows, err := s.db.Query(`SELECT p.id, p.name, p.api_key, p.platform, p.created_by, p.created_at, p.retention_days, p.quota_items_per_minute, m.role FROM projects p JOIN project_members m ON m.project_id=p.id WHERE m.user_id=$1 ORDER BY p.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.APIKey, &p.Platform, &p.CreatedBy, tsCol{&p.CreatedAt}, &p.RetentionDays, &p.QuotaItemsPerMinute, &p.Role); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		s.fillProjectCounters(&out[i])
	}
	return out, nil
}

func (s *Store) UpdateProject(id int64, name, platform string, retentionDays, quotaItemsPerMinute int) error {
	res, err := s.db.Exec(`UPDATE projects SET name=$1, platform=$2, retention_days=$3, quota_items_per_minute=$4 WHERE id=$5`, strings.TrimSpace(name), platform, retentionDays, quotaItemsPerMinute, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RotateKey replaces the project's API key. The old key stops working at once
// and envelopes still sent with it are rejected, which is the whole point of
// rotating one.
func (s *Store) RotateKey(id int64) (string, error) {
	key := randomKey()
	res, err := s.db.Exec(`UPDATE projects SET api_key=$1 WHERE id=$2`, key, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return key, nil
}

// DeleteProject removes the project and everything under it. The session ids are
// collected before the transaction because the frame files live on disk, outside
// it, and can only be unlinked once the rows are safely gone.
func (s *Store) DeleteProject(id int64) error {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE project_id=$1`, id)
	if err != nil {
		return err
	}
	var sessions []string
	for rows.Next() {
		var sid string
		_ = rows.Scan(&sid)
		sessions = append(sessions, sid)
	}
	rows.Close()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM release_artifacts WHERE project_id=$1`,
		`DELETE FROM frames WHERE session_id IN (SELECT id FROM sessions WHERE project_id=$1)`,
		`DELETE FROM items WHERE project_id=$1`,
		`DELETE FROM spans WHERE project_id=$1`,
		`DELETE FROM issues WHERE project_id=$1`,
		`DELETE FROM sessions WHERE project_id=$1`,
		`DELETE FROM project_members WHERE project_id=$1`,
		`DELETE FROM projects WHERE id=$1`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// The rows are gone; frames are removed afterwards and best-effort, because
	// an object store hiccup must not resurrect a deleted project. Whatever is
	// left behind is unreachable and gets swept by the retention job.
	ctx := context.Background()
	for _, sid := range sessions {
		if err := s.blobs.DeletePrefix(ctx, blob.SessionPrefix(sid)); err != nil {
			log.Printf("delete project %d: frames of session %s: %v", id, sid, err)
		}
	}
	if err := s.blobs.DeletePrefix(ctx, fmt.Sprintf("sourcemaps/%d/", id)); err != nil {
		log.Printf("delete project %d: source maps: %v", id, err)
	}
	return nil
}
