// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"sightpane/internal/store"
)

func (s *Server) getPathFlow(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	rootEvent := c.Query("root_event")
	direction := c.Query("direction", "forward")
	stepLimit, _ := strconv.Atoi(c.Query("step_limit", "4"))
	days, _ := strconv.Atoi(c.Query("days", "14"))
	threshold, _ := strconv.ParseFloat(c.Query("threshold", "1.0"), 64)

	var excludeEvents []string
	if ex := c.Query("exclude"); ex != "" {
		for _, e := range strings.Split(ex, ",") {
			if trimmed := strings.TrimSpace(e); trimmed != "" {
				excludeEvents = append(excludeEvents, trimmed)
			}
		}
	}

	result, err := s.store.CalculateUserPaths(c.Context(), store.PathOptions{
		ProjectID:           projectID,
		RootEvent:           rootEvent,
		Direction:           direction,
		StepLimit:           stepLimit,
		Days:                days,
		ExcludeEvents:       excludeEvents,
		MinThresholdPercent: threshold,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(result)
}

func (s *Server) getPathSessions(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	source := c.Query("source")
	target := c.Query("target")
	if source == "" || target == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "source and target required"})
	}

	days, _ := strconv.Atoi(c.Query("days", "14"))
	limit, _ := strconv.Atoi(c.Query("limit", "50"))

	sessions, err := s.store.GetPathSessions(c.Context(), projectID, source, target, days, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if sessions == nil {
		sessions = []string{}
	}

	return c.JSON(fiber.Map{
		"source":      source,
		"target":      target,
		"session_ids": sessions,
	})
}
