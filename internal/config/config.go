// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config reads every environment variable the server understands, in
// one place, so the defaults are visible at a glance and main stays wiring only.
package config

import (
	"log"
	"os"
	"strings"
	"sync"
)

type Config struct {
	// Addr is the listen address, e.g. ":8790" or "127.0.0.1:18790".
	Addr string
	// DataDir holds sightpane.db and frames/<session>/<seq>.png.
	DataDir string

	// AdminEmail/AdminPassword are created on first start if absent, so a fresh
	// install can be logged into without a setup step.
	AdminEmail    string
	AdminPassword string

	// DefaultProject/DefaultKey are guaranteed to exist on start; the key is
	// what a developer pastes into Hog.init during a first run.
	DefaultProject string
	DefaultKey     string

	// UIDir serves a built dashboard. Empty falls back to the embedded page.
	UIDir string

	// Frames selects where replay frames are kept: "fs" (a directory under
	// DataDir) or "s3" (any S3-compatible object store). "s3" is what a Ceph
	// RADOS Gateway is reached through, and it is the only option that works
	// with more than one replica, since a local directory is not shared.
	Frames string
	S3     S3Config

	// ProxyProtocol makes the listener read a PROXY protocol (v1/v2) header, the
	// only way a layer-4 proxy can pass the real client address through TCP.
	ProxyProtocol bool
	// TrustedProxies limits which peers may send that header (comma-separated
	// IPs or CIDRs). Empty trusts every peer, which lets a direct client forge
	// its own address — set it whenever the port is reachable from outside.
	TrustedProxies string
}

// S3Config addresses an S3-compatible object store; with Ceph this is the RADOS
// Gateway. The endpoint is host:port with no scheme.
type S3Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool
}

func Load() Config {
	return Config{
		Addr:           env("ADDR", ":8790"),
		DataDir:        env("DATA", "./data"),
		AdminEmail:     env("ADMIN_EMAIL", "admin@sightpane.local"),
		AdminPassword:  env("ADMIN_PASSWORD", "admin123"),
		DefaultProject: env("DEFAULT_PROJECT", "default"),
		DefaultKey:     env("DEFAULT_KEY", "dev"),
		UIDir:          env("UI_DIR", ""),
		Frames:         env("FRAMES", "fs"),
		S3: S3Config{
			Endpoint:  env("S3_ENDPOINT", ""),
			Bucket:    env("S3_BUCKET", "sightpane-frames"),
			AccessKey: env("S3_ACCESS_KEY", ""),
			SecretKey: env("S3_SECRET_KEY", ""),
			Region:    env("S3_REGION", ""),
			UseSSL:    env("S3_USE_SSL", "") != "",
		},
		ProxyProtocol:  env("PROXY_PROTOCOL", "") != "",
		TrustedProxies: env("TRUSTED_PROXIES", ""),
	}
}

// Prefixes the settings are read under. The project was called flutter-hog
// before, so `HOG_` is still accepted: an existing deployment keeps running
// after an upgrade instead of silently falling back to defaults. It logs once
// so the variable actually gets renamed rather than living on forever.
const (
	prefix    = "SIGHTPANE_"
	oldPrefix = "HOG_"
)

var warnOnce sync.Once

// env reads SIGHTPANE_<name>, then the deprecated HOG_<name>, then the default.
func env(name, def string) string {
	if v := os.Getenv(prefix + name); v != "" {
		return v
	}
	if v := os.Getenv(oldPrefix + name); v != "" {
		warnOnce.Do(func() {
			log.Printf("using deprecated %s* environment variables; rename them to %s* (found %s)",
				oldPrefix, prefix, strings.Join(oldNamesInUse(), ", "))
		})
		return v
	}
	return def
}

// oldNamesInUse lists the deprecated variables that are actually set, so the
// warning names them instead of being a vague nag.
func oldNamesInUse() []string {
	var found []string
	for _, e := range os.Environ() {
		if name, _, ok := strings.Cut(e, "="); ok && strings.HasPrefix(name, oldPrefix) {
			found = append(found, name)
		}
	}
	return found
}
