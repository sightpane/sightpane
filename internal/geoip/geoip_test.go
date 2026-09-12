// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package geoip

import (
	"testing"
)

func TestLocalAndPrivateIPs(t *testing.T) {
	res := New(Options{})
	defer res.Close()

	testCases := []string{
		"172.21.0.1",   // Docker default bridge network
		"127.0.0.1",    // IPv4 localhost
		"::1",          // IPv6 localhost
		"10.0.4.15",    // Class A private
		"192.168.1.10", // Class C private
		"localhost",
	}

	for _, ip := range testCases {
		loc := res.Lookup(ip)
		if loc.CountryCode != "LOCAL" {
			t.Errorf("IP %s: expected CountryCode 'LOCAL', got %q", ip, loc.CountryCode)
		}
		if loc.CountryName != "Local Network" {
			t.Errorf("IP %s: expected CountryName 'Local Network', got %q", ip, loc.CountryName)
		}
		if loc.City != "Local" {
			t.Errorf("IP %s: expected City 'Local', got %q", ip, loc.City)
		}
		if loc.Region != "Local Region" {
			t.Errorf("IP %s: expected Region 'Local Region', got %q", ip, loc.Region)
		}
	}
}

func TestDevOverride(t *testing.T) {
	res := New(Options{
		DevCountry: "TR",
		DevRegion:  "Marmara",
		DevCity:    "Istanbul",
	})
	defer res.Close()

	loc := res.Lookup("172.21.0.1")
	if loc.CountryCode != "TR" {
		t.Errorf("expected CountryCode 'TR', got %q", loc.CountryCode)
	}
	if loc.CountryName != "Turkey" {
		t.Errorf("expected CountryName 'Turkey', got %q", loc.CountryName)
	}
	if loc.Region != "Marmara" {
		t.Errorf("expected Region 'Marmara', got %q", loc.Region)
	}
	if loc.City != "Istanbul" {
		t.Errorf("expected City 'Istanbul', got %q", loc.City)
	}
}

func TestEdgeHeaders(t *testing.T) {
	res := New(Options{})
	defer res.Close()

	headers := map[string]string{
		"CF-IPCountry":   "DE",
		"CF-Region":      "Berlin",
		"CF-IPCity":      "Berlin",
		"CF-IPLatitude":  "52.5200",
		"CF-IPLongitude": "13.4050",
	}

	loc := res.Lookup("198.51.100.24", headers)
	if loc.CountryCode != "DE" {
		t.Errorf("expected CountryCode 'DE', got %q", loc.CountryCode)
	}
	if loc.CountryName != "Germany" {
		t.Errorf("expected CountryName 'Germany', got %q", loc.CountryName)
	}
	if loc.Region != "Berlin" {
		t.Errorf("expected Region 'Berlin', got %q", loc.Region)
	}
	if loc.City != "Berlin" {
		t.Errorf("expected City 'Berlin', got %q", loc.City)
	}
	if loc.Latitude != 52.5200 || loc.Longitude != 13.4050 {
		t.Errorf("expected lat/lon 52.5200/13.4050, got %f/%f", loc.Latitude, loc.Longitude)
	}
}

func TestWellKnownIPs(t *testing.T) {
	res := New(Options{})
	defer res.Close()

	locGoogle := res.Lookup("8.8.8.8")
	if locGoogle.CountryCode != "US" || locGoogle.CountryName != "United States" || locGoogle.Region != "California" {
		t.Errorf("expected 8.8.8.8 to resolve to US / California, got %+v", locGoogle)
	}
	if locGoogle.Latitude == 0 || locGoogle.Longitude == 0 {
		t.Errorf("expected non-zero coordinates for 8.8.8.8, got %f/%f", locGoogle.Latitude, locGoogle.Longitude)
	}

	locCF := res.Lookup("1.1.1.1")
	if locCF.CountryCode != "AU" || locCF.CountryName != "Australia" || locCF.Region != "Victoria" {
		t.Errorf("expected 1.1.1.1 to resolve to AU / Victoria, got %+v", locCF)
	}
	if locCF.Latitude == 0 || locCF.Longitude == 0 {
		t.Errorf("expected non-zero coordinates for 1.1.1.1, got %f/%f", locCF.Latitude, locCF.Longitude)
	}
}

func TestCountryName(t *testing.T) {
	if CountryName("TR") != "Turkey" {
		t.Errorf("expected TR -> Turkey, got %q", CountryName("TR"))
	}
	if CountryName("US") != "United States" {
		t.Errorf("expected US -> United States, got %q", CountryName("US"))
	}
	if CountryName("LOCAL") != "Local Network" {
		t.Errorf("expected LOCAL -> Local Network, got %q", CountryName("LOCAL"))
	}
}
