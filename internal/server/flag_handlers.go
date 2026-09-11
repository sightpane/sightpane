// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"sightpane/internal/apierr"
	"sightpane/internal/store"
)

type createFlagRequest struct {
	Key               string              `json:"key"`
	Name              string              `json:"name"`
	Description       string              `json:"description"`
	Enabled           *bool               `json:"enabled"`
	RolloutPercentage *int                `json:"rollout_percentage"`
	Filters           []store.FlagFilter  `json:"filters"`
	Variants          []store.FlagVariant `json:"variants"`
}

type updateFlagRequest struct {
	Name              string              `json:"name"`
	Description       string              `json:"description"`
	Enabled           *bool               `json:"enabled"`
	RolloutPercentage *int                `json:"rollout_percentage"`
	Filters           []store.FlagFilter  `json:"filters"`
	Variants          []store.FlagVariant `json:"variants"`
}

type evaluateFlagsRequest struct {
	DistinctID string         `json:"distinct_id"`
	Properties map[string]any `json:"properties"`
}

func (s *Server) evaluateFlags(c fiber.Ctx) error {
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

	var req evaluateFlagsRequest
	if len(c.Body()) > 0 {
		if err := c.Bind().Body(&req); err != nil {
			return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "invalid request body")
		}
	}

	flags, err := s.store.EvaluateProjectFlags(p.ID, req.DistinctID, req.Properties)
	if err != nil {
		return err
	}

	return c.JSON(fiber.Map{
		"flags": flags,
	})
}

func (s *Server) listFeatureFlags(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	flags, err := s.store.ListFeatureFlags(projectID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if flags == nil {
		flags = []*store.FeatureFlag{}
	}
	return c.JSON(flags)
}

func (s *Server) createFeatureFlag(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	var req createFlagRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	key := strings.TrimSpace(req.Key)
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "flag key is required"})
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = key
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	rollout := 100
	if req.RolloutPercentage != nil {
		rollout = *req.RolloutPercentage
	}

	flag, err := s.store.CreateFeatureFlag(projectID, key, name, req.Description, enabled, rollout, req.Filters, req.Variants)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "a feature flag with this key already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusCreated).JSON(flag)
}

func (s *Server) getFeatureFlag(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	fidStr := c.Params("fid")
	fid, pErr := strconv.ParseInt(fidStr, 10, 64)
	var flag *store.FeatureFlag
	if pErr == nil {
		flag, err = s.store.GetFeatureFlagByID(projectID, fid)
	} else {
		flag, err = s.store.GetFeatureFlag(projectID, fidStr)
	}

	if errors.Is(err, store.ErrFeatureFlagNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "feature flag not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(flag)
}

func (s *Server) updateFeatureFlag(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	fid, err := strconv.ParseInt(c.Params("fid"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid flag id"})
	}

	var req updateFlagRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name cannot be empty"})
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	rollout := 100
	if req.RolloutPercentage != nil {
		rollout = *req.RolloutPercentage
	}

	flag, err := s.store.UpdateFeatureFlag(projectID, fid, name, req.Description, enabled, rollout, req.Filters, req.Variants)
	if errors.Is(err, store.ErrFeatureFlagNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "feature flag not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(flag)
}

func (s *Server) deleteFeatureFlag(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	fid, err := strconv.ParseInt(c.Params("fid"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid flag id"})
	}

	err = s.store.DeleteFeatureFlag(projectID, fid)
	if errors.Is(err, store.ErrFeatureFlagNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "feature flag not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"ok": true})
}

func (s *Server) testFeatureFlag(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	fid, err := strconv.ParseInt(c.Params("fid"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid flag id"})
	}

	flag, err := s.store.GetFeatureFlagByID(projectID, fid)
	if errors.Is(err, store.ErrFeatureFlagNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "feature flag not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	var req evaluateFlagsRequest
	if len(c.Body()) > 0 {
		_ = c.Bind().Body(&req)
	}

	val, active := store.EvaluateFlag(flag, req.DistinctID, req.Properties)
	return c.JSON(fiber.Map{
		"key":     flag.Key,
		"value":   val,
		"active":  active,
		"enabled": flag.Enabled,
	})
}
