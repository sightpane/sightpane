// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"sightpane/internal/blob"
	"sightpane/internal/symbol"
)

// --- Envelope ingest ---

type Envelope struct {
	SDK struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"sdk"`
	Session struct {
		ID        string          `json:"id"`
		StartedAt string          `json:"started_at"`
		User      json.RawMessage `json:"user"`
		Device    json.RawMessage `json:"device"`
		Props     json.RawMessage `json:"props"`
	} `json:"session"`
	Items []json.RawMessage `json:"items"`
}

type itemHead struct {
	Type      string `json:"type"`
	TS        string `json:"ts"`
	Name      string `json:"name"`
	Message   string `json:"message"`
	Exception string `json:"exception"`
	Stack     string `json:"stack"`
	Seq       int    `json:"seq"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	PNG       string `json:"png"`
	Taps      any    `json:"taps"`
	Category  string `json:"category"`
	Kind      string `json:"kind"`
	Route     string `json:"route"`
	// Frames is the stack the SDK already parsed out of the browser's own
	// format. It is only sent by a web build, where `stack` is minified
	// JavaScript that the backend can map back to Dart with an uploaded source
	// map. An SDK that does not send it loses nothing: `stack` is unchanged.
	Frames []symbol.Frame `json:"frames"`

	// Spans and transactions for performance monitoring
	Op           string          `json:"op"`
	DurationMs   float64         `json:"duration_ms"`
	Status       string          `json:"status"`
	ParentSpanID string          `json:"parent_span_id"`
	SpanID       string          `json:"span_id"`
	TraceID      string          `json:"trace_id"`
	Tags         json.RawMessage `json:"tags"`
	Spans        []spanChild     `json:"spans"`

	// Continuous profiling
	TransactionName string          `json:"transaction_name"`
	CPUTimeMs       float64         `json:"cpu_time_ms"`
	ThreadName      string          `json:"thread_name"`
	Platform        string          `json:"platform"`
	ProfileData     json.RawMessage `json:"profile_data"`
}

type spanChild struct {
	Op           string          `json:"op"`
	Name         string          `json:"name"`
	TS           string          `json:"ts"`
	DurationMs   float64         `json:"duration_ms"`
	Status       string          `json:"status"`
	ParentSpanID string          `json:"parent_span_id"`
	SpanID       string          `json:"span_id"`
	Tags         json.RawMessage `json:"tags"`
}

type IngestResult struct {
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
}

// timestampLayouts are what a `ts` from the SDK may look like. Dart's
// toIso8601String() writes the first for a UTC DateTime and the second for a
// local one; the rest are what other clients have sent.
var timestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
}

// parseTS turns whatever arrived into an instant. Something unparseable falls
// back to [fallback], which is the moment the envelope was received — the
// alternative is failing an envelope over one malformed field, and the column
// cannot hold the raw string anyway.
func parseTS(v string, fallback time.Time) time.Time {
	if v != "" {
		for _, layout := range timestampLayouts {
			if t, err := time.Parse(layout, v); err == nil {
				return t.UTC()
			}
		}
	}
	return fallback
}

