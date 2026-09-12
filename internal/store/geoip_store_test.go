// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"testing"

	"sightpane/internal/testdb"
)

func TestIngestGeoIPAndCoordinates(t *testing.T) {
	st, err := Open(Options{
		DSN:             testdb.DSN(t),
		DataDir:         t.TempDir(),
		DevGeoIPCountry: "TR",
		DevGeoIPCity:    "Istanbul",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	admin, err := st.CreateUser("admin-geo@sightpane.local", "Admin Geo", "pass123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	proj, err := st.CreateProject("Geo Project", "web", "key_geo", &admin.ID)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// 1. Ingest session with well-known public IP (8.8.8.8 -> US, Mountain View, California, 37.4223, -122.0848)
	envJSON := `{
		"sdk": {"name": "@sightpane/browser", "version": "1.0.0"},
		"session": {
			"id": "sess-geo-1",
			"started_at": "2026-09-12T10:00:00Z",
			"user": {"id": "user-geo@example.com"}
		},
		"items": [
			{"type": "breadcrumb", "category": "navigation", "message": "/dashboard"}
		]
	}`
	var env Envelope
	if err := json.Unmarshal([]byte(envJSON), &env); err != nil {
		t.Fatal(err)
	}

	if _, err := st.Ingest(ctx, proj.ID, &env, "8.8.8.8"); err != nil {
		t.Fatalf("Ingest with 8.8.8.8: %v", err)
	}

	detail, err := st.GetSession("sess-geo-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if detail.Session.CountryCode != "US" {
		t.Errorf("expected CountryCode US, got %q", detail.Session.CountryCode)
	}
	if detail.Session.City != "Mountain View" {
		t.Errorf("expected City Mountain View, got %q", detail.Session.City)
	}
	if detail.Session.Latitude == nil || *detail.Session.Latitude == 0 {
		t.Errorf("expected non-nil latitude, got %v", detail.Session.Latitude)
	}
	if detail.Session.Longitude == nil || *detail.Session.Longitude == 0 {
		t.Errorf("expected non-nil longitude, got %v", detail.Session.Longitude)
	}

	// Verify ListSessions also returns them
	list, err := st.ListSessions(SessionFilter{ProjectID: proj.ID})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 session, got %d", len(list))
	}
	if list[0].CountryCode != "US" || list[0].City != "Mountain View" {
		t.Errorf("ListSessions mismatch: %+v", list[0])
	}
	if list[0].Latitude == nil || list[0].Longitude == nil {
		t.Errorf("ListSessions coordinates nil: %+v", list[0])
	}

	// 2. Local network IP with DevGeoIPCountry and DevGeoIPCity
	envLocalJSON := `{
		"sdk": {"name": "@sightpane/browser", "version": "1.0.0"},
		"session": {
			"id": "sess-geo-2",
			"started_at": "2026-09-12T11:00:00Z"
		},
		"items": []
	}`
	var envLocal Envelope
	if err := json.Unmarshal([]byte(envLocalJSON), &envLocal); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Ingest(ctx, proj.ID, &envLocal, "192.168.1.100"); err != nil {
		t.Fatalf("Ingest with 192.168.1.100: %v", err)
	}

	detailLocal, err := st.GetSession("sess-geo-2")
	if err != nil {
		t.Fatalf("GetSession local: %v", err)
	}
	if detailLocal.Session.CountryCode != "TR" || detailLocal.Session.City != "Istanbul" {
		t.Errorf("expected TR/Istanbul from dev override, got %q/%q", detailLocal.Session.CountryCode, detailLocal.Session.City)
	}
}
