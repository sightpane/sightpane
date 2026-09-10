// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
)

// getPerformanceSummary returns aggregated performance data across transactions/spans.
func (s *Server) getPerformanceSummary(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	days, _ := strconv.Atoi(c.Query("days", "14"))
	op := c.Query("op", "")

	resp, err := s.store.GetPerformanceSummary(c.Context(), projectID, days, op)
	if err != nil {
		return err
	}
	return c.JSON(resp)
}

// getTransactionDetail returns detailed metrics, daily trend and slowest samples.
func (s *Server) getTransactionDetail(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	days, _ := strconv.Atoi(c.Query("days", "14"))
	op := c.Query("op", "")
	name := c.Query("name", "")
	if name == "" {
		name = c.Params("*")
	}

	resp, err := s.store.GetTransactionDetail(c.Context(), projectID, op, name, days)
	if err != nil {
		return err
	}
	return c.JSON(resp)
}
