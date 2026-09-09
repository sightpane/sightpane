package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
	"sightpane/internal/store"
	"sightpane/internal/testdb"
)

const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

// userTok is the token every read endpoint below is sent with. A test that has
// to be anonymous or somebody else swaps it and puts the old value back, which
// keeps the helpers free of an auth argument nearly every call would repeat.
var userTok string

func newTestServer(t *testing.T) (*fiber.App, *store.Store) {
	t.Helper()
	st := testStore(t)
	u, err := st.CreateUser("owner@x.io", "Owner", "secret1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProject("test", "flutter", "key1", &u.ID); err != nil {
		t.Fatal(err)
	}
	userTok, _ = st.IssueToken(u.ID)
	return New(st, "", nil), st
}

// testStore opens the database one test runs against: a schema of its own inside
// the TimescaleDB that testdb started for this binary.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(store.Options{DSN: testdb.DSN(t), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// resp exposes the few things the tests need from a response, with the same
// shape httptest.ResponseRecorder had, so the assertions below did not change
// when the server moved to Fiber.
type resp struct {
	Code   int
	Body   *bytes.Buffer
	header http.Header
}

func (r resp) Header() http.Header { return r.header }

// send runs a request through the app in memory. The 10s timeout is for the
// slow paths (PBKDF2 on register/login) under -race.
func send(t *testing.T, app *fiber.App, req *http.Request) resp {
	t.Helper()
	r, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{Code: r.StatusCode, Body: bytes.NewBuffer(b), header: r.Header}
}

func do(t *testing.T, app *fiber.App, method, path, key string, body any) resp {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	// Fiber's in-memory test connection always reports 0.0.0.0, so the client
	// address has to arrive in a header here. The connection-address fallback
	// is covered over a real socket in TestProxyProtocolListener.
	req.Header.Set("X-Real-IP", "192.0.2.1")
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Hog-Key", key)
	}
	if userTok != "" {
		req.Header.Set("Authorization", "Bearer "+userTok)
	}
	return send(t, app, req)
}

func post(t *testing.T, app *fiber.App, path, key string, body any) resp {
	return do(t, app, "POST", path, key, body)
}

func get(t *testing.T, app *fiber.App, path string, out any) resp {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if userTok != "" {
		req.Header.Set("Authorization", "Bearer "+userTok)
	}
	rr := send(t, app, req)
	if out != nil && rr.Code == 200 {
		if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, rr.Body.String())
		}
	}
	return rr
}

func envelope(session string, items ...map[string]any) map[string]any {
	return map[string]any{
		"sdk":     map[string]any{"name": "sightpane", "version": "0.1.0"},
		"session": map[string]any{"id": session, "started_at": time.Now().UTC().Format(time.RFC3339Nano), "user": map[string]any{"id": "u1", "email": "a@b.c"}, "device": map[string]any{"platform": "linux", "release": "1.0", "browser": "Chrome"}, "props": map[string]any{"location": "NOVO"}},
		"items":   items,
	}
}

