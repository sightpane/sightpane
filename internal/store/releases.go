package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

type ReleaseHealth struct {
	Version           string  `json:"version"`
	FirstSeen         string  `json:"first_seen"`
	LastSeen          string  `json:"last_seen"`
	SessionCount      int64   `json:"session_count"`
	ErrorSessionCount int64   `json:"error_session_count"`
	ErrorCount        int64   `json:"error_count"`
	UserCount         int64   `json:"user_count"`
	CrashFreeRate     float64 `json:"crash_free_rate"`
	AdoptionRate      float64 `json:"adoption_rate"`
}

func (s *Store) ListReleases(ctx context.Context, projectID int64) ([]ReleaseHealth, error) {
	var totalSessions int64
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE project_id=$1 AND `+visitsOnly, projectID).Scan(&totalSessions)

	q := `
		SELECT
			s.release,
			MIN(s.started_at),
			MAX(s.last_seen_at),
			COUNT(*),
			COUNT(CASE WHEN s.error_count > 0 THEN 1 END),
			COALESCE(SUM(s.error_count), 0),
			COUNT(DISTINCT s.user_id)
		FROM sessions s
		WHERE s.project_id = $1 AND s.release != '' AND s.` + visitsOnly + `
		GROUP BY s.release
		ORDER BY MAX(s.last_seen_at) DESC
	`
	rows, err := s.db.QueryContext(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var releases []ReleaseHealth
	for rows.Next() {
		var r ReleaseHealth
		if err := rows.Scan(
			&r.Version,
			tsCol{&r.FirstSeen},
			tsCol{&r.LastSeen},
			&r.SessionCount,
			&r.ErrorSessionCount,
			&r.ErrorCount,
			&r.UserCount,
		); err != nil {
			return nil, err
		}

		if r.SessionCount > 0 {
			r.CrashFreeRate = math.Round(float64(r.SessionCount-r.ErrorSessionCount)/float64(r.SessionCount)*10000) / 100
		} else {
			r.CrashFreeRate = 100.0
		}

		if totalSessions > 0 {
			r.AdoptionRate = math.Round(float64(r.SessionCount)/float64(totalSessions)*10000) / 100
		}

		releases = append(releases, r)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return releases, nil
}

func (s *Store) GetReleaseHealth(ctx context.Context, projectID int64, version string) (*ReleaseHealth, error) {
	var totalSessions int64
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE project_id=$1 AND `+visitsOnly, projectID).Scan(&totalSessions)

	q := `
		SELECT
			s.release,
			MIN(s.started_at),
			MAX(s.last_seen_at),
			COUNT(*),
			COUNT(CASE WHEN s.error_count > 0 THEN 1 END),
			COALESCE(SUM(s.error_count), 0),
			COUNT(DISTINCT s.user_id)
		FROM sessions s
		WHERE s.project_id = $1 AND s.release = $2 AND s.` + visitsOnly + `
		GROUP BY s.release
	`
	row := s.db.QueryRowContext(ctx, q, projectID, version)
	var r ReleaseHealth
	if err := row.Scan(
		&r.Version,
		tsCol{&r.FirstSeen},
		tsCol{&r.LastSeen},
		&r.SessionCount,
		&r.ErrorSessionCount,
		&r.ErrorCount,
		&r.UserCount,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if r.SessionCount > 0 {
		r.CrashFreeRate = math.Round(float64(r.SessionCount-r.ErrorSessionCount)/float64(r.SessionCount)*10000) / 100
	} else {
		r.CrashFreeRate = 100.0
	}

	if totalSessions > 0 {
		r.AdoptionRate = math.Round(float64(r.SessionCount)/float64(totalSessions)*10000) / 100
	}

	return &r, nil
}
