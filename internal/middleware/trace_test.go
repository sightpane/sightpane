// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestW3CTraceparentParsingAndFormatting(t *testing.T) {
	// Valid traceparent
	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	tc, err := ParseTraceparent(valid)
	if err != nil {
		t.Fatalf("unexpected error parsing valid traceparent: %v", err)
	}
	if tc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("unexpected trace ID: %s", tc.TraceID)
	}
	if tc.ParentSpanID != "00f067aa0ba902b7" {
		t.Errorf("unexpected parent span ID: %s", tc.ParentSpanID)
	}
	if !tc.Sampled {
		t.Errorf("expected sampled=true")
	}

	formatted := FormatTraceparent(tc)
	if formatted != valid {
		t.Errorf("expected formatted %s, got %s", valid, formatted)
	}

	// Invalid cases
	invalids := []string{
		"",
		"00-4bf92f35-00f067aa-01",                                              // too short
		"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",             // bad version
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",             // all zero trace ID
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",             // all zero span ID
	}
	for _, inv := range invalids {
		if _, err := ParseTraceparent(inv); err == nil {
			t.Errorf("expected error for invalid traceparent %q, got nil", inv)
		}
	}
}

func TestTraceMiddleware(t *testing.T) {
	app := fiber.New()
	app.Use(TraceMiddleware("test-api"))

	var extractedCtx *TraceContext
	app.Get("/test", func(c fiber.Ctx) error {
		extractedCtx = GetTraceContext(c)
		return c.SendString("ok")
	})

	// Case 1: Client sends incoming traceparent
	incoming := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("traceparent", incoming)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if extractedCtx == nil {
		t.Fatalf("expected trace context to be bound to locals")
	}
	if extractedCtx.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("expected continued trace ID %s, got %s", "4bf92f3577b34da6a3ce929d0e0e4736", extractedCtx.TraceID)
	}
	respTraceparent := resp.Header.Get("traceparent")
	if !strings.HasPrefix(respTraceparent, "00-4bf92f3577b34da6a3ce929d0e0e4736-") {
		t.Errorf("expected response traceparent header to continue trace, got %s", respTraceparent)
	}

	// Case 2: Client sends no header -> generates new trace ID
	extractedCtx = nil
	req2 := httptest.NewRequest("GET", "/test", nil)
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if extractedCtx == nil || len(extractedCtx.TraceID) != 32 {
		t.Fatalf("expected generated 32-char trace ID, got %+v", extractedCtx)
	}
	resp2Trace := resp2.Header.Get("traceparent")
	if len(resp2Trace) == 0 {
		t.Errorf("expected response header traceparent to be set")
	}
}
