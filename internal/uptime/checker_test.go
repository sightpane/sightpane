// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package uptime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckerProbeOnce(t *testing.T) {
	// 1. Success server
	serverOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "val" {
			http.Error(w, "missing header", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer serverOK.Close()

	c := NewChecker(5)
	ctx := context.Background()

	code, ms, isUp, errMsg := c.ProbeOnce(ctx, "GET", serverOK.URL, map[string]string{"X-Custom": "val"}, 200, 2*time.Second)
	if !isUp || code != 200 || errMsg != "" {
		t.Fatalf("expected up, got code=%d isUp=%v ms=%d err=%s", code, isUp, ms, errMsg)
	}

	// Missing required header -> 400
	code, _, isUp, errMsg = c.ProbeOnce(ctx, "GET", serverOK.URL, nil, 200, 2*time.Second)
	if isUp || code != 400 || errMsg == "" {
		t.Fatalf("expected failure, got code=%d isUp=%v err=%s", code, isUp, errMsg)
	}

	// 2. Closed server / connection failure
	serverOK.Close()
	_, _, isUp, errMsg = c.ProbeOnce(ctx, "GET", serverOK.URL, nil, 200, 1*time.Second)
	if isUp || errMsg == "" {
		t.Fatalf("expected connection failure, got isUp=%v", isUp)
	}
}

func TestCheckerRetryLogic(t *testing.T) {
	var attempts int32

	// Flaky server: fails first request, succeeds on retry
	serverFlaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			http.Error(w, "transient error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer serverFlaky.Close()

	c := NewChecker(5)
	c.retryDelay = 10 * time.Millisecond
	ctx := context.Background()

	// ProbeOnce directly fails on attempt 1
	code, _, isUp, _ := c.ProbeOnce(ctx, "GET", serverFlaky.URL, nil, 200, 2*time.Second)
	if isUp || code != 500 {
		t.Fatalf("expected first attempt to fail, got code=%d", code)
	}

	// Attempt 2 succeeds
	code, _, isUp, _ = c.ProbeOnce(ctx, "GET", serverFlaky.URL, nil, 200, 2*time.Second)
	if !isUp || code != 200 {
		t.Fatalf("expected second attempt to succeed, got code=%d", code)
	}
}
