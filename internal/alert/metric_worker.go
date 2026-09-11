// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package alert

import (
	"fmt"
	"log"
	"sync"
	"time"

	"sightpane/internal/store"
)

// MetricWorker evaluates rolling metric alerts and anomaly detection in the background.
type MetricWorker struct {
	store     *store.Store
	notifier  *Notifier
	stop      chan struct{}
	wg        sync.WaitGroup
	tickEvery time.Duration
}

func NewMetricWorker(st *store.Store, n *Notifier) *MetricWorker {
	return &MetricWorker{
		store:     st,
		notifier:  n,
		stop:      make(chan struct{}),
		tickEvery: time.Minute,
	}
}

func (w *MetricWorker) SetTickInterval(d time.Duration) {
	w.tickEvery = d
}

func (w *MetricWorker) Start() {
	w.wg.Add(1)
	go w.run()
}

func (w *MetricWorker) Stop() {
	close(w.stop)
	w.wg.Wait()
}

func (w *MetricWorker) RunOnce() {
	w.evaluateAll()
}

func (w *MetricWorker) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.evaluateAll()
		}
	}
}

func (w *MetricWorker) evaluateAll() {
	rules, err := w.store.ListAllActiveMetricAlertRules()
	if err != nil {
		log.Printf("metric worker: list active rules: %v", err)
		return
	}

	for _, r := range rules {
		w.EvaluateRule(&r)
	}
}

func (w *MetricWorker) EvaluateRule(r *store.MetricAlertRule) {
	val, _, err := w.store.EvaluateMetricValue(r.ProjectID, r.MetricType, r.TargetFilter, r.WindowMinutes)
	if err != nil {
		log.Printf("metric worker: evaluate rule %d (%s): %v", r.ID, r.Name, err)
		return
	}

	isFiring := false
	isWarning := false
	summary := ""

	switch r.ComparisonOperator {
	case "spike_multiplier":
		base, _ := w.store.EvaluateMetricBaseline(r.ProjectID, r.MetricType, r.TargetFilter, r.WindowMinutes)
		anom := EvaluateAnomaly(val, base, r.CriticalThreshold, 5.0)
		isFiring = anom.IsAnomaly
		summary = anom.Reason

	case "gt":
		isFiring = val > r.CriticalThreshold
		isWarning = r.WarningThreshold != nil && val > *r.WarningThreshold
		summary = fmt.Sprintf("%s was %.2f in last %dm (critical threshold: > %.2f)", r.MetricType, val, r.WindowMinutes, r.CriticalThreshold)

	case "gte":
		isFiring = val >= r.CriticalThreshold
		isWarning = r.WarningThreshold != nil && val >= *r.WarningThreshold
		summary = fmt.Sprintf("%s was %.2f in last %dm (critical threshold: >= %.2f)", r.MetricType, val, r.WindowMinutes, r.CriticalThreshold)

	case "lt":
		isFiring = val < r.CriticalThreshold
		isWarning = r.WarningThreshold != nil && val < *r.WarningThreshold
		summary = fmt.Sprintf("%s was %.2f in last %dm (critical threshold: < %.2f)", r.MetricType, val, r.WindowMinutes, r.CriticalThreshold)
	}

	// State machine:
	if isFiring {
		if r.CurrentStatus != "firing" {
			_ = w.store.UpdateMetricRuleStatus(r.ID, "firing")
			inc, err := w.store.CreateMetricIncident(r.ID, r.ProjectID, val, summary)
			if err == nil && w.notifier != nil {
				w.notifier.NotifyMetricIncident(r, inc, false)
			}
		} else {
			// Already firing, update peak value if higher
			inc, err := w.store.GetActiveIncidentForRule(r.ID)
			if err == nil && inc != nil {
				_ = w.store.UpdateIncidentPeak(inc.ID, val)
			}
		}
	} else if isWarning {
		if r.CurrentStatus == "firing" {
			// Dropped from firing to warning -> auto-resolve incident
			inc, err := w.store.GetActiveIncidentForRule(r.ID)
			if err == nil && inc != nil {
				_ = w.store.ResolveActiveIncident(inc.ID, val)
				if w.notifier != nil {
					w.notifier.NotifyMetricIncident(r, inc, true)
				}
			}
			_ = w.store.UpdateMetricRuleStatus(r.ID, "warning")
		} else if r.CurrentStatus != "warning" {
			_ = w.store.UpdateMetricRuleStatus(r.ID, "warning")
		}
	} else {
		// OK
		if r.CurrentStatus == "firing" {
			// Auto-resolve incident
			inc, err := w.store.GetActiveIncidentForRule(r.ID)
			if err == nil && inc != nil {
				_ = w.store.ResolveActiveIncident(inc.ID, val)
				if w.notifier != nil {
					w.notifier.NotifyMetricIncident(r, inc, true)
				}
			}
			_ = w.store.UpdateMetricRuleStatus(r.ID, "ok")
		} else if r.CurrentStatus != "ok" {
			_ = w.store.UpdateMetricRuleStatus(r.ID, "ok")
		}
	}
}
