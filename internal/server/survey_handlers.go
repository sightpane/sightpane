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

type createSurveyRequest struct {
	Name        string                `json:"name"`
	Type        string                `json:"type"`
	Question    string                `json:"question"`
	Description string                `json:"description"`
	Choices     []string              `json:"choices"`
	Targeting   store.SurveyTargeting `json:"targeting"`
	Active      *bool                 `json:"active"`
}

// updateSurveyRequest changes only the fields it carries: the dashboard's
// Active switch sends `{"active": …}` alone. Description and targeting are
// pointers because empty is a value someone can mean.
type updateSurveyRequest struct {
	Name        string                 `json:"name"`
	Type        string                 `json:"type"`
	Question    string                 `json:"question"`
	Description *string                `json:"description"`
	Choices     []string               `json:"choices"`
	Targeting   *store.SurveyTargeting `json:"targeting"`
	Active      *bool                  `json:"active"`
}

type submitSurveyResponseRequest struct {
	SessionID    *string `json:"session_id"`
	UserID       string  `json:"user_id"`
	Score        *int    `json:"score"`
	ResponseText string  `json:"response_text"`
}

// activeSurveys returns surveys currently active for client SDK targeting.
func (s *Server) activeSurveys(c fiber.Ctx) error {
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

	surveys, err := s.store.GetActiveSurveysForClient(p.ID)
	if err != nil {
		return err
	}

	return c.JSON(fiber.Map{
		"surveys": surveys,
	})
}

// submitSurveyResponse handles client SDK submissions of survey answers.
func (s *Server) submitSurveyResponse(c fiber.Ctx) error {
	surveyID, err := strconv.ParseInt(c.Params("surveyId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid survey id"})
	}

	key := projectKey(c)
	var p *store.Project

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

	var req submitSurveyResponseRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	// Verify survey exists and belongs to project
	_, err = s.store.GetSurvey(p.ID, surveyID)
	if errors.Is(err, store.ErrSurveyNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "survey not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	resp := &store.SurveyResponse{
		SurveyID:     surveyID,
		ProjectID:    p.ID,
		SessionID:    req.SessionID,
		UserID:       strings.TrimSpace(req.UserID),
		Score:        req.Score,
		ResponseText: strings.TrimSpace(req.ResponseText),
	}

	saved, err := s.store.SubmitSurveyResponse(resp)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusCreated).JSON(saved)
}

func (s *Server) listSurveys(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	surveys, err := s.store.ListSurveys(projectID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(surveys)
}

func (s *Server) getSurvey(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	surveyID, err := strconv.ParseInt(c.Params("surveyId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid survey id"})
	}

	survey, err := s.store.GetSurvey(projectID, surveyID)
	if errors.Is(err, store.ErrSurveyNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "survey not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(survey)
}

func (s *Server) createSurvey(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	var req createSurveyRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
	}
	stype := strings.ToLower(strings.TrimSpace(req.Type))
	switch stype {
	case "nps", "csat", "rating", "open_text", "single_choice":
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid survey type"})
	}

	question := strings.TrimSpace(req.Question)
	if question == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "question is required"})
	}

	active := true
	if req.Active != nil {
		active = *req.Active
	}

	sur := &store.Survey{
		ProjectID:   projectID,
		Name:        name,
		Type:        stype,
		Question:    question,
		Description: strings.TrimSpace(req.Description),
		Choices:     req.Choices,
		Targeting:   req.Targeting,
		Active:      active,
	}

	created, err := s.store.CreateSurvey(sur)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusCreated).JSON(created)
}

func (s *Server) updateSurvey(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	surveyID, err := strconv.ParseInt(c.Params("surveyId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid survey id"})
	}

	var req updateSurveyRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	existing, err := s.store.GetSurvey(projectID, surveyID)
	if errors.Is(err, store.ErrSurveyNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "survey not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if req.Name != "" {
		existing.Name = strings.TrimSpace(req.Name)
	}
	if req.Type != "" {
		stype := strings.ToLower(strings.TrimSpace(req.Type))
		switch stype {
		case "nps", "csat", "rating", "open_text", "single_choice":
			existing.Type = stype
		default:
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid survey type"})
		}
	}
	if req.Question != "" {
		existing.Question = strings.TrimSpace(req.Question)
	}
	if req.Description != nil {
		existing.Description = strings.TrimSpace(*req.Description)
	}
	if req.Choices != nil {
		existing.Choices = req.Choices
	}
	if req.Targeting != nil {
		existing.Targeting = *req.Targeting
	}
	if req.Active != nil {
		existing.Active = *req.Active
	}

	updated, err := s.store.UpdateSurvey(existing)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(updated)
}

func (s *Server) deleteSurvey(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	surveyID, err := strconv.ParseInt(c.Params("surveyId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid survey id"})
	}

	if err := s.store.DeleteSurvey(projectID, surveyID); err != nil {
		if errors.Is(err, store.ErrSurveyNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "survey not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Server) getSurveyResults(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	surveyID, err := strconv.ParseInt(c.Params("surveyId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid survey id"})
	}

	results, err := s.store.GetSurveyResults(projectID, surveyID)
	if errors.Is(err, store.ErrSurveyNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "survey not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(results)
}

func (s *Server) listSurveyResponses(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	surveyID, err := strconv.ParseInt(c.Params("surveyId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid survey id"})
	}

	limit := 100
	if lStr := c.Query("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	responses, err := s.store.ListSurveyResponses(projectID, surveyID, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(responses)
}
