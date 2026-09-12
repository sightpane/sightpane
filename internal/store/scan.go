// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"fmt"
	"time"
)

// The JSON API hands out timestamps as RFC3339Nano strings in UTC, and has since
// before the database could store an instant. Postgres returns a time.Time from
// a TIMESTAMPTZ column, so these two adapt the column to the field rather than
// every model growing a second representation and every handler a conversion.

// tsCol scans a timestamp column into the RFC3339Nano string the API returns.
type tsCol struct{ dst *string }

func (t tsCol) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*t.dst = ""
	case time.Time:
		*t.dst = v.UTC().Format(time.RFC3339Nano)
	case string:
		*t.dst = v
	default:
		return fmt.Errorf("timestamp column: cannot scan %T", src)
	}
	return nil
}

// nullTSCol is tsCol for a nullable column: SQL NULL stays a nil pointer rather
// than becoming an empty string, because `ended_at` being absent is what marks a
// session as still open.
type nullTSCol struct{ dst **string }

func (t nullTSCol) Scan(src any) error {
	if src == nil {
		*t.dst = nil
		return nil
	}
	var s string
	if err := (tsCol{&s}).Scan(src); err != nil {
		return err
	}
	*t.dst = &s
	return nil
}

// nullFloatCol is for scanning nullable float columns (e.g. latitude, longitude).
// SQL NULL stays a nil pointer rather than 0.0.
type nullFloatCol struct{ dst **float64 }

func (f nullFloatCol) Scan(src any) error {
	if src == nil {
		*f.dst = nil
		return nil
	}
	switch v := src.(type) {
	case float64:
		*f.dst = &v
	case float32:
		f64 := float64(v)
		*f.dst = &f64
	case []byte:
		var f64 float64
		if _, err := fmt.Sscanf(string(v), "%f", &f64); err != nil {
			return err
		}
		*f.dst = &f64
	case string:
		var f64 float64
		if _, err := fmt.Sscanf(v, "%f", &f64); err != nil {
			return err
		}
		*f.dst = &f64
	default:
		return fmt.Errorf("float column: cannot scan %T", src)
	}
	return nil
}

// asTime parses one of the RFC3339Nano strings a model carries back into an
// instant, for a query that has to compare against it. A value that will not
// parse becomes the zero time, which as a lower bound matches everything —
// the query returns too much rather than nothing.
func asTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// utcDay is the SQL that renders the UTC calendar day of a timestamp column as
// `2006-01-02`, which is what the dashboard's charts key on. The connection
// pins the session timezone to UTC, but spelling the conversion out keeps the
// answer right even on a connection that did not.
func utcDay(col string) string {
	return `to_char(` + col + ` AT TIME ZONE 'UTC','YYYY-MM-DD')`
}
