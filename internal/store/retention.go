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
		scanErr := false
		for rows.Next() {
			var sid string
			if err := rows.Scan(&sid); err != nil {
				log.Printf("retention scan project %d: %v", p.ID, err)
				scanErr = true
				break
			}
			sessions = append(sessions, sid)
		}
		if err := rows.Err(); err != nil {
			log.Printf("retention iterate project %d: %v", p.ID, err)
			rows.Close()
			continue
		}
		rows.Close()
		if scanErr {
			continue
		}

		if len(sessions) == 0 {
			continue
		}

		const batchSize = 500
		for i := 0; i < len(sessions); i += batchSize {
			end := i + batchSize
			if end > len(sessions) {
				end = len(sessions)
			}
			batch := sessions[i:end]

			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return totalPurged, err
			}

			tsCutoff := now.Add(24 * time.Hour)

			if _, err := tx.ExecContext(ctx, `DELETE FROM frames WHERE session_id = ANY($1)`, batch); err != nil {
				_ = tx.Rollback()
				log.Printf("retention delete frames project %d: %v", p.ID, err)
				break
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE session_id = ANY($1) AND ts < $2`, batch, tsCutoff); err != nil {
				_ = tx.Rollback()
				log.Printf("retention delete items project %d: %v", p.ID, err)
				break
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM spans WHERE session_id = ANY($1) AND ts < $2`, batch, tsCutoff); err != nil {
				_ = tx.Rollback()
				log.Printf("retention delete spans project %d: %v", p.ID, err)
				break
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ANY($1)`, batch); err != nil {
				_ = tx.Rollback()
				log.Printf("retention delete sessions project %d: %v", p.ID, err)
				break
			}

			if err := tx.Commit(); err != nil {
				log.Printf("retention commit project %d: %v", p.ID, err)
				break
			}

			totalPurged += len(batch)

			// Clean up frame files from blob storage
			for _, sid := range batch {
				if err := s.blobs.DeletePrefix(ctx, blob.SessionPrefix(sid)); err != nil {
					log.Printf("retention frames delete prefix %s: %v", sid, err)
				}
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

	allIDs := make([]string, 0, len(sessionIDs))
	for sid := range sessionIDs {
		allIDs = append(allIDs, sid)
	}

	// Check which sessions actually exist in the database in batches
	swept := 0
	const checkBatch = 500
	for i := 0; i < len(allIDs); i += checkBatch {
		end := i + checkBatch
		if end > len(allIDs) {
			end = len(allIDs)
		}
		batch := allIDs[i:end]

		rows, err := s.db.QueryContext(ctx, `SELECT id FROM sessions WHERE id = ANY($1)`, batch)
		if err != nil {
			log.Printf("sweep orphans check batch: %v", err)
			continue
		}
		existing := make(map[string]bool, len(batch))
		scanFailed := false
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				log.Printf("sweep orphans scan batch: %v", err)
				scanFailed = true
				break
			}
			existing[id] = true
		}
		if err := rows.Err(); err != nil {
			log.Printf("sweep orphans iterate batch: %v", err)
			rows.Close()
			continue
		}
		rows.Close()
		if scanFailed {
			continue
		}

		for _, sid := range batch {
			if !existing[sid] {
				if err := s.blobs.DeletePrefix(ctx, blob.SessionPrefix(sid)); err != nil {
					log.Printf("sweep orphans delete %s: %v", sid, err)
				} else {
					swept++
				}
			}
		}
	}
	return swept, nil
}

