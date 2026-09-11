// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"sightpane/internal/blob"
)

type ScrubRule struct {
	Field   string `json:"field"`
	Regex   string `json:"regex"`
	Replace string `json:"replace"`
}

var scrubRegexCache sync.Map // map[string]*regexp.Regexp

func getScrubRegex(pattern string) *regexp.Regexp {
	if val, ok := scrubRegexCache.Load(pattern); ok {
		if re, ok := val.(*regexp.Regexp); ok {
			return re
		}
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		scrubRegexCache.Store(pattern, (*regexp.Regexp)(nil))
		return nil
	}
	scrubRegexCache.Store(pattern, re)
	return re
}

func AnonymizeIP(ipStr string) string {
	ipStr = strings.TrimSpace(ipStr)
	if ipStr == "" {
		return ""
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		v4[3] = 0
		return v4.String()
	}
	// IPv6: zero out last 80 bits (keep /48, bytes 0..5)
	for i := 6; i < len(ip); i++ {
		ip[i] = 0
	}
	return ip.String()
}

func ApplyScrubRules(rules []ScrubRule, text string, field string) string {
	for _, r := range rules {
		if r.Field != "" && field != "" && r.Field != field {
			continue
		}
		if r.Regex == "" {
			continue
		}
		if re := getScrubRegex(r.Regex); re != nil {
			text = re.ReplaceAllString(text, r.Replace)
		}
	}
	return text
}

func ApplyScrubRulesToMap(rules []ScrubRule, m map[string]any) {
	for k, v := range m {
		switch val := v.(type) {
		case string:
			m[k] = ApplyScrubRules(rules, val, k)
		case map[string]any:
			ApplyScrubRulesToMap(rules, val)
		case []any:
			for i, elem := range val {
				if s, ok := elem.(string); ok {
					val[i] = ApplyScrubRules(rules, s, k)
				} else if subMap, ok := elem.(map[string]any); ok {
					ApplyScrubRulesToMap(rules, subMap)
				}
			}
		}
	}
}

// DeleteUserData removes all sessions, items, spans, and frame files associated
// with a user under a specific project.
func (s *Store) DeleteUserData(ctx context.Context, projectID int64, userID string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM sessions WHERE project_id=$1 AND user_id=$2`, projectID, userID)
	if err != nil {
		return err
	}
	var sessions []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err == nil {
			sessions = append(sessions, sid)
		}
	}
	rows.Close()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const batchSize = 500
	for i := 0; i < len(sessions); i += batchSize {
		end := i + batchSize
		if end > len(sessions) {
			end = len(sessions)
		}
		batch := sessions[i:end]

		if _, err := tx.ExecContext(ctx, `DELETE FROM frames WHERE session_id = ANY($1)`, batch); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE project_id=$1 AND session_id = ANY($2)`, projectID, batch); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM spans WHERE project_id=$1 AND session_id = ANY($2)`, projectID, batch); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE project_id=$1 AND user_id=$2`, projectID, userID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Clean up blob frame files for deleted sessions
	for _, sid := range sessions {
		if err := s.blobs.DeletePrefix(ctx, blob.SessionPrefix(sid)); err != nil {
			log.Printf("delete user data: frames of session %s: %v", sid, err)
		}
	}
	return nil
}

