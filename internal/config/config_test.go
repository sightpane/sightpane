// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import "testing"

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
