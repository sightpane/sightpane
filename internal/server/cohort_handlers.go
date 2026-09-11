// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"errors"
	"strconv"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
	"sightpane/internal/store"
)

func (s *Server) listCohorts(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid project id")
	}

	cohorts, err := s.store.ListCohorts(projectID)
	if err != nil {
		return err
	}
	if cohorts == nil {
		cohorts = []*store.Cohort{}
	}
	return c.JSON(cohorts)
}

func (s *Server) createCohort(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid project id")
	}

	var in struct {
		Name           string             `json:"name"`
		Description    string             `json:"description"`
		IsStatic       bool               `json:"is_static"`
		Rules          []store.CohortRule `json:"rules"`
		InitialMembers []string           `json:"initial_members"`
	}
	if err := c.Bind().Body(&in); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "malformed request body")
	}

	if in.Name == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "cohort name required")
	}

	cohort, err := s.store.CreateCohort(projectID, in.Name, in.Description, in.IsStatic, in.Rules, in.InitialMembers)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(cohort)
}

func (s *Server) getCohort(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid project id")
	}
	cohortID, err := strconv.ParseInt(c.Params("cohortId"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid cohort id")
	}

	cohort, err := s.store.GetCohort(projectID, cohortID)
	if err != nil {
		if errors.Is(err, store.ErrCohortNotFound) {
			return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "cohort not found")
		}
		return err
	}
	return c.JSON(cohort)
}

func (s *Server) updateCohort(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid project id")
	}
	cohortID, err := strconv.ParseInt(c.Params("cohortId"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid cohort id")
	}

	var in struct {
		Name        string             `json:"name"`
		Description string             `json:"description"`
		Rules       []store.CohortRule `json:"rules"`
	}
	if err := c.Bind().Body(&in); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "malformed request body")
	}

	cohort, err := s.store.UpdateCohort(projectID, cohortID, in.Name, in.Description, in.Rules)
	if err != nil {
		if errors.Is(err, store.ErrCohortNotFound) {
			return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "cohort not found")
		}
		return err
	}
	return c.JSON(cohort)
}

func (s *Server) deleteCohort(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid project id")
	}
	cohortID, err := strconv.ParseInt(c.Params("cohortId"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid cohort id")
	}

	err = s.store.DeleteCohort(projectID, cohortID)
	if err != nil {
		if errors.Is(err, store.ErrCohortNotFound) {
			return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "cohort not found")
		}
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Server) listCohortMembers(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid project id")
	}
	cohortID, err := strconv.ParseInt(c.Params("cohortId"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid cohort id")
	}

	limit := 100
	if lStr := c.Query("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	members, err := s.store.ListCohortMembers(projectID, cohortID, limit)
	if err != nil {
		if errors.Is(err, store.ErrCohortNotFound) {
			return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "cohort not found")
		}
		return err
	}
	if members == nil {
		members = []string{}
	}
	return c.JSON(members)
}

func (s *Server) refreshCohort(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid project id")
	}
	cohortID, err := strconv.ParseInt(c.Params("cohortId"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid cohort id")
	}

	if err := s.store.RefreshCohortMembers(projectID, cohortID); err != nil {
		if errors.Is(err, store.ErrCohortNotFound) {
			return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "cohort not found")
		}
		return err
	}

	cohort, err := s.store.GetCohort(projectID, cohortID)
	if err != nil {
		return err
	}
	return c.JSON(cohort)
}

func (s *Server) getRetentionMatrix(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid project id")
	}

	period := c.Query("period", "day")
	days := 30
	if dStr := c.Query("days"); dStr != "" {
		if d, err := strconv.Atoi(dStr); err == nil && d > 0 {
			days = d
		}
	}

	startEvent := c.Query("start_event", "session_start")
	returnEvent := c.Query("return_event", "session_start")

	var cohortID *int64
	if cidStr := c.Query("cohort_id"); cidStr != "" {
		if cid, err := strconv.ParseInt(cidStr, 10, 64); err == nil && cid > 0 {
			cohortID = &cid
		}
	}

	result, err := s.store.CalculateRetentionMatrix(projectID, period, days, startEvent, returnEvent, cohortID)
	if err != nil {
		return err
	}
	return c.JSON(result)
}
