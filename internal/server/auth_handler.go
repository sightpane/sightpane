// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"github.com/gofiber/fiber/v3"

	"sightpane/internal/store"
)

type authResponse struct {
	Token string      `json:"token"`
	User  *store.User `json:"user"`
}

func (s *Server) register(c fiber.Ctx) error {
	var in struct{ Email, Name, Password string }
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	u, err := s.store.CreateUser(in.Email, in.Name, in.Password)
	if err != nil {
		return err // CreateUser carries the status and code itself.
	}
	tok, err := s.store.IssueToken(u.ID)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(authResponse{Token: tok, User: u})
}

func (s *Server) login(c fiber.Ctx) error {
	var in struct{ Email, Password string }
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	tok, u, err := s.store.Login(in.Email, in.Password)
	if err != nil {
		return err
	}
	return c.JSON(authResponse{Token: tok, User: u})
}

func (s *Server) logout(c fiber.Ctx) error {
	_ = s.store.RevokeToken(bearer(c))
	return c.JSON(fiber.Map{"ok": true})
}

func (s *Server) me(c fiber.Ctx) error { return c.JSON(currentUser(c)) }

// updateMe currently only carries the dashboard language. It lives on the
// server rather than in the browser so the same account gets the same language
// from another machine, and so text the server sends later (alert emails) can
// be written in it.
func (s *Server) updateMe(c fiber.Ctx) error {
	var in struct {
		Locale *string `json:"locale"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return badJSON()
	}
	u := currentUser(c)
	if in.Locale != nil {
		if err := s.store.SetLocale(u.ID, *in.Locale); err != nil {
			return err
		}
	}
	fresh, err := s.store.UserByID(u.ID)
	if err != nil {
		return err
	}
	return c.JSON(fresh)
}
