// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"os"
	"testing"
	"time"

	"sightpane/internal/blob"
	"sightpane/internal/store"
	"sightpane/internal/testdb"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.Main(m))
}

func TestEngineLifecycle(t *testing.T) {
	eng := New(Options{
		RetentionDays: 30,
	})

	eng.Start()

	// Ensure start doesn't panic and gracefully stops immediately
	done := make(chan struct{})
	go func() {
		eng.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler engine Stop timed out")
	}
}

func TestEngineMaintenanceExecution(t *testing.T) {
	tempDir := t.TempDir()
	frames, err := blob.NewFS(tempDir)
	if err != nil {
		t.Fatalf("blob.NewFS: %v", err)
	}
	st, err := store.Open(store.Options{
		DSN:           testdb.DSN(t),
		DataDir:       tempDir,
		Frames:        frames,
		RetentionDays: 30,
	})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	eng := New(Options{
		Store:         st,
		RetentionDays: 30,
	})

	// Directly invoke executeMaintenance to verify it runs PurgeExpired and SweepOrphanFrames without error
	eng.executeMaintenance()
}
