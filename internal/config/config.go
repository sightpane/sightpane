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
	"strconv"
	"strings"
	"sync"
)

type Config struct {
	// Addr is the listen address, e.g. ":8790" or "127.0.0.1:18790".
	Addr string
	// DataDir holds frames/<session>/<seq>.png, when the frames are on disk.
	DataDir string

	// DB is the database, as a `postgres://…` URL or a libpq key/value string.
	// It is required: there is no local default to fall back to.
	DB string
	// SourceMapCacheMB bounds the parsed release source maps held in memory. A
	// dart2js map is 10–30 MB, so the default is a handful of releases.
	SourceMapCacheMB int
	// RetentionDays drops items older than this many days. It is enforced by a
	// TimescaleDB retention policy, so it does nothing on a Postgres without the
	// extension. Zero keeps everything forever.
	RetentionDays int
	// IngestRate sets the default items-per-minute rate limit for envelope ingest.
	// 0 means unlimited. Projects can override this with quota_items_per_minute.
	IngestRate int

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

	// PublicURL is the external URL of the dashboard (for notification links).
	PublicURL string
	// SMTP is the mail server configuration for alert emails.
	SMTP SMTPConfig

	// ProxyProtocol makes the listener read a PROXY protocol (v1/v2) header, the
	// only way a layer-4 proxy can pass the real client address through TCP.
	ProxyProtocol bool
	// TrustedProxies limits which peers may send that header (comma-separated
	// IPs or CIDRs). Empty trusts every peer, which lets a direct client forge
	// its own address — set it whenever the port is reachable from outside.
	TrustedProxies string

	// GeoIPDB is the file path to MaxMind GeoLite2/GeoIP2 City MMDB.
	GeoIPDB string
	// DevGeoIPCountry overrides the detected country code for local/private IPs in dev.
	DevGeoIPCountry string
	// DevGeoIPCity overrides the detected city for local/private IPs in dev.
	DevGeoIPCity string
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

// SMTPConfig configures sending alert notification emails via net/smtp.
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

func Load() Config {
	return Config{
		Addr:             env("ADDR", ":8790"),
		DataDir:          env("DATA", "./data"),
		DB:               env("DB", ""),
		RetentionDays:    envInt("RETENTION_DAYS", 30),
		IngestRate:       envInt("INGEST_RATE", 0),
		SourceMapCacheMB: envInt("SOURCEMAP_CACHE_MB", 128),
		AdminEmail:       env("ADMIN_EMAIL", "admin@sightpane.local"),
		AdminPassword:    env("ADMIN_PASSWORD", "admin123"),
		DefaultProject:   env("DEFAULT_PROJECT", "default"),
		DefaultKey:       env("DEFAULT_KEY", "dev"),
		UIDir:            env("UI_DIR", ""),
		Frames:           env("FRAMES", "fs"),
		S3: S3Config{
			Endpoint:  env("S3_ENDPOINT", ""),
			Bucket:    env("S3_BUCKET", "sightpane-frames"),
			AccessKey: env("S3_ACCESS_KEY", ""),
			SecretKey: envSecret("S3_SECRET_KEY", "S3_SECRET_KEY_FILE", ""),
			Region:    env("S3_REGION", ""),
			UseSSL:    env("S3_USE_SSL", "") != "",
		},
		PublicURL: strings.TrimRight(env("PUBLIC_URL", "http://localhost:8790"), "/"),
		SMTP: SMTPConfig{
			Host: env("SMTP_HOST", ""),
			Port: envInt("SMTP_PORT", 587),
			User: env("SMTP_USER", ""),
			Pass: env("SMTP_PASS", ""),
			From: env("SMTP_FROM", "alerts@sightpane.local"),
		},
		ProxyProtocol:   env("PROXY_PROTOCOL", "") != "",
		TrustedProxies:  env("TRUSTED_PROXIES", ""),
		GeoIPDB:         env("GEOIP_DB_PATH", ""),
		DevGeoIPCountry: env("DEV_GEOIP_COUNTRY", ""),
		DevGeoIPCity:    env("DEV_GEOIP_CITY", ""),
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

// env reads SIGHTPANE_<name>, then <name>, then the deprecated HOG_<name>, then the default.
func env(name, def string) string {
	if v := os.Getenv(prefix + name); v != "" {
		return v
	}
	if v := os.Getenv(name); v != "" {
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

// envSecret reads SIGHTPANE_<fileEnvName> (e.g. SIGHTPANE_S3_SECRET_KEY_FILE),
// then reads that file's content. If absent or failing to read, it falls back
// to env(name, def).
func envSecret(name, fileEnvName, def string) string {
	if filePath := env(fileEnvName, ""); filePath != "" {
		if data, err := os.ReadFile(filePath); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return env(name, def)
}

// envInt reads a whole number, and keeps the default when the value is not one
// rather than failing to start over a typo in an optional setting.
func envInt(name string, def int) int {
	v := env(name, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("%s%s=%q is not a number; using %d", prefix, name, v, def)
		return def
	}
	return n
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
