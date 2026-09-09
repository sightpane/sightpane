// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bufio"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/netx"
)

// PROXY protocol only shows its real behaviour on a real socket: the library's
// default policy is REQUIRE, which would reject every plain connection, and no
// mock would have revealed that. So this drives an actual listener and writes
// raw HTTP.
//
// What must hold: with the header, the session records the client address the
// proxy announced, not the proxy's; without it, the connection still works; and
// a header from a peer outside SIGHTPANE_TRUSTED_PROXIES is ignored rather than
// believed, otherwise anyone reaching the port could claim any address.
func TestProxyProtocolListener(t *testing.T) {
	app, st := newTestServer(t)

	envelope := func(session string) string {
		return `{"session":{"id":"` + session + `"},"items":[]}`
	}
	request := func(header, body string) string {
		return header + "POST /api/v1/envelope HTTP/1.1\r\nHost: x\r\n" +
			"X-Hog-Key: key1\r\nContent-Type: application/json\r\n" +
			"Content-Length: " + strconv.Itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + body
	}
	serve := func(t *testing.T, trusted string) net.Listener {
		t.Helper()
		raw, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ln := netx.ProxyListener(raw, trusted)
		go app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) //nolint:errcheck
		t.Cleanup(func() { ln.Close() })
		return raw
	}
	send := func(t *testing.T, addr, header, session string) int {
		t.Helper()
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Write([]byte(request(header, envelope(session)))); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatalf("%s: %v", session, err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	raw := serve(t, "")
	if code := send(t, raw.Addr().String(), "PROXY TCP4 198.51.100.5 10.0.0.1 40000 8790\r\n", "pp1"); code != 202 {
		t.Fatalf("with header: status %d", code)
	}
	if code := send(t, raw.Addr().String(), "", "pp2"); code != 202 {
		t.Fatalf("without header: status %d", code)
	}
	if d, err := st.GetSession("pp1"); err != nil || d.IP != "198.51.100.5" {
		t.Fatalf("proxy protocol ip: %v", err)
	}
	if d, err := st.GetSession("pp2"); err != nil || !strings.HasPrefix(d.IP, "127.0.0.1") {
		t.Fatalf("plain connection ip: %v", err)
	}

	// 127.0.0.1 is not in the trusted list, so its header must be discarded.
	raw2 := serve(t, "10.0.0.0/8")
	if code := send(t, raw2.Addr().String(), "PROXY TCP4 198.51.100.5 10.0.0.1 40000 8790\r\n", "pp3"); code != 202 {
		t.Fatalf("untrusted peer: status %d", code)
	}
	d3, err := st.GetSession("pp3")
	if err != nil {
		t.Fatalf("pp3 was not ingested: %v", err)
	}
	if !strings.HasPrefix(d3.IP, "127.0.0.1") {
		t.Fatalf("a header from an untrusted peer must be ignored, got %q", d3.IP)
	}
}
