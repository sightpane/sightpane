// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"sightpane/internal/config"
	"sightpane/internal/store"
)

type Notifier struct {
	store     *store.Store
	cfg       config.Config
	client    *http.Client
	events    chan store.IssueEvent
	stop      chan struct{}
	wg        sync.WaitGroup
	tickEvery time.Duration
}

type NotificationPayload struct {
	EventKind   string `json:"event_kind"` // new_issue, regression, rate_spike, session_crash_free, test
	ProjectID   int64  `json:"project_id"`
	ProjectName string `json:"project_name"`
	Title       string `json:"title"`
	IssueID     int64  `json:"issue_id,omitempty"`
	Count       int    `json:"count,omitempty"`
	FirstSeen   string `json:"first_seen,omitempty"`
	LastSeen    string `json:"last_seen,omitempty"`
	Route       string `json:"route,omitempty"`
	Browser     string `json:"browser,omitempty"`
	URL         string `json:"url,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

func NewNotifier(st *store.Store, cfg config.Config) *Notifier {
	n := &Notifier{
		store:     st,
		cfg:       cfg,
		client:    &http.Client{Timeout: 10 * time.Second},
		events:    make(chan store.IssueEvent, 2048),
		stop:      make(chan struct{}),
		tickEvery: time.Minute,
	}
	return n
}

// SetTickInterval overrides the default 1-minute ticker (useful for unit tests).
func (n *Notifier) SetTickInterval(d time.Duration) {
	n.tickEvery = d
}

// Start launches the background worker goroutine.
func (n *Notifier) Start() {
	n.wg.Add(1)
	go n.run()
}

// Stop signals the background worker to shut down and waits for it to finish.
func (n *Notifier) Stop() {
	close(n.stop)
	n.wg.Wait()
}

// Enqueue drops issue events into the in-memory queue. It is non-blocking to protect
// ingest latency.
func (n *Notifier) Enqueue(events []store.IssueEvent) {
	for _, ev := range events {
		select {
		case n.events <- ev:
		default:
			log.Printf("alert notifier: queue full, dropping event for issue %d", ev.IssueID)
		}
	}
}

func (n *Notifier) run() {
	defer n.wg.Done()
	ticker := time.NewTicker(n.tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-n.stop:
			return
		case ev := <-n.events:
			n.handleIssueEvent(ev)
		case <-ticker.C:
			n.evaluateRateRules()
		}
	}
}

func (n *Notifier) handleIssueEvent(ev store.IssueEvent) {
	rules, err := n.store.ListAlertRules(ev.ProjectID)
	if err != nil {
		log.Printf("alert notifier: list rules for project %d: %v", ev.ProjectID, err)
		return
	}

	p, _ := n.store.ProjectByID(ev.ProjectID)
	projName := fmt.Sprintf("Project %d", ev.ProjectID)
	if p != nil {
		projName = p.Name
	}

	deepLink := fmt.Sprintf("%s/projects/%d/issues/%d", n.cfg.PublicURL, ev.ProjectID, ev.IssueID)

	payload := NotificationPayload{
		EventKind:   ev.Kind,
		ProjectID:   ev.ProjectID,
		ProjectName: projName,
		Title:       ev.Title,
		IssueID:     ev.IssueID,
		Count:       ev.Count,
		FirstSeen:   ev.FirstSeen.UTC().Format(time.RFC3339),
		LastSeen:    ev.LastSeen.UTC().Format(time.RFC3339),
		Route:       ev.Route,
		Browser:     ev.Browser,
		URL:         deepLink,
	}

	for _, r := range rules {
		if !r.Enabled || r.Kind != ev.Kind {
			continue
		}

		// Deduplication: do not send again for the same event & rule
		delivered, err := n.store.HasDeliveredAlert(r.ID, ev.IssueID)
		if err != nil {
			log.Printf("alert notifier: check delivery for rule %d issue %d: %v", r.ID, ev.IssueID, err)
			continue
		}
		if delivered {
			continue
		}

		for _, chID := range r.ChannelIDs {
			ch, err := n.store.GetAlertChannel(ev.ProjectID, chID)
			if err != nil {
				log.Printf("alert notifier: channel %d not found: %v", chID, err)
				continue
			}

			err = n.sendToChannel(context.Background(), ch, payload)
			status := "success"
			errStr := ""
			if err != nil {
				status = "failed"
				errStr = err.Error()
				log.Printf("alert notifier: failed sending to channel %d (%s): %v", ch.ID, ch.Kind, err)
			}

			issueID := ev.IssueID
			if err := n.store.RecordAlertDelivery(r.ID, ch.ID, &issueID, ev.Fingerprint, status, errStr); err != nil {
				log.Printf("alert notifier: record delivery: %v", err)
			}
		}
	}
}

func (n *Notifier) EvaluateRateRules() {
	n.evaluateRateRules()
}

func (n *Notifier) evaluateRateRules() {
	projects, err := n.store.ListAllProjects()
	if err != nil {
		return
	}

	for _, p := range projects {
		rules, err := n.store.ListAlertRules(p.ID)
		if err != nil {
			continue
		}

		for _, r := range rules {
			if !r.Enabled {
				continue
			}
			if r.Kind != "rate_spike" && r.Kind != "session_crash_free" {
				continue
			}

			var params struct {
				Threshold       float64 `json:"threshold"`
				WindowMinutes   int     `json:"window_minutes"`
				CooldownMinutes int     `json:"cooldown_minutes"`
			}
			_ = json.Unmarshal(r.Params, &params)
			if params.WindowMinutes <= 0 {
				params.WindowMinutes = 15
			}
			if params.CooldownMinutes <= 0 {
				params.CooldownMinutes = 30
			}

			// Cooldown check
			lastSent, err := n.store.LastAlertDelivery(r.ID)
			if err == nil && !lastSent.IsZero() {
				if time.Since(lastSent) < time.Duration(params.CooldownMinutes)*time.Minute {
					continue
				}
			}

			errs, sess, crashFree, err := n.store.EvaluateRateMetrics(p.ID, params.WindowMinutes)
			if err != nil {
				continue
			}

			triggered := false
			summary := ""
			if r.Kind == "rate_spike" {
				// Threshold can be error count (e.g. > 10) or error rate
				threshold := params.Threshold
				if threshold <= 0 {
					threshold = 10 // default 10 errors
				}
				if float64(errs) >= threshold {
					triggered = true
					summary = fmt.Sprintf("%d errors detected in the last %d minutes (threshold: %.0f)", errs, params.WindowMinutes, threshold)
				}
			} else if r.Kind == "session_crash_free" {
				threshold := params.Threshold
				if threshold <= 0 {
					threshold = 0.90 // default 90% crash-free
				}
				if sess >= 5 && crashFree < threshold {
					triggered = true
					summary = fmt.Sprintf("Crash-free sessions dropped to %.1f%% in the last %d minutes (threshold: %.1f%%)", crashFree*100, params.WindowMinutes, threshold*100)
				}
			}

			if !triggered {
				continue
			}

			deepLink := fmt.Sprintf("%s/projects/%d", n.cfg.PublicURL, p.ID)
			payload := NotificationPayload{
				EventKind:   r.Kind,
				ProjectID:   p.ID,
				ProjectName: p.Name,
				Title:       r.Name,
				Count:       errs,
				URL:         deepLink,
				Summary:     summary,
			}

			for _, chID := range r.ChannelIDs {
				ch, err := n.store.GetAlertChannel(p.ID, chID)
				if err != nil {
					continue
				}
				err = n.sendToChannel(context.Background(), ch, payload)
				status := "success"
				errStr := ""
				if err != nil {
					status = "failed"
					errStr = err.Error()
				}
				_ = n.store.RecordAlertDelivery(r.ID, ch.ID, nil, "", status, errStr)
			}
		}
	}
}

// SendTest sends a test notification to the specified channel to verify delivery.
func (n *Notifier) SendTest(ctx context.Context, ch *store.AlertChannel) error {
	p, _ := n.store.ProjectByID(ch.ProjectID)
	projName := fmt.Sprintf("Project %d", ch.ProjectID)
	if p != nil {
		projName = p.Name
	}
	payload := NotificationPayload{
		EventKind:   "test",
		ProjectID:   ch.ProjectID,
		ProjectName: projName,
		Title:       "Test Notification",
		Summary:     fmt.Sprintf("This is a test notification from Sightpane for channel %q (%s).", ch.Name, ch.Kind),
		URL:         fmt.Sprintf("%s/projects/%d/settings", n.cfg.PublicURL, ch.ProjectID),
	}
	return n.sendToChannel(ctx, ch, payload)
}

func (n *Notifier) sendToChannel(ctx context.Context, ch *store.AlertChannel, p NotificationPayload) error {
	switch ch.Kind {
	case "email":
		return n.sendEmail(ch.Target, p)
	case "slack":
		return n.sendSlack(ctx, ch.Target, p)
	case "webhook":
		return n.sendWebhook(ctx, ch.Target, ch.Secret, p)
	default:
		return fmt.Errorf("unsupported channel kind: %s", ch.Kind)
	}
}

func (n *Notifier) sendSlack(ctx context.Context, webhookURL string, p NotificationPayload) error {
	text := formatAlertText(p)
	bodyMap := map[string]any{
		"text": text,
		"blocks": []map[string]any{
			{
				"type": "header",
				"text": map[string]any{
					"type": "plain_text",
					"text": fmt.Sprintf("[%s] %s", strings.ToUpper(p.EventKind), p.Title),
				},
			},
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": text,
				},
			},
		},
	}
	b, err := json.Marshal(bodyMap)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (n *Notifier) sendWebhook(ctx context.Context, targetURL, secret string, p NotificationPayload) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Sightpane-Alert-Webhook/1.0")

	// HMAC-SHA256 signature
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("X-Sightpane-Timestamp", timestamp)
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(timestamp))
		mac.Write([]byte("."))
		mac.Write(b)
		signature := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Sightpane-Signature", fmt.Sprintf("t=%s,v1=%s", timestamp, signature))
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (n *Notifier) sendEmail(to string, p NotificationPayload) error {
	smtpCfg := n.cfg.SMTP
	if smtpCfg.Host == "" {
		return fmt.Errorf("smtp host is not configured (SMTP_HOST)")
	}

	subject := fmt.Sprintf("[Sightpane Alert] %s: %s", p.ProjectName, p.Title)
	if p.EventKind == "regression" {
		subject = fmt.Sprintf("[Sightpane Regression] %s: %s", p.ProjectName, p.Title)
	} else if p.EventKind == "test" {
		subject = fmt.Sprintf("[Sightpane Test] %s: %s", p.ProjectName, p.Title)
	}

	body := formatAlertText(p)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
		smtpCfg.From, to, subject, body)

	addr := fmt.Sprintf("%s:%d", smtpCfg.Host, smtpCfg.Port)
	var auth smtp.Auth
	if smtpCfg.User != "" && smtpCfg.Pass != "" {
		auth = smtp.PlainAuth("", smtpCfg.User, smtpCfg.Pass, smtpCfg.Host)
	}

	return smtp.SendMail(addr, auth, smtpCfg.From, []string{to}, []byte(msg))
}

func formatAlertText(p NotificationPayload) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("*Project:* %s (ID: %d)\n", p.ProjectName, p.ProjectID))
	b.WriteString(fmt.Sprintf("*Event:* %s\n", p.EventKind))
	if p.Title != "" {
		b.WriteString(fmt.Sprintf("*Title:* %s\n", p.Title))
	}
	if p.Summary != "" {
		b.WriteString(fmt.Sprintf("*Details:* %s\n", p.Summary))
	}
	if p.Count > 0 {
		b.WriteString(fmt.Sprintf("*Occurrences:* %d\n", p.Count))
	}
	if p.Route != "" {
		b.WriteString(fmt.Sprintf("*Route:* %s\n", p.Route))
	}
	if p.Browser != "" {
		b.WriteString(fmt.Sprintf("*Browser:* %s\n", p.Browser))
	}
	if p.LastSeen != "" {
		b.WriteString(fmt.Sprintf("*Time:* %s\n", p.LastSeen))
	}
	if p.URL != "" {
		b.WriteString(fmt.Sprintf("*View in Dashboard:* %s\n", p.URL))
	}
	return b.String()
}
