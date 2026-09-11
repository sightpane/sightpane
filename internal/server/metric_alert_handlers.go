// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/alert"
	"sightpane/internal/store"
)

type createMetricAlertRuleRequest struct {
	Name               string   `json:"name"`
	MetricType         string   `json:"metric_type"`
	TargetFilter       string   `json:"target_filter"`
	ComparisonOperator string   `json:"comparison_operator"`
	CriticalThreshold  float64  `json:"critical_threshold"`
	WarningThreshold   *float64 `json:"warning_threshold,omitempty"`
	WindowMinutes      int      `json:"window_minutes"`
	ChannelIDs         []int64  `json:"channel_ids"`
	IsActive           *bool    `json:"is_active"`
}

type updateMetricAlertRuleRequest struct {
	Name               *string  `json:"name"`
	MetricType         *string  `json:"metric_type"`
	TargetFilter       *string  `json:"target_filter"`
	ComparisonOperator *string  `json:"comparison_operator"`
	CriticalThreshold  *float64 `json:"critical_threshold"`
	WarningThreshold   *float64 `json:"warning_threshold"`
	WindowMinutes      *int     `json:"window_minutes"`
	ChannelIDs         []int64  `json:"channel_ids"`
	IsActive           *bool    `json:"is_active"`
}

func (s *Server) listMetricAlertRules(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	rules, err := s.store.ListMetricAlertRules(projectID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if rules == nil {
		rules = []store.MetricAlertRule{}
	}
	return c.JSON(rules)
}

func (s *Server) createMetricAlertRule(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	var req createMetricAlertRuleRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
	}
	if req.WindowMinutes <= 0 {
		req.WindowMinutes = 5
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	rule := &store.MetricAlertRule{
		ProjectID:          projectID,
		Name:               req.Name,
		MetricType:         strings.TrimSpace(req.MetricType),
		TargetFilter:       strings.TrimSpace(req.TargetFilter),
		ComparisonOperator: strings.TrimSpace(req.ComparisonOperator),
		CriticalThreshold:  req.CriticalThreshold,
		WarningThreshold:   req.WarningThreshold,
		WindowMinutes:      req.WindowMinutes,
		ChannelIDs:         req.ChannelIDs,
		IsActive:           isActive,
	}

	created, err := s.store.CreateMetricAlertRule(rule)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(created)
}

func (s *Server) getMetricAlertRule(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	ruleID, err := strconv.ParseInt(c.Params("ruleId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid rule id"})
	}

	rule, err := s.store.GetMetricAlertRule(projectID, ruleID)
	if err != nil {
		return err
	}
	return c.JSON(rule)
}

func (s *Server) updateMetricAlertRule(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	ruleID, err := strconv.ParseInt(c.Params("ruleId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid rule id"})
	}

	existing, err := s.store.GetMetricAlertRule(projectID, ruleID)
	if err != nil {
		return err
	}

	var req updateMetricAlertRuleRequest
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	if req.Name != nil {
		existing.Name = strings.TrimSpace(*req.Name)
	}
	if req.MetricType != nil {
		existing.MetricType = strings.TrimSpace(*req.MetricType)
	}
	if req.TargetFilter != nil {
		existing.TargetFilter = strings.TrimSpace(*req.TargetFilter)
	}
	if req.ComparisonOperator != nil {
		existing.ComparisonOperator = strings.TrimSpace(*req.ComparisonOperator)
	}
	if req.CriticalThreshold != nil {
		existing.CriticalThreshold = *req.CriticalThreshold
	}
	if req.WarningThreshold != nil {
		existing.WarningThreshold = req.WarningThreshold
	}
	if req.WindowMinutes != nil && *req.WindowMinutes > 0 {
		existing.WindowMinutes = *req.WindowMinutes
	}
	if req.ChannelIDs != nil {
		existing.ChannelIDs = req.ChannelIDs
	}
	if req.IsActive != nil {
		existing.IsActive = *req.IsActive
	}

	updated, err := s.store.UpdateMetricAlertRule(existing)
	if err != nil {
		return err
	}
	return c.JSON(updated)
}

