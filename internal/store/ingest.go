// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
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
	Route     string `json:"route"`
	// Frames is the stack the SDK already parsed out of the browser's own
	// format. It is only sent by a web build, where `stack` is minified
	// JavaScript that the backend can map back to Dart with an uploaded source
	// map. An SDK that does not send it loses nothing: `stack` is unchanged.
	Frames []symbol.Frame `json:"frames"`
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
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(userJSON), &u)
	userID = u.ID
	deviceJSON := rawOr(env.Session.Device, "{}")
	var d struct {
		Platform string `json:"platform"`
		Release  string `json:"release"`
		Browser  string `json:"browser"`
		OS       string `json:"os"`
		UA       string `json:"user_agent"`
	}
	_ = json.Unmarshal([]byte(deviceJSON), &d)
	propsJSON := rawOr(env.Session.Props, "{}")
	browser := BrowserLabel(d.Browser, d.UA, d.Platform, d.OS)
	// The visitor key deliberately mixes in the browser and the IP: the same user
	// id seen from another browser or another address counts as another visitor.
	visitor := VisitorKey(userID, ip, browser)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO sessions(id, project_id, started_at, last_seen_at, user_id, user_json, device_json, props_json, platform, release, ip, browser, visitor_key, current_route)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT(id) DO UPDATE SET last_seen_at=excluded.last_seen_at, user_id=excluded.user_id, user_json=excluded.user_json,
		  device_json=excluded.device_json, props_json=excluded.props_json, platform=excluded.platform, release=excluded.release,
		  ip=CASE WHEN excluded.ip='' THEN sessions.ip ELSE excluded.ip END,
		  browser=excluded.browser, visitor_key=excluded.visitor_key,
		  current_route=CASE WHEN excluded.current_route='' THEN sessions.current_route ELSE excluded.current_route END`,
		env.Session.ID, projectID, started, now, userID, userJSON, deviceJSON, propsJSON, d.Platform, d.Release, ip, browser, visitor, route)
	if err != nil {
		return nil, err
	}

	res := &IngestResult{}
	var errs, events, frames int
	for i, raw := range env.Items {
		h := heads[i]
		if h == nil {
			res.Rejected++
			continue
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
			fp, title := fingerprintOf(h.Exception, h.Message, h.Stack, resolved)
			// RETURNING on the upsert: the id of the row that was inserted or
			// the one that was updated, without a second lookup.
			var issueID int64
			if err := tx.QueryRow(`INSERT INTO issues(project_id, fingerprint, title, exception, first_seen, last_seen, count) VALUES($1,$2,$3,$4,$5,$6,1)
				ON CONFLICT(project_id, fingerprint) DO UPDATE SET last_seen=excluded.last_seen, count=issues.count+1, resolved=FALSE
				RETURNING id`, projectID, fp, title, h.Exception, ts, ts).Scan(&issueID); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`INSERT INTO items(session_id, project_id, ts, type, name, body_json, issue_id, symbolicated_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				env.Session.ID, projectID, ts, "error", title, string(raw), issueID, symbolicated); err != nil {
				return nil, err
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
		case "event", "breadcrumb", "pointer":
			name := h.Name
			if h.Type == "breadcrumb" {
				name = h.Category
			}
			if h.Type == "pointer" {
				name = "pointer"
			}
			if _, err := tx.Exec(`INSERT INTO items(session_id, project_id, ts, type, name, body_json) VALUES($1,$2,$3,$4,$5,$6)`, env.Session.ID, projectID, ts, h.Type, name, string(raw)); err != nil {
				return nil, err
			}
			if h.Type == "event" {
				events++
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
	return res, tx.Commit()
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
