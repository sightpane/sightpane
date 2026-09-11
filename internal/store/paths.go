// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type PathNode struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Step  int    `json:"step"`
	Count int64  `json:"count"`
}

type PathLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Count  int64  `json:"count"`
}

type PathResult struct {
	RootEvent string     `json:"root_event"`
	Direction string     `json:"direction"`
	StepLimit int        `json:"step_limit"`
	Nodes     []PathNode `json:"nodes"`
	Links     []PathLink `json:"links"`
}

type PathOptions struct {
	ProjectID           int64
	RootEvent           string
	Direction           string // "forward" or "reverse"
	StepLimit           int    // default 4, max 5
	Days                int
	ExcludeEvents       []string
	MinThresholdPercent float64
}

func (s *Store) CalculateUserPaths(ctx context.Context, opt PathOptions) (*PathResult, error) {
	if opt.StepLimit <= 0 || opt.StepLimit > 5 {
		opt.StepLimit = 4
	}
	if opt.Days <= 0 {
		opt.Days = 14
	}
	if opt.Direction != "reverse" {
		opt.Direction = "forward"
	}
	if opt.MinThresholdPercent <= 0 {
		opt.MinThresholdPercent = 1.0
	}

	since := time.Now().AddDate(0, 0, -opt.Days)

	// Build exclude events clause if specified
	excludeSQL := ""
	var args []any
	args = append(args, opt.ProjectID, since)

	if len(opt.ExcludeEvents) > 0 {
		placeholders := make([]string, len(opt.ExcludeEvents))
		for i, ex := range opt.ExcludeEvents {
			args = append(args, ex)
			placeholders[i] = fmt.Sprintf("$%d", len(args))
		}
		excludeSQL = fmt.Sprintf("AND event_name NOT IN (%s)", strings.Join(placeholders, ","))
	}

	rootEventPlaceholder := ""
	if opt.RootEvent != "" {
		args = append(args, opt.RootEvent)
		rootEventPlaceholder = fmt.Sprintf("$%d", len(args))
	}

	var query string
	if opt.RootEvent == "" {
		// Forward from first event of each session
		query = fmt.Sprintf(`
WITH raw_events AS (
    SELECT
        session_id,
        ts,
        CASE
            WHEN type = 'event' THEN name
            WHEN type = 'breadcrumb' AND name = 'navigation' THEN
                'route:' || COALESCE(NULLIF(body_json::jsonb->'data'->>'to', ''), NULLIF(body_json::jsonb->>'message', ''), 'route')
            WHEN type = 'error' THEN
                'error:' || COALESCE(NULLIF(body_json::jsonb->>'type', ''), 'Exception')
            ELSE ''
        END AS event_name
    FROM items
    WHERE project_id = $1
      AND ts >= $2
      AND type IN ('event', 'breadcrumb', 'error')
),
filtered_events AS (
    SELECT session_id, ts, event_name
    FROM raw_events
    WHERE event_name != '' %s
),
indexed_events AS (
    SELECT
        session_id,
        (ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY ts) - 1) AS step_idx,
        event_name
    FROM filtered_events
),
windowed AS (
    SELECT session_id, step_idx, event_name
    FROM indexed_events
    WHERE step_idx <= %d
),
consecutive AS (
    SELECT
        session_id,
        step_idx,
        event_name AS source_name,
        LEAD(event_name) OVER (PARTITION BY session_id ORDER BY step_idx) AS target_name
    FROM windowed
)
SELECT
    step_idx,
    source_name,
    COALESCE(target_name, 'Exit') AS target_name,
    COUNT(*) AS cnt
FROM consecutive
WHERE step_idx < %d
GROUP BY step_idx, source_name, target_name
HAVING COUNT(*) > 0
ORDER BY step_idx, cnt DESC
`, excludeSQL, opt.StepLimit, opt.StepLimit)
	} else if opt.Direction == "forward" {
		query = fmt.Sprintf(`
WITH raw_events AS (
    SELECT
        session_id,
        ts,
        CASE
            WHEN type = 'event' THEN name
            WHEN type = 'breadcrumb' AND name = 'navigation' THEN
                'route:' || COALESCE(NULLIF(body_json::jsonb->'data'->>'to', ''), NULLIF(body_json::jsonb->>'message', ''), 'route')
            WHEN type = 'error' THEN
                'error:' || COALESCE(NULLIF(body_json::jsonb->>'type', ''), 'Exception')
            ELSE ''
        END AS event_name
    FROM items
    WHERE project_id = $1
      AND ts >= $2
      AND type IN ('event', 'breadcrumb', 'error')
),
filtered_events AS (
    SELECT session_id, ts, event_name
    FROM raw_events
    WHERE event_name != '' %s
),
root_occurrences AS (
    SELECT session_id, MIN(ts) AS root_ts
    FROM filtered_events
    WHERE event_name = %s
    GROUP BY session_id
),
anchored_events AS (
    SELECT
        e.session_id,
        e.ts,
        e.event_name,
        (ROW_NUMBER() OVER (PARTITION BY e.session_id ORDER BY e.ts) - 1) AS step_idx
    FROM filtered_events e
    JOIN root_occurrences r ON r.session_id = e.session_id AND e.ts >= r.root_ts
),
windowed AS (
    SELECT session_id, step_idx, event_name
    FROM anchored_events
    WHERE step_idx <= %d
),
consecutive AS (
    SELECT
        session_id,
        step_idx,
        event_name AS source_name,
        LEAD(event_name) OVER (PARTITION BY session_id ORDER BY step_idx) AS target_name
    FROM windowed
)
SELECT
    step_idx,
    source_name,
    COALESCE(target_name, 'Exit') AS target_name,
    COUNT(*) AS cnt
FROM consecutive
WHERE step_idx < %d
GROUP BY step_idx, source_name, target_name
HAVING COUNT(*) > 0
ORDER BY step_idx, cnt DESC
`, excludeSQL, rootEventPlaceholder, opt.StepLimit, opt.StepLimit)
	} else {
		// Reverse direction
		query = fmt.Sprintf(`
WITH raw_events AS (
    SELECT
        session_id,
        ts,
        CASE
            WHEN type = 'event' THEN name
            WHEN type = 'breadcrumb' AND name = 'navigation' THEN
                'route:' || COALESCE(NULLIF(body_json::jsonb->'data'->>'to', ''), NULLIF(body_json::jsonb->>'message', ''), 'route')
            WHEN type = 'error' THEN
                'error:' || COALESCE(NULLIF(body_json::jsonb->>'type', ''), 'Exception')
            ELSE ''
        END AS event_name
    FROM items
    WHERE project_id = $1
      AND ts >= $2
      AND type IN ('event', 'breadcrumb', 'error')
),
filtered_events AS (
    SELECT session_id, ts, event_name
    FROM raw_events
    WHERE event_name != '' %s
),
root_occurrences AS (
    SELECT session_id, MAX(ts) AS root_ts
    FROM filtered_events
    WHERE event_name = %s
    GROUP BY session_id
),
anchored_events AS (
    SELECT
        e.session_id,
        e.ts,
        e.event_name,
        (ROW_NUMBER() OVER (PARTITION BY e.session_id ORDER BY e.ts DESC) - 1) AS rev_idx
    FROM filtered_events e
    JOIN root_occurrences r ON r.session_id = e.session_id AND e.ts <= r.root_ts
),
windowed AS (
    SELECT
        session_id,
        (%d - rev_idx) AS step_idx,
        event_name
    FROM anchored_events
    WHERE rev_idx <= %d
),
consecutive AS (
    SELECT
        session_id,
        step_idx,
        event_name AS source_name,
        LEAD(event_name) OVER (PARTITION BY session_id ORDER BY step_idx) AS target_name
    FROM windowed
)
SELECT
    step_idx,
    source_name,
    COALESCE(target_name, %s) AS target_name,
    COUNT(*) AS cnt
FROM consecutive
WHERE step_idx < %d AND target_name IS NOT NULL
GROUP BY step_idx, source_name, target_name
HAVING COUNT(*) > 0
ORDER BY step_idx, cnt DESC
`, excludeSQL, rootEventPlaceholder, opt.StepLimit, opt.StepLimit, rootEventPlaceholder, opt.StepLimit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("calculate paths query: %w", err)
	}
	defer rows.Close()

	nodeMap := make(map[string]*PathNode)
	var links []PathLink
	var totalRootCount int64

	for rows.Next() {
		var stepIdx int
		var sourceName, targetName string
		var cnt int64

		if err := rows.Scan(&stepIdx, &sourceName, &targetName, &cnt); err != nil {
			return nil, err
		}

		if stepIdx == 0 {
			totalRootCount += cnt
		}

		sourceID := fmt.Sprintf("%d:%s", stepIdx, sourceName)
		targetID := fmt.Sprintf("%d:%s", stepIdx+1, targetName)

		if _, ok := nodeMap[sourceID]; !ok {
			nodeMap[sourceID] = &PathNode{
				ID:    sourceID,
				Name:  sourceName,
				Step:  stepIdx,
				Count: 0,
			}
		}
		nodeMap[sourceID].Count += cnt

		if _, ok := nodeMap[targetID]; !ok {
			nodeMap[targetID] = &PathNode{
				ID:    targetID,
				Name:  targetName,
				Step:  stepIdx + 1,
				Count: 0,
			}
		}
		nodeMap[targetID].Count += cnt

		links = append(links, PathLink{
			Source: sourceID,
			Target: targetID,
			Count:  cnt,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Filter out tiny links if threshold is specified and totalRootCount > 0
	finalLinks := make([]PathLink, 0, len(links))
	activeNodes := make(map[string]bool)

	minCount := int64(float64(totalRootCount) * (opt.MinThresholdPercent / 100.0))
	if minCount < 1 {
		minCount = 1
	}

	for _, l := range links {
		if l.Count >= minCount || l.Target == fmt.Sprintf("%s:Exit", strings.Split(l.Target, ":")[0]) {
			finalLinks = append(finalLinks, l)
			activeNodes[l.Source] = true
			activeNodes[l.Target] = true
		}
	}

	nodes := make([]PathNode, 0, len(activeNodes))
	for id := range activeNodes {
		if node, ok := nodeMap[id]; ok {
			nodes = append(nodes, *node)
		}
	}

	// Sort nodes deterministically: step asc, count desc, name asc
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Step != nodes[j].Step {
			return nodes[i].Step < nodes[j].Step
		}
		if nodes[i].Count != nodes[j].Count {
			return nodes[i].Count > nodes[j].Count
		}
		return nodes[i].Name < nodes[j].Name
	})

	sort.Slice(finalLinks, func(i, j int) bool {
		if finalLinks[i].Count != finalLinks[j].Count {
			return finalLinks[i].Count > finalLinks[j].Count
		}
		return finalLinks[i].Source < finalLinks[j].Source
	})

	return &PathResult{
		RootEvent: opt.RootEvent,
		Direction: opt.Direction,
		StepLimit: opt.StepLimit,
		Nodes:     nodes,
		Links:     finalLinks,
	}, nil
}

func (s *Store) GetPathSessions(ctx context.Context, projectID int64, sourceNode, targetNode string, days int, limit int) ([]string, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	since := time.Now().AddDate(0, 0, -days)

	// Parse source and target node
	srcParts := strings.SplitN(sourceNode, ":", 2)
	tgtParts := strings.SplitN(targetNode, ":", 2)
	if len(srcParts) < 2 || len(tgtParts) < 2 {
		return nil, fmt.Errorf("invalid source or target node format")
	}
	var srcStep, tgtStep int
	if _, err := fmt.Sscanf(srcParts[0], "%d", &srcStep); err != nil {
		return nil, fmt.Errorf("invalid source step: %w", err)
	}
	if _, err := fmt.Sscanf(tgtParts[0], "%d", &tgtStep); err != nil {
		return nil, fmt.Errorf("invalid target step: %w", err)
	}
	srcName := srcParts[1]
	tgtName := tgtParts[1]

	query := `
WITH raw_events AS (
    SELECT
        session_id,
        ts,
        CASE
            WHEN type = 'event' THEN name
            WHEN type = 'breadcrumb' AND name = 'navigation' THEN
                'route:' || COALESCE(NULLIF(body_json::jsonb->'data'->>'to', ''), NULLIF(body_json::jsonb->>'message', ''), 'route')
            WHEN type = 'error' THEN
                'error:' || COALESCE(NULLIF(body_json::jsonb->>'type', ''), 'Exception')
            ELSE ''
        END AS event_name
    FROM items
    WHERE project_id = $1
      AND ts >= $2
      AND type IN ('event', 'breadcrumb', 'error')
),
filtered_events AS (
    SELECT session_id, ts, event_name
    FROM raw_events
    WHERE event_name != ''
),
indexed_events AS (
    SELECT
        session_id,
        (ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY ts) - 1) AS step_idx,
        event_name
    FROM filtered_events
),
consecutive AS (
    SELECT
        session_id,
        step_idx,
        event_name AS source_name,
        LEAD(event_name) OVER (PARTITION BY session_id ORDER BY step_idx) AS target_name
    FROM indexed_events
)
SELECT DISTINCT session_id
FROM consecutive
WHERE step_idx = $3
  AND source_name = $4
  AND ($5 = 'Exit' OR target_name = $5)
LIMIT $6
`

	rows, err := s.db.QueryContext(ctx, query, projectID, since, srcStep, srcName, tgtName, limit)
	if err != nil {
		return nil, fmt.Errorf("query path sessions: %w", err)
	}
	defer rows.Close()

	var sessions []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		sessions = append(sessions, sid)
	}

	return sessions, rows.Err()
}
