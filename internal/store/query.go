// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"sightpane/internal/blob"
)

// --- Read queries ---

type Session struct {
	ID         string          `json:"id"`
	ProjectID  int64           `json:"project_id"`
	StartedAt  string          `json:"started_at"`
	LastSeenAt string          `json:"last_seen_at"`
	EndedAt    *string         `json:"ended_at"`
	UserID     string          `json:"user_id"`
	User       json.RawMessage `json:"user"`
	Device     json.RawMessage `json:"device"`
	Props      json.RawMessage `json:"props"`
	Platform   string          `json:"platform"`
	Release    string          `json:"release"`
	ErrorCount int             `json:"error_count"`
	EventCount int             `json:"event_count"`
	FrameCount int             `json:"frame_count"`
	IP         string          `json:"ip"`
	Browser    string          `json:"browser"`
	VisitorKey string          `json:"visitor_key"`
	Route      string          `json:"current_route"`
}

type SessionFilter struct {
	ProjectID  int64
	UserID     string
	OnlyErrors bool
	Limit      int
	Query      string
	Cursor     string
}

const sessionCols = `id, project_id, started_at, last_seen_at, ended_at, user_id, user_json, device_json, props_json, platform, release, error_count, event_count, frame_count, ip, browser, visitor_key, current_route`

func scanSession(sc interface{ Scan(...any) error }) (*Session, error) {
	var s Session
	var user, device, props string
	if err := sc.Scan(&s.ID, &s.ProjectID, tsCol{&s.StartedAt}, tsCol{&s.LastSeenAt}, nullTSCol{&s.EndedAt}, &s.UserID, &user, &device, &props, &s.Platform, &s.Release, &s.ErrorCount, &s.EventCount, &s.FrameCount, &s.IP, &s.Browser, &s.VisitorKey, &s.Route); err != nil {
		return nil, err
	}
	s.User, s.Device, s.Props = json.RawMessage(user), json.RawMessage(device), json.RawMessage(props)
	return &s, nil
}

