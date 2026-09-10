// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package blob

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrate(t *testing.T) {
	ctx := context.Background()

	srcDir := filepath.Join(t.TempDir(), "src")
	dstDir := filepath.Join(t.TempDir(), "dst")

	src, err := NewFS(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := NewFS(dstDir)
	if err != nil {
		t.Fatal(err)
	}

	// Populate src with 3 frames
	k1 := FrameKey("sess-1", 1)
	k2 := FrameKey("sess-1", 2)
	k3 := FrameKey("sess-2", 1)

	_ = src.Put(ctx, k1, []byte("frame-1-data"))
	_ = src.Put(ctx, k2, []byte("frame-2-data-longer"))
	_ = src.Put(ctx, k3, []byte("frame-3-data"))

	// Pre-populate dst with k1 to test resumability
	_ = dst.Put(ctx, k1, []byte("frame-1-data"))

	stats, err := Migrate(ctx, src, dst)
	if err != nil {
		t.Fatal(err)
	}

	if stats.Scanned != 3 {
		t.Fatalf("expected 3 scanned, got %d", stats.Scanned)
	}
	if stats.Skipped != 1 {
		t.Fatalf("expected 1 skipped (k1), got %d", stats.Skipped)
	}
	if stats.Migrated != 2 {
		t.Fatalf("expected 2 migrated (k2, k3), got %d", stats.Migrated)
	}

	// Verify dst has all 3 keys
	for _, k := range []string{k1, k2, k3} {
		rc, _, err := dst.Get(ctx, k)
		if err != nil {
			t.Fatalf("expected key %s in destination: %v", k, err)
		}
		rc.Close()
	}

	// Running migrate a second time should skip all 3
	stats2, err := Migrate(ctx, src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if stats2.Scanned != 3 || stats2.Skipped != 3 || stats2.Migrated != 0 {
		t.Fatalf("expected 3 skipped, got %+v", stats2)
	}
}
