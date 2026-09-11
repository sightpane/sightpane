// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package crons

import (
	"fmt"
	"log"
	"sync"
	"time"

	"sightpane/internal/alert"
	"sightpane/internal/store"
)

type Evaluator struct {
	store     *store.Store
	notifier  *alert.Notifier
	tickEvery time.Duration
	stop      chan struct{}
	wg        sync.WaitGroup
}

func NewEvaluator(st *store.Store, notifier *alert.Notifier) *Evaluator {
	return &Evaluator{
		store:     st,
		notifier:  notifier,
		tickEvery: 30 * time.Second,
		stop:      make(chan struct{}),
	}
}

func (e *Evaluator) SetTickInterval(d time.Duration) {
	e.tickEvery = d
}

func (e *Evaluator) Start() {
	e.wg.Add(1)
	go e.run()
}

func (e *Evaluator) Stop() {
	close(e.stop)
	e.wg.Wait()
}

func (e *Evaluator) EvaluateOnce() ([]*store.CronMonitor, error) {
	if e.store == nil {
		return nil, nil
	}
	monitors, err := e.store.EvaluateCronDeadlines()
	if err != nil {
		return nil, err
	}
	if e.notifier != nil {
		for _, m := range monitors {
			e.notifier.NotifyCronIncident(
				m.ProjectID,
				m.Slug,
				m.Name,
				"cron_"+m.Status,
				fmt.Sprintf("Cron monitor %q transitioned to %s", m.Name, m.Status),
			)
		}
	}
	return monitors, nil
}

func (e *Evaluator) run() {
	defer e.wg.Done()
	ticker := time.NewTicker(e.tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-e.stop:
			return
		case <-ticker.C:
			if _, err := e.EvaluateOnce(); err != nil {
				log.Printf("cron evaluator: %v", err)
			}
		}
	}
}
