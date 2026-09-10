// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package blob

import (
	"context"
	"fmt"
	"io"
)

// MigrateStats reports what a migration run did.
type MigrateStats struct {
	Scanned  int64
	Migrated int64
	Skipped  int64
	Bytes    int64
}

// Migrate copies objects from src to dst. It is resumable: any object that
// already exists in dst with the same size is skipped. Every newly written
// object is verified against its source size.
func Migrate(ctx context.Context, src, dst Store) (MigrateStats, error) {
	var stats MigrateStats
	err := src.Walk(ctx, "", func(key string, size int64) error {
		stats.Scanned++

		// Check if destination already has this object with matching size (resumable)
		rc, destSize, err := dst.Get(ctx, key)
		if err == nil {
			rc.Close()
			if destSize == size {
				stats.Skipped++
				return nil
			}
		}

		// Read from source
		srcRC, srcSize, err := src.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("migrate: read source %s: %w", key, err)
		}
		data, err := io.ReadAll(srcRC)
		srcRC.Close()
		if err != nil {
			return fmt.Errorf("migrate: read source data %s: %w", key, err)
		}
		if int64(len(data)) != srcSize {
			return fmt.Errorf("migrate: size mismatch on %s: read %d, stat %d", key, len(data), srcSize)
		}

		// Put to destination
		if err := dst.Put(ctx, key, data); err != nil {
			return fmt.Errorf("migrate: write dest %s: %w", key, err)
		}

		// Verify size in destination
		vrc, vSize, err := dst.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("migrate: verify dest %s: %w", key, err)
		}
		vrc.Close()
		if vSize != srcSize {
			return fmt.Errorf("migrate: verification failed on %s: dest size %d != src size %d", key, vSize, srcSize)
		}

		stats.Migrated++
		stats.Bytes += srcSize
		return nil
	})
	return stats, err
}
