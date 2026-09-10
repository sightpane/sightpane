// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
)

func (s *Server) listProjects(c fiber.Ctx) error {
	ps, err := s.store.ListProjectsForUser(currentUser(c).ID)
	if err != nil {
		return err
	}
	return c.JSON(ps)
}

func (s *Server) createProject(c fiber.Ctx) error {
	var in struct{ Name, Platform string }
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	uid := currentUser(c).ID
	p, err := s.store.CreateProject(in.Name, in.Platform, "", &uid)
	if err != nil {
		return err
	}
	p.Role = roleOwner
	return c.Status(fiber.StatusCreated).JSON(p)
}

func (s *Server) getProject(c fiber.Ctx) error {
	p, err := s.store.ProjectByID(pathID(c, "id"))
	if err != nil {
		return apierr.New(fiber.StatusNotFound, apierr.CodeProjectNotFound, "project not found")
	}
	p.Role, _ = s.store.MemberRole(p.ID, currentUser(c).ID)
	return c.JSON(p)
}

func (s *Server) updateProject(c fiber.Ctx) error {
	var in struct {
		Name                string  `json:"name"`
		Platform            string  `json:"platform"`
		RetentionDays       *int    `json:"retention_days"`
		QuotaItemsPerMinute *int    `json:"quota_items_per_minute"`
		StoreIP             *string `json:"store_ip"`
		ScrubRulesJSON      *string `json:"scrub_rules_json"`
	}
	if err := c.Bind().JSON(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeProjectNameNeeded, "name required")
	}
	if in.Platform == "" {
		in.Platform = "flutter"
	}
	pid := pathID(c, "id")
	p, err := s.store.ProjectByID(pid)
	if err != nil {
		return err
	}
	retentionDays := p.RetentionDays
	if in.RetentionDays != nil && *in.RetentionDays >= 0 {
		retentionDays = *in.RetentionDays
	}
	quota := p.QuotaItemsPerMinute
	if in.QuotaItemsPerMinute != nil && *in.QuotaItemsPerMinute >= 0 {
		quota = *in.QuotaItemsPerMinute
	}
	storeIP := p.StoreIP
	if in.StoreIP != nil && (*in.StoreIP == "full" || *in.StoreIP == "anonymized" || *in.StoreIP == "none") {
		storeIP = *in.StoreIP
	}
	scrubRulesJSON := p.ScrubRulesJSON
	if in.ScrubRulesJSON != nil {
		scrubRulesJSON = *in.ScrubRulesJSON
	}
	if err := s.store.UpdateProject(pid, in.Name, in.Platform, retentionDays, quota, storeIP, scrubRulesJSON); err != nil {
		return err
	}
	return s.getProject(c)
}

func (s *Server) deleteUserData(c fiber.Ctx) error {
	pid := pathID(c, "id")
	userID := c.Params("userId")
	if userID == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeUserIDRequired, "user id required")
	}
	if err := s.store.DeleteUserData(c.Context(), pid, userID); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"deleted": true, "user_id": userID})
}

func (s *Server) exportUserData(c fiber.Ctx) error {
	pid := pathID(c, "id")
	userID := c.Params("userId")
	if userID == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeUserIDRequired, "user id required")
	}
	zipBytes, err := s.store.ExportUserData(c.Context(), pid, userID)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/zip")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="user-%s-export.zip"`, userID))
	return c.Send(zipBytes)
}

func (s *Server) deleteProject(c fiber.Ctx) error {
	if err := s.store.DeleteProject(pathID(c, "id")); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"deleted": true})
}

// rotateKey invalidates the old key immediately: envelopes still sent with it
// are rejected, which is the point of rotating.
func (s *Server) rotateKey(c fiber.Ctx) error {
	key, err := s.store.RotateKey(pathID(c, "id"))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"api_key": key})
}

func (s *Server) listMembers(c fiber.Ctx) error {
	ms, err := s.store.ListMembers(pathID(c, "id"))
	if err != nil {
		return err
	}
	return c.JSON(ms)
}

// addMember only links an existing account. There is no invitation flow yet, so
// the person must have registered first — saying so is more useful than 404.
func (s *Server) addMember(c fiber.Ctx) error {
	var in struct{ Email, Role string }
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	u, _, err := s.store.UserByEmail(in.Email)
	if err != nil {
		return apierr.New(fiber.StatusNotFound, apierr.CodeMemberUnknownEmail,
			"no user with that email; they must register first")
	}
	if in.Role != roleOwner {
		in.Role = roleMember
	}
	if err := s.store.AddMember(pathID(c, "id"), u.ID, in.Role); err != nil {
		return err
	}
	return s.listMembers(c)
}

// removeMember refuses self-removal: an owner who leaves their own project
// could leave it with no owner and no way back in.
func (s *Server) removeMember(c fiber.Ctx) error {
	uid := pathID(c, "uid")
	if uid == currentUser(c).ID {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeMemberSelfRemove, "owners cannot remove themselves")
	}
	if err := s.store.RemoveMember(pathID(c, "id"), uid); err != nil {
		return err
	}
	return s.listMembers(c)
}

func (s *Server) stats(c fiber.Ctx) error {
	st, err := s.store.Stats(pathID(c, "id"), queryInt(c, "days"))
	if err != nil {
		return err
	}
	return c.JSON(st)
}

func (s *Server) live(c fiber.Ctx) error {
	st, err := s.store.Live(pathID(c, "id"), queryInt(c, "window"))
	if err != nil {
		return err
	}
	return c.JSON(st)
}
