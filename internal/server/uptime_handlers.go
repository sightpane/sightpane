// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/store"
	"sightpane/internal/uptime"
)

type createUptimeMonitorRequest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	Method             string            `json:"method"`
	Headers            map[string]string `json:"headers"`
	ExpectedStatusCode int               `json:"expected_status_code"`
	IntervalSeconds    int               `json:"interval_seconds"`
	TimeoutSeconds     int               `json:"timeout_seconds"`
	SSLCheckEnabled    *bool             `json:"ssl_check_enabled"`
}

type updateUptimeMonitorRequest struct {
	Name               *string           `json:"name"`
	URL                *string           `json:"url"`
	Method             *string           `json:"method"`
	Headers            map[string]string `json:"headers"`
	ExpectedStatusCode *int              `json:"expected_status_code"`
	IntervalSeconds    *int              `json:"interval_seconds"`
	TimeoutSeconds     *int              `json:"timeout_seconds"`
	SSLCheckEnabled    *bool             `json:"ssl_check_enabled"`
}

func (s *Server) listUptimeMonitors(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	monitors, err := s.store.ListUptimeMonitors(projectID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if monitors == nil {
		monitors = []*store.UptimeMonitor{}
	}
	stats, _ := s.store.GetUptimeStats(projectID)

	return c.JSON(fiber.Map{
		"monitors": monitors,
		"stats":    stats,
	})
}

func (s *Server) createUptimeMonitor(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	var req createUptimeMonitorRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
	}
	if req.URL == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "url is required"})
	}

	parsedURL, err := url.Parse(req.URL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid url: must be http or https"})
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "HEAD" && method != "POST" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "method must be GET, HEAD, or POST"})
	}

	expectedStatus := req.ExpectedStatusCode
	if expectedStatus <= 0 {
		expectedStatus = 200
	}

	interval := req.IntervalSeconds
	if interval <= 0 {
		interval = 60
	}
	if interval != 30 && interval != 60 && interval != 300 && interval != 600 {
		interval = 60
	}

	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = 10
	}

	sslCheck := true
	if req.SSLCheckEnabled != nil {
		sslCheck = *req.SSLCheckEnabled
	}

	m, err := s.store.CreateUptimeMonitor(
		projectID,
		req.Name,
		req.URL,
		method,
		req.Headers,
		expectedStatus,
		interval,
		timeout,
		sslCheck,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Trigger initial check asynchronously
	checker := uptime.NewChecker(1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = checker.CheckMonitor(ctx, s.store, m)
	}()

	return c.Status(fiber.StatusCreated).JSON(m)
}

func (s *Server) getUptimeMonitor(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	monitorID, err := strconv.ParseInt(c.Params("monitorId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid monitor id"})
	}

	days := 30
	if qDays := c.Query("days"); qDays != "" {
		if d, err := strconv.Atoi(qDays); err == nil && d > 0 {
			days = d
		}
	}

	detail, err := s.store.GetUptimeHistory(projectID, monitorID, days)
	if errors.Is(err, store.ErrUptimeMonitorNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "uptime monitor not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(detail)
}

func (s *Server) updateUptimeMonitor(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	monitorID, err := strconv.ParseInt(c.Params("monitorId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid monitor id"})
	}

	var req updateUptimeMonitorRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name cannot be empty"})
		}
		req.Name = &name
	}

	if req.URL != nil {
		u := strings.TrimSpace(*req.URL)
		parsedURL, err := url.Parse(u)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid url: must be http or https"})
		}
		req.URL = &u
	}

	if req.Method != nil {
		method := strings.ToUpper(strings.TrimSpace(*req.Method))
		if method != "GET" && method != "HEAD" && method != "POST" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "method must be GET, HEAD, or POST"})
		}
		req.Method = &method
	}

	if req.IntervalSeconds != nil {
		interval := *req.IntervalSeconds
		if interval != 30 && interval != 60 && interval != 300 && interval != 600 {
			interval = 60
		}
		req.IntervalSeconds = &interval
	}

	m, err := s.store.UpdateUptimeMonitor(
		projectID,
		monitorID,
		req.Name,
		req.URL,
		req.Method,
		req.Headers,
		req.ExpectedStatusCode,
		req.IntervalSeconds,
		req.TimeoutSeconds,
		req.SSLCheckEnabled,
	)
	if errors.Is(err, store.ErrUptimeMonitorNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "uptime monitor not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(m)
}

func (s *Server) deleteUptimeMonitor(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	monitorID, err := strconv.ParseInt(c.Params("monitorId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid monitor id"})
	}

	err = s.store.DeleteUptimeMonitor(projectID, monitorID)
	if errors.Is(err, store.ErrUptimeMonitorNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "uptime monitor not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"status": "deleted"})
}

func (s *Server) triggerUptimeCheck(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	monitorID, err := strconv.ParseInt(c.Params("monitorId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid monitor id"})
	}

	m, err := s.store.GetUptimeMonitor(projectID, monitorID)
	if errors.Is(err, store.ErrUptimeMonitorNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "uptime monitor not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	checker := uptime.NewChecker(1)
	res, err := checker.CheckMonitor(c.Context(), s.store, m)
	if err != nil {
		return fmt.Errorf("check failed: %w", err)
	}

	return c.JSON(res)
}
