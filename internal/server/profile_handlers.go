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

// listProfiles returns a list of recent profile summaries matching filters.
func (s *Server) listProfiles(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	days, _ := strconv.Atoi(c.Query("days", "14"))
	txName := strings.TrimSpace(c.Query("transaction", ""))
	if txName == "" {
		txName = strings.TrimSpace(c.Query("transaction_name", ""))
	}
	limit, _ := strconv.Atoi(c.Query("limit", "50"))

	profiles, err := s.store.ListProfiles(c.Context(), projectID, txName, days, limit)
	if err != nil {
		return err
	}

	return c.JSON(profiles)
}

// getProfile returns the full profile record including raw call tree JSON data.
func (s *Server) getProfile(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	profileID := strings.TrimSpace(c.Params("profileId"))
	if profileID == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "profileId required")
	}

	profile, err := s.store.GetProfile(c.Context(), projectID, profileID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "not found") {
			return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "profile not found")
		}
		return err
	}

	return c.JSON(profile)
}

// getTopSlowFunctions returns aggregated slowest functions with self-time and total-time.
func (s *Server) getTopSlowFunctions(c fiber.Ctx) error {
	projectID := pathID(c, "id")
	days, _ := strconv.Atoi(c.Query("days", "14"))
	txName := strings.TrimSpace(c.Query("transaction", ""))
	limit, _ := strconv.Atoi(c.Query("limit", "20"))

	funcs, err := s.store.GetTopSlowFunctions(c.Context(), projectID, txName, days, limit)
	if err != nil {
		return err
	}

	return c.JSON(funcs)
}

// createProfile directly inserts a profile record.
func (s *Server) createProfile(c fiber.Ctx) error {
	projectID := pathID(c, "id")

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

	var p store.ProfileRecord
	if err := c.Bind().JSON(&p); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "invalid profile json")
	}

	p.ProjectID = projectID
	if p.TransactionName == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "transaction_name required")
	}

	if err := s.store.InsertProfile(c.Context(), &p); err != nil {
		return err
	}

	return c.Status(fiber.StatusCreated).JSON(p)
}
