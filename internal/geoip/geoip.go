// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package geoip provides IP-to-location resolution for client sessions, resolving
// country code, country name, region, and city. It supports dual-database cascading
// (first DB-IP City Lite, fallback to MaxMind GeoLite2-City), edge reverse proxy headers
// (Cloudflare, Nginx), local/private network identification, and daily automatic updates
// from wp-statistics/geo CDN endpoints.
package geoip

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang"
)

const (
	// DBIPCityLiteURL is the CDN download endpoint for DB-IP City Lite (wp-statistics/geo).
	DBIPCityLiteURL = "https://cdn.jsdelivr.net/npm/dbip-city-lite/dbip-city-lite.mmdb.gz"
	// GeoLite2CityURL is the CDN download endpoint for MaxMind GeoLite2-City (wp-statistics/geo).
	GeoLite2CityURL = "https://cdn.jsdelivr.net/npm/geolite2-city/GeoLite2-City.mmdb.gz"

	DBIPFileName     = "dbip-city-lite.mmdb"
	GeoLite2FileName = "GeoLite2-City.mmdb"
)

// Location holds resolved geographical attributes for a client.
type Location struct {
	CountryCode string  `json:"country_code"`
	CountryName string  `json:"country_name"`
	Region      string  `json:"region"`
	RegionCode  string  `json:"region_code"`
	City        string  `json:"city"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
}

// Options configures the GeoIP resolver.
type Options struct {
	DBPath         string // Legacy single DB path override
	PrimaryDBPath  string // Path to DB-IP City Lite mmdb
	FallbackDBPath string // Path to GeoLite2-City mmdb
	DataDir        string
	DevCountry     string
	DevRegion      string
	DevCity        string
	DevLatitude    float64
	DevLongitude   float64
}

// Resolver resolves IP addresses to countries, regions, and cities.
type Resolver struct {
	mu           sync.RWMutex
	primaryDB    *geoip2.Reader // DB-IP City Lite (first priority)
	fallbackDB   *geoip2.Reader // MaxMind GeoLite2-City (fallback)
	dataDir      string
	devCountry   string
	devRegion    string
	devCity      string
	devLatitude  float64
	devLongitude float64
	client       *http.Client
}

// New initializes a GeoIP Resolver. If DB-IP or MaxMind MMDB files are found at
// the given paths or in the data directory, they will be loaded. Otherwise it
// gracefully falls back to header hints, private IP detection, and built-in dictionary lookups.
func New(opts Options) *Resolver {
	r := &Resolver{
		dataDir:      opts.DataDir,
		devCountry:   strings.ToUpper(strings.TrimSpace(opts.DevCountry)),
		devRegion:    strings.TrimSpace(opts.DevRegion),
		devCity:      strings.TrimSpace(opts.DevCity),
		devLatitude:  opts.DevLatitude,
		devLongitude: opts.DevLongitude,
		client:       &http.Client{Timeout: 90 * time.Second},
	}

	r.loadDatabases(opts)
	return r
}

func (r *Resolver) loadDatabases(opts Options) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Primary DB (DB-IP City Lite)
	primaryPath := opts.PrimaryDBPath
	if primaryPath == "" && opts.DataDir != "" {
		p := filepath.Join(opts.DataDir, "geoip", DBIPFileName)
		if _, err := os.Stat(p); err == nil {
			primaryPath = p
		} else {
			// Check direct dataDir
			p2 := filepath.Join(opts.DataDir, DBIPFileName)
			if _, err := os.Stat(p2); err == nil {
				primaryPath = p2
			}
		}
	}
	if primaryPath != "" {
		if reader, err := geoip2.Open(primaryPath); err == nil {
			if r.primaryDB != nil {
				_ = r.primaryDB.Close()
			}
			r.primaryDB = reader
			log.Printf("geoip: loaded primary DB-IP database from %s", primaryPath)
		} else {
			log.Printf("geoip: failed to open primary DB %s: %v", primaryPath, err)
		}
	}

	// 2. Fallback DB (GeoLite2-City)
	fallbackPath := opts.FallbackDBPath
	if fallbackPath == "" {
		fallbackPath = opts.DBPath
	}
	if fallbackPath == "" && opts.DataDir != "" {
		p := filepath.Join(opts.DataDir, "geoip", GeoLite2FileName)
		if _, err := os.Stat(p); err == nil {
			fallbackPath = p
		} else {
			p2 := filepath.Join(opts.DataDir, GeoLite2FileName)
			if _, err := os.Stat(p2); err == nil {
				fallbackPath = p2
			}
		}
	}

	// Also search standard fallback paths if still empty
	if fallbackPath == "" {
		searchPaths := []string{
			"./GeoLite2-City.mmdb",
			"/usr/share/GeoIP/GeoLite2-City.mmdb",
			"/data/GeoLite2-City.mmdb",
		}
		for _, p := range searchPaths {
			if _, err := os.Stat(p); err == nil {
				fallbackPath = p
				break
			}
		}
	}

	if fallbackPath != "" {
		if reader, err := geoip2.Open(fallbackPath); err == nil {
			if r.fallbackDB != nil {
				_ = r.fallbackDB.Close()
			}
			r.fallbackDB = reader
			log.Printf("geoip: loaded fallback GeoLite2 database from %s", fallbackPath)
		} else {
			log.Printf("geoip: failed to open fallback DB %s: %v", fallbackPath, err)
		}
	}
}

// Lookup resolves country code, country name, region, and city for the given IP address
// and optional HTTP headers map.
// Order of resolution:
//  1. Edge proxy headers (Cloudflare, Nginx)
//  2. Private / local / Docker network detection
//  3. Primary database: DB-IP City Lite
//  4. Fallback database: GeoLite2-City (if country/city missing)
//  5. Built-in well-known IP catalog
func (r *Resolver) Lookup(ipStr string, headerMaps ...map[string]string) Location {
	var loc Location
	trimmedIP := strings.TrimSpace(ipStr)

	// Step 1: Check proxy / edge headers (e.g. Cloudflare CF-IPCountry, CF-IPCity, CF-Region)
	if len(headerMaps) > 0 && headerMaps[0] != nil {
		h := headerMaps[0]
		for k, v := range h {
			val := strings.TrimSpace(v)
			if val == "" {
				continue
			}
			switch strings.ToLower(k) {
			case "cf-ipcountry", "x-country-code", "x-geoip-country":
				upper := strings.ToUpper(val)
				if upper != "XX" && upper != "T1" && loc.CountryCode == "" {
					loc.CountryCode = upper
					loc.CountryName = CountryName(upper)
				}
			case "cf-ipcity", "x-city", "x-geoip-city":
				if loc.City == "" {
					loc.City = val
				}
			case "cf-region", "x-region", "x-geoip-region":
				if loc.Region == "" {
					loc.Region = val
				}
			case "cf-region-code", "x-region-code":
				if loc.RegionCode == "" {
					loc.RegionCode = strings.ToUpper(val)
				}
			case "cf-iplatitude", "x-latitude":
				if lat, err := strconv.ParseFloat(val, 64); err == nil && loc.Latitude == 0 {
					loc.Latitude = lat
				}
			case "cf-iplongitude", "x-longitude":
				if lon, err := strconv.ParseFloat(val, 64); err == nil && loc.Longitude == 0 {
					loc.Longitude = lon
				}
			}
		}
		if loc.CountryCode != "" && loc.City != "" && loc.Region != "" {
			return loc
		}
	}

	if trimmedIP == "" {
		return loc
	}

	parsedIP := net.ParseIP(trimmedIP)
	if parsedIP == nil {
		// Could be "localhost"
		if strings.EqualFold(trimmedIP, "localhost") {
			return r.resolveLocal(loc)
		}
		return loc
	}

	// Step 2: Private / loopback / Docker network detection (e.g. 172.21.0.1)
	if isPrivateOrLocal(parsedIP) {
		return r.resolveLocal(loc)
	}

	// Read readers with read lock
	r.mu.RLock()
	primary := r.primaryDB
	fallback := r.fallbackDB
	r.mu.RUnlock()

	// Step 3: First search in DB-IP City Lite (Primary)
	if primary != nil {
		if rec, err := primary.City(parsedIP); err == nil && rec != nil {
			if loc.CountryCode == "" && rec.Country.IsoCode != "" {
				loc.CountryCode = rec.Country.IsoCode
				if name, ok := rec.Country.Names["en"]; ok && name != "" {
					loc.CountryName = name
				} else {
					loc.CountryName = CountryName(loc.CountryCode)
				}
			}
			if loc.Region == "" && len(rec.Subdivisions) > 0 {
				loc.RegionCode = rec.Subdivisions[0].IsoCode
				if name, ok := rec.Subdivisions[0].Names["en"]; ok && name != "" {
					loc.Region = name
				} else if loc.RegionCode != "" {
					loc.Region = loc.RegionCode
				}
			}
			if loc.City == "" {
				if name, ok := rec.City.Names["en"]; ok && name != "" {
					loc.City = name
				}
			}
			if loc.Latitude == 0 && loc.Longitude == 0 && (rec.Location.Latitude != 0 || rec.Location.Longitude != 0) {
				loc.Latitude = rec.Location.Latitude
				loc.Longitude = rec.Location.Longitude
			}
		}
	}

	// Step 4: Fallback to GeoLite2-City if country or city was not found in DB-IP
	if (loc.CountryCode == "" || loc.City == "") && fallback != nil {
		if rec, err := fallback.City(parsedIP); err == nil && rec != nil {
			if loc.CountryCode == "" && rec.Country.IsoCode != "" {
				loc.CountryCode = rec.Country.IsoCode
				if name, ok := rec.Country.Names["en"]; ok && name != "" {
					loc.CountryName = name
				} else {
					loc.CountryName = CountryName(loc.CountryCode)
				}
			}
			if loc.Region == "" && len(rec.Subdivisions) > 0 {
				loc.RegionCode = rec.Subdivisions[0].IsoCode
				if name, ok := rec.Subdivisions[0].Names["en"]; ok && name != "" {
					loc.Region = name
				} else if loc.RegionCode != "" {
					loc.Region = loc.RegionCode
				}
			}
			if loc.City == "" {
				if name, ok := rec.City.Names["en"]; ok && name != "" {
					loc.City = name
				}
			}
			if loc.Latitude == 0 && loc.Longitude == 0 && (rec.Location.Latitude != 0 || rec.Location.Longitude != 0) {
				loc.Latitude = rec.Location.Latitude
				loc.Longitude = rec.Location.Longitude
			}
		}
	}

	if loc.CountryCode != "" && (loc.City != "" || loc.Region != "") {
		return loc
	}

	// Step 5: Built-in anycast & well-known public test ranges
	if known, ok := wellKnownIPs[trimmedIP]; ok {
		if loc.CountryCode == "" {
			loc.CountryCode = known.CountryCode
			loc.CountryName = known.CountryName
		}
		if loc.Region == "" {
			loc.Region = known.Region
			loc.RegionCode = known.RegionCode
		}
		if loc.City == "" {
			loc.City = known.City
		}
		if loc.Latitude == 0 && loc.Longitude == 0 {
			loc.Latitude = known.Latitude
			loc.Longitude = known.Longitude
		}
		return loc
	}

	// If country code was set from header but name was missing, fill it
	if loc.CountryCode != "" && loc.CountryName == "" {
		loc.CountryName = CountryName(loc.CountryCode)
	}

	return loc
}

func (r *Resolver) resolveLocal(current Location) Location {
	loc := current
	if r.devCountry != "" {
		if loc.CountryCode == "" {
			loc.CountryCode = r.devCountry
			loc.CountryName = CountryName(r.devCountry)
		}
		if loc.Region == "" {
			if r.devRegion != "" {
				loc.Region = r.devRegion
			} else {
				loc.Region = "Local Region"
			}
		}
		if loc.City == "" {
			if r.devCity != "" {
				loc.City = r.devCity
			} else {
				loc.City = "Local"
			}
		}
		if loc.Latitude == 0 && loc.Longitude == 0 && (r.devLatitude != 0 || r.devLongitude != 0) {
			loc.Latitude = r.devLatitude
			loc.Longitude = r.devLongitude
		}
		return loc
	}

	if loc.CountryCode == "" {
		loc.CountryCode = "LOCAL"
		loc.CountryName = "Local Network"
	}
	if loc.Region == "" {
		loc.Region = "Local Region"
	}
	if loc.City == "" {
		loc.City = "Local"
	}
	return loc
}

func isPrivateOrLocal(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 10 {
			return true
		}
		if v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31 {
			return true
		}
		if v4[0] == 192 && v4[1] == 168 {
			return true
		}
		if v4[0] == 127 {
			return true
		}
	}
	return false
}

// CheckAndApplyUpdates checks the wp-statistics CDN endpoints for newer versions of
// DB-IP City Lite and GeoLite2-City databases, downloads and extracts them if updated,
// and atomically reloads the readers into the resolver.
func (r *Resolver) CheckAndApplyUpdates(ctx context.Context) error {
	dir := r.dataDir
	if dir == "" {
		dir = "./data"
	}
	geoDir := filepath.Join(dir, "geoip")
	if err := os.MkdirAll(geoDir, 0755); err != nil {
		return fmt.Errorf("create geoip dir: %w", err)
	}

	var updateErrors []string

	// 1. Update DB-IP City Lite
	primaryTarget := filepath.Join(geoDir, DBIPFileName)
	if err := r.syncDatabaseFile(ctx, DBIPCityLiteURL, primaryTarget); err != nil {
		updateErrors = append(updateErrors, fmt.Sprintf("dbip update: %v", err))
	}

	// 2. Update GeoLite2-City
	fallbackTarget := filepath.Join(geoDir, GeoLite2FileName)
	if err := r.syncDatabaseFile(ctx, GeoLite2CityURL, fallbackTarget); err != nil {
		updateErrors = append(updateErrors, fmt.Sprintf("geolite2 update: %v", err))
	}

	// Reload readers if any file exists
	r.loadDatabases(Options{
		PrimaryDBPath:  primaryTarget,
		FallbackDBPath: fallbackTarget,
		DataDir:        r.dataDir,
		DevCountry:     r.devCountry,
		DevRegion:      r.devRegion,
		DevCity:        r.devCity,
	})

	if len(updateErrors) > 0 {
		return fmt.Errorf("geoip update warnings: %s", strings.Join(updateErrors, "; "))
	}
	return nil
}

// syncDatabaseFile checks whether remote URL has an update using ETag/Last-Modified,
// downloads .mmdb.gz, decompresses gzip into target file atomically.
func (r *Resolver) syncDatabaseFile(ctx context.Context, downloadURL, targetFile string) error {
	metaFile := targetFile + ".meta"
	savedETag := ""
	if b, err := os.ReadFile(metaFile); err == nil {
		savedETag = strings.TrimSpace(string(b))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("head request: %w", err)
	}
	if savedETag != "" {
		req.Header.Set("If-None-Match", savedETag)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("do head: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		// No update needed
		return nil
	}

	// Check if already exists and no new etag
	remoteETag := resp.Header.Get("ETag")
	if remoteETag != "" && remoteETag == savedETag {
		if _, err := os.Stat(targetFile); err == nil {
			return nil
		}
	}

	log.Printf("geoip: downloading database update from %s ...", downloadURL)

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("get request: %w", err)
	}
	getResp, err := r.client.Do(getReq)
	if err != nil {
		return fmt.Errorf("do get: %w", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode < 200 || getResp.StatusCode >= 300 {
		return fmt.Errorf("download returned status %d", getResp.StatusCode)
	}

	gzReader, err := gzip.NewReader(getResp.Body)
	if err != nil {
		return fmt.Errorf("create gzip reader: %w", err)
	}
	defer gzReader.Close()

	tmpFile := targetFile + ".tmp"
	out, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open tmp file: %w", err)
	}

	if _, err := io.Copy(out, gzReader); err != nil {
		out.Close()
		_ = os.Remove(tmpFile)
		return fmt.Errorf("decompress error: %w", err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(tmpFile)
		return err
	}
	out.Close()

	// Verify the database can be opened
	testReader, err := geoip2.Open(tmpFile)
	if err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("verify database failed: %w", err)
	}
	_ = testReader.Close()

	// Atomic rename
	if err := os.Rename(tmpFile, targetFile); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("rename to target file: %w", err)
	}

	// Write new meta
	newETag := getResp.Header.Get("ETag")
	if newETag != "" {
		_ = os.WriteFile(metaFile, []byte(newETag), 0644)
	}

	log.Printf("geoip: successfully installed database update to %s", targetFile)
	return nil
}

// Close releases resources associated with the open databases.
func (r *Resolver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []string
	if r.primaryDB != nil {
		if err := r.primaryDB.Close(); err != nil {
			errs = append(errs, err.Error())
		}
		r.primaryDB = nil
	}
	if r.fallbackDB != nil {
		if err := r.fallbackDB.Close(); err != nil {
			errs = append(errs, err.Error())
		}
		r.fallbackDB = nil
	}
	if len(errs) > 0 {
		return fmt.Errorf("close geoip readers: %s", strings.Join(errs, ", "))
	}
	return nil
}

// CountryName returns the common English name for an ISO 3166-1 alpha-2 code.
func CountryName(code string) string {
	upper := strings.ToUpper(strings.TrimSpace(code))
	if name, ok := isoCountries[upper]; ok {
		return name
	}
	if upper == "LOCAL" {
		return "Local Network"
	}
	return upper
}

var wellKnownIPs = map[string]Location{
	"8.8.8.8":        {CountryCode: "US", CountryName: "United States", Region: "California", RegionCode: "CA", City: "Mountain View", Latitude: 37.422, Longitude: -122.084},
	"8.8.4.4":        {CountryCode: "US", CountryName: "United States", Region: "California", RegionCode: "CA", City: "Mountain View", Latitude: 37.422, Longitude: -122.084},
	"1.1.1.1":        {CountryCode: "AU", CountryName: "Australia", Region: "Victoria", RegionCode: "VIC", City: "Melbourne", Latitude: -37.8136, Longitude: 144.9631},
	"1.0.0.1":        {CountryCode: "AU", CountryName: "Australia", Region: "Victoria", RegionCode: "VIC", City: "Melbourne", Latitude: -37.8136, Longitude: 144.9631},
	"9.9.9.9":        {CountryCode: "US", CountryName: "United States", Region: "California", RegionCode: "CA", City: "Berkeley", Latitude: 37.8716, Longitude: -122.2727},
	"208.67.222.222": {CountryCode: "US", CountryName: "United States", Region: "California", RegionCode: "CA", City: "San Francisco", Latitude: 37.7749, Longitude: -122.4194},
	"208.67.220.220": {CountryCode: "US", CountryName: "United States", Region: "California", RegionCode: "CA", City: "San Francisco", Latitude: 37.7749, Longitude: -122.4194},
}

var isoCountries = map[string]string{
	"AF": "Afghanistan",
	"AL": "Albania",
	"DZ": "Algeria",
	"AS": "American Samoa",
	"AD": "Andorra",
	"AO": "Angola",
	"AI": "Anguilla",
	"AQ": "Antarctica",
	"AG": "Antigua and Barbuda",
	"AR": "Argentina",
	"AM": "Armenia",
	"AW": "Aruba",
	"AU": "Australia",
	"AT": "Austria",
	"AZ": "Azerbaijan",
	"BS": "Bahamas",
	"BH": "Bahrain",
	"BD": "Bangladesh",
	"BB": "Barbados",
	"BY": "Belarus",
	"BE": "Belgium",
	"BZ": "Belize",
	"BJ": "Benin",
	"BM": "Bermuda",
	"BT": "Bhutan",
	"BO": "Bolivia",
	"BA": "Bosnia and Herzegovina",
	"BW": "Botswana",
	"BR": "Brazil",
	"IO": "British Indian Ocean Territory",
	"BN": "Brunei",
	"BG": "Bulgaria",
	"BF": "Burkina Faso",
	"BI": "Burundi",
	"KH": "Cambodia",
	"CM": "Cameroon",
	"CA": "Canada",
	"CV": "Cape Verde",
	"KY": "Cayman Islands",
	"CF": "Central African Republic",
	"TD": "Chad",
	"CL": "Chile",
	"CN": "China",
	"CO": "Colombia",
	"KM": "Comoros",
	"CG": "Congo",
	"CD": "DR Congo",
	"CR": "Costa Rica",
	"CI": "Ivory Coast",
	"HR": "Croatia",
	"CU": "Cuba",
	"CY": "Cyprus",
	"CZ": "Czechia",
	"DK": "Denmark",
	"DJ": "Djibouti",
	"DM": "Dominica",
	"DO": "Dominican Republic",
	"EC": "Ecuador",
	"EG": "Egypt",
	"SV": "El Salvador",
	"GQ": "Equatorial Guinea",
	"ER": "Eritrea",
	"EE": "Estonia",
	"SZ": "Eswatini",
	"ET": "Ethiopia",
	"FJ": "Fiji",
	"FI": "Finland",
	"FR": "France",
	"GA": "Gabon",
	"GM": "Gambia",
	"GE": "Georgia",
	"DE": "Germany",
	"GH": "Ghana",
	"GR": "Greece",
	"GD": "Grenada",
	"GT": "Guatemala",
	"GN": "Guinea",
	"GW": "Guinea-Bissau",
	"GY": "Guyana",
	"HT": "Haiti",
	"HN": "Honduras",
	"HK": "Hong Kong",
	"HU": "Hungary",
	"IS": "Iceland",
	"IN": "India",
	"ID": "Indonesia",
	"IR": "Iran",
	"IQ": "Iraq",
	"IE": "Ireland",
	"IL": "Israel",
	"IT": "Italy",
	"JM": "Jamaica",
	"JP": "Japan",
	"JO": "Jordan",
	"KZ": "Kazakhstan",
	"KE": "Kenya",
	"KR": "South Korea",
	"KW": "Kuwait",
	"KG": "Kyrgyzstan",
	"LV": "Latvia",
	"LB": "Lebanon",
	"LS": "Lesotho",
	"LR": "Liberia",
	"LY": "Libya",
	"LI": "Liechtenstein",
	"LT": "Lithuania",
	"LU": "Luxembourg",
	"MG": "Madagascar",
	"MW": "Malawi",
	"MY": "Malaysia",
	"MV": "Maldives",
	"ML": "Mali",
	"MT": "Malta",
	"MX": "Mexico",
	"MD": "Moldova",
	"MC": "Monaco",
	"MN": "Mongolia",
	"ME": "Montenegro",
	"MA": "Morocco",
	"MZ": "Mozambique",
	"MM": "Myanmar",
	"NA": "Namibia",
	"NP": "Nepal",
	"NL": "Netherlands",
	"NZ": "New Zealand",
	"NI": "Nicaragua",
	"NE": "Niger",
	"NG": "Nigeria",
	"MK": "North Macedonia",
	"NO": "Norway",
	"OM": "Oman",
	"PK": "Pakistan",
	"PS": "Palestine",
	"PA": "Panama",
	"PG": "Papua New Guinea",
	"PY": "Paraguay",
	"PE": "Peru",
	"PH": "Philippines",
	"PL": "Poland",
	"PT": "Portugal",
	"QA": "Qatar",
	"RO": "Romania",
	"RU": "Russia",
	"RW": "Rwanda",
	"SA": "Saudi Arabia",
	"SN": "Senegal",
	"RS": "Serbia",
	"SG": "Singapore",
	"SK": "Slovakia",
	"SI": "Slovenia",
	"SO": "Somalia",
	"ZA": "South Africa",
	"ES": "Spain",
	"LK": "Sri Lanka",
	"SD": "Sudan",
	"SE": "Sweden",
	"CH": "Switzerland",
	"SY": "Syria",
	"TW": "Taiwan",
	"TJ": "Tajikistan",
	"TZ": "Tanzania",
	"TH": "Thailand",
	"TN": "Tunisia",
	"TR": "Turkey",
	"TM": "Turkmenistan",
	"UG": "Uganda",
	"UA": "Ukraine",
	"AE": "United Arab Emirates",
	"GB": "United Kingdom",
	"US": "United States",
	"UY": "Uruguay",
	"UZ": "Uzbekistan",
	"VE": "Venezuela",
	"VN": "Vietnam",
	"YE": "Yemen",
	"ZM": "Zambia",
	"ZW": "Zimbabwe",
}
