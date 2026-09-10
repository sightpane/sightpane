// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"log"
	"time"

	"sightpane/internal/blob"
)

// PurgeExpired drops sessions (and their frames/items/spans) older than retention_days.
// If project.RetentionDays <= 0, defaultDays is used. If defaultDays <= 0, nothing is purged.
// Issue counters are preserved.
// It returns the total number of purged sessions.
func (s *Store) PurgeExpired(ctx context.Context, defaultDays int) (int, error) {
	projects, err := s.ListAllProjects()
	if err != nil {
		return 0, fmt.Errorf("list projects for retention: %w", err)
	}

	totalPurged := 0
	now := time.Now().UTC()

	for _, p := range projects {
		days := p.RetentionDays
		if days <= 0 {
			days = defaultDays
		}
		if days <= 0 {
			continue
		}

		cutoff := now.AddDate(0, 0, -days)
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM sessions WHERE project_id=$1 AND started_at < $2`, p.ID, cutoff)
		if err != nil {
			log.Printf("retention query project %d: %v", p.ID, err)
			continue
		}

		var sessions []string
		for rows.Next() {
			var sid string
			if err := rows.Scan(&sid); err == nil {
				sessions = append(sessions, sid)
			}
		}
		rows.Close()

		if len(sessions) == 0 {
			continue
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return totalPurged, err
		}

		for _, sid := range sessions {
			_, _ = tx.ExecContext(ctx, `DELETE FROM frames WHERE session_id=$1`, sid)
			_, _ = tx.ExecContext(ctx, `DELETE FROM items WHERE session_id=$1`, sid)
			_, _ = tx.ExecContext(ctx, `DELETE FROM spans WHERE session_id=$1`, sid)
			_, _ = tx.ExecContext(ctx, `DELETE FROM sessions WHERE id=$1`, sid)
		}

		if err := tx.Commit(); err != nil {
			log.Printf("retention commit project %d: %v", p.ID, err)
			continue
		}

		totalPurged += len(sessions)

		// Clean up frame files from blob storage
		for _, sid := range sessions {
			if err := s.blobs.DeletePrefix(ctx, blob.SessionPrefix(sid)); err != nil {
				log.Printf("retention frames delete prefix %s: %v", sid, err)
			}
		}
	}

	return totalPurged, nil
}
