// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
)

// --- Alert Channels ---

func (s *Server) listAlertChannels(c fiber.Ctx) error {
	pid := pathID(c, "id")
	chs, err := s.store.ListAlertChannels(pid)
	if err != nil {
		return err
	}
	return c.JSON(chs)
}

func (s *Server) createAlertChannel(c fiber.Ctx) error {
	pid := pathID(c, "id")
	var in struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Target string `json:"target"`
		Secret string `json:"secret"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	ch, err := s.store.CreateAlertChannel(pid, in.Name, in.Kind, in.Target, in.Secret)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(ch)
}

func (s *Server) updateAlertChannel(c fiber.Ctx) error {
	pid := pathID(c, "id")
	cid := pathID(c, "cid")
	var in struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Target string `json:"target"`
		Secret string `json:"secret"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	ch, err := s.store.UpdateAlertChannel(pid, cid, in.Name, in.Kind, in.Target, in.Secret)
	if err != nil {
		return err
	}
	return c.JSON(ch)
}

func (s *Server) deleteAlertChannel(c fiber.Ctx) error {
	pid := pathID(c, "id")
	cid := pathID(c, "cid")
	if err := s.store.DeleteAlertChannel(pid, cid); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"status": "ok"})
}

func (s *Server) testAlertChannel(c fiber.Ctx) error {
	pid := pathID(c, "id")
	cid := pathID(c, "cid")
	ch, err := s.store.GetAlertChannel(pid, cid)
	if err != nil {
		return err
	}
	if s.notifier == nil {
		return apierr.New(fiber.StatusInternalServerError, apierr.CodeInternal, "notifier not initialized")
	}
	if err := s.notifier.SendTest(c.Context(), ch); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeAlertSendFailed, err.Error())
	}
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Alert Rules ---

func (s *Server) listAlertRules(c fiber.Ctx) error {
	pid := pathID(c, "id")
	rules, err := s.store.ListAlertRules(pid)
	if err != nil {
		return err
	}
	return c.JSON(rules)
}

func (s *Server) createAlertRule(c fiber.Ctx) error {
	pid := pathID(c, "id")
	var in struct {
		Name       string          `json:"name"`
		Kind       string          `json:"kind"`
		Params     json.RawMessage `json:"params"`
		ChannelIDs []int64         `json:"channel_ids"`
		Enabled    *bool           `json:"enabled"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	rule, err := s.store.CreateAlertRule(pid, in.Name, in.Kind, in.Params, in.ChannelIDs, enabled)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(rule)
}

func (s *Server) updateAlertRule(c fiber.Ctx) error {
	pid := pathID(c, "id")
	rid := pathID(c, "rid")
	var in struct {
		Name       string          `json:"name"`
		Kind       string          `json:"kind"`
		Params     json.RawMessage `json:"params"`
		ChannelIDs []int64         `json:"channel_ids"`
		Enabled    *bool           `json:"enabled"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	// Fetch existing to keep fields if not provided
	existing, err := s.store.GetAlertRule(pid, rid)
	if err != nil {
		return err
	}
	name := in.Name
	if name == "" {
		name = existing.Name
	}
	kind := in.Kind
	if kind == "" {
		kind = existing.Kind
	}
	params := in.Params
	if len(params) == 0 {
		params = existing.Params
	}
	channelIDs := in.ChannelIDs
	if channelIDs == nil {
		channelIDs = existing.ChannelIDs
	}
	enabled := existing.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	rule, err := s.store.UpdateAlertRule(pid, rid, name, kind, params, channelIDs, enabled)
	if err != nil {
		return err
	}
	return c.JSON(rule)
}

func (s *Server) deleteAlertRule(c fiber.Ctx) error {
	pid := pathID(c, "id")
	rid := pathID(c, "rid")
	if err := s.store.DeleteAlertRule(pid, rid); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"status": "ok"})
}
