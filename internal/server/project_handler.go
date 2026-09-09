// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
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
	var in struct{ Name, Platform string }
	if err := c.Bind().JSON(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeProjectNameNeeded, "name required")
	}
	if in.Platform == "" {
		in.Platform = "flutter"
	}
	if err := s.store.UpdateProject(pathID(c, "id"), in.Name, in.Platform); err != nil {
		return err
	}
	return s.getProject(c)
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
