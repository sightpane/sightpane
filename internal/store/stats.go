// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"time"
)

// --- Statistics ---

type DayStat struct {
	Day      string `json:"day"`
	Sessions int    `json:"sessions"`
	Users    int    `json:"users"`
	Errors   int    `json:"errors"`
	Events   int    `json:"events"`
}

type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type ProjectStats struct {
	Days       int         `json:"days"`
	Sessions   int         `json:"sessions"`
	Users      int         `json:"users"`
	Errors     int         `json:"errors"`
	Events     int         `json:"events"`
	Frames     int         `json:"frames"`
	OpenIssues int         `json:"open_issues"`
	CrashFree  float64     `json:"crash_free"` // share of sessions with no error, 0–1
	Daily      []DayStat   `json:"daily"`
	Platforms  []NameCount `json:"platforms"`
	Releases   []NameCount `json:"releases"`
	TopIssues  []Issue     `json:"top_issues"`
	TopEvents  []NameCount `json:"top_events"`
}

// Stats summarises a project over the last [days] days. The daily series has a
// row for every day in the window, including the ones with nothing in them, so
// a chart in the dashboard can plot it without filling the gaps itself.
func (s *Store) Stats(projectID int64, days int) (*ProjectStats, error) {
	if days <= 0 || days > 90 {
		days = 14
	}
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour).Format(time.RFC3339Nano)
	st := &ProjectStats{Days: days, Platforms: []NameCount{}, Releases: []NameCount{}, TopIssues: []Issue{}, TopEvents: []NameCount{}}
	row := s.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT visitor_key), COALESCE(SUM(frame_count),0), COALESCE(SUM(CASE WHEN error_count=0 THEN 1 ELSE 0 END),0) FROM sessions WHERE project_id=? AND started_at>=?`, projectID, since)
	var crashFreeSessions int
	if err := row.Scan(&st.Sessions, &st.Users, &st.Frames, &crashFreeSessions); err != nil {
		return nil, err
	}
	if st.Sessions > 0 {
		st.CrashFree = float64(crashFreeSessions) / float64(st.Sessions)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM items WHERE project_id=? AND type='error' AND ts>=?`, projectID, since).Scan(&st.Errors); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM items WHERE project_id=? AND type='event' AND ts>=?`, projectID, since).Scan(&st.Events); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM issues WHERE project_id=? AND resolved=0`, projectID).Scan(&st.OpenIssues); err != nil {
		return nil, err
	}
	// Per-day series
	byDay := map[string]*DayStat{}
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -(days - 1 - i)).Format("2006-01-02")
		ds := &DayStat{Day: d}
		byDay[d] = ds
		st.Daily = append(st.Daily, *ds)
	}
	fill := func(q string, set func(ds *DayStat, n int)) error {
		rows, err := s.db.Query(q, projectID, since)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var day string
			var n int
			if err := rows.Scan(&day, &n); err != nil {
				return err
			}
			for i := range st.Daily {
				if st.Daily[i].Day == day {
					set(&st.Daily[i], n)
				}
			}
		}
		return rows.Err()
	}
	if err := fill(`SELECT substr(started_at,1,10), COUNT(*) FROM sessions WHERE project_id=? AND started_at>=? GROUP BY 1`, func(d *DayStat, n int) { d.Sessions = n }); err != nil {
		return nil, err
	}
	if err := fill(`SELECT substr(started_at,1,10), COUNT(DISTINCT visitor_key) FROM sessions WHERE project_id=? AND started_at>=? GROUP BY 1`, func(d *DayStat, n int) { d.Users = n }); err != nil {
		return nil, err
	}
	if err := fill(`SELECT substr(ts,1,10), COUNT(*) FROM items WHERE project_id=? AND type='error' AND ts>=? GROUP BY 1`, func(d *DayStat, n int) { d.Errors = n }); err != nil {
		return nil, err
	}
	if err := fill(`SELECT substr(ts,1,10), COUNT(*) FROM items WHERE project_id=? AND type='event' AND ts>=? GROUP BY 1`, func(d *DayStat, n int) { d.Events = n }); err != nil {
		return nil, err
	}
	nameCounts := func(q string) ([]NameCount, error) {
		rows, err := s.db.Query(q, projectID, since)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []NameCount{}
		for rows.Next() {
			var nc NameCount
			if err := rows.Scan(&nc.Name, &nc.Count); err != nil {
				return nil, err
			}
			out = append(out, nc)
		}
		return out, rows.Err()
	}
	var err error
	if st.Platforms, err = nameCounts(`SELECT COALESCE(NULLIF(platform,''),'?'), COUNT(*) FROM sessions WHERE project_id=? AND started_at>=? GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return nil, err
	}
	if st.Releases, err = nameCounts(`SELECT COALESCE(NULLIF(release,''),'?'), COUNT(*) FROM sessions WHERE project_id=? AND started_at>=? GROUP BY 1 ORDER BY 2 DESC LIMIT 8`); err != nil {
		return nil, err
	}
	if st.TopEvents, err = nameCounts(`SELECT name, COUNT(*) FROM items WHERE project_id=? AND type='event' AND ts>=? GROUP BY 1 ORDER BY 2 DESC LIMIT 8`); err != nil {
		return nil, err
	}
	issues, err := s.ListIssues(projectID, false)
	if err != nil {
		return nil, err
	}
	sortIssuesByCount(issues)
	if len(issues) > 5 {
		issues = issues[:5]
	}
	st.TopIssues = issues
	return st, nil
}

func sortIssuesByCount(is []Issue) {
	for i := 1; i < len(is); i++ {
		for j := i; j > 0 && is[j].Count > is[j-1].Count; j-- {
			is[j], is[j-1] = is[j-1], is[j]
		}
	}
}