func TestIngestAndQuery(t *testing.T) {
	h, _ := newTestServer(t)
	stack := "#0 CashierPage.settle (package:casinocrm2/features/cashier/cashier_page.dart:120:5)\n#1 _InkResponseState.handleTap (package:flutter/src/material/ink_well.dart:1176:21)\n#2 main (package:casinocrm2/main.dart:10:3)"
	rr := post(t, h, "/api/v1/envelope", "key1",
		envelope("s1",
			map[string]any{"type": "breadcrumb", "ts": "2026-09-07T10:00:01Z", "category": "navigation", "message": "push /cashier"},
			map[string]any{"type": "event", "ts": "2026-09-07T10:00:02Z", "name": "deposit", "props": map[string]any{"amount": 50}},
			map[string]any{"type": "frame", "ts": "2026-09-07T10:00:03Z", "seq": 1, "width": 1, "height": 1, "png": tinyPNG, "taps": []map[string]any{{"x": 0.5, "y": 0.5, "ts": "2026-09-07T10:00:03Z"}}},
			map[string]any{"type": "error", "ts": "2026-09-07T10:00:04Z", "message": "Bad state: boom", "exception": "StateError", "stack": stack, "frame_seq": 1},
			map[string]any{"type": "pointer", "ts": "2026-09-07T10:00:03Z", "events": []map[string]any{{"t": 0, "x": 0.1, "y": 0.2, "k": "move"}, {"t": 120, "x": 0.5, "y": 0.5, "k": "down"}}},
			map[string]any{"type": "bogus"},
		))
	if rr.Code != 202 {
		t.Fatalf("ingest: %d %s", rr.Code, rr.Body.String())
	}
	var res store.IngestResult
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	if res.Accepted != 5 || res.Rejected != 1 {
		t.Fatalf("accepted/rejected = %d/%d", res.Accepted, res.Rejected)
	}

	// The same error again in a second session, reported from a different line:
	// the fingerprint ignores line numbers, so it must join the same issue.
	stack2 := strings.ReplaceAll(stack, "120:5", "131:9")
	post(t, h, "/api/v1/envelope", "key1", envelope("s2", map[string]any{"type": "error", "ts": "2026-09-07T11:00:00Z", "message": "Bad state: boom", "exception": "StateError", "stack": stack2}, map[string]any{"type": "session_end", "ts": "2026-09-07T11:00:05Z"}))

	var sessions []store.Session
	get(t, h, "/api/v1/projects/1/sessions", &sessions)
	if len(sessions) != 2 || sessions[0].ID != "s2" {
		t.Fatalf("sessions: %+v", sessions)
	}
	if sessions[1].IP != "192.0.2.1" {
		t.Fatalf("ip not recorded: %q", sessions[1].IP)
	}
	if sessions[1].ErrorCount != 1 || sessions[1].EventCount != 1 || sessions[1].FrameCount != 1 || sessions[1].UserID != "u1" || sessions[1].Platform != "linux" {
		t.Fatalf("s1 counters: %+v", sessions[1])
	}
	if sessions[0].EndedAt == nil {
		t.Fatalf("s2 should be ended")
	}
	var onlyErr []store.Session
	get(t, h, "/api/v1/projects/1/sessions?errors=1&user=u1", &onlyErr)
	if len(onlyErr) != 2 {
		t.Fatalf("errors filter: %d", len(onlyErr))
	}

	var d store.SessionDetail
	get(t, h, "/api/v1/sessions/s1", &d)
	if len(d.Items) != 4 || len(d.Frames) != 1 || d.Items[0].Type != "breadcrumb" || d.Items[3].Type != "error" || d.Items[3].IssueID == nil {
		t.Fatalf("detail: %+v", d)
	}
	if d.Items[2].Type != "pointer" || !strings.Contains(string(d.Items[2].Body), `"k":"down"`) {
		t.Fatalf("pointer item: %+v", d.Items[2])
	}
	if string(d.Frames[0].Taps) == "[]" {
		t.Fatalf("taps lost")
	}
	fr := get(t, h, "/api/v1/sessions/s1/frames/1.png", nil)
	want, _ := base64.StdEncoding.DecodeString(tinyPNG)
	if fr.Code != 200 || fr.Header().Get("Content-Type") != "image/png" || !bytes.Equal(fr.Body.Bytes(), want) {
		t.Fatalf("frame: %d %s", fr.Code, fr.Header().Get("Content-Type"))
	}
	if get(t, h, "/api/v1/sessions/s1/frames/9.png", nil).Code != 404 {
		t.Fatalf("missing frame should 404")
	}

	var issues []store.Issue
	get(t, h, "/api/v1/projects/1/issues", &issues)
	if len(issues) != 1 || issues[0].Count != 2 || issues[0].Title != "StateError: Bad state: boom" {
		t.Fatalf("issues: %+v", issues)
	}
	var issue store.IssueDetail
	get(t, h, "/api/v1/issues/1", &issue)
	if len(issue.Occurrences) != 2 || issue.Occurrences[0].Session != "s2" {
		t.Fatalf("issue detail: %+v", issue)
	}
	if post(t, h, "/api/v1/issues/1/resolve", "", nil).Code != 200 {
		t.Fatal("resolve")
	}
	get(t, h, "/api/v1/projects/1/issues", &issues)
	if len(issues) != 0 {
		t.Fatalf("resolved issue still listed")
	}
	// A resolved issue reopens by itself the next time it is seen — a fix that
	// did not hold should come back on the list without anyone reopening it.
	post(t, h, "/api/v1/envelope", "key1", envelope("s3", map[string]any{"type": "error", "message": "Bad state: boom", "exception": "StateError", "stack": stack}))
	get(t, h, "/api/v1/projects/1/issues", &issues)
	if len(issues) != 1 || issues[0].Count != 3 {
		t.Fatalf("regressed issue: %+v", issues)
	}

	var ev []store.EventCount
	get(t, h, "/api/v1/projects/1/events/summary?days=36500", &ev)
	if len(ev) != 1 || ev[0].Name != "deposit" || ev[0].Count != 1 || ev[0].Users != 1 {
		t.Fatalf("events: %+v", ev)
	}
}

