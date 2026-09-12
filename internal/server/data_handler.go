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
	"sightpane/internal/blob"
	"sightpane/internal/store"
)

func (s *Server) listSessions(c fiber.Ctx) error {
	out, err := s.store.ListSessions(store.SessionFilter{
		ProjectID:  pathID(c, "id"),
		UserID:     c.Query("user"),
		OnlyErrors: c.Query("errors") == "1",
		Limit:      queryInt(c, "limit"),
		Query:      c.Query("q"),
		Cursor:     c.Query("cursor"),
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
	out, err := s.store.ListIssuesWithFilter(store.IssueFilter{
		ProjectID:       pathID(c, "id"),
		IncludeResolved: c.Query("resolved") == "1",
		Query:           c.Query("q"),
		Limit:           queryInt(c, "limit"),
	})
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
// that is seen again reopens by itself during ingest if seen in a newer release.
func (s *Server) resolveIssue(c fiber.Ctx) error {
	id, err := s.issueAllowed(c)
	if err != nil {
		return err
	}
	resolved := c.Query("undo") != "1"
	var req struct {
		Release string `json:"release"`
	}
	_ = c.Bind().JSON(&req)
	rel := req.Release
	if rel == "" {
		rel = c.Query("release")
	}
	if err := s.store.SetIssueResolvedWithRelease(id, resolved, rel); err != nil {
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

type assignReq struct {
	UserID *int64 `json:"user_id"`
}

func (s *Server) assignIssue(c fiber.Ctx) error {
	id, err := s.issueAllowed(c)
	if err != nil {
		return err
	}
	var req assignReq
	if err := c.Bind().JSON(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, "invalid_json", "invalid json body")
	}
	if err := s.store.AssignIssue(id, req.UserID); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"status": "ok"})
}

type statusReq struct {
	Status string `json:"status"`
}

func (s *Server) setIssueStatus(c fiber.Ctx) error {
	id, err := s.issueAllowed(c)
	if err != nil {
		return err
	}
	var req statusReq
	if err := c.Bind().JSON(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, "invalid_json", "invalid json body")
	}
	if err := s.store.SetIssueStatus(id, req.Status); err != nil {
		return apierr.New(fiber.StatusBadRequest, "invalid_status", err.Error())
	}
	return c.JSON(fiber.Map{"status": req.Status})
}

type snoozeReq struct {
	Until          *time.Time `json:"until"`
	CountThreshold int        `json:"count_threshold"`
}

func (s *Server) snoozeIssue(c fiber.Ctx) error {
	id, err := s.issueAllowed(c)
	if err != nil {
		return err
	}
	var req snoozeReq
	if err := c.Bind().JSON(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, "invalid_json", "invalid json body")
	}
	if err := s.store.SnoozeIssue(id, req.Until, req.CountThreshold); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"status": "snoozed"})
}

type mergeReq struct {
	TargetID int64 `json:"target_id"`
}

func (s *Server) mergeIssue(c fiber.Ctx) error {
	id, err := s.issueAllowed(c)
	if err != nil {
		return err
	}
	var req mergeReq
	if err := c.Bind().JSON(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, "invalid_json", "invalid json body")
	}
	if req.TargetID <= 0 {
		return apierr.New(fiber.StatusBadRequest, "invalid_target", "invalid target_id")
	}
	if err := s.store.MergeIssue(id, req.TargetID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.New(fiber.StatusNotFound, "not_found", "issue not found")
		}
		if errors.Is(err, store.ErrCrossProjectMerge) {
			return apierr.New(fiber.StatusForbidden, "forbidden", err.Error())
		}
		return apierr.New(fiber.StatusBadRequest, "merge_failed", err.Error())
	}
	return c.JSON(fiber.Map{"status": "merged"})
}

func (s *Server) listIssueComments(c fiber.Ctx) error {
	id, err := s.issueAllowed(c)
	if err != nil {
		return err
	}
	comments, err := s.store.ListIssueComments(id)
	if err != nil {
		return err
	}
	return c.JSON(comments)
}

type commentReq struct {
	Body string `json:"body"`
}

func (s *Server) addIssueComment(c fiber.Ctx) error {
	id, err := s.issueAllowed(c)
	if err != nil {
		return err
	}
	u := currentUser(c)
	if u == nil {
		return apierr.New(fiber.StatusUnauthorized, apierr.CodeLoginRequired, "login required")
	}
	var req commentReq
	if err := c.Bind().JSON(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, "invalid_json", "invalid json body")
	}
	if strings.TrimSpace(req.Body) == "" {
		return apierr.New(fiber.StatusBadRequest, "body_required", "comment body required")
	}
	comment, err := s.store.AddIssueComment(id, u.ID, req.Body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(comment)
}

func (s *Server) listFingerprintRules(c fiber.Ctx) error {
	rules, err := s.store.ListFingerprintRules(pathID(c, "id"))
	if err != nil {
		return err
	}
	return c.JSON(rules)
}

func (s *Server) createFingerprintRule(c fiber.Ctx) error {
	pid := pathID(c, "id")
	var r store.ProjectFingerprintRule
	if err := c.Bind().JSON(&r); err != nil {
		return apierr.New(fiber.StatusBadRequest, "invalid_json", "invalid json body")
	}
	r.ProjectID = pid
	created, err := s.store.CreateFingerprintRule(r)
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, "invalid_rule", err.Error())
	}
	return c.Status(fiber.StatusCreated).JSON(created)
}

func (s *Server) deleteFingerprintRule(c fiber.Ctx) error {
	pid := pathID(c, "id")
	ruleID := pathID(c, "ruleId")
	if err := s.store.DeleteFingerprintRule(pid, ruleID); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Server) listProjectUsers(c fiber.Ctx) error {
	days := queryInt(c, "days")
	if days <= 0 {
		days = 14
	}
	out, err := s.store.ListProjectUsers(c.Context(), pathID(c, "id"), days, c.Query("q"))
	if err != nil {
		return err
	}
	return c.JSON(out)
}
