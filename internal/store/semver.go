package store

import (
	"strconv"
	"strings"
)

// CompareVersions compares two version strings (e.g. "1.2.3", "v1.2.4", "0.9.0-rc1").
// Returns 1 if v1 > v2, -1 if v1 < v2, and 0 if v1 == v2.
func CompareVersions(v1, v2 string) int {
	v1 = strings.TrimSpace(v1)
	v2 = strings.TrimSpace(v2)
	if v1 == v2 {
		return 0
	}
	if v1 == "" {
		return -1
	}
	if v2 == "" {
		return 1
	}

	// Strip leading 'v' or 'V'
	v1Clean := strings.TrimPrefix(strings.TrimPrefix(v1, "v"), "V")
	v2Clean := strings.TrimPrefix(strings.TrimPrefix(v2, "v"), "V")

	// Strip build metadata e.g. +build123
	if idx := strings.IndexByte(v1Clean, '+'); idx != -1 {
		v1Clean = v1Clean[:idx]
	}
	if idx := strings.IndexByte(v2Clean, '+'); idx != -1 {
		v2Clean = v2Clean[:idx]
	}

	// Split pre-release
	var pre1, pre2 string
	if idx := strings.IndexByte(v1Clean, '-'); idx != -1 {
		pre1 = v1Clean[idx+1:]
		v1Clean = v1Clean[:idx]
	}
	if idx := strings.IndexByte(v2Clean, '-'); idx != -1 {
		pre2 = v2Clean[idx+1:]
		v2Clean = v2Clean[:idx]
	}

	parts1 := strings.Split(v1Clean, ".")
	parts2 := strings.Split(v2Clean, ".")

	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int64
		var err1, err2 error
		var p1, p2 string

		if i < len(parts1) {
			p1 = parts1[i]
			n1, err1 = strconv.ParseInt(p1, 10, 64)
		}
		if i < len(parts2) {
			p2 = parts2[i]
			n2, err2 = strconv.ParseInt(p2, 10, 64)
		}

		if err1 == nil && err2 == nil {
			if n1 < n2 {
				return -1
			}
			if n1 > n2 {
				return 1
			}
		} else {
			if p1 < p2 {
				return -1
			}
			if p1 > p2 {
				return 1
			}
		}
	}

	// If numeric parts are equal, handle pre-release:
	// A version without a pre-release is greater than one with a pre-release (e.g. 1.0.0 > 1.0.0-rc1)
	if pre1 == "" && pre2 != "" {
		return 1
	}
	if pre1 != "" && pre2 == "" {
		return -1
	}
	if pre1 != "" && pre2 != "" {
		if pre1 < pre2 {
			return -1
		}
		if pre1 > pre2 {
			return 1
		}
	}

	return 0
}