func TestAuth(t *testing.T) {
	h, _ := newTestServer(t)
	if rr := post(t, h, "/api/v1/envelope", "", envelope("s")); rr.Code != 401 {
		t.Fatalf("no key: %d", rr.Code)
	}
	if rr := post(t, h, "/api/v1/envelope", "nope", envelope("s")); rr.Code != 401 {
		t.Fatalf("bad key: %d", rr.Code)
	}
	if rr := post(t, h, "/api/v1/envelope", "key1", map[string]any{"items": []any{}}); rr.Code != 400 {
		t.Fatalf("missing session: %d", rr.Code)
	}
	// The read endpoints want a user token, not the project API key.
	saved := userTok
	userTok = ""
	if rr := get(t, h, "/api/v1/projects", nil); rr.Code != 401 {
		t.Fatalf("no token: %d", rr.Code)
	}
	userTok = "bogus"
	if rr := get(t, h, "/api/v1/projects", nil); rr.Code != 401 {
		t.Fatalf("bad token: %d", rr.Code)
	}
	userTok = saved
	// CORS preflight, exactly as a browser sends it. The dashboard runs on
	// another origin during development, so this has to answer 204 with the
	// allow headers; a request without an Origin is not a CORS request at all
	// and deliberately gets no CORS headers.
	req := httptest.NewRequest("OPTIONS", "/api/v1/envelope", nil)
	req.Header.Set("Origin", "http://localhost:5000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "x-hog-key")
	rr := send(t, h, req)
	if rr.Code != 204 || rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("cors preflight: %d %q", rr.Code, rr.Header().Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(strings.ToLower(rr.Header().Get("Access-Control-Allow-Headers")), "x-hog-key") {
		t.Fatalf("cors headers: %q", rr.Header().Get("Access-Control-Allow-Headers"))
	}
}

func TestUsersProjectsMembersStats(t *testing.T) {
	h, _ := newTestServer(t)
	// Registering, then logging in.
	if rr := post(t, h, "/api/v1/auth/register", "", map[string]any{"email": "ayse@x.io", "name": "Ayşe", "password": "123456"}); rr.Code != 201 {
		t.Fatalf("register: %d %s", rr.Code, rr.Body.String())
	}
	if rr := post(t, h, "/api/v1/auth/register", "", map[string]any{"email": "ayse@x.io", "password": "123456"}); rr.Code != 409 {
		t.Fatalf("dup register: %d", rr.Code)
	}
	if rr := post(t, h, "/api/v1/auth/register", "", map[string]any{"email": "kisa@x.io", "password": "123"}); rr.Code != 400 {
		t.Fatalf("short password: %d", rr.Code)
	}
	if rr := post(t, h, "/api/v1/auth/login", "", map[string]any{"email": "AYSE@x.io", "password": "wrong"}); rr.Code != 401 {
		t.Fatalf("wrong password: %d", rr.Code)
	}
	rr := post(t, h, "/api/v1/auth/login", "", map[string]any{"email": "AYSE@x.io", "password": "123456"})
	if rr.Code != 200 {
		t.Fatalf("login: %d %s", rr.Code, rr.Body.String())
	}
	var ar authResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &ar)
	ownerTok := userTok
	userTok = ar.Token
	var me store.User
	get(t, h, "/api/v1/auth/me", &me)
	if me.Email != "ayse@x.io" || me.Name != "Ayşe" {
		t.Fatalf("me: %+v", me)
	}
	// Ayşe is a member of nothing, so the project the fixture created is invisible
	// to her — and asking for it directly is a 404, not a 403.
	var ps []store.Project
	get(t, h, "/api/v1/projects", &ps)
	if len(ps) != 0 {
		t.Fatalf("ayse should have no projects: %+v", ps)
	}
	if rr := get(t, h, "/api/v1/projects/1", nil); rr.Code != 404 {
		t.Fatalf("foreign project: %d", rr.Code)
	}
	// She creates her own project: she becomes its owner and a key is generated.
	rr = post(t, h, "/api/v1/projects", "", map[string]any{"name": "Kasa App", "platform": "flutter"})
	if rr.Code != 201 {
		t.Fatalf("create project: %d %s", rr.Code, rr.Body.String())
	}
	var p store.Project
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.Role != "owner" || len(p.APIKey) != 32 || p.ID != 2 {
		t.Fatalf("project: %+v", p)
	}
	// An envelope sent with that key has to reach the project's statistics.
	if rr := post(t, h, "/api/v1/envelope", p.APIKey, envelope("k1", map[string]any{"type": "event", "name": "open"}, map[string]any{"type": "error", "message": "x", "exception": "E"})); rr.Code != 202 {
		t.Fatalf("envelope with new key: %d", rr.Code)
	}
	var st store.ProjectStats
	get(t, h, "/api/v1/projects/2/stats?days=7", &st)
	if st.Sessions != 1 || st.Errors != 1 || st.Events != 1 || st.Users != 1 || len(st.Daily) != 7 || st.OpenIssues != 1 || st.CrashFree != 0 || st.Platforms[0].Name != "linux" || st.TopEvents[0].Name != "open" || len(st.TopIssues) != 1 {
		t.Fatalf("stats: %+v", st)
	}
	if st.Daily[6].Sessions != 1 {
		t.Fatalf("today's bucket should hold the session: %+v", st.Daily)
	}
	get(t, h, "/api/v1/projects", &ps)
	if len(ps) != 1 || ps[0].Sessions24h != 1 || ps[0].Errors24h != 1 || ps[0].OpenIssues != 1 {
		t.Fatalf("project counters: %+v", ps)
	}
	// Rotating the key: the old one stops being accepted straight away.
	rr = post(t, h, "/api/v1/projects/2/rotate-key", "", nil)
	var rk map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &rk)
	if rr.Code != 200 || rk["api_key"] == p.APIKey {
		t.Fatalf("rotate: %d %v", rr.Code, rk)
	}
	if rr := post(t, h, "/api/v1/envelope", p.APIKey, envelope("k2")); rr.Code != 401 {
		t.Fatalf("old key must fail: %d", rr.Code)
	}
	// Adding a member: owner@x.io can now see the project, but deleting it stays
	// an owner's privilege.
	if rr := post(t, h, "/api/v1/projects/2/members", "", map[string]any{"email": "owner@x.io"}); rr.Code != 200 {
		t.Fatalf("add member: %d %s", rr.Code, rr.Body.String())
	}
	if rr := post(t, h, "/api/v1/projects/2/members", "", map[string]any{"email": "yok@x.io"}); rr.Code != 404 {
		t.Fatalf("unknown member: %d", rr.Code)
	}
	userTok = ownerTok
	get(t, h, "/api/v1/projects", &ps)
	if len(ps) != 2 {
		t.Fatalf("owner should now see 2 projects: %+v", ps)
	}
	if rr := do(t, h, "DELETE", "/api/v1/projects/2", "", nil); rr.Code != 403 {
		t.Fatalf("member delete: %d", rr.Code)
	}
	if rr := get(t, h, "/api/v1/sessions/k1", nil); rr.Code != 200 {
		t.Fatalf("member reads session: %d", rr.Code)
	}
	// Logging out has to invalidate the token it was made with.
	post(t, h, "/api/v1/auth/logout", "", nil)
	if rr := get(t, h, "/api/v1/projects", nil); rr.Code != 401 {
		t.Fatalf("after logout: %d", rr.Code)
	}
	// The owner deletes the project, and its sessions go with it.
	userTok = ar.Token
	if rr := do(t, h, "DELETE", "/api/v1/projects/2", "", nil); rr.Code != 200 {
		t.Fatalf("owner delete: %d %s", rr.Code, rr.Body.String())
	}
	if rr := get(t, h, "/api/v1/sessions/k1", nil); rr.Code != 404 {
		t.Fatalf("session should be gone: %d", rr.Code)
	}
}