func (s *Store) ListSessions(f SessionFilter) ([]Session, error) {
	// The one query assembled at runtime, so the placeholders are numbered from
	// the argument list rather than written into the text.
	q := `SELECT ` + sessionCols + ` FROM sessions WHERE project_id=$1`
	args := []any{f.ProjectID}
	next := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.UserID != "" {
		q += ` AND user_id=` + next(f.UserID)
	}
	if f.OnlyErrors {
		q += ` AND error_count>0`
	}
	if f.Cursor != "" {
		if t, err := time.Parse(time.RFC3339Nano, f.Cursor); err == nil {
			q += ` AND last_seen_at < ` + next(t)
		} else if t, err := time.Parse(time.RFC3339, f.Cursor); err == nil {
			q += ` AND last_seen_at < ` + next(t)
		}
	}
	if f.Query != "" {
		whereClause, err := BuildSessionSearchWhere(f.Query, next)
		if err != nil {
			return nil, err
		}
		q += whereClause
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q += ` ORDER BY last_seen_at DESC LIMIT ` + next(limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

type Item struct {
	ID      int64           `json:"id"`
	TS      string          `json:"ts"`
	Type    string          `json:"type"`
	Name    string          `json:"name"`
	Body    json.RawMessage `json:"body"`
	IssueID *int64          `json:"issue_id,omitempty"`
	Session string          `json:"session_id,omitempty"`
	// Symbolicated is `{"frames":[…]}` for an error from a release build whose
	// source map was uploaded, and absent otherwise. It sits beside Body rather
	// than inside it because Body is what the SDK sent, unaltered.
	Symbolicated json.RawMessage `json:"symbolicated,omitempty"`
}

type Frame struct {
	Seq    int             `json:"seq"`
	TS     string          `json:"ts"`
	Width  int             `json:"width"`
	Height int             `json:"height"`
	Taps   json.RawMessage `json:"taps"`
}

type SessionDetail struct {
	Session
	Items  []Item  `json:"items"`
	Frames []Frame `json:"frames"`
}

func (s *Store) GetSession(id string) (*SessionDetail, error) {
	sess, err := scanSession(s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d := &SessionDetail{Session: *sess, Items: []Item{}, Frames: []Frame{}}
	// No lower bound on ts here, unlike GetIssue: a bound would have to come from
	// the session's own timestamps, and an SDK with a skewed clock can report an
	// item outside them. Visiting one index per chunk is the price of never
	// dropping an item from the session it belongs to.
	rows, err := s.db.Query(`SELECT id, ts, type, name, body_json, issue_id, symbolicated_json FROM items WHERE session_id=$1 ORDER BY ts, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		var body string
		var symbolicated sql.NullString
		if err := rows.Scan(&it.ID, tsCol{&it.TS}, &it.Type, &it.Name, &body, &it.IssueID, &symbolicated); err != nil {
			return nil, err
		}
		it.Body = json.RawMessage(body)
		if symbolicated.Valid {
			it.Symbolicated = json.RawMessage(symbolicated.String)
		}
		d.Items = append(d.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	frows, err := s.db.Query(`SELECT seq, ts, width, height, taps_json FROM frames WHERE session_id=$1 ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var f Frame
		var taps string
		if err := frows.Scan(&f.Seq, tsCol{&f.TS}, &f.Width, &f.Height, &taps); err != nil {
			return nil, err
		}
		f.Taps = json.RawMessage(taps)
		d.Frames = append(d.Frames, f)
	}
	return d, frows.Err()
}

// FrameReader opens one replay frame, and reports ErrNotFound when the session
// has no such frame.
//
// The row is checked first and the object second: the database is the record of
// what exists, so a missing object is a storage fault worth surfacing, not a
// 404 to paper over.
func (s *Store) FrameReader(ctx context.Context, sessionID string, seq int) (io.ReadCloser, int64, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM frames WHERE session_id=$1 AND seq=$2`, sessionID, seq).Scan(&n); err != nil {
		return nil, 0, err
	}
	if n == 0 {
		return nil, 0, ErrNotFound
	}
	return s.blobs.Get(ctx, blob.FrameKey(sessionID, seq))
}

type Issue struct {
	ID                   int64      `json:"id"`
	ProjectID            int64      `json:"project_id"`
	Fingerprint          string     `json:"fingerprint"`
	Title                string     `json:"title"`
	Exception            string     `json:"exception"`
	FirstSeen            string     `json:"first_seen"`
	LastSeen             string     `json:"last_seen"`
	Count                int        `json:"count"`
	Resolved             bool       `json:"resolved"`
	Status               string     `json:"status"`
	AssigneeUserID       *int64     `json:"assignee_user_id,omitempty"`
	AssigneeEmail        string     `json:"assignee_email,omitempty"`
	SnoozeUntil          *time.Time `json:"snooze_until,omitempty"`
	SnoozeCountThreshold int        `json:"snooze_count_threshold,omitempty"`
	MergedInto           *int64     `json:"merged_into,omitempty"`
	FirstRelease         string     `json:"first_release,omitempty"`
	LastRelease          string     `json:"last_release,omitempty"`
	ResolvedInRelease    string     `json:"resolved_in_release,omitempty"`
}

const issueCols = `i.id, i.project_id, i.fingerprint, i.title, i.exception, i.first_seen, i.last_seen, i.count, i.resolved, i.status, i.assignee_user_id, u.email, i.snooze_until, i.snooze_count_threshold, i.merged_into, i.first_release, i.last_release, i.resolved_in_release`

func scanIssue(sc interface{ Scan(...any) error }) (*Issue, error) {
	var i Issue
	var assigneeEmail sql.NullString
	var snoozeUntil *time.Time
	if err := sc.Scan(&i.ID, &i.ProjectID, &i.Fingerprint, &i.Title, &i.Exception, tsCol{&i.FirstSeen}, tsCol{&i.LastSeen}, &i.Count, &i.Resolved, &i.Status, &i.AssigneeUserID, &assigneeEmail, &snoozeUntil, &i.SnoozeCountThreshold, &i.MergedInto, &i.FirstRelease, &i.LastRelease, &i.ResolvedInRelease); err != nil {
		return nil, err
	}
	if assigneeEmail.Valid {
		i.AssigneeEmail = assigneeEmail.String
	}
	i.SnoozeUntil = snoozeUntil
	if i.Status == "" {
		if i.Resolved {
			i.Status = "resolved"
		} else {
			i.Status = "open"
		}
	}
	i.Resolved = (i.Status == "resolved")
	return &i, nil
}


type IssueFilter struct {
	ProjectID       int64
	IncludeResolved bool
	Status          string
	Query           string
	Limit           int
}

func (s *Store) ListIssues(projectID int64, includeResolved bool) ([]Issue, error) {
	return s.ListIssuesWithFilter(IssueFilter{
		ProjectID:       projectID,
		IncludeResolved: includeResolved,
	})
}

func (s *Store) ListIssuesWithFilter(f IssueFilter) ([]Issue, error) {
	q := `SELECT ` + issueCols + ` FROM issues i LEFT JOIN users u ON u.id = i.assignee_user_id WHERE i.project_id=$1`
	args := []any{f.ProjectID}
	next := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.Status != "" {
		q += ` AND i.status = ` + next(f.Status)
	} else if !f.IncludeResolved && !strings.Contains(f.Query, "status:") {
		q += ` AND i.status = 'open'`
	} else if f.IncludeResolved && !strings.Contains(f.Query, "status:") {
		q += ` AND i.status != 'ignored'`
	}
	if f.Query != "" {
		whereClause, err := BuildIssueSearchWhere(f.Query, next)
		if err != nil {
			return nil, err
		}
		q += whereClause
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	q += ` ORDER BY i.last_seen DESC LIMIT ` + next(limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Issue{}
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

type IssueDetail struct {
	Issue
	Occurrences []Item `json:"occurrences"`
}

func (s *Store) GetIssue(id int64) (*IssueDetail, error) {
	q := `SELECT ` + issueCols + ` FROM issues i LEFT JOIN users u ON u.id = i.assignee_user_id WHERE i.id=$1`
	row := s.db.QueryRow(q, id)
	i, err := scanIssue(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d := &IssueDetail{Issue: *i, Occurrences: []Item{}}
	// Include occurrences from this issue and any merged issues
	rows, err := s.db.Query(`SELECT id, session_id, ts, type, name, body_json, symbolicated_json FROM items WHERE (issue_id=$1 OR issue_id IN (SELECT id FROM issues WHERE merged_into=$1)) AND ts>=$2 ORDER BY ts DESC LIMIT 50`, id, asTime(i.FirstSeen))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		var body string
		var symbolicated sql.NullString
		if err := rows.Scan(&it.ID, &it.Session, tsCol{&it.TS}, &it.Type, &it.Name, &body, &symbolicated); err != nil {
			return nil, err
		}
		it.Body = json.RawMessage(body)
		if symbolicated.Valid {
			it.Symbolicated = json.RawMessage(symbolicated.String)
		}
		d.Occurrences = append(d.Occurrences, it)
	}
	return d, rows.Err()
}

func (s *Store) SetIssueResolved(id int64, resolved bool) error {
	return s.SetIssueResolvedWithRelease(id, resolved, "")
}

func (s *Store) SetIssueResolvedWithRelease(id int64, resolved bool, release string) error {
	status := "open"
	if resolved {
		status = "resolved"
	}
	var res sql.Result
	var err error
	if resolved {
		if release != "" {
			res, err = s.db.Exec(`UPDATE issues SET status=$1, resolved=$2, resolved_in_release=$3 WHERE id=$4`, status, resolved, release, id)
		} else {
			res, err = s.db.Exec(`UPDATE issues SET status=$1, resolved=$2, resolved_in_release=COALESCE(NULLIF(last_release, ''), resolved_in_release) WHERE id=$3`, status, resolved, id)
		}
	} else {
		res, err = s.db.Exec(`UPDATE issues SET status=$1, resolved=$2 WHERE id=$3`, status, resolved, id)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}


type EventCount struct {
	Name  string `json:"name"`
	Day   string `json:"day"`
	Count int    `json:"count"`
	Users int    `json:"users"`
}

// EventSummary counts each event name per day over the last [days] days, which
// is what the events page charts. Users is a distinct visitor count, so an event
// fired repeatedly by one visitor does not look like reach.
func (s *Store) EventSummary(projectID int64, days int) ([]EventCount, error) {
	if days <= 0 {
		days = 7
	}
	since := time.Now().UTC().AddDate(0, 0, -days)
	// This one reads the raw items rather than items_daily. The distinct visitor
	// count needs the session behind every event, which a continuous aggregate
	// cannot hold, so joining sessions is unavoidable and the aggregate would
	// only add a second pass over the same rows.
	rows, err := s.db.Query(`SELECT i.name, `+utcDay("i.ts")+` AS day, COUNT(*), COUNT(DISTINCT s.visitor_key)
		FROM items i JOIN sessions s ON s.id=i.session_id
		WHERE i.project_id=$1 AND i.type='event' AND i.ts>=$2 GROUP BY i.name, day ORDER BY day DESC, COUNT(*) DESC`, projectID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventCount{}
	for rows.Next() {
		var e EventCount
		if err := rows.Scan(&e.Name, &e.Day, &e.Count, &e.Users); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SessionProject returns the project a session belongs to. Session ids are
// global, so this is how a request for one gets a project to check membership
// against.
func (s *Store) SessionProject(sessionID string) (int64, error) {
	var pid int64
	if err := s.db.QueryRow(`SELECT project_id FROM sessions WHERE id=$1`, sessionID).Scan(&pid); err != nil {
		return 0, ErrNotFound
	}
	return pid, nil
}

// IssueProject returns the project an issue belongs to, for the same membership
// check as SessionProject: issue ids are global too.
func (s *Store) IssueProject(issueID int64) (int64, error) {
	var pid int64
	if err := s.db.QueryRow(`SELECT project_id FROM issues WHERE id=$1`, issueID).Scan(&pid); err != nil {
		return 0, ErrNotFound
	}
	return pid, nil
}

// VisitorKey digests user id, IP and browser into one short key. An anonymous
// visitor has no user id, so the other two still separate them from each other
// instead of collapsing everyone signed out into a single visitor.
func VisitorKey(userID, ip, browser string) string {
	sum := sha1.Sum([]byte(userID + "|" + ip + "|" + browser))
	return hex.EncodeToString(sum[:8])
}

type LiveViewer struct {
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	UserLabel string `json:"user_label"`
	IP        string `json:"ip"`
	Browser   string `json:"browser"`
	Platform  string `json:"platform"`
	Route     string `json:"route"`
	LastSeen  string `json:"last_seen"`
	StartedAt string `json:"started_at"`
}

type LiveStatus struct {
	Window   int          `json:"window_seconds"`
	Count    int          `json:"count"`
	Visitors int          `json:"visitors"`
	Routes   []NameCount  `json:"routes"`
	Viewers  []LiveViewer `json:"viewers"`
}

// Live lists the sessions that sent an envelope within the last [window] seconds
// and have not ended, plus the routes they are on. A session that stops sending
// simply falls out of the window, so a client that dies without a session_end
// does not stay on the live view forever.
func (s *Store) Live(projectID int64, window int) (*LiveStatus, error) {
	if window <= 0 {
		window = 60
	}
	since := time.Now().UTC().Add(-time.Duration(window) * time.Second)
	rows, err := s.db.Query(`SELECT id, user_id, user_json, ip, browser, platform, current_route, last_seen_at, started_at, visitor_key FROM sessions
		WHERE project_id=$1 AND last_seen_at>=$2 AND ended_at IS NULL ORDER BY last_seen_at DESC LIMIT 500`, projectID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	st := &LiveStatus{Window: window, Routes: []NameCount{}, Viewers: []LiveViewer{}}
	routes := map[string]int{}
	visitors := map[string]bool{}
	for rows.Next() {
		var v LiveViewer
		var userJSON, vk string
		if err := rows.Scan(&v.SessionID, &v.UserID, &userJSON, &v.IP, &v.Browser, &v.Platform, &v.Route, tsCol{&v.LastSeen}, tsCol{&v.StartedAt}, &vk); err != nil {
			return nil, err
		}
		var u struct {
			Email, Name string
		}
		_ = json.Unmarshal([]byte(userJSON), &u)
		v.UserLabel = u.Email
		if v.UserLabel == "" {
			v.UserLabel = u.Name
		}
		if v.UserLabel == "" {
			v.UserLabel = v.UserID
		}
		st.Viewers = append(st.Viewers, v)
		routes[v.Route]++
		visitors[vk] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	st.Count = len(st.Viewers)
	st.Visitors = len(visitors)
	for r, n := range routes {
		st.Routes = append(st.Routes, NameCount{Name: r, Count: n})
	}
	sort.Slice(st.Routes, func(i, j int) bool {
		if st.Routes[i].Count != st.Routes[j].Count {
			return st.Routes[i].Count > st.Routes[j].Count
		}
		return st.Routes[i].Name < st.Routes[j].Name
	})
	return st, nil
}

// BrowserLabel is what the dashboard shows next to a session: the browser name
// the SDK reported, or one derived from the user agent when it did not, or the
// operating system on platforms that are not the web. On the web with nothing to
// go on it stays empty rather than repeating the platform, which would read as
// "web · web".
func BrowserLabel(browser, ua, platform, os string) string {
	if browser != "" && browser != platform && browser != os {
		return browser
	}
	if ua != "" {
		return browserFromUA(ua)
	}
	if platform != "web" && os != "" && os != "web" {
		return os
	}
	return ""
}

func browserFromUA(ua string) string {
	switch {
	case strings.Contains(ua, "Edg/"):
		return "Edge"
	case strings.Contains(ua, "OPR/"), strings.Contains(ua, "Opera"):
		return "Opera"
	case strings.Contains(ua, "SamsungBrowser"):
		return "Samsung"
	case strings.Contains(ua, "Firefox/"):
		return "Firefox"
	case strings.Contains(ua, "Chrome/"), strings.Contains(ua, "CriOS/"):
		return "Chrome"
	case strings.Contains(ua, "Safari/"):
		return "Safari"
	}
	return "Browser"
}
