// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package uptime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"sightpane/internal/store"
)

type Checker struct {
	client      *http.Client
	concurrency int
	retryDelay  time.Duration
}

func NewChecker(concurrency int) *Checker {
	if concurrency <= 0 {
		concurrency = 20
	}
	return &Checker{
		client: &http.Client{
			// Individual requests set their own context deadlines
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("stopped after 10 redirects")
				}
				return nil
			},
		},
		concurrency: concurrency,
		retryDelay:  500 * time.Millisecond,
	}
}

// CheckResult represents the outcome of probing an endpoint.
type CheckResult struct {
	StatusCode     *int
	ResponseTimeMs int
	IsUp           bool
	ErrorMessage   string
	SSLIssuer      string
	SSLExpiresAt   *time.Time
}

// ProbeOnce executes a single HTTP request probe against the target URL.
func (c *Checker) ProbeOnce(ctx context.Context, method, targetURL string, headers map[string]string, expectedStatus int, timeout time.Duration) (int, int, bool, string) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, targetURL, nil)
	if err != nil {
		return 0, 0, false, fmt.Sprintf("invalid request: %v", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Sightpane-Uptime-Bot/1.0 (+https://sightpane.com)")
	}

	start := time.Now()
	resp, err := c.client.Do(req)
	durationMs := int(time.Since(start).Milliseconds())

	if err != nil {
		return 0, durationMs, false, fmt.Sprintf("connection failed: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	statusCode := resp.StatusCode
	if statusCode == expectedStatus {
		return statusCode, durationMs, true, ""
	}

	return statusCode, durationMs, false, fmt.Sprintf("unexpected status code: %d (expected %d)", statusCode, expectedStatus)
}

// CheckMonitor executes the check with retry logic, checks SSL, and records the result.
func (c *Checker) CheckMonitor(ctx context.Context, st *store.Store, m *store.UptimeMonitor) (*CheckResult, error) {
	timeout := time.Duration(m.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// 1. Initial attempt
	statusCode, ms, isUp, errMsg := c.ProbeOnce(ctx, m.Method, m.URL, m.Headers, m.ExpectedStatusCode, timeout)

	// 2. Failure confirmation: retry once before declaring down to avoid transient network false alarms
	if !isUp {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.retryDelay):
		}

		retryStatus, retryMs, retryUp, retryErr := c.ProbeOnce(ctx, m.Method, m.URL, m.Headers, m.ExpectedStatusCode, timeout)
		if retryUp {
			statusCode = retryStatus
			ms = retryMs
			isUp = true
			errMsg = ""
		} else {
			statusCode = retryStatus
			ms = retryMs
			errMsg = fmt.Sprintf("%s (retry failed: %s)", errMsg, retryErr)
		}
	}

	// 3. SSL Probe if enabled
	var sslIssuer string
	var sslExpiresAt *time.Time
	if m.SSLCheckEnabled {
		if sslResult, err := ProbeSSLCertificate(m.URL, timeout); err == nil && sslResult != nil {
			sslIssuer = sslResult.Issuer
			sslExpiresAt = &sslResult.ExpiresAt
		}
	}

	var statusPtr *int
	if statusCode > 0 {
		statusPtr = &statusCode
	}

	// 4. Record to database
	err := st.RecordUptimeCheck(
		m.ID, m.ProjectID, statusPtr, ms, isUp, errMsg, sslIssuer, sslExpiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("record uptime check: %w", err)
	}

	return &CheckResult{
		StatusCode:     statusPtr,
		ResponseTimeMs: ms,
		IsUp:           isUp,
		ErrorMessage:   errMsg,
		SSLIssuer:      sslIssuer,
		SSLExpiresAt:   sslExpiresAt,
	}, nil
}

// RunDueChecks searches for monitors that need checking and runs them concurrently.
func (c *Checker) RunDueChecks(ctx context.Context, st *store.Store) (int, error) {
	monitors, err := st.GetMonitorsDueForCheck()
	if err != nil {
		return 0, fmt.Errorf("monitors due: %w", err)
	}

	if len(monitors) == 0 {
		return 0, nil
	}

	sem := make(chan struct{}, c.concurrency)
	var wg sync.WaitGroup
	count := 0
	var mu sync.Mutex

	for _, m := range monitors {
		select {
		case <-ctx.Done():
			break
		default:
		}

		sem <- struct{}{}
		wg.Add(1)

		go func(mon *store.UptimeMonitor) {
			defer func() {
				<-sem
				wg.Done()
			}()

			_, err := c.CheckMonitor(ctx, st, mon)
			if err == nil {
				mu.Lock()
				count++
				mu.Unlock()
			}
		}(m)
	}

	wg.Wait()
	return count, nil
}