// clientIP resolves the address a session is recorded under. The order is
// deliberate — a CDN header first because Cloudflare rewrites the others, then
// the standard Forwarded header, then X-Forwarded-For whose first entry is the
// client, then X-Real-IP, and the connection itself last.
func TestClientIP(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error { return c.SendString(clientIP(c)) })
	ask := func(t *testing.T, headers map[string]string) string {
		t.Helper()
		req := httptest.NewRequest("GET", "/", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return send(t, app, req).Body.String()
	}

	for _, c := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"no headers falls back to the connection", nil, "0.0.0.0"},
		{"x-real-ip", map[string]string{"X-Real-IP": "203.0.113.9"}, "203.0.113.9"},
		{"x-forwarded-for wins over x-real-ip, first entry is the client",
			map[string]string{"X-Real-IP": "203.0.113.9", "X-Forwarded-For": "198.51.100.7, 10.0.0.1"},
			"198.51.100.7"},
		{"rfc 7239 Forwarded wins over x-forwarded-for, port stripped",
			map[string]string{"X-Forwarded-For": "198.51.100.7", "Forwarded": `for="198.51.100.3:4711";proto=https, for=10.0.0.2`},
			"198.51.100.3"},
		{"cloudflare wins over everything",
			map[string]string{"Forwarded": "for=198.51.100.3", "CF-Connecting-IP": "192.0.2.77"},
			"192.0.2.77"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ask(t, c.headers); got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestVisitorsAndLive(t *testing.T) {
	h, _ := newTestServer(t)
	post(t, h, "/api/v1/envelope", "key1", envelope("v1", map[string]any{"type": "heartbeat", "route": "/cashier"}))
	// The same user id from another browser is a separate visitor.
	e2 := envelope("v2", map[string]any{"type": "breadcrumb", "category": "navigation", "message": "/tables"})
	e2["session"].(map[string]any)["device"] = map[string]any{"platform": "web", "browser": "Firefox"}
	post(t, h, "/api/v1/envelope", "key1", e2)
	// Same user, same browser, same IP: one visitor, even in a new session.
	post(t, h, "/api/v1/envelope", "key1", envelope("v3", map[string]any{"type": "heartbeat", "route": "/cashier"}))
	// A session that has ended is not live any more.
	post(t, h, "/api/v1/envelope", "key1", envelope("v4", map[string]any{"type": "session_end"}))

	var st store.ProjectStats
	get(t, h, "/api/v1/projects/1/stats?days=7", &st)
	if st.Sessions != 4 || st.Users != 2 {
		t.Fatalf("sessions/visitors = %d/%d, want 4/2", st.Sessions, st.Users)
	}
	var live store.LiveStatus
	get(t, h, "/api/v1/projects/1/live?window=60", &live)
	if live.Count != 3 || live.Visitors != 2 {
		t.Fatalf("live: %+v", live)
	}
	if len(live.Routes) != 2 || live.Routes[0].Name != "/cashier" || live.Routes[0].Count != 2 || live.Routes[1].Name != "/tables" {
		t.Fatalf("routes: %+v", live.Routes)
	}
	var seen []string
	for _, v := range live.Viewers {
		seen = append(seen, v.SessionID+":"+v.Route+":"+v.Browser+":"+v.UserLabel)
	}
	if !strings.Contains(strings.Join(seen, ","), "v2:/tables:Firefox:a@b.c") {
		t.Fatalf("viewers: %v", seen)
	}
	// A heartbeat stores no item of its own; all it leaves behind is the route
	// and the visitor key on the session row.
	var d store.SessionDetail
	get(t, h, "/api/v1/sessions/v1", &d)
	if len(d.Items) != 0 || d.Route != "/cashier" || d.VisitorKey == "" {
		t.Fatalf("heartbeat session: %+v", d.Session)
	}
	if store.VisitorKey("u", "1.1.1.1", "Chrome") == store.VisitorKey("u", "1.1.1.1", "Firefox") {
		t.Fatal("browser must change the visitor key")
	}
	for _, c := range []struct{ b, ua, p, os, want string }{
		{"Chrome", "", "web", "web", "Chrome"},
		{"", "Mozilla/5.0 ... Chrome/128 Safari/537", "web", "web", "Chrome"},
		{"", "Mozilla/5.0 ... Firefox/130", "web", "web", "Firefox"},
		{"web", "", "web", "web", ""},
		{"", "", "linux", "linux", "linux"},
	} {
		if got := store.BrowserLabel(c.b, c.ua, c.p, c.os); got != c.want {
			t.Fatalf("store.BrowserLabel(%q,%q,%q,%q)=%q want %q", c.b, c.ua, c.p, c.os, got, c.want)
		}
	}
}

// errCode pulls the `code` out of an error body. That code is the contract: the
// dashboard picks the sentence it shows in its own language from it (see
// `describeError` in frontend/lib/core/auth.dart), so the English text may be
// reworded freely, but changing a code makes the dashboard show the wrong one.
func errCode(rr resp) string {
	var b struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &b)
	return b.Code
}

