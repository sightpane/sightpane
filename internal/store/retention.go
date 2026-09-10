// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"log"
	"strings"
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

	// Also sweep orphaned objects left behind by previous failures or unlinked sessions
	if swept, err := s.SweepOrphanFrames(ctx); err != nil {
		log.Printf("retention sweep orphans: %v", err)
	} else if swept > 0 {
		log.Printf("retention swept %d orphaned session frames", swept)
	}

	return totalPurged, nil
}

// SweepOrphanFrames walks the blob store and removes frames whose session rows
// no longer exist in the database (e.g. from failed DeleteProject or expired sessions).
func (s *Store) SweepOrphanFrames(ctx context.Context) (int, error) {
	sessionIDs := make(map[string]struct{})
	err := s.blobs.Walk(ctx, "", func(key string, size int64) error {
		// Ignore sourcemaps namespace
		if strings.HasPrefix(key, "sourcemaps/") {
			return nil
		}
		parts := strings.SplitN(key, "/", 2)
		if len(parts) == 2 && parts[0] != "" {
			sessionIDs[parts[0]] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("sweep orphans walk: %w", err)
	}

	if len(sessionIDs) == 0 {
		return 0, nil
	}

	// Check which sessions actually exist in the database
	swept := 0
	for sid := range sessionIDs {
		var exists bool
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE id=$1)`, sid).Scan(&exists)
		if err != nil {
			log.Printf("sweep orphans check session %s: %v", sid, err)
			continue
		}
		if !exists {
			if err := s.blobs.DeletePrefix(ctx, blob.SessionPrefix(sid)); err != nil {
				log.Printf("sweep orphans delete %s: %v", sid, err)
			} else {
				swept++
			}
		}
	}
	return swept, nil
}

