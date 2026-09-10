// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	reEdge    = regexp.MustCompile(`Edg(?:e|A|iOS)?/([0-9.]+)`)
	reOpera   = regexp.MustCompile(`(?:OPR|Opera)/([0-9.]+)`)
	reSamsung = regexp.MustCompile(`SamsungBrowser/([0-9.]+)`)
	reFirefox = regexp.MustCompile(`Firefox/([0-9.]+)`)
	reChrome  = regexp.MustCompile(`(?:Chrome|CriOS)/([0-9.]+)`)
	reSafari  = regexp.MustCompile(`Version/([0-9.]+)`)

	reWinVer = regexp.MustCompile(`Windows NT ([0-9.]+)`)
	reMacVer = regexp.MustCompile(`Mac OS X ([0-9_]+)`)
	reAndVer = regexp.MustCompile(`Android ([0-9.]+)`)
	reIOSVer = regexp.MustCompile(`(?:iPhone|iPad|iPod).*OS ([0-9_]+)`)
)

type ParsedDevice struct {
	Platform         string
	PlatformCategory string
	Release          string
	Browser          string
	BrowserVersion   string
	OS               string
	OSVersion        string
	Kernel           string
	KernelVersion    string
	UA               string
}

// EnrichDeviceJSON parses, normalizes, and enriches raw device JSON from envelopes.
// It fills in platform category (desktop, mobile, web), browser name & version,
// operating system & version, kernel details, and architecture when not already provided.
func EnrichDeviceJSON(raw string) (string, ParsedDevice) {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		m = make(map[string]any)
	}

	getStr := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}

	platform := getStr("platform")
	category := getStr("platform_category")
	release := getStr("release")
	browser := getStr("browser")
	browserVer := getStr("browser_version")
	os := getStr("os")
	osVer := getStr("os_version")
	kernel := getStr("kernel")
	kernelVer := getStr("kernel_version")
	ua := getStr("user_agent")
	arch := getStr("arch")

	// Deduce platform category if empty
	if category == "" {
		pLower := strings.ToLower(platform)
		switch {
		case pLower == "web":
			category = "web"
		case pLower == "android" || pLower == "ios" || pLower == "fuchsia":
			category = "mobile"
		case pLower == "linux" || pLower == "macos" || pLower == "windows":
			category = "desktop"
		case strings.Contains(ua, "Mobile") || strings.Contains(ua, "Android") || strings.Contains(ua, "iPhone"):
			category = "mobile"
		case ua != "":
			category = "web"
		default:
			category = "desktop"
		}
		m["platform_category"] = category
	}

	// Browser name & version parsing from UA if missing
	if ua != "" {
		if browser == "" || browser == "web" || browser == "Browser" {
			switch {
			case reEdge.MatchString(ua):
				browser = "Edge"
			case reOpera.MatchString(ua):
				browser = "Opera"
			case reSamsung.MatchString(ua):
				browser = "Samsung"
			case reFirefox.MatchString(ua):
				browser = "Firefox"
			case reChrome.MatchString(ua):
				browser = "Chrome"
			case strings.Contains(ua, "Safari/"):
				browser = "Safari"
			}
			if browser != "" {
				m["browser"] = browser
			}
		}

		if browserVer == "" {
			switch browser {
			case "Edge":
				if match := reEdge.FindStringSubmatch(ua); len(match) > 1 {
					browserVer = match[1]
				}
			case "Opera":
				if match := reOpera.FindStringSubmatch(ua); len(match) > 1 {
					browserVer = match[1]
				}
			case "Samsung":
				if match := reSamsung.FindStringSubmatch(ua); len(match) > 1 {
					browserVer = match[1]
				}
			case "Firefox":
				if match := reFirefox.FindStringSubmatch(ua); len(match) > 1 {
					browserVer = match[1]
				}
			case "Chrome":
				if match := reChrome.FindStringSubmatch(ua); len(match) > 1 {
					browserVer = match[1]
				}
			case "Safari":
				if match := reSafari.FindStringSubmatch(ua); len(match) > 1 {
					browserVer = match[1]
				}
			}
			if browserVer != "" {
				m["browser_version"] = browserVer
			}
		}

		// OS & OS Version from UA if missing
		if os == "" || os == "web" {
			switch {
			case strings.Contains(ua, "Windows"):
				os = "Windows"
				if match := reWinVer.FindStringSubmatch(ua); len(match) > 1 {
					if match[1] == "10.0" {
						osVer = "10/11"
					} else {
						osVer = match[1]
					}
				}
			case strings.Contains(ua, "Mac OS X"):
				os = "macOS"
				if match := reMacVer.FindStringSubmatch(ua); len(match) > 1 {
					osVer = strings.ReplaceAll(match[1], "_", ".")
				}
			case strings.Contains(ua, "Android"):
				os = "Android"
				if match := reAndVer.FindStringSubmatch(ua); len(match) > 1 {
					osVer = match[1]
				}
			case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad") || strings.Contains(ua, "iPod"):
				os = "iOS"
				if match := reIOSVer.FindStringSubmatch(ua); len(match) > 1 {
					osVer = strings.ReplaceAll(match[1], "_", ".")
				}
			case strings.Contains(ua, "Ubuntu"):
				os = "Ubuntu"
			case strings.Contains(ua, "Linux") || strings.Contains(ua, "X11"):
				os = "Linux"
			case strings.Contains(ua, "CrOS"):
				os = "ChromeOS"
			}
			if os != "" {
				m["os"] = os
			}
			if osVer != "" {
				m["os_version"] = osVer
			}
		}

		if arch == "" {
			if strings.Contains(ua, "x86_64") || strings.Contains(ua, "Win64") || strings.Contains(ua, "WOW64") || strings.Contains(ua, "x64") {
				arch = "x86_64"
				m["arch"] = arch
			} else if strings.Contains(ua, "arm64") || strings.Contains(ua, "aarch64") {
				arch = "arm64"
				m["arch"] = arch
			}
		}
	}

	b, err := json.Marshal(m)
	resJSON := raw
	if err == nil {
		resJSON = string(b)
	}

	return resJSON, ParsedDevice{
		Platform:         platform,
		PlatformCategory: category,
		Release:          release,
		Browser:          browser,
		BrowserVersion:   browserVer,
		OS:               os,
		OSVersion:        osVer,
		Kernel:           kernel,
		KernelVersion:    kernelVer,
		UA:               ua,
	}
}
