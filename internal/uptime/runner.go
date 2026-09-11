// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package uptime

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"sightpane/internal/alert"
	"sightpane/internal/store"
)

type Runner struct {
	store     *store.Store
	checker   *Checker
	notifier  *alert.Notifier
	tickEvery time.Duration
	stop      chan struct{}
	wg        sync.WaitGroup
}

func NewRunner(st *store.Store, checker *Checker, notifier *alert.Notifier) *Runner {
	if checker == nil {
		checker = NewChecker(20)
	}
	return &Runner{
		store:     st,
		checker:   checker,
		notifier:  notifier,
		tickEvery: 15 * time.Second,
		stop:      make(chan struct{}),
	}
}

func (r *Runner) SetTickInterval(d time.Duration) {
	r.tickEvery = d
}

func (r *Runner) Start() {
	r.wg.Add(1)
	go r.run()
}

func (r *Runner) Stop() {
	close(r.stop)
	r.wg.Wait()
}

func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	if r.store == nil {
		return 0, nil
	}

	monitors, err := r.store.GetMonitorsDueForCheck()
	if err != nil {
		return 0, fmt.Errorf("monitors due: %w", err)
	}

	if len(monitors) == 0 {
		return 0, nil
	}

	checkedCount := 0
	for _, m := range monitors {
		previousStatus := m.Status
		res, err := r.checker.CheckMonitor(ctx, r.store, m)
		if err != nil {
			log.Printf("uptime runner: check monitor %q error: %v", m.Name, err)
			continue
		}
		checkedCount++

		// Dispatch alert on status change
		currentStatus := "up"
		if !res.IsUp {
			currentStatus = "down"
		}

		if previousStatus != currentStatus && r.notifier != nil {
			r.notifier.NotifyUptimeIncident(
				m.ProjectID,
				m.ID,
				m.Name,
				m.URL,
				currentStatus,
				res.ErrorMessage,
			)
		}
	}

	return checkedCount, nil
}

func (r *Runner) run() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := r.RunOnce(ctx)
			cancel()
			if err != nil {
				log.Printf("uptime runner: %v", err)
			}
		}
	}
}
