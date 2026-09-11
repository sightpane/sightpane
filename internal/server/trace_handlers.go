// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
	"sightpane/internal/store"
)

// listTraces returns a list of recent traces matching filters.
func (s *Server) listTraces(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	days, _ := strconv.Atoi(c.Query("days", "14"))
	service := strings.TrimSpace(c.Query("service", ""))
	status := strings.TrimSpace(c.Query("status", ""))
	minDur, _ := strconv.ParseFloat(c.Query("min_duration_ms", "0"), 64)
	query := strings.TrimSpace(c.Query("query", ""))
	limit, _ := strconv.Atoi(c.Query("limit", "50"))

	opts := store.ListTracesOptions{
		Days:          days,
		Service:       service,
		Status:        status,
		MinDurationMs: minDur,
		Query:         query,
		Limit:         limit,
	}

	traces, err := s.store.ListTraces(c.Context(), projectID, opts)
	if err != nil {
		return err
	}

	return c.JSON(traces)
}

// getTrace returns the full waterfall trace tree and N+1 query diagnostics.
func (s *Server) getTrace(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	traceID := strings.TrimSpace(c.Params("traceId"))
	if traceID == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "traceId required")
	}

	trace, err := s.store.GetTrace(c.Context(), projectID, traceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "trace not found")
		}
		return err
	}

	return c.JSON(trace)
}

type ingestSpansRequest struct {
	Spans []store.SpanRecord `json:"spans"`
}

// ingestSpans accepts a batch of spans from backend services or agents.
func (s *Server) ingestSpans(c fiber.Ctx) error {
	projectID := pathID(c, "id")

	// Allow authentication via X-Sightpane-Key / key query param or user token
	key := projectKey(c)
	if key != "" {
		p, err := s.store.ProjectByKey(key)
		if err != nil {
			return apierr.New(fiber.StatusUnauthorized, apierr.CodeUnknownKey, "unknown api key")
		}
		if projectID == 0 {
			projectID = p.ID
		} else if projectID != p.ID {
			return apierr.New(fiber.StatusForbidden, apierr.CodeForbidden, "key does not match project")
		}
	} else {
		if err := s.authenticate(c); err != nil {
			return err
		}
		if err := s.allow(c, projectID, roleMember); err != nil {
			return err
		}
	}

	var req ingestSpansRequest
	if err := c.Bind().JSON(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "invalid JSON payload")
	}

	if len(req.Spans) == 0 {
		return c.JSON(fiber.Map{"accepted": 0})
	}

	if err := s.store.IngestSpans(c.Context(), projectID, req.Spans); err != nil {
		return err
	}

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"accepted": len(req.Spans),
	})
}
