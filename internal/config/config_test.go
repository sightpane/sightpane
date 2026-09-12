// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"testing"
)

// An existing deployment sets HOG_* variables. After the rename those must keep
// working, or an upgrade would silently fall back to the defaults — a different
// data directory and a fresh admin password, which looks like data loss.
func TestLoadAcceptsBothPrefixes(t *testing.T) {
	t.Setenv("HOG_ADDR", ":9001")
	t.Setenv("HOG_DATA", "/old/data")
	if got := Load(); got.Addr != ":9001" || got.DataDir != "/old/data" {
		t.Fatalf("deprecated prefix ignored: %+v", got)
	}

	// The new name wins when both are set, so a half-finished migration does
	// not leave the old value in charge.
	t.Setenv("SIGHTPANE_ADDR", ":9002")
	if got := Load(); got.Addr != ":9002" {
		t.Fatalf("SIGHTPANE_ADDR should win: %q", got.Addr)
	}

	// Neither set: the documented defaults.
	t.Setenv("HOG_ADDR", "")
	t.Setenv("SIGHTPANE_ADDR", "")
	t.Setenv("HOG_DATA", "")
	if got := Load(); got.Addr != ":8790" || got.DataDir != "./data" {
		t.Fatalf("defaults: %+v", got)
	}
}

func TestLoadS3SecretKeyFile(t *testing.T) {
	secretFile := t.TempDir() + "/secret.txt"
	if err := os.WriteFile(secretFile, []byte("super-secret-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SIGHTPANE_S3_SECRET_KEY_FILE", secretFile)
	t.Setenv("SIGHTPANE_S3_SECRET_KEY", "direct-env-secret")
	if got := Load(); got.S3.SecretKey != "super-secret-from-file" {
		t.Fatalf("expected secret from file, got %q", got.S3.SecretKey)
	}

	t.Setenv("SIGHTPANE_S3_SECRET_KEY_FILE", "")
	if got := Load(); got.S3.SecretKey != "direct-env-secret" {
		t.Fatalf("expected direct secret, got %q", got.S3.SecretKey)
	}
}

func TestLoadBooleans(t *testing.T) {
	t.Setenv("SIGHTPANE_S3_USE_SSL", "false")
	t.Setenv("SIGHTPANE_PROXY_PROTOCOL", "false")
	cfg := Load()
	if cfg.S3.UseSSL {
		t.Fatal("expected S3.UseSSL to be false for 'false'")
	}
	if cfg.ProxyProtocol {
		t.Fatal("expected ProxyProtocol to be false for 'false'")
	}

	t.Setenv("SIGHTPANE_S3_USE_SSL", "true")
	t.Setenv("SIGHTPANE_PROXY_PROTOCOL", "1")
	cfg = Load()
	if !cfg.S3.UseSSL {
		t.Fatal("expected S3.UseSSL to be true for 'true'")
	}
	if !cfg.ProxyProtocol {
		t.Fatal("expected ProxyProtocol to be true for '1'")
	}
}