func TestErrorCodes(t *testing.T) {
	h, _ := newTestServer(t)
	saved := userTok
	defer func() { userTok = saved }()
	// The frame endpoints need a session that really exists, otherwise the case
	// below would fail on the session lookup and never reach the frame branch.
	post(t, h, "/api/v1/envelope", "key1", envelope("e1", map[string]any{"type": "event", "name": "x"}))

	for _, c := range []struct {
		name         string
		token        string
		method, path string
		key          string
		body         any
		status       int
		code         string
	}{
		{"a read without a token", "", "GET", "/api/v1/projects", "", nil, 401, apierr.CodeLoginRequired},
		{"an expired or invented token", "bogus", "GET", "/api/v1/projects", "", nil, 401, apierr.CodeInvalidToken},
		{"a project you are not a member of", saved, "GET", "/api/v1/projects/999", "", nil, 404, apierr.CodeProjectNotFound},
		{"an envelope with no key", saved, "POST", "/api/v1/envelope", "", envelope("z"), 401, apierr.CodeKeyRequired},
		{"a key nobody knows", saved, "POST", "/api/v1/envelope", "nope", envelope("z"), 401, apierr.CodeUnknownKey},
		{"an envelope with no session.id", saved, "POST", "/api/v1/envelope", "key1", map[string]any{"items": []any{}}, 400, apierr.CodeEnvelopeBad},
		{"an email that is already registered", saved, "POST", "/api/v1/auth/register", "", map[string]any{"email": "owner@x.io", "password": "secret1"}, 409, apierr.CodeEmailTaken},
		{"a password that is too short", saved, "POST", "/api/v1/auth/register", "", map[string]any{"email": "yeni@x.io", "password": "123"}, 400, apierr.CodePasswordTooShort},
		{"an address that is not an email", saved, "POST", "/api/v1/auth/register", "", map[string]any{"email": "posta-degil", "password": "123456"}, 400, apierr.CodeInvalidEmail},
		{"a wrong password", saved, "POST", "/api/v1/auth/login", "", map[string]any{"email": "owner@x.io", "password": "yanlis"}, 401, apierr.CodeInvalidCredentials},
		{"a project with no name", saved, "POST", "/api/v1/projects", "", map[string]any{"name": "  "}, 400, apierr.CodeProjectNameNeeded},
		{"an update that empties the name", saved, "PATCH", "/api/v1/projects/1", "", map[string]any{"name": ""}, 400, apierr.CodeProjectNameNeeded},
		{"a member who never registered", saved, "POST", "/api/v1/projects/1/members", "", map[string]any{"email": "yok@x.io"}, 404, apierr.CodeMemberUnknownEmail},
		{"removing yourself", saved, "DELETE", "/api/v1/projects/1/members/1", "", nil, 400, apierr.CodeMemberSelfRemove},
		{"a session that does not exist", saved, "GET", "/api/v1/sessions/yok", "", nil, 404, apierr.CodeSessionNotFound},
		{"a frame that does not exist", saved, "GET", "/api/v1/sessions/e1/frames/9.png", "", nil, 404, apierr.CodeFrameNotFound},
		{"an issue that does not exist", saved, "GET", "/api/v1/issues/999", "", nil, 404, apierr.CodeIssueNotFound},
		{"an unsupported language", saved, "PATCH", "/api/v1/auth/me", "", map[string]any{"locale": "de"}, 400, apierr.CodeUnsupportedLocale},
	} {
		t.Run(c.name, func(t *testing.T) {
			userTok = c.token
			rr := do(t, h, c.method, c.path, c.key, c.body)
			if rr.Code != c.status || errCode(rr) != c.code {
				t.Fatalf("%s %s → %d %q; want %d %q (%s)", c.method, c.path, rr.Code, errCode(rr), c.status, c.code, rr.Body.String())
			}
		})
	}

	// An action that needs the owner role, tried by someone who is only a member.
	userTok = saved
	post(t, h, "/api/v1/auth/register", "", map[string]any{"email": "uye@x.io", "name": "Üye", "password": "123456"})
	post(t, h, "/api/v1/projects/1/members", "", map[string]any{"email": "uye@x.io"})
	rr := post(t, h, "/api/v1/auth/login", "", map[string]any{"email": "uye@x.io", "password": "123456"})
	var ar authResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &ar)
	userTok = ar.Token
	if d := do(t, h, "DELETE", "/api/v1/projects/1", "", nil); d.Code != 403 || errCode(d) != apierr.CodeProjectOwnerNeeded {
		t.Fatalf("a member cannot delete: %d %q", d.Code, errCode(d))
	}
}

