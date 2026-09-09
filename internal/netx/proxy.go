// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package netx wraps the TCP listener. Behind a layer-4 proxy (caddy-l4,
// HAProxy) no HTTP header is added and the server would only ever see the
// proxy's own address, so the real client address arrives in a PROXY protocol
// header at the start of the connection instead.
package netx

import (
	"log"
	"net"
	"strings"
	"time"

	"github.com/pires/go-proxyproto"
)

// ProxyListener reads a PROXY protocol (v1/v2) header when one is present and
// passes the connection through untouched when it is not.
//
// [trusted] is a comma-separated list of IPs or CIDRs. When it is set the
// header is honoured only for connections coming from those peers and ignored
// (read and discarded, never REQUIRE) for everyone else — otherwise any client
// reaching the port could claim to be any address. When it is empty every peer
// is trusted, which is only safe if the port is not reachable directly.
func ProxyListener(ln net.Listener, trusted string) net.Listener {
	nets := parseCIDRs(trusted)
	return &proxyproto.Listener{
		Listener:          ln,
		ReadHeaderTimeout: 5 * time.Second,
		ConnPolicy: func(o proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
			if len(nets) == 0 {
				return proxyproto.USE, nil
			}
			if addr, ok := o.Upstream.(*net.TCPAddr); ok {
				for _, n := range nets {
					if n.Contains(addr.IP) {
						return proxyproto.USE, nil
					}
				}
			}
			return proxyproto.IGNORE, nil
		},
	}
}

// parseCIDRs turns "127.0.0.1, 10.0.0.0/8" into networks. A bare address is
// treated as a single host. Unparseable entries are skipped with a log line
// rather than failing startup, so one typo cannot take the service down.
func parseCIDRs(list string) []*net.IPNet {
	var nets []*net.IPNet
	for _, t := range strings.Split(list, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if !strings.Contains(t, "/") {
			if strings.Contains(t, ":") {
				t += "/128"
			} else {
				t += "/32"
			}
		}
		if _, n, err := net.ParseCIDR(t); err == nil {
			nets = append(nets, n)
		} else {
			log.Printf("SIGHTPANE_TRUSTED_PROXIES: skipped %q (%v)", t, err)
		}
	}
	return nets
}
