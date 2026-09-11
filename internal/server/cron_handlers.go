// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
	"sightpane/internal/store"
)

type createCronMonitorRequest struct {
	Slug               string `json:"slug"`
	Name               string `json:"name"`
	Schedule           string `json:"schedule"`
	Timezone           string `json:"timezone"`
	GracePeriodMinutes int    `json:"grace_period_minutes"`
	MaxRuntimeMinutes  int    `json:"max_runtime_minutes"`
}

type updateCronMonitorRequest struct {
	Name               string `json:"name"`
	Schedule           string `json:"schedule"`
	Timezone           string `json:"timezone"`
	GracePeriodMinutes int    `json:"grace_period_minutes"`
	MaxRuntimeMinutes  int    `json:"max_runtime_minutes"`
}

type cronCheckinRequest struct {
	Status     string `json:"status"`
	DurationMs *int   `json:"duration_ms"`
	Message    string `json:"message"`
}

// cronCheckin handles lightweight heartbeat pings from scheduled jobs.
// It supports both POST and GET, and accepts auth via header (X-Sightpane-Key) or query (?key=).
func (s *Server) cronCheckin(c fiber.Ctx) error {
	slug := c.Params("slug")
	if slug == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "monitor slug is required"})
	}

	key := projectKey(c)
	var p *store.Project
	var err error

	if key != "" {
		p, err = s.store.ProjectByKey(key)
		if errors.Is(err, store.ErrNotFound) {
			return apierr.New(fiber.StatusUnauthorized, apierr.CodeUnknownKey, "unknown api key")
		}
		if err != nil {
			return err
		}
	} else {
		tok := bearer(c)
		if tok != "" && (strings.HasPrefix(tok, "sp_") || strings.HasPrefix(tok, "hog_")) {
			apiTok, tErr := s.store.APITokenBySecret(tok)
			if tErr != nil || (apiTok.ExpiresAt != nil && time.Now().After(*apiTok.ExpiresAt)) {
				return apierr.New(fiber.StatusUnauthorized, apierr.CodeInvalidToken, "invalid or expired API token")
			}
			if apiTok.ProjectID != nil {
				p, err = s.store.ProjectByID(*apiTok.ProjectID)
			} else {
				projs, pErr := s.store.ListProjectsByOrg(apiTok.OrgID)
				if pErr == nil && len(projs) > 0 {
					p = &projs[0]
				} else {
					return apierr.New(fiber.StatusBadRequest, apierr.CodeProjectNotFound, "project not found for token")
				}
			}
		} else {
			return apierr.New(fiber.StatusUnauthorized, apierr.CodeKeyRequired, "API key required")
		}
	}

	status := "ok"
	var durationMs *int
	message := ""

	// Try reading body if present
	var req cronCheckinRequest
	if len(c.Body()) > 0 {
		_ = c.Bind().Body(&req)
	}

	if req.Status != "" {
		status = req.Status
	} else if qStatus := c.Query("status"); qStatus != "" {
		status = qStatus
	}

	if req.DurationMs != nil {
		durationMs = req.DurationMs
	} else if qDur := c.Query("duration_ms"); qDur != "" {
		if d, err := strconv.Atoi(qDur); err == nil {
			durationMs = &d
		}
	}

	if req.Message != "" {
		message = req.Message
	} else if qMsg := c.Query("message"); qMsg != "" {
		message = qMsg
	}

	status = strings.ToLower(status)
	if status != "ok" && status != "in_progress" && status != "error" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid status: must be ok, in_progress, or error",
		})
	}

	checkin, monitor, err := s.store.RecordCronCheckin(p.ID, slug, status, durationMs, message)
	if errors.Is(err, store.ErrCronMonitorNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fmt.Sprintf("cron monitor with slug %q not found", slug),
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if status == "error" && s.notifier != nil {
		s.notifier.NotifyCronIncident(
			p.ID,
			monitor.Slug,
			monitor.Name,
			"cron_error",
			fmt.Sprintf("Cron job %q reported error: %s", monitor.Name, message),
		)
	}

	return c.JSON(fiber.Map{
		"status":           "ok",
		"checkin_id":       checkin.ID,
		"monitor_status":   monitor.Status,
		"last_checkin_at":  monitor.LastCheckinAt,
		"next_expected_at": monitor.NextExpectedAt,
	})
}