func (s *Server) deleteMetricAlertRule(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	ruleID, err := strconv.ParseInt(c.Params("ruleId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid rule id"})
	}

	if err := s.store.DeleteMetricAlertRule(projectID, ruleID); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (s *Server) getMetricAlertRulePreview(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	ruleID, err := strconv.ParseInt(c.Params("ruleId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid rule id"})
	}

	rule, err := s.store.GetMetricAlertRule(projectID, ruleID)
	if err != nil {
		return err
	}

	days := 7
	if dStr := c.Query("days"); dStr != "" {
		if d, err := strconv.Atoi(dStr); err == nil && d > 0 {
			days = d
		}
	}

	points, err := s.store.GetMetricHistoryPreview(projectID, rule.MetricType, rule.TargetFilter, rule.WindowMinutes, days)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"rule":   rule,
		"points": points,
	})
}

func (s *Server) getMetricAlertPreview(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	metricType := c.Query("metric_type", "error_count")
	targetFilter := c.Query("target_filter", "")
	windowMinutes := 5
	if wStr := c.Query("window_minutes"); wStr != "" {
		if w, err := strconv.Atoi(wStr); err == nil && w > 0 {
			windowMinutes = w
		}
	}
	days := 7
	if dStr := c.Query("days"); dStr != "" {
		if d, err := strconv.Atoi(dStr); err == nil && d > 0 {
			days = d
		}
	}

	points, err := s.store.GetMetricHistoryPreview(projectID, metricType, targetFilter, windowMinutes, days)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"metric_type":    metricType,
		"target_filter":  targetFilter,
		"window_minutes": windowMinutes,
		"points":         points,
	})
}

func (s *Server) listMetricAlertIncidents(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}

	var ruleID *int64
	if rStr := c.Query("rule_id"); rStr != "" {
		if rid, err := strconv.ParseInt(rStr, 10, 64); err == nil {
			ruleID = &rid
		}
	}

	limit := 50
	if lStr := c.Query("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	incidents, err := s.store.ListMetricAlertIncidents(projectID, ruleID, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if incidents == nil {
		incidents = []store.MetricAlertIncident{}
	}
	return c.JSON(incidents)
}

func (s *Server) testMetricAlertRule(c fiber.Ctx) error {
	projectID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid project id"})
	}
	ruleID, err := strconv.ParseInt(c.Params("ruleId"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid rule id"})
	}

	rule, err := s.store.GetMetricAlertRule(projectID, ruleID)
	if err != nil {
		return err
	}

	val, count, err := s.store.EvaluateMetricValue(projectID, rule.MetricType, rule.TargetFilter, rule.WindowMinutes)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	isFiring := false
	summary := ""
	switch rule.ComparisonOperator {
	case "spike_multiplier":
		base, _ := s.store.EvaluateMetricBaseline(projectID, rule.MetricType, rule.TargetFilter, rule.WindowMinutes)
		anom := alert.EvaluateAnomaly(val, base, rule.CriticalThreshold, 5.0)
		isFiring = anom.IsAnomaly
		summary = anom.Reason
	case "gt":
		isFiring = val > rule.CriticalThreshold
		summary = fmt.Sprintf("%s was %.2f in last %dm (critical threshold: > %.2f)", rule.MetricType, val, rule.WindowMinutes, rule.CriticalThreshold)
	case "gte":
		isFiring = val >= rule.CriticalThreshold
		summary = fmt.Sprintf("%s was %.2f in last %dm (critical threshold: >= %.2f)", rule.MetricType, val, rule.WindowMinutes, rule.CriticalThreshold)
	case "lt":
		isFiring = val < rule.CriticalThreshold
		summary = fmt.Sprintf("%s was %.2f in last %dm (critical threshold: < %.2f)", rule.MetricType, val, rule.WindowMinutes, rule.CriticalThreshold)
	}

	return c.JSON(fiber.Map{
		"rule_id":       rule.ID,
		"rule_name":     rule.Name,
		"current_value": val,
		"event_count":   count,
		"is_firing":     isFiring,
		"summary":       summary,
	})
}
