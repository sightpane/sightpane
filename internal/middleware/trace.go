// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// TraceContext represents W3C TraceContext traceparent metadata.
type TraceContext struct {
	Version      string
	TraceID      string
	ParentSpanID string
	SpanID       string
	Sampled      bool
}

// ParseTraceparent parses a W3C traceparent header: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
func ParseTraceparent(h string) (*TraceContext, error) {
	h = strings.TrimSpace(h)
	parts := strings.Split(h, "-")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid traceparent format: expected 4 segments")
	}

	version := parts[0]
	traceID := strings.ToLower(parts[1])
	parentSpanID := strings.ToLower(parts[2])
	flags := parts[3]

	if version != "00" {
		return nil, fmt.Errorf("unsupported traceparent version: %s", version)
	}
	if len(traceID) != 32 || traceID == "00000000000000000000000000000000" {
		return nil, fmt.Errorf("invalid trace ID: %s", traceID)
	}
	if len(parentSpanID) != 16 || parentSpanID == "0000000000000000" {
		return nil, fmt.Errorf("invalid span ID: %s", parentSpanID)
	}

	sampled := len(flags) == 2 && (flags == "01" || flags[1] == '1')

	return &TraceContext{
		Version:      version,
		TraceID:      traceID,
		ParentSpanID: parentSpanID,
		Sampled:      sampled,
	}, nil
}

// FormatTraceparent formats a TraceContext into W3C traceparent string.
func FormatTraceparent(tc *TraceContext) string {
	flag := "00"
	if tc.Sampled {
		flag = "01"
	}
	spanID := tc.SpanID
	if spanID == "" {
		spanID = tc.ParentSpanID
	}
	return fmt.Sprintf("00-%s-%s-%s", tc.TraceID, spanID, flag)
}

// RandomHex returns n random bytes encoded as hex string.
func RandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const (
	ContextKeyTrace = "trace_context"
)

// TraceMiddleware returns a Fiber middleware that reads or creates W3C traceparent,
// sets the response traceparent header, and binds TraceContext to fiber context locals.
func TraceMiddleware(serviceName string) fiber.Handler {
	if serviceName == "" {
		serviceName = "api-server"
	}
	return func(c fiber.Ctx) error {
		raw := c.Get("traceparent")
		var tc *TraceContext
		if raw != "" {
			if parsed, err := ParseTraceparent(raw); err == nil {
				tc = parsed
			}
		}

		if tc == nil {
			tc = &TraceContext{
				Version: "00",
				TraceID: RandomHex(16), // 32 hex chars
				Sampled: true,
			}
		}
		// Generate span ID for this server execution
		tc.SpanID = RandomHex(8) // 16 hex chars

		// Set response header so clients know the traceparent
		c.Set("traceparent", FormatTraceparent(tc))
		c.Locals(ContextKeyTrace, tc)

		return c.Next()
	}
}

// GetTraceContext retrieves the TraceContext from Fiber context.
func GetTraceContext(c fiber.Ctx) *TraceContext {
	if val := c.Locals(ContextKeyTrace); val != nil {
		if tc, ok := val.(*TraceContext); ok {
			return tc
		}
	}
	return nil
}

// Now returns current UTC time (helper for span timestamps)
func Now() time.Time {
	return time.Now().UTC()
}
