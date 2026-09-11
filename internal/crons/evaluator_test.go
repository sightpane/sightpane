// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package crons

import (
	"testing"
	"time"

	"sightpane/internal/store"
)

func TestComputeNextExpected(t *testing.T) {
	refTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Standard hourly
	next, err := store.ComputeNextExpected("0 * * * *", "UTC", refTime)
	if err != nil {
		t.Fatalf("expected valid schedule: %v", err)
	}
	expected := time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, next)
	}

	// @daily descriptor
	next, err = store.ComputeNextExpected("@daily", "UTC", refTime)
	if err != nil {
		t.Fatalf("expected valid @daily schedule: %v", err)
	}
	expected = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, next)
	}

	// Invalid schedule
	_, err = store.ComputeNextExpected("invalid schedule string", "UTC", refTime)
	if err == nil {
		t.Fatalf("expected error for invalid schedule")
	}
}

func TestEvaluatorLifecycle(t *testing.T) {
	evaluator := NewEvaluator(nil, nil)
	evaluator.SetTickInterval(10 * time.Millisecond)
	evaluator.Start()
	time.Sleep(30 * time.Millisecond)
	evaluator.Stop()
}
