// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"sightpane/internal/store"
)

func TestEnrichDeviceJSON(t *testing.T) {
	// Case 1: Web Chrome on Linux UA
	rawWeb := `{"platform":"web","user_agent":"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.6613.120 Safari/537.36"}`
	resJSON, p := store.EnrichDeviceJSON(rawWeb)
	if p.PlatformCategory != "web" {
		t.Fatalf("expected platform_category web, got %s", p.PlatformCategory)
	}
	if p.Browser != "Chrome" {
		t.Fatalf("expected browser Chrome, got %s", p.Browser)
	}
	if p.BrowserVersion != "128.0.6613.120" {
		t.Fatalf("expected browser_version 128.0.6613.120, got %s", p.BrowserVersion)
	}
	if p.OS != "Linux" {
		t.Fatalf("expected OS Linux, got %s", p.OS)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(resJSON), &m); err != nil {
		t.Fatalf("failed to unmarshal enriched json: %v", err)
	}
	if m["browser_version"] != "128.0.6613.120" {
		t.Fatalf("expected browser_version in json: %v", m)
	}

	// Case 2: Linux desktop with distro and kernel details
	rawLinux := `{"platform":"linux","platform_category":"desktop","os":"Ubuntu","os_version":"24.04","kernel":"Linux","kernel_version":"6.8.0-40-generic","arch":"x86_64","cpu_cores":8}`
	_, pLinux := store.EnrichDeviceJSON(rawLinux)
	if pLinux.PlatformCategory != "desktop" {
		t.Fatalf("expected desktop, got %s", pLinux.PlatformCategory)
	}
	if pLinux.OS != "Ubuntu" || pLinux.OSVersion != "24.04" {
		t.Fatalf("expected Ubuntu 24.04, got %s %s", pLinux.OS, pLinux.OSVersion)
	}
	if pLinux.Kernel != "Linux" || pLinux.KernelVersion != "6.8.0-40-generic" {
		t.Fatalf("expected Linux 6.8.0-40-generic, got %s %s", pLinux.Kernel, pLinux.KernelVersion)
	}
}

func TestBuildSessionSearchWhereWithDevice(t *testing.T) {
	idx := 1
	next := func(v any) string {
		s := fmt.Sprintf("$%d", idx)
		idx++
		return s
	}

	where, err := store.BuildSessionSearchWhere("os:Ubuntu kernel:Linux category:desktop", next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if where == "" {
		t.Fatal("expected non-empty where clause")
	}
}
