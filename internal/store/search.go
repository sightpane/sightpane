// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"sightpane/internal/apierr"
)

type searchToken struct {
	Key   string
	Value string
}

// tokenizeSearch splits a search query string into key-value pairs or text words,
// respecting single and double quotes.
func tokenizeSearch(q string) []searchToken {
	var tokens []searchToken
	runes := []rune(strings.TrimSpace(q))
	n := len(runes)
	i := 0

	for i < n {
		// Skip whitespace
		for i < n && unicode.IsSpace(runes[i]) {
			i++
		}
		if i >= n {
			break
		}

		// Read token
		var sb strings.Builder
		inQuote := rune(0)
		hasColon := false
		colonPos := -1

		for i < n {
			r := runes[i]
			if inQuote != 0 {
				if r == inQuote {
					inQuote = 0
				} else {
					sb.WriteRune(r)
				}
				i++
				continue
			}

			if r == '"' || r == '\'' {
				inQuote = r
				i++
				continue
			}

			if unicode.IsSpace(r) {
				break
			}

			if r == ':' && !hasColon {
				hasColon = true
				colonPos = sb.Len()
			}

			sb.WriteRune(r)
			i++
		}

		raw := sb.String()
		if raw == "" {
			continue
		}

		if hasColon && colonPos > 0 {
			key := raw[:colonPos]
			val := raw[colonPos+1:]
			tokens = append(tokens, searchToken{
				Key:   strings.ToLower(key),
				Value: val,
			})
		} else {
			tokens = append(tokens, searchToken{
				Key:   "",
				Value: raw,
			})
		}
	}

	return tokens
}

func parseDate(val string) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, val); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date %q", val)
}

// namesCategory reports whether the search picks a platform category itself;
// ListSessions then lets it choose instead of hiding server processes.
func namesCategory(q string) bool {
	for _, tok := range tokenizeSearch(q) {
		if tok.Key == "category" || tok.Key == "platform_category" {
			return true
		}
	}
	return false
}

