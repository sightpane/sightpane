// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"sightpane/internal/store"
)

type createExperimentRequest struct {
	Name               string                    `json:"name"`
	Description        string                    `json:"description"`
	FeatureFlagKey     string                    `json:"feature_flag_key"`
	PrimaryMetricEvent string                    `json:"primary_metric_event"`
	Variants           []store.ExperimentVariant `json:"variants"`
	MinimumSampleSize  int                       `json:"minimum_sample_size"`
}

type updateExperimentRequest struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	Status            string `json:"status"`
	MinimumSampleSize int    `json:"minimum_sample_size"`
}

type declareWinnerRequest struct {
	WinnerVariant string `json:"winner_variant"`
}

func (s *Server) listExperiments(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	exps, err := s.store.ListExperiments(projectID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if exps == nil {
		exps = []*store.Experiment{}
	}
	return c.JSON(exps)
}

func (s *Server) createExperiment(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	var req createExperimentRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
	}
	flagKey := strings.TrimSpace(req.FeatureFlagKey)
	if flagKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "feature flag key is required"})
	}
	metric := strings.TrimSpace(req.PrimaryMetricEvent)
	if metric == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "primary metric event is required"})
	}

	exp, err := s.store.CreateExperiment(projectID, name, req.Description, flagKey, metric, req.Variants, req.MinimumSampleSize)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusCreated).JSON(exp)
}

func (s *Server) getExperiment(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	expID, err := strconv.ParseInt(c.Params("expId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid experiment id"})
	}

	exp, err := s.store.GetExperiment(projectID, expID)
	if errors.Is(err, store.ErrExperimentNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "experiment not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(exp)
}

func (s *Server) updateExperiment(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	expID, err := strconv.ParseInt(c.Params("expId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid experiment id"})
	}

	var req updateExperimentRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	exp, err := s.store.UpdateExperiment(projectID, expID, req.Name, req.Description, req.Status, req.MinimumSampleSize)
	if errors.Is(err, store.ErrExperimentNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "experiment not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(exp)
}

func (s *Server) deleteExperiment(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	expID, err := strconv.ParseInt(c.Params("expId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid experiment id"})
	}

	err = s.store.DeleteExperiment(projectID, expID)
	if errors.Is(err, store.ErrExperimentNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "experiment not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"ok": true})
}

func (s *Server) getExperimentResults(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	expID, err := strconv.ParseInt(c.Params("expId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid experiment id"})
	}

	days, _ := strconv.Atoi(c.Query("days", "14"))

	results, err := s.store.CalculateExperimentResults(c.Context(), projectID, expID, days)
	if errors.Is(err, store.ErrExperimentNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "experiment not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(results)
}

func (s *Server) declareExperimentWinner(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	expID, err := strconv.ParseInt(c.Params("expId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid experiment id"})
	}

	var req declareWinnerRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	winner := strings.TrimSpace(req.WinnerVariant)
	if winner == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "winner_variant is required"})
	}

	exp, err := s.store.ConcludeExperimentWinner(projectID, expID, winner)
	if errors.Is(err, store.ErrExperimentNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "experiment not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(exp)
}