func (s *Server) listCronMonitors(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	monitors, err := s.store.ListCronMonitors(projectID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if monitors == nil {
		monitors = []*store.CronMonitor{}
	}

	stats, _ := s.store.GetCronStats(projectID)

	return c.JSON(fiber.Map{
		"monitors": monitors,
		"stats":    stats,
	})
}

func (s *Server) getCronMonitor(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	monitorID, err := strconv.ParseInt(c.Params("monitorId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid monitor id"})
	}

	m, err := s.store.GetCronMonitor(projectID, monitorID)
	if errors.Is(err, store.ErrCronMonitorNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "cron monitor not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(m)
}

func (s *Server) createCronMonitor(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	var req createCronMonitorRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	req.Slug = strings.TrimSpace(req.Slug)
	req.Name = strings.TrimSpace(req.Name)
	req.Schedule = strings.TrimSpace(req.Schedule)

	if req.Slug == "" || req.Name == "" || req.Schedule == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "slug, name, and schedule are required",
		})
	}

	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if req.GracePeriodMinutes <= 0 {
		req.GracePeriodMinutes = 15
	}
	if req.MaxRuntimeMinutes <= 0 {
		req.MaxRuntimeMinutes = 60
	}

	// Validate crontab expression
	if _, err := store.ComputeNextExpected(req.Schedule, req.Timezone, time.Now()); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("invalid schedule: %v", err),
		})
	}

	m := store.CronMonitor{
		ProjectID:          projectID,
		Slug:               req.Slug,
		Name:               req.Name,
		Schedule:           req.Schedule,
		Timezone:           req.Timezone,
		GracePeriodMinutes: req.GracePeriodMinutes,
		MaxRuntimeMinutes:  req.MaxRuntimeMinutes,
		Status:             "ok",
	}

	created, err := s.store.CreateCronMonitor(&m)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusCreated).JSON(created)
}

func (s *Server) updateCronMonitor(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	monitorID, err := strconv.ParseInt(c.Params("monitorId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid monitor id"})
	}

	var req updateCronMonitorRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	existing, err := s.store.GetCronMonitor(projectID, monitorID)
	if errors.Is(err, store.ErrCronMonitorNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "cron monitor not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if req.Name != "" {
		existing.Name = strings.TrimSpace(req.Name)
	}
	if req.Schedule != "" {
		schedule := strings.TrimSpace(req.Schedule)
		tz := existing.Timezone
		if req.Timezone != "" {
			tz = req.Timezone
		}
		if _, err := store.ComputeNextExpected(schedule, tz, time.Now()); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("invalid schedule: %v", err),
			})
		}
		existing.Schedule = schedule
	}
	if req.Timezone != "" {
		existing.Timezone = req.Timezone
	}
	if req.GracePeriodMinutes > 0 {
		existing.GracePeriodMinutes = req.GracePeriodMinutes
	}
	if req.MaxRuntimeMinutes > 0 {
		existing.MaxRuntimeMinutes = req.MaxRuntimeMinutes
	}

	updated, err := s.store.UpdateCronMonitor(existing)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(updated)
}

func (s *Server) deleteCronMonitor(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	monitorID, err := strconv.ParseInt(c.Params("monitorId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid monitor id"})
	}

	if err := s.store.DeleteCronMonitor(projectID, monitorID); err != nil {
		if errors.Is(err, store.ErrCronMonitorNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "cron monitor not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Server) listCronCheckins(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	monitorID, err := strconv.ParseInt(c.Params("monitorId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid monitor id"})
	}
	limit, _ := strconv.Atoi(c.Query("limit", "100"))

	checkins, err := s.store.ListCronCheckins(projectID, monitorID, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if checkins == nil {
		checkins = []*store.CronCheckin{}
	}

	return c.JSON(fiber.Map{"checkins": checkins})
}
