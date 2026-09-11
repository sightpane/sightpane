// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package uptime

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

type SSLProbeResult struct {
	Valid         bool
	Issuer        string
	ExpiresAt     time.Time
	DaysRemaining int
}

// ProbeSSLCertificate connects via TLS to extract certificate expiration and issuer.
func ProbeSSLCertificate(targetURL string, timeout time.Duration) (*SSLProbeResult, error) {
	u, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	if u.Scheme != "https" {
		return nil, nil
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		host = net.JoinHostPort(host, "443")
	}

	dialer := &net.Dialer{
		Timeout: timeout,
	}

	// Connect with TLS
	conn, err := tls.DialWithDialer(dialer, "tcp", host, &tls.Config{
		ServerName: u.Hostname(),
	})
	if err != nil {
		return nil, fmt.Errorf("tls dial %s: %w", host, err)
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, fmt.Errorf("no peer certificates presented by %s", host)
	}

	leaf := certs[0]
	now := time.Now().UTC()
	valid := now.Before(leaf.NotAfter) && now.After(leaf.NotBefore)
	daysRemaining := int(time.Until(leaf.NotAfter).Hours() / 24)

	issuer := leaf.Issuer.CommonName
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}

	return &SSLProbeResult{
		Valid:         valid,
		Issuer:        issuer,
		ExpiresAt:     leaf.NotAfter.UTC(),
		DaysRemaining: daysRemaining,
	}, nil
}
