// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"sightpane/internal/apierr"
	"sightpane/internal/store"
)

type createDashboardRequest struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	IsDefault   bool                  `json:"is_default"`
	Layout      []store.DashboardTile `json:"layout"`
}

type updateDashboardRequest struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	IsDefault   bool                  `json:"is_default"`
	Layout      []store.DashboardTile `json:"layout"`
}

type createInsightRequest struct {
	DashboardID *string            `json:"dashboard_id"`
	Name        string             `json:"name"`
	ChartType   string             `json:"chart_type"`
	Query       store.InsightQuery `json:"query"`
}

type updateInsightRequest struct {
	DashboardID *string            `json:"dashboard_id"`
	Name        string             `json:"name"`
	ChartType   string             `json:"chart_type"`
	Query       store.InsightQuery `json:"query"`
}

// listDashboards returns all dashboards for a project.
func (s *Server) listDashboards(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	dashboards, err := s.store.ListDashboards(projectID)
	if err != nil {
		return err
	}
	return c.JSON(dashboards)
}

// createDashboard creates a new dashboard.
func (s *Server) createDashboard(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	var req createDashboardRequest
	if err := c.Bind().JSON(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "invalid request body")
	}
	if req.Name == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "name is required")
	}

	d, err := s.store.CreateDashboard(projectID, req.Name, req.Description, req.IsDefault, req.Layout)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(d)
}

// getDashboard returns a dashboard by ID.
func (s *Server) getDashboard(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	dashID := c.Params("dashboardId")

	d, err := s.store.GetDashboard(projectID, dashID)
	if errors.Is(err, store.ErrDashboardNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "dashboard not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(d)
}

// updateDashboard updates a dashboard.
func (s *Server) updateDashboard(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	dashID := c.Params("dashboardId")

	var req updateDashboardRequest
	if err := c.Bind().JSON(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "invalid request body")
	}
	if req.Name == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "name is required")
	}

	d, err := s.store.UpdateDashboard(projectID, dashID, req.Name, req.Description, req.IsDefault, req.Layout)
	if errors.Is(err, store.ErrDashboardNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "dashboard not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(d)
}

// deleteDashboard deletes a dashboard.
func (s *Server) deleteDashboard(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	dashID := c.Params("dashboardId")

	err := s.store.DeleteDashboard(projectID, dashID)
	if errors.Is(err, store.ErrDashboardNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "dashboard not found")
	}
	if err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// setDefaultDashboard sets a dashboard as default.
func (s *Server) setDefaultDashboard(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	dashID := c.Params("dashboardId")

	err := s.store.SetDefaultDashboard(projectID, dashID)
	if errors.Is(err, store.ErrDashboardNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "dashboard not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"ok": true})
}

// listInsights returns all insights for a project or filtered by dashboard.
func (s *Server) listInsights(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	var dashIDPtr *string
	if dashID := c.Query("dashboard_id"); dashID != "" {
		dashIDPtr = &dashID
	}

	insights, err := s.store.ListInsights(projectID, dashIDPtr)
	if err != nil {
		return err
	}
	return c.JSON(insights)
}

// createInsight creates a new insight.
func (s *Server) createInsight(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	var req createInsightRequest
	if err := c.Bind().JSON(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "invalid request body")
	}
	if req.Name == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "name is required")
	}

	ins, err := s.store.CreateInsight(projectID, req.DashboardID, req.Name, req.ChartType, req.Query)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(ins)
}

// getInsight returns an insight by ID.
func (s *Server) getInsight(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	insID := c.Params("insightId")

	ins, err := s.store.GetInsight(projectID, insID)
	if errors.Is(err, store.ErrInsightNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "insight not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(ins)
}

// updateInsight updates an insight.
func (s *Server) updateInsight(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	insID := c.Params("insightId")

	var req updateInsightRequest
	if err := c.Bind().JSON(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "invalid request body")
	}
	if req.Name == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "name is required")
	}

	ins, err := s.store.UpdateInsight(projectID, insID, req.DashboardID, req.Name, req.ChartType, req.Query)
	if errors.Is(err, store.ErrInsightNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "insight not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(ins)
}

// deleteInsight deletes an insight.
func (s *Server) deleteInsight(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	insID := c.Params("insightId")

	err := s.store.DeleteInsight(projectID, insID)
	if errors.Is(err, store.ErrInsightNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "insight not found")
	}
	if err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// queryInsight executes an arbitrary insight query.
func (s *Server) queryInsight(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	var q store.InsightQuery
	if err := c.Bind().JSON(&q); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "invalid request body")
	}

	res, err := s.store.ExecuteInsightQuery(c.Context(), projectID, q)
	if err != nil {
		return err
	}
	return c.JSON(res)
}

// getInsightResults executes an insight's stored query with optional overrides.
func (s *Server) getInsightResults(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	insID := c.Params("insightId")

	ins, err := s.store.GetInsight(projectID, insID)
	if errors.Is(err, store.ErrInsightNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "insight not found")
	}
	if err != nil {
		return err
	}

	q := ins.Query
	if dr := c.Query("date_range"); dr != "" {
		q.DateRange = dr
	}
	if interval := c.Query("interval"); interval != "" {
		q.Interval = interval
	}

	res, err := s.store.ExecuteInsightQuery(c.Context(), projectID, q)
	if err != nil {
		return err
	}
	return c.JSON(res)
}