// Ingest writes one envelope in a single transaction, so a batch either lands
// whole or not at all and a failure halfway through leaves no partial session.
// [ip] is the client address; the last envelope of a session wins, because the
// device can move between networks while the session is still open.
//
// The frame PNGs are decoded and written to the blob store before the
// transaction opens, so a pooled connection is never held across a network round
// trip to an object store.
func (s *Store) Ingest(ctx context.Context, projectID int64, env *Envelope, ip string) (*IngestResult, error) {
	if env.Session.ID == "" {
		return nil, ErrSessionIDRequired
	}
	now := time.Now().UTC()
	started := parseTS(env.Session.StartedAt, now)

	// Parse every item once. A nil entry is one that could not be read, which
	// the loop below counts as rejected in the position it arrived in.
	heads := make([]*itemHead, len(env.Items))
	for i, raw := range env.Items {
		var h itemHead
		if err := json.Unmarshal(raw, &h); err != nil || h.Type == "" {
			continue
		}
		heads[i] = &h
	}

	// The current route is whatever the last heartbeat or navigation breadcrumb in
	// this envelope reported, so the live view can show where a session is now.
	route := ""
	for _, h := range heads {
		if h == nil {
			continue
		}
		if h.Type == "heartbeat" && h.Route != "" {
			route = h.Route
		} else if h.Type == "breadcrumb" && h.Category == "navigation" && h.Message != "" {
			route = h.Message
		}
	}

	// Frames first and outside the transaction. A frame that cannot be stored
	// still fails the whole envelope, because nothing has been written yet.
	for _, h := range heads {
		if h == nil || h.Type != "frame" || h.PNG == "" || h.Seq <= 0 {
			continue
		}
		if err := s.saveFrame(ctx, env.Session.ID, h.Seq, h.PNG); err != nil {
			return nil, err
		}
	}

	userJSON, userID := rawOr(env.Session.User, "{}"), ""
	var u struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
	}
	_ = json.Unmarshal([]byte(userJSON), &u)
	userID = u.ID
	if userID == "" {
		userID = u.UserID
	}
	rawDev := rawOr(env.Session.Device, "{}")
	deviceJSON, d := EnrichDeviceJSON(rawDev)
	propsJSON := rawOr(env.Session.Props, "{}")

	// Geolocation resolution: resolve from raw client IP before anonymization/suppression
	var countryCode, countryName, region, city string
	var lat, lon *float64
	if s.geoip != nil && ip != "" {
		loc := s.geoip.Lookup(ip)
		countryCode = loc.CountryCode
		countryName = loc.CountryName
		region = loc.Region
		city = loc.City
		if loc.Latitude != 0 || loc.Longitude != 0 {
			latVal := loc.Latitude
			lonVal := loc.Longitude
			lat = &latVal
			lon = &lonVal
		}
	}

	// Privacy & PII settings
	var storeIP, scrubRulesJSON string
	_ = s.db.QueryRowContext(ctx, `SELECT store_ip, scrub_rules_json FROM projects WHERE id=$1`, projectID).Scan(&storeIP, &scrubRulesJSON)
	if storeIP == "" {
		storeIP = "full"
	}
	switch storeIP {
	case "none":
		ip = ""
	case "anonymized":
		ip = AnonymizeIP(ip)
	}

	var scrubRules []ScrubRule
	if scrubRulesJSON != "" && scrubRulesJSON != "[]" {
		_ = json.Unmarshal([]byte(scrubRulesJSON), &scrubRules)
	}
	if len(scrubRules) > 0 && propsJSON != "{}" {
		var propsMap map[string]any
		if err := json.Unmarshal([]byte(propsJSON), &propsMap); err == nil {
			ApplyScrubRulesToMap(scrubRules, propsMap)
			if b, err := json.Marshal(propsMap); err == nil {
				propsJSON = string(b)
			}
		}
	}

	browser := BrowserLabel(d.Browser, d.UA, d.Platform, d.OS)
	// The visitor key deliberately mixes in the browser and the IP: the same user
	// id seen from another browser or another address counts as another visitor.
	visitor := VisitorKey(userID, ip, browser)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO sessions(id, project_id, started_at, last_seen_at, user_id, user_json, device_json, props_json, platform, release, ip, browser, visitor_key, current_route, sdk_name, sdk_version, app_type, os, os_version, country_code, country_name, region, city, latitude, longitude)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)
		ON CONFLICT(id) DO UPDATE SET last_seen_at=excluded.last_seen_at, user_id=excluded.user_id, user_json=excluded.user_json,
		  device_json=excluded.device_json, props_json=excluded.props_json, platform=excluded.platform, release=excluded.release,
		  ip=CASE WHEN $26='none' THEN '' WHEN excluded.ip='' THEN sessions.ip ELSE excluded.ip END,
		  browser=excluded.browser, visitor_key=excluded.visitor_key,
		  current_route=CASE WHEN excluded.current_route='' THEN sessions.current_route ELSE excluded.current_route END,
		  sdk_name=CASE WHEN excluded.sdk_name!='' THEN excluded.sdk_name ELSE sessions.sdk_name END,
		  sdk_version=CASE WHEN excluded.sdk_version!='' THEN excluded.sdk_version ELSE sessions.sdk_version END,
		  app_type=CASE WHEN excluded.app_type!='' THEN excluded.app_type ELSE sessions.app_type END,
		  os=CASE WHEN excluded.os!='' THEN excluded.os ELSE sessions.os END,
		  os_version=CASE WHEN excluded.os_version!='' THEN excluded.os_version ELSE sessions.os_version END,
		  country_code=CASE WHEN excluded.country_code!='' THEN excluded.country_code ELSE sessions.country_code END,
		  country_name=CASE WHEN excluded.country_name!='' THEN excluded.country_name ELSE sessions.country_name END,
		  region=CASE WHEN excluded.region!='' THEN excluded.region ELSE sessions.region END,
		  city=CASE WHEN excluded.city!='' THEN excluded.city ELSE sessions.city END,
		  latitude=COALESCE(excluded.latitude, sessions.latitude),
		  longitude=COALESCE(excluded.longitude, sessions.longitude)`,
		env.Session.ID, projectID, started, now, userID, userJSON, deviceJSON, propsJSON, d.Platform, d.Release, ip, browser, visitor, route, env.SDK.Name, env.SDK.Version, d.AppType, d.OS, d.OSVersion, countryCode, countryName, region, city, lat, lon, storeIP)
	if err != nil {
		return nil, err
	}

	if d.Release != "" {
		_, _ = tx.Exec(`
			INSERT INTO project_releases(project_id, version, first_seen, last_seen, session_count)
			VALUES($1, $2, $3, $4, 1)
			ON CONFLICT(project_id, version) DO UPDATE SET
				last_seen = GREATEST(project_releases.last_seen, EXCLUDED.last_seen)`,
			projectID, d.Release, started, now)
	}

	res := &IngestResult{}
	var errs, events, frames int
	var issueEvents []IssueEvent
	for i, raw := range env.Items {
		h := heads[i]
		if h == nil {
			res.Rejected++
			continue
		}
		if len(scrubRules) > 0 {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err == nil {
				ApplyScrubRulesToMap(scrubRules, m)
				if b, err := json.Marshal(m); err == nil {
					raw = b
				}
			}
			h.Message = ApplyScrubRules(scrubRules, h.Message, "message")
			h.Exception = ApplyScrubRules(scrubRules, h.Exception, "exception")
			h.Stack = ApplyScrubRules(scrubRules, h.Stack, "stack")
		}
		ts := parseTS(h.TS, now)
		switch h.Type {
		case "frame":
			if h.PNG == "" || h.Seq <= 0 {
				res.Rejected++
				continue
			}
			taps := "[]"
			if h.Taps != nil {
				b, _ := json.Marshal(h.Taps)
				taps = string(b)
			}
			// A resent frame replaces the row it already has.
			if _, err := tx.Exec(`INSERT INTO frames(session_id, seq, ts, width, height, taps_json) VALUES($1,$2,$3,$4,$5,$6)
				ON CONFLICT(session_id, seq) DO UPDATE SET ts=excluded.ts, width=excluded.width, height=excluded.height, taps_json=excluded.taps_json`,
				env.Session.ID, h.Seq, ts, h.Width, h.Height, taps); err != nil {
				return nil, err
			}
			frames++
		case "error":
			// Resolving before fingerprinting is the point of the exercise: a
			// minified stack has no `package:` frame, so today's grouping falls
			// back to the message and every release lands in its own group.
			resolved := s.symbols.Resolve(ctx, projectID, d.Release, h.Frames)
			var symbolicated any // NULL unless something actually resolved
			if len(resolved) > 0 {
				if b, err := json.Marshal(map[string]any{"frames": resolved}); err == nil {
					symbolicated = string(b)
				}
			}
			fp, title := fingerprintOf(h.Exception, h.Message, h.Stack, env.SDK.Name, resolved)

			// Check fingerprint rules for the project
			rules, _ := s.ListFingerprintRules(projectID)
			matchedRule := MatchFingerprintRule(rules, h.Exception, h.Message, h.Stack)
			if matchedRule != nil && matchedRule.Action == "group_as" && matchedRule.GroupFingerprint != "" {
				fp = matchedRule.GroupFingerprint
			}

			// Determine existing issue status and snooze/ignore/regression behavior
			var prevStatus string
			var prevSnoozeUntil *time.Time
			var prevSnoozeThreshold, prevSnoozeStart, prevCount int
			var prevFirstRelease, prevLastRelease, prevResolvedInRelease string
			err := tx.QueryRow(`
				SELECT status, snooze_until, snooze_count_threshold, snooze_start_count, count,
				       COALESCE(first_release, ''), COALESCE(last_release, ''), COALESCE(resolved_in_release, '')
				FROM issues WHERE project_id=$1 AND fingerprint=$2`,
				projectID, fp,
			).Scan(&prevStatus, &prevSnoozeUntil, &prevSnoozeThreshold, &prevSnoozeStart, &prevCount,
				&prevFirstRelease, &prevLastRelease, &prevResolvedInRelease)

			var eventKind string
			targetStatus := "open"
			if matchedRule != nil && matchedRule.Action == "ignore" {
				targetStatus = "ignored"
			}

			if errors.Is(err, sql.ErrNoRows) {
				if targetStatus != "ignored" {
					eventKind = "new_issue"
				}
			} else if err == nil {
				if prevStatus == "ignored" {
					targetStatus = "ignored"
				} else if prevStatus == "snoozed" {
					woken := false
					if prevSnoozeUntil != nil && !ts.Before(*prevSnoozeUntil) {
						woken = true
					}
					if prevSnoozeThreshold > 0 && (prevCount+1-prevSnoozeStart) >= prevSnoozeThreshold {
						woken = true
					}
					if woken {
						targetStatus = "open"
						eventKind = "regression"
					} else {
						targetStatus = "snoozed"
					}
				} else if prevStatus == "resolved" {
					if prevResolvedInRelease != "" && d.Release != "" {
						if CompareVersions(d.Release, prevResolvedInRelease) < 0 {
							// Older release: do not regress!
							targetStatus = "resolved"
						} else {
							// Same or newer release: regressed!
							targetStatus = "open"
							eventKind = "regression"
						}
					} else {
						targetStatus = "open"
						eventKind = "regression"
					}
				} else {

					targetStatus = prevStatus
				}
			}

			// Upsert issue with updated status and release tracking
			var issueID int64
			var newCount int
			firstRel := d.Release
			lastRel := d.Release
			if prevFirstRelease != "" {
				firstRel = prevFirstRelease
			}
			if lastRel == "" {
				lastRel = prevLastRelease
			}

			if err := tx.QueryRow(`INSERT INTO issues(project_id, fingerprint, title, exception, first_seen, last_seen, count, status, resolved, first_release, last_release)
				VALUES($1,$2,$3,$4,$5,$6,1,$7,$8,$9,$10)
				ON CONFLICT(project_id, fingerprint) DO UPDATE SET
					last_seen=excluded.last_seen,
					count=issues.count+1,
					status=$7,
					resolved=$8,
					first_release=CASE WHEN issues.first_release='' THEN excluded.first_release ELSE issues.first_release END,
					last_release=CASE WHEN excluded.last_release!='' THEN excluded.last_release ELSE issues.last_release END
				RETURNING id, count`, projectID, fp, title, h.Exception, ts, ts, targetStatus, targetStatus == "resolved", firstRel, lastRel).Scan(&issueID, &newCount); err != nil {
				return nil, err
			}

			if _, err := tx.Exec(`INSERT INTO items(session_id, project_id, ts, type, name, body_json, issue_id, symbolicated_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				env.Session.ID, projectID, ts, "error", title, string(raw), issueID, symbolicated); err != nil {
				return nil, err
			}
			if eventKind != "" {
				issueEvents = append(issueEvents, IssueEvent{
					ProjectID:   projectID,
					IssueID:     issueID,
					Fingerprint: fp,
					Title:       title,
					Exception:   h.Exception,
					Kind:        eventKind,
					Count:       newCount,
					FirstSeen:   ts,
					LastSeen:    ts,
					Route:       route,
					Browser:     browser,
				})
			}
			errs++
		case "session_end":
			if _, err := tx.Exec(`UPDATE sessions SET ended_at=$1 WHERE id=$2`, ts, env.Session.ID); err != nil {
				return nil, err
			}
		case "heartbeat":
			// The session row already took last_seen_at and current_route from the
			// upsert above, so a heartbeat stores no item of its own. All that is
			// left is to revive a session an earlier session_end had closed.
			if _, err := tx.Exec(`UPDATE sessions SET ended_at=NULL WHERE id=$1`, env.Session.ID); err != nil {
				return nil, err
			}
		case "event", "breadcrumb", "pointer", "dom":
			name := h.Name
			if h.Type == "breadcrumb" {
				name = h.Category
			}
			if h.Type == "pointer" {
				name = "pointer"
			}
			if h.Type == "dom" {
				name = h.Kind
				if name == "" {
					name = "dom"
				}
			}
			if _, err := tx.Exec(`INSERT INTO items(session_id, project_id, ts, type, name, body_json) VALUES($1,$2,$3,$4,$5,$6)`, env.Session.ID, projectID, ts, h.Type, name, string(raw)); err != nil {
				return nil, err
			}
			if h.Type == "event" {
				events++
			}
		case "transaction", "span":
			op := h.Op
			if op == "" {
				op = "custom"
			}
			name := h.Name
			dur := h.DurationMs
			status := h.Status
			if status == "" {
				status = "ok"
			}
			tags := rawOr(h.Tags, "{}")
			if _, err := tx.Exec(`INSERT INTO spans(project_id, session_id, ts, op, name, duration_ms, status, parent_span_id, span_id, trace_id, tags_json)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				projectID, env.Session.ID, ts, op, name, dur, status, h.ParentSpanID, h.SpanID, h.TraceID, tags); err != nil {
				return nil, err
			}
			for _, child := range h.Spans {
				childTS := parseTS(child.TS, ts)
				childOp := child.Op
				if childOp == "" {
					childOp = op
				}
				childStatus := child.Status
				if childStatus == "" {
					childStatus = "ok"
				}
				childParent := child.ParentSpanID
				if childParent == "" {
					childParent = h.SpanID
				}
				childTags := rawOr(child.Tags, "{}")
				if _, err := tx.Exec(`INSERT INTO spans(project_id, session_id, ts, op, name, duration_ms, status, parent_span_id, span_id, trace_id, tags_json)
					VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
					projectID, env.Session.ID, childTS, childOp, child.Name, child.DurationMs, childStatus, childParent, child.SpanID, h.TraceID, childTags); err != nil {
					return nil, err
				}
			}
			if _, err := tx.Exec(`INSERT INTO items(session_id, project_id, ts, type, name, body_json) VALUES($1,$2,$3,$4,$5,$6)`, env.Session.ID, projectID, ts, h.Type, name, string(raw)); err != nil {
				return nil, err
			}
		case "profile":
			txName := h.TransactionName
			if txName == "" {
				txName = h.Name
			}
			thread := h.ThreadName
			if thread == "" {
				thread = "main"
			}
			dur := h.DurationMs
			cpu := h.CPUTimeMs
			if cpu <= 0 {
				cpu = dur
			}
			profData := h.ProfileData
			if len(profData) == 0 {
				profData = json.RawMessage("{}")
			}
			var sessID any
			if env.Session.ID != "" {
				sessID = env.Session.ID
			}
			plat := h.Platform
			if plat == "" {
				plat = d.Platform
			}
			if _, err := tx.Exec(`INSERT INTO profiles(project_id, transaction_name, session_id, trace_id, duration_ms, cpu_time_ms, thread_name, platform, profile_data, created_at)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				projectID, txName, sessID, h.TraceID, dur, cpu, thread, plat, profData, ts); err != nil {
				return nil, err
			}
		default:
			res.Rejected++
			continue
		}
		res.Accepted++
	}
	if _, err := tx.Exec(`UPDATE sessions SET error_count=error_count+$1, event_count=event_count+$2, frame_count=frame_count+$3 WHERE id=$4`, errs, events, frames, env.Session.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if len(issueEvents) > 0 && s.onIssueEvent != nil {
		s.onIssueEvent(issueEvents)
	}
	return res, nil
}

func rawOr(r json.RawMessage, def string) string {
	if len(r) == 0 || string(r) == "null" {
		return def
	}
	return string(r)
}

// saveFrame decodes the base64 PNG and hands it to the frame store. It runs
// before any row is written, so a frame that cannot be stored fails the whole
// envelope rather than leaving a row pointing at nothing.
func (s *Store) saveFrame(ctx context.Context, sessionID string, seq int, b64 string) error {
	data, err := decodeBase64(b64)
	if err != nil {
		return fmt.Errorf("frame %d: %w", seq, err)
	}
	return s.blobs.Put(ctx, blob.FrameKey(sessionID, seq), data)
}