// ExportUserData generates a zip archive containing export.json (sessions, items, spans)
// and frame files under frames/<session_id>/<seq>.png.
func (s *Store) ExportUserData(ctx context.Context, projectID int64, userID string) ([]byte, error) {
	// Query sessions
	sRows, err := s.db.QueryContext(ctx, `
		SELECT id, started_at, last_seen_at, user_json, device_json, props_json, platform, release, ip, browser, current_route
		FROM sessions WHERE project_id=$1 AND user_id=$2 ORDER BY started_at`, projectID, userID)
	if err != nil {
		return nil, err
	}
	defer sRows.Close()

	var sessions []map[string]any
	var sessionIDs []string
	for sRows.Next() {
		var id, platform, release, ip, browser, route string
		var startedAt, lastSeenAt time.Time
		var userJSON, deviceJSON, propsJSON string
		if err := sRows.Scan(&id, &startedAt, &lastSeenAt, &userJSON, &deviceJSON, &propsJSON, &platform, &release, &ip, &browser, &route); err != nil {
			return nil, err
		}
		sessionIDs = append(sessionIDs, id)
		sessions = append(sessions, map[string]any{
			"id":            id,
			"started_at":    startedAt.Format(time.RFC3339Nano),
			"last_seen_at":  lastSeenAt.Format(time.RFC3339Nano),
			"user_json":     userJSON,
			"device_json":   deviceJSON,
			"props_json":    propsJSON,
			"platform":      platform,
			"release":       release,
			"ip":            ip,
			"browser":       browser,
			"current_route": route,
		})
	}

	// Query items
	var items []map[string]any
	for _, sid := range sessionIDs {
		iRows, err := s.db.QueryContext(ctx, `SELECT id, ts, type, name, body_json FROM items WHERE project_id=$1 AND session_id=$2 ORDER BY ts`, projectID, sid)
		if err == nil {
			for iRows.Next() {
				var id int64
				var ts time.Time
				var typ, name, bodyJSON string
				if err := iRows.Scan(&id, &ts, &typ, &name, &bodyJSON); err == nil {
					items = append(items, map[string]any{
						"id":         id,
						"session_id": sid,
						"ts":         ts.Format(time.RFC3339Nano),
						"type":       typ,
						"name":       name,
						"body_json":  bodyJSON,
					})
				}
			}
			iRows.Close()
		}
	}

	// Query spans
	var spans []map[string]any
	for _, sid := range sessionIDs {
		spRows, err := s.db.QueryContext(ctx, `SELECT trace_id, span_id, parent_span_id, name, op, status, duration_ms, tags_json, ts FROM spans WHERE project_id=$1 AND session_id=$2 ORDER BY ts`, projectID, sid)
		if err == nil {
			for spRows.Next() {
				var traceID, spanID, parentSpanID, name, op, status, tagsJSON string
				var durationMs float64
				var ts time.Time
				if err := spRows.Scan(&traceID, &spanID, &parentSpanID, &name, &op, &status, &durationMs, &tagsJSON, &ts); err == nil {
					spans = append(spans, map[string]any{
						"session_id":     sid,
						"trace_id":       traceID,
						"span_id":        spanID,
						"parent_span_id": parentSpanID,
						"name":           name,
						"op":             op,
						"status":         status,
						"duration_ms":    durationMs,
						"tags_json":      tagsJSON,
						"ts":             ts.Format(time.RFC3339Nano),
					})
				}
			}
			spRows.Close()
		}
	}

	exportDoc := map[string]any{
		"project_id":  projectID,
		"user_id":     userID,
		"exported_at": time.Now().UTC().Format(time.RFC3339Nano),
		"sessions":    sessions,
		"items":       items,
		"spans":       spans,
	}

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// Write export.json
	metaBytes, err := json.MarshalIndent(exportDoc, "", "  ")
	if err != nil {
		return nil, err
	}
	f, err := zw.Create("export.json")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(metaBytes); err != nil {
		return nil, err
	}

	// Write frame PNGs
	for _, sid := range sessionIDs {
		fRows, err := s.db.QueryContext(ctx, `SELECT seq FROM frames WHERE session_id=$1 ORDER BY seq`, sid)
		if err != nil {
			continue
		}
		var seqs []int
		for fRows.Next() {
			var seq int
			if err := fRows.Scan(&seq); err == nil {
				seqs = append(seqs, seq)
			}
		}
		fRows.Close()

		for _, seq := range seqs {
			rc, _, err := s.blobs.Get(ctx, blob.FrameKey(sid, seq))
			if err == nil && rc != nil {
				pngData, _ := io.ReadAll(rc)
				rc.Close()
				if len(pngData) > 0 {
					path := fmt.Sprintf("frames/%s/%d.png", sid, seq)
					zf, err := zw.Create(path)
					if err == nil {
						_, _ = zf.Write(pngData)
					}
				}
			}
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
