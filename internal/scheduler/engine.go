// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"sightpane/internal/alert"
	"sightpane/internal/crons"
	"sightpane/internal/store"
	"sightpane/internal/uptime"
)

// Options configures the background scheduler engine.
type Options struct {
	Store         *store.Store
	Notifier      *alert.Notifier
	UptimeChecker *uptime.Checker
	RetentionDays int
}

// Engine coordinates and manages the lifecycle of all background runners,
// monitors, and daily maintenance loops in Sightpane.
type Engine struct {
	store         *store.Store
	notifier      *alert.Notifier
	cronEvaluator *crons.Evaluator
	uptimeRunner  *uptime.Runner
	metricWorker  *alert.MetricWorker
	retentionDays int

	stop chan struct{}
	wg   sync.WaitGroup
}

// New creates a new background scheduling engine.
func New(opts Options) *Engine {
	e := &Engine{
		store:         opts.Store,
		notifier:      opts.Notifier,
		retentionDays: opts.RetentionDays,
		stop:          make(chan struct{}),
	}

	if opts.Store != nil {
		e.cronEvaluator = crons.NewEvaluator(opts.Store, opts.Notifier)
		checker := opts.UptimeChecker
		if checker == nil {
			checker = uptime.NewChecker(20)
		}
		e.uptimeRunner = uptime.NewRunner(opts.Store, checker, opts.Notifier)
		e.metricWorker = alert.NewMetricWorker(opts.Store, opts.Notifier)
	}

	return e
}

// Start launches all background workers and maintenance routines.
func (e *Engine) Start() {
	if e.cronEvaluator != nil {
		e.cronEvaluator.Start()
	}
	if e.uptimeRunner != nil {
		e.uptimeRunner.Start()
	}
	if e.metricWorker != nil {
		e.metricWorker.Start()
	}

	e.wg.Add(1)
	go e.runMaintenance()
}

// Stop gracefully shuts down all background workers and waits for completion.
func (e *Engine) Stop() {
	if e.cronEvaluator != nil {
		e.cronEvaluator.Stop()
	}
	if e.uptimeRunner != nil {
		e.uptimeRunner.Stop()
	}
	if e.metricWorker != nil {
		e.metricWorker.Stop()
	}

	close(e.stop)
	e.wg.Wait()
}

// runMaintenance runs daily periodic tasks such as session retention purge and GeoIP updates.
func (e *Engine) runMaintenance() {
	defer e.wg.Done()

	// Initial check shortly after startup (1 minute delay to allow fast startup)
	select {
	case <-time.After(1 * time.Minute):
		e.executeMaintenance()
	case <-e.stop:
		return
	}

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.executeMaintenance()
		case <-e.stop:
			return
		}
	}
}

func (e *Engine) executeMaintenance() {
	if e.store == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// 1. Retention cleanup
	if e.retentionDays > 0 {
		if n, err := e.store.PurgeExpired(ctx, e.retentionDays); err != nil {
			log.Printf("scheduler: retention cleanup failed: %v", err)
		} else if n > 0 {
			log.Printf("scheduler: purged %d expired sessions", n)
		}
	}

	// 2. Check and apply daily GeoIP database updates
	if geo := e.store.GeoIP(); geo != nil {
		if err := geo.CheckAndApplyUpdates(ctx); err != nil {
			log.Printf("scheduler: geoip database update check: %v", err)
		}
	}
}