// BuildSessionSearchWhere generates SQL filter expressions and adds parameters using nextPlaceholder.
func BuildSessionSearchWhere(q string, nextPlaceholder func(any) string) (string, error) {
	tokens := tokenizeSearch(q)
	if len(tokens) == 0 {
		return "", nil
	}

	var clauses []string

	for _, tok := range tokens {
		if tok.Key == "" {
			// Free text search across user, route, ip, location
			p := "%" + tok.Value + "%"
			ph := nextPlaceholder(p)
			clauses = append(clauses, fmt.Sprintf("(user_id ILIKE %s OR current_route ILIKE %s OR ip ILIKE %s OR country_code ILIKE %s OR country_name ILIKE %s OR city ILIKE %s OR user_json::text ILIKE %s OR device_json::text ILIKE %s)", ph, ph, ph, ph, ph, ph, ph, ph))
			continue
		}

		switch {
		case tok.Key == "release":
			ph := nextPlaceholder(tok.Value)
			clauses = append(clauses, fmt.Sprintf("release = %s", ph))
		case tok.Key == "country" || tok.Key == "country_code":
			ph := nextPlaceholder(strings.ToUpper(tok.Value))
			phName := nextPlaceholder("%" + tok.Value + "%")
			clauses = append(clauses, fmt.Sprintf("(country_code = %s OR country_name ILIKE %s)", ph, phName))
		case tok.Key == "city":
			ph := nextPlaceholder("%" + tok.Value + "%")
			clauses = append(clauses, fmt.Sprintf("city ILIKE %s", ph))
		case tok.Key == "region":
			ph := nextPlaceholder("%" + tok.Value + "%")
			clauses = append(clauses, fmt.Sprintf("region ILIKE %s", ph))
		case tok.Key == "platform":
			ph := nextPlaceholder(strings.ToLower(tok.Value))
			clauses = append(clauses, fmt.Sprintf("LOWER(platform) = %s", ph))
		case tok.Key == "category" || tok.Key == "platform_category":
			val := strings.ToLower(tok.Value)
			ph := nextPlaceholder(val)
			phLike := nextPlaceholder("%" + val + "%")
			clauses = append(clauses, fmt.Sprintf("(LOWER(platform) = %s OR LOWER(platform_category) = %s OR platform_category ILIKE %s)", ph, ph, phLike))
		case tok.Key == "os":
			ph := nextPlaceholder("%" + tok.Value + "%")
			phExact := nextPlaceholder(strings.ToLower(tok.Value))
			clauses = append(clauses, fmt.Sprintf("(device_json::jsonb->>'os' ILIKE %s OR LOWER(platform) = %s)", ph, phExact))
		case tok.Key == "kernel":
			ph := nextPlaceholder("%" + tok.Value + "%")
			clauses = append(clauses, fmt.Sprintf("device_json::jsonb->>'kernel' ILIKE %s", ph))
		case tok.Key == "browser_version":
			ph := nextPlaceholder("%" + tok.Value + "%")
			clauses = append(clauses, fmt.Sprintf("device_json::jsonb->>'browser_version' ILIKE %s", ph))
		case tok.Key == "arch":
			ph := nextPlaceholder("%" + tok.Value + "%")
			clauses = append(clauses, fmt.Sprintf("device_json::jsonb->>'arch' ILIKE %s", ph))
		case tok.Key == "browser":
			ph := nextPlaceholder("%" + tok.Value + "%")
			clauses = append(clauses, fmt.Sprintf("browser ILIKE %s", ph))
		case tok.Key == "route" || tok.Key == "current_route":
			ph := nextPlaceholder(tok.Value)
			clauses = append(clauses, fmt.Sprintf("current_route = %s", ph))
		case tok.Key == "user" || tok.Key == "user_id":
			ph := nextPlaceholder(tok.Value)
			phLike := nextPlaceholder("%" + tok.Value + "%")
			clauses = append(clauses, fmt.Sprintf("(user_id = %s OR user_json::jsonb->>'email' ILIKE %s OR user_json::jsonb->>'name' ILIKE %s)", ph, phLike, phLike))
		case tok.Key == "ip":
			ph := nextPlaceholder(tok.Value)
			clauses = append(clauses, fmt.Sprintf("ip = %s", ph))
		case tok.Key == "errors":
			v := strings.ToLower(tok.Value)
			if v == "true" || v == "1" {
				clauses = append(clauses, "error_count > 0")
			} else if v == "false" || v == "0" {
				clauses = append(clauses, "error_count = 0")
			} else {
				return "", apierr.New(400, apierr.CodeSearchInvalid, fmt.Sprintf("invalid boolean value for errors: %q", tok.Value))
			}
		case strings.HasPrefix(tok.Key, "props."):
			propName := strings.TrimPrefix(tok.Key, "props.")
			if propName == "" {
				return "", apierr.New(400, apierr.CodeSearchInvalid, "empty prop name in props. filter")
			}
			ph := nextPlaceholder(tok.Value)
			clauses = append(clauses, fmt.Sprintf("(props_json::jsonb->>'%s') = %s", strings.ReplaceAll(propName, "'", "''"), ph))
		case tok.Key == "after":
			t, err := parseDate(tok.Value)
			if err != nil {
				return "", apierr.New(400, apierr.CodeSearchInvalid, fmt.Sprintf("invalid after date: %s", tok.Value))
			}
			ph := nextPlaceholder(t)
			clauses = append(clauses, fmt.Sprintf("started_at >= %s", ph))
		case tok.Key == "before":
			t, err := parseDate(tok.Value)
			if err != nil {
				return "", apierr.New(400, apierr.CodeSearchInvalid, fmt.Sprintf("invalid before date: %s", tok.Value))
			}
			ph := nextPlaceholder(t)
			clauses = append(clauses, fmt.Sprintf("started_at <= %s", ph))
		default:
			return "", apierr.New(400, apierr.CodeSearchInvalid, fmt.Sprintf("unknown search filter key: %q", tok.Key))
		}
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(clauses, " AND "), nil
}

// BuildIssueSearchWhere generates SQL filter expressions for issues.
func BuildIssueSearchWhere(q string, nextPlaceholder func(any) string) (string, error) {
	tokens := tokenizeSearch(q)
	if len(tokens) == 0 {
		return "", nil
	}

	var clauses []string

	for _, tok := range tokens {
		if tok.Key == "" {
			p := "%" + tok.Value + "%"
			ph := nextPlaceholder(p)
			clauses = append(clauses, fmt.Sprintf("(i.title ILIKE %s OR i.exception ILIKE %s)", ph, ph))
			continue
		}

		switch tok.Key {
		case "exception":
			ph := nextPlaceholder("%" + tok.Value + "%")
			clauses = append(clauses, fmt.Sprintf("i.exception ILIKE %s", ph))
		case "title":
			ph := nextPlaceholder("%" + tok.Value + "%")
			clauses = append(clauses, fmt.Sprintf("i.title ILIKE %s", ph))
		case "status":
			st := strings.ToLower(tok.Value)
			if st != "open" && st != "resolved" && st != "ignored" && st != "snoozed" {
				return "", apierr.New(400, apierr.CodeSearchInvalid, fmt.Sprintf("invalid status: %q", tok.Value))
			}
			ph := nextPlaceholder(st)
			clauses = append(clauses, fmt.Sprintf("i.status = %s", ph))
		case "assignee":
			val := strings.ToLower(tok.Value)
			if val == "unassigned" || val == "none" {
				clauses = append(clauses, "i.assignee_user_id IS NULL")
			} else {
				ph := nextPlaceholder(tok.Value)
				clauses = append(clauses, fmt.Sprintf("u.email ILIKE %s", ph))
			}
		case "resolved":
			v := strings.ToLower(tok.Value)
			if v == "true" || v == "1" {
				clauses = append(clauses, "i.resolved = true")
			} else if v == "false" || v == "0" {
				clauses = append(clauses, "i.resolved = false")
			} else {
				return "", apierr.New(400, apierr.CodeSearchInvalid, fmt.Sprintf("invalid boolean for resolved: %q", tok.Value))
			}
		case "after":
			t, err := parseDate(tok.Value)
			if err != nil {
				return "", apierr.New(400, apierr.CodeSearchInvalid, fmt.Sprintf("invalid after date: %s", tok.Value))
			}
			ph := nextPlaceholder(t)
			clauses = append(clauses, fmt.Sprintf("i.last_seen >= %s", ph))
		case "before":
			t, err := parseDate(tok.Value)
			if err != nil {
				return "", apierr.New(400, apierr.CodeSearchInvalid, fmt.Sprintf("invalid before date: %s", tok.Value))
			}
			ph := nextPlaceholder(t)
			clauses = append(clauses, fmt.Sprintf("i.last_seen <= %s", ph))
		default:
			return "", apierr.New(400, apierr.CodeSearchInvalid, fmt.Sprintf("unknown search filter key: %q", tok.Key))
		}
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(clauses, " AND "), nil
}
