// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"math"
	"sync"
	"time"
)

type projectBucket struct {
	tokens     float64
	lastUpdate time.Time
}

// IngestLimiter maintains an in-memory token bucket per project.
type IngestLimiter struct {
	mu           sync.Mutex
	buckets      map[int64]*projectBucket
	defaultLimit int // default items/min when project quota is 0
}

func NewIngestLimiter(defaultLimit int) *IngestLimiter {
	return &IngestLimiter{
		buckets:      make(map[int64]*projectBucket),
		defaultLimit: defaultLimit,
	}
}

// Allow checks if n items can be ingested for project pid with limit quotaPerMinute.
// If quotaPerMinute <= 0, defaultLimit is used. If that is also <= 0, Allow returns true, 0.
// If not allowed, it returns false and the recommended Retry-After in seconds.
func (l *IngestLimiter) Allow(pid int64, quotaPerMinute, n int) (bool, int) {
	limit := quotaPerMinute
	if limit <= 0 {
		limit = l.defaultLimit
	}
	if limit <= 0 {
		return true, 0
	}

	needed := float64(n)
	if needed < 1 {
		needed = 1
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[pid]
	now := time.Now()
	ratePerSec := float64(limit) / 60.0

	if !ok {
		// New bucket starts full
		b = &projectBucket{
			tokens:     float64(limit),
			lastUpdate: now,
		}
		l.buckets[pid] = b
	} else {
		elapsed := now.Sub(b.lastUpdate).Seconds()
		b.tokens = math.Min(float64(limit), b.tokens+elapsed*ratePerSec)
		b.lastUpdate = now
	}

	if b.tokens >= needed {
		b.tokens -= needed
		return true, 0
	}

	missing := needed - b.tokens
	retryAfter := int(math.Ceil(missing / ratePerSec))
	if retryAfter < 1 {
		retryAfter = 1
	}
	if retryAfter > 60 {
		retryAfter = 60
	}
	return false, retryAfter
}