func TestUserLocale(t *testing.T) {
	h, _ := newTestServer(t)
	var me store.User
	get(t, h, "/api/v1/auth/me", &me)
	if me.Locale != "" {
		t.Fatalf("a new user must start with no language so the dashboard falls back to the browser: %q", me.Locale)
	}
	rr := do(t, h, "PATCH", "/api/v1/auth/me", "", map[string]any{"locale": "EN"})
	if rr.Code != 200 {
		t.Fatalf("writing the language: %d %s", rr.Code, rr.Body.String())
	}
	var updated store.User
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.Locale != "en" {
		t.Fatalf("the language must be lower-cased: %q", updated.Locale)
	}
	// A later request sees the same language: it lives on the server, not in the
	// token or the browser, so another machine gets the same dashboard.
	get(t, h, "/api/v1/auth/me", &me)
	if me.Locale != "en" {
		t.Fatalf("me: %q", me.Locale)
	}
	// The empty string clears the choice again.
	if rr := do(t, h, "PATCH", "/api/v1/auth/me", "", map[string]any{"locale": ""}); rr.Code != 200 {
		t.Fatalf("clearing the language: %d", rr.Code)
	}
	get(t, h, "/api/v1/auth/me", &me)
	if me.Locale != "" {
		t.Fatalf("the language should have been cleared: %q", me.Locale)
	}
	// /health advertises the supported languages, so the dashboard can read the
	// list from the server instead of keeping its own copy of it.
	var hb struct {
		Locales []string `json:"locales"`
	}
	get(t, h, "/api/v1/health", &hb)
	if len(hb.Locales) != 2 || hb.Locales[0] != "tr" {
		t.Fatalf("health locales: %+v", hb.Locales)
	}
}

// The dashboard is a single-page app: any path that is not a file on disk has
// to return index.html with 200, or the browser shows an error page instead of
// letting the client router handle the route. API paths must keep 404ing.
func TestDashboardIsServedAsASinglePageApp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>hog</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.dart.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.Options{DSN: testdb.DSN(t), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	app := New(st, dir, nil)

	for _, c := range []struct {
		name, path, wantBody string
		wantCode             int
	}{
		{"root serves the app", "/", "<!doctype html>", 200},
		{"a client route serves the app", "/projects/1/sessions", "<!doctype html>", 200},
		{"a real file is served as itself", "/main.dart.js", "console.log(1)", 200},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := send(t, app, httptest.NewRequest("GET", c.path, nil))
			if rr.Code != c.wantCode || !strings.Contains(rr.Body.String(), c.wantBody) {
				t.Fatalf("%s → %d %q", c.path, rr.Code, rr.Body.String())
			}
		})
	}
	// The static handler is registered last, so it must not swallow API 404s.
	if rr := send(t, app, httptest.NewRequest("GET", "/api/v1/nope", nil)); rr.Code != 404 ||
		strings.Contains(rr.Body.String(), "<!doctype html>") {
		t.Fatalf("unknown api path: %d %q", rr.Code, rr.Body.String())
	}
}

