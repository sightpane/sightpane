// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"testing"
	"time"
)

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
