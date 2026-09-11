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

func (s *Server) listFunnels(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	funnels, err := s.store.ListFunnels(projectID)
	if err != nil {
		return err
	}
	if funnels == nil {
		funnels = []*store.Funnel{}
	}
	return c.JSON(funnels)
}

func (s *Server) createFunnel(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	var in struct {
		Name                    string             `json:"name"`
		Description             string             `json:"description"`
		Steps                   []store.FunnelStep `json:"steps"`
		ConversionWindowSeconds int                `json:"conversion_window_seconds"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	if in.Name == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "funnel name required")
	}
	if len(in.Steps) < 2 {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "funnel requires at least 2 steps")
	}

	f, err := s.store.CreateFunnel(projectID, in.Name, in.Description, in.Steps, in.ConversionWindowSeconds)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(f)
}

func (s *Server) getFunnel(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	funnelID, err := strconv.ParseInt(c.Params("funnelId"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid funnel id")
	}
	f, err := s.store.GetFunnel(projectID, funnelID)
	if errors.Is(err, store.ErrFunnelNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "funnel not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(f)
}

func (s *Server) updateFunnel(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	funnelID, err := strconv.ParseInt(c.Params("funnelId"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid funnel id")
	}
	var in struct {
		Name                    string             `json:"name"`
		Description             string             `json:"description"`
		Steps                   []store.FunnelStep `json:"steps"`
		ConversionWindowSeconds int                `json:"conversion_window_seconds"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	f, err := s.store.UpdateFunnel(projectID, funnelID, in.Name, in.Description, in.Steps, in.ConversionWindowSeconds)
	if errors.Is(err, store.ErrFunnelNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "funnel not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(f)
}

func (s *Server) deleteFunnel(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	funnelID, err := strconv.ParseInt(c.Params("funnelId"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid funnel id")
	}
	err = s.store.DeleteFunnel(projectID, funnelID)
	if errors.Is(err, store.ErrFunnelNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "funnel not found")
	}
	if err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Server) getFunnelResults(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	funnelID, err := strconv.ParseInt(c.Params("funnelId"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid funnel id")
	}
	days, _ := strconv.Atoi(c.Query("days", "14"))
	res, err := s.store.CalculateFunnelResults(projectID, funnelID, days)
	if errors.Is(err, store.ErrFunnelNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "funnel not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(res)
}

func (s *Server) getFunnelDropoffs(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	funnelID, err := strconv.ParseInt(c.Params("funnelId"), 10, 64)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidInput, "invalid funnel id")
	}
	step, _ := strconv.Atoi(c.Query("step", "0"))
	days, _ := strconv.Atoi(c.Query("days", "14"))
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	sessionIDs, err := s.store.GetFunnelDropoffs(projectID, funnelID, step, days, limit)
	if errors.Is(err, store.ErrFunnelNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "funnel not found")
	}
	if err != nil {
		return err
	}
	if sessionIDs == nil {
		sessionIDs = []string{}
	}
	return c.JSON(fiber.Map{
		"step":        step,
		"session_ids": sessionIDs,
	})
}