// A JSON body without a Content-Type header must still parse. Fiber's
// Bind().Body() dispatches on the header; the SDK and curl users do not always
// send one, and the old net/http server always decoded JSON.
func TestJSONBodyWithoutContentType(t *testing.T) {
	app, _ := newTestServer(t)
	body := `{"email":"owner@x.io","password":"secret1"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Del("Content-Type")
	rr := send(t, app, req)
	if rr.Code != 200 {
		t.Fatalf("login without content-type: %d %s", rr.Code, rr.Body.String())
	}
	var ar authResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &ar); err != nil || ar.Token == "" {
		t.Fatalf("no token: %v %s", err, rr.Body.String())
	}
}

// The project was renamed, so the ingest header was too. An SDK already shipped
// inside somebody's app keeps sending the old name and is not rebuilt because
// the server was upgraded, so both have to work.
func TestIngestAcceptsBothProjectKeyHeaders(t *testing.T) {
	app, st := newTestServer(t)
	for _, header := range []string{"X-Sightpane-Key", "X-Hog-Key"} {
		t.Run(header, func(t *testing.T) {
			session := "hdr-" + header
			body, _ := json.Marshal(envelope(session, map[string]any{"type": "event", "name": "x"}))
			req := httptest.NewRequest("POST", "/api/v1/envelope", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(header, "key1")
			if rr := send(t, app, req); rr.Code != 202 {
				t.Fatalf("%s: %d %s", header, rr.Code, rr.Body.String())
			}
			if _, err := st.GetSession(session); err != nil {
				t.Fatalf("%s: session not stored: %v", header, err)
			}
		})
	}
	// Neither header at all is still a 401 with the same code.
	req := httptest.NewRequest("POST", "/api/v1/envelope", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	if rr := send(t, app, req); rr.Code != 401 || errCode(rr) != apierr.CodeKeyRequired {
		t.Fatalf("no key: %d %q", rr.Code, errCode(rr))
	}
}

// --- Source maps ---

// One mapping, written out rather than pasted from a build: generated line 1
// column 0, and again on generated line 2, both from sources[0] line 119
// (0-based, so 120 on screen) column 4, in names[0]. The encoding is base64 VLQ
// of deltas; internal/symbol/symbol_test.go builds these programmatically and
// explains it.
//
// Two generated lines mapping to one source line is the whole point: it is what
// a rebuild looks like, where the same Dart code lands somewhere else in the
// bundle.
const sourceMapFixture = `{"version":3,"file":"main.dart.js",` +
	`"sources":["org-dartlang-app:///lib/cashier.dart"],` +
	`"names":["openTill"],"mappings":"AAuHIA;AAAAA"}`

// postSourceMap uploads a map the way a deploy script would: multipart, owner
// token, no project API key — the key ships inside the app and may only write
// envelopes.
func postSourceMap(t *testing.T, app *fiber.App, path, filename, body string) resp {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	f, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	w.Close()
	req := httptest.NewRequest("POST", path, bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	if userTok != "" {
		req.Header.Set("Authorization", "Bearer "+userTok)
	}
	return send(t, app, req)
}

// releaseEnvelope is envelope() with a release, which is what ties an error to
// an uploaded map.
func releaseEnvelope(session, release string, items ...map[string]any) map[string]any {
	e := envelope(session, items...)
	e["session"].(map[string]any)["device"] = map[string]any{
		"platform": "web", "release": release, "browser": "Chrome",
	}
	return e
}

func webError(message, member string, line int) map[string]any {
	return map[string]any{
		"type": "error", "message": message, "exception": "StateError",
		// What a minified release build actually sends: the stack is JavaScript
		// and names nothing, and `frames` is the same thing already parsed.
		"stack": "Error\n    at " + member + " (https://app.example.com/main.dart.js:" + strconv.Itoa(line) + ":1)",
		"frames": []map[string]any{
			{"uri": "https://app.example.com/main.dart.js", "line": line, "column": 1, "member": member},
		},
	}
}

func TestSourceMapsSymbolicateAndStopGroupingDrift(t *testing.T) {
	h, _ := newTestServer(t)

	if rr := postSourceMap(t, h, "/api/v1/projects/1/releases/1.0.0/sourcemaps", "main.dart.js.map", sourceMapFixture); rr.Code != 201 {
		t.Fatalf("upload: %d %s", rr.Code, rr.Body.String())
	}
	var arts []store.ReleaseArtifact
	get(t, h, "/api/v1/projects/1/releases?release=1.0.0", &arts)
	if len(arts) != 1 || arts[0].Filename != "main.dart.js.map" || arts[0].Size == 0 {
		t.Fatalf("artifact list: %+v", arts)
	}

	// Two errors from the same Dart line, at two different places in the bundle
	// — one build and the next. Today both would group by message, and with two
	// different messages they would be two issues.
	for _, e := range []map[string]any{
		webError("Bad state: till 7 is jammed", "aI.$2", 1),
		webError("Bad state: till 9 is jammed", "zQ.$0", 2),
	} {
		if rr := post(t, h, "/api/v1/envelope", "key1", releaseEnvelope("web1", "1.0.0", e)); rr.Code != 202 {
			t.Fatalf("ingest: %d %s", rr.Code, rr.Body.String())
		}
	}

	var issues []store.Issue
	get(t, h, "/api/v1/projects/1/issues", &issues)
	if len(issues) != 1 || issues[0].Count != 2 {
		t.Fatalf("two builds of one failure must be one issue seen twice: %+v", issues)
	}

	var d store.IssueDetail
	get(t, h, "/api/v1/issues/1", &d)
	if len(d.Occurrences) != 2 {
		t.Fatalf("occurrences: %+v", d.Occurrences)
	}
	var got struct {
		Frames []struct {
			File, Function, Minified string
			Line                     int
			Resolved                 bool
		} `json:"frames"`
	}
	if err := json.Unmarshal(d.Occurrences[0].Symbolicated, &got); err != nil {
		t.Fatalf("symbolicated: %v (%s)", err, d.Occurrences[0].Symbolicated)
	}
	if len(got.Frames) != 1 {
		t.Fatalf("frames: %+v", got.Frames)
	}
	f := got.Frames[0]
	if f.File != "lib/cashier.dart" || f.Line != 120 || f.Function != "openTill" || !f.Resolved {
		t.Fatalf("frame not resolved to source: %+v", f)
	}
	// The minified original stays on the record: a map can be wrong, and this is
	// the only way anyone finds out.
	if !strings.Contains(f.Minified, "main.dart.js") {
		t.Fatalf("minified frame lost: %+v", f)
	}
	// What the SDK sent is handed back untouched; the resolution sits beside it.
	if !strings.Contains(string(d.Occurrences[0].Body), `"stack"`) {
		t.Fatalf("the raw body must be unchanged: %s", d.Occurrences[0].Body)
	}
}

// A project that never uploads a map — every native app, and any web app that
// has not set this up — has to behave exactly as it did before.
func TestWithoutASourceMapNothingChanges(t *testing.T) {
	h, _ := newTestServer(t)
	e := webError("Bad state: boom", "aI.$2", 1)
	if rr := post(t, h, "/api/v1/envelope", "key1", releaseEnvelope("web2", "2.0.0", e)); rr.Code != 202 {
		t.Fatalf("ingest: %d %s", rr.Code, rr.Body.String())
	}
	var d store.IssueDetail
	get(t, h, "/api/v1/issues/1", &d)
	if len(d.Occurrences) != 1 {
		t.Fatalf("occurrences: %+v", d.Occurrences)
	}
	if d.Occurrences[0].Symbolicated != nil {
		t.Fatalf("nothing should be symbolicated without a map: %s", d.Occurrences[0].Symbolicated)
	}
}

// Uploading is a deploy-time action with a user token. A member may look, only
// an owner may write, and the project API key cannot do it at all.
func TestSourceMapUploadIsOwnerOnly(t *testing.T) {
	h, st := newTestServer(t)
	saved := userTok
	defer func() { userTok = saved }()

	member, err := st.CreateUser("member@x.io", "Member", "secret1")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(1, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	userTok, _ = st.IssueToken(member.ID)
	if rr := postSourceMap(t, h, "/api/v1/projects/1/releases/1.0.0/sourcemaps", "main.dart.js.map", sourceMapFixture); rr.Code != 403 {
		t.Fatalf("a member must not upload: %d", rr.Code)
	}
	if rr := get(t, h, "/api/v1/projects/1/releases", nil); rr.Code != 200 {
		t.Fatalf("a member may list: %d", rr.Code)
	}

	userTok = ""
	if rr := postSourceMap(t, h, "/api/v1/projects/1/releases/1.0.0/sourcemaps", "main.dart.js.map", sourceMapFixture); rr.Code != 401 {
		t.Fatalf("anonymous must not upload: %d", rr.Code)
	}

	userTok = saved
	// Only a source map: an arbitrary file would be a way to use the server as
	// a file host.
	if rr := postSourceMap(t, h, "/api/v1/projects/1/releases/1.0.0/sourcemaps", "payload.zip", "not a map"); rr.Code != 400 {
		t.Fatalf("a non-map upload must be rejected: %d %s", rr.Code, rr.Body.String())
	}
}
