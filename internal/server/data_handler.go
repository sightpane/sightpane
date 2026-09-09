// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
	"sightpane/internal/blob"
	"sightpane/internal/store"
)

func (s *Server) listSessions(c fiber.Ctx) error {
	out, err := s.store.ListSessions(store.SessionFilter{
		ProjectID:  pathID(c, "id"),
		UserID:     c.Query("user"),
		OnlyErrors: c.Query("errors") == "1",
		Limit:      queryInt(c, "limit"),
	})
	if err != nil {
		return err
	}
	return c.JSON(out)
}

// sessionAllowed resolves the session's project and checks membership. Session
// ids are global, so authorization cannot come from the path alone.
func (s *Server) sessionAllowed(c fiber.Ctx) error {
	pid, err := s.store.SessionProject(c.Params("id"))
	if err != nil {
		return apierr.New(fiber.StatusNotFound, apierr.CodeSessionNotFound, "session not found")
	}
	return s.allow(c, pid, roleMember)
}

func (s *Server) getSession(c fiber.Ctx) error {
	if err := s.sessionAllowed(c); err != nil {
		return err
	}
	d, err := s.store.GetSession(c.Params("id"))
	if err != nil {
		return err
	}
	return c.JSON(d)
}

// getFrame serves one replay frame as a PNG. The player loads these with an
// <img> tag, which is why bearer() also accepts ?token=.
//
// The bytes are streamed through this handler rather than redirected to a
// presigned object-store URL: access to a frame is decided per project on every
// request, and a presigned URL would outlive that decision.
func (s *Server) getFrame(c fiber.Ctx) error {
	if err := s.sessionAllowed(c); err != nil {
		return err
	}
	seq, _ := strconv.Atoi(strings.TrimSuffix(c.Params("seq"), ".png"))
	rc, size, err := s.store.FrameReader(c.Context(), c.Params("id"), seq)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, blob.ErrNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeFrameNotFound, "frame not found")
	}
	if err != nil {
		return err
	}
	// No `defer rc.Close()`: fasthttp writes the body after this handler returns
	// and closes the stream itself, so closing here would close the reader
	// before a single byte had been read.
	c.Set(fiber.HeaderContentType, "image/png")
	// Frames never change once written, so they cache for a day; private
	// because they are session recordings.
	c.Set(fiber.HeaderCacheControl, "private, max-age=86400")
	return c.SendStream(rc, int(size))
}

func (s *Server) listIssues(c fiber.Ctx) error {
	out, err := s.store.ListIssues(pathID(c, "id"), c.Query("resolved") == "1")
	if err != nil {
		return err
	}
	return c.JSON(out)
}

func (s *Server) issueAllowed(c fiber.Ctx) (int64, error) {
	id := pathID(c, "id")
	pid, err := s.store.IssueProject(id)
	if err != nil {
		return 0, apierr.New(fiber.StatusNotFound, apierr.CodeIssueNotFound, "issue not found")
	}
	return id, s.allow(c, pid, roleMember)
}

func (s *Server) getIssue(c fiber.Ctx) error {
	id, err := s.issueAllowed(c)
	if err != nil {
		return err
	}
	d, err := s.store.GetIssue(id)
	if err != nil {
		return err
	}
	return c.JSON(d)
}

// resolveIssue closes a group, or reopens it with ?undo=1. A resolved group
// that is seen again reopens by itself during ingest.
func (s *Server) resolveIssue(c fiber.Ctx) error {
	id, err := s.issueAllowed(c)
	if err != nil {
		return err
	}
	resolved := c.Query("undo") != "1"
	if err := s.store.SetIssueResolved(id, resolved); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"resolved": resolved})
}

func (s *Server) eventSummary(c fiber.Ctx) error {
	out, err := s.store.EventSummary(pathID(c, "id"), queryInt(c, "days"))
	if err != nil {
		return err
	}
	return c.JSON(out)
}
