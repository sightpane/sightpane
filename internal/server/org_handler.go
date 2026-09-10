// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
	"sightpane/internal/store"
)

type createOrgReq struct {
	Name string `json:"name"`
}

func (s *Server) listOrgs(c fiber.Ctx) error {
	u := currentUser(c)
	orgs, err := s.store.ListOrgsForUser(u.ID)
	if err != nil {
		return err
	}
	return c.JSON(orgs)
}

func (s *Server) createOrg(c fiber.Ctx) error {
	var req createOrgReq
	if err := c.Bind().Body(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "malformed request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return apierr.New(fiber.StatusBadRequest, "org_name_required", "organization name required")
	}
	u := currentUser(c)
	org, err := s.store.CreateOrg(req.Name, u.ID)
	if err != nil {
		return err
	}
	_ = s.store.CreateAuditLog(org.ID, nil, &u.ID, "org.create", fmt.Sprintf("org:%d (%s)", org.ID, org.Name), clientIP(c))
	return c.Status(fiber.StatusCreated).JSON(org)
}

func (s *Server) getOrg(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	org, err := s.store.GetOrg(orgID)
	if err != nil {
		return err
	}
	role, _ := c.Locals("org_role").(string)
	org.Role = role
	return c.JSON(org)
}

func (s *Server) updateOrg(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	var req createOrgReq
	if err := c.Bind().Body(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "malformed request body")
	}
	if err := s.store.UpdateOrg(orgID, req.Name); err != nil {
		return err
	}
	u := currentUser(c)
	_ = s.store.CreateAuditLog(orgID, nil, &u.ID, "org.update", fmt.Sprintf("org:%d (%s)", orgID, req.Name), clientIP(c))
	return s.getOrg(c)
}

func (s *Server) deleteOrg(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	u := currentUser(c)
	if err := s.store.DeleteOrg(orgID); err != nil {
		return err
	}
	_ = s.store.CreateAuditLog(orgID, nil, &u.ID, "org.delete", fmt.Sprintf("org:%d", orgID), clientIP(c))
	return c.JSON(fiber.Map{"deleted": true})
}

// --- Org Members ---

type addOrgMemberReq struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type updateOrgMemberRoleReq struct {
	Role string `json:"role"`
}

func (s *Server) listOrgMembers(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	ms, err := s.store.ListOrgMembers(orgID)
	if err != nil {
		return err
	}
	return c.JSON(ms)
}

func (s *Server) addOrgMember(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	var req addOrgMemberReq
	if err := c.Bind().Body(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "malformed request body")
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeInvalidEmail, "valid email required")
	}
	u, _, err := s.store.UserByEmail(req.Email)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(fiber.StatusNotFound, apierr.CodeMemberUnknownEmail, "no user with that email")
	}
	if err != nil {
		return err
	}
	if err := s.store.AddOrgMember(orgID, u.ID, req.Role); err != nil {
		return err
	}
	curUser := currentUser(c)
	_ = s.store.CreateAuditLog(orgID, nil, &curUser.ID, "member.add", fmt.Sprintf("user:%d (%s) as %s", u.ID, u.Email, req.Role), clientIP(c))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"org_id":     orgID,
		"user_id":    u.ID,
		"email":      u.Email,
		"name":       u.Name,
		"role":       req.Role,
		"created_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) updateOrgMemberRole(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	targetUID := pathID(c, "uid")
	var req updateOrgMemberRoleReq
	if err := c.Bind().Body(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "malformed request body")
	}
	if err := s.store.UpdateOrgMemberRole(orgID, targetUID, req.Role); err != nil {
		return err
	}
	curUser := currentUser(c)
	_ = s.store.CreateAuditLog(orgID, nil, &curUser.ID, "member.update_role", fmt.Sprintf("user:%d role -> %s", targetUID, req.Role), clientIP(c))
	return c.JSON(fiber.Map{"updated": true, "role": req.Role})
}

func (s *Server) removeOrgMember(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	targetUID := pathID(c, "uid")
	curUser := currentUser(c)
	if curUser.ID == targetUID {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeMemberSelfRemove, "cannot remove yourself from organization")
	}
	if err := s.store.RemoveOrgMember(orgID, targetUID); err != nil {
		return err
	}
	_ = s.store.CreateAuditLog(orgID, nil, &curUser.ID, "member.remove", fmt.Sprintf("user:%d", targetUID), clientIP(c))
	return c.JSON(fiber.Map{"deleted": true})
}

// --- Audit Log ---

func (s *Server) listOrgAuditLogs(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	limit := queryInt(c, "limit")
	offset := queryInt(c, "offset")
	logs, err := s.store.ListAuditLogs(orgID, limit, offset)
	if err != nil {
		return err
	}
	return c.JSON(logs)
}

// --- Scoped API Tokens ---

type createTokenReq struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ProjectID     *int64   `json:"project_id"`
	ExpiresInDays *int     `json:"expires_in_days"`
}

func (s *Server) listOrgAPITokens(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	tokens, err := s.store.ListAPITokens(orgID)
	if err != nil {
		return err
	}
	return c.JSON(tokens)
}

func (s *Server) createOrgAPIToken(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	var req createTokenReq
	if err := c.Bind().Body(&req); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "malformed request body")
	}
	var expiresAt *time.Time
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		exp := time.Now().UTC().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &exp
	}
	curUser := currentUser(c)
	secret, tok, err := s.store.CreateAPIToken(orgID, req.ProjectID, &curUser.ID, req.Name, req.Scopes, expiresAt)
	if err != nil {
		return err
	}
	_ = s.store.CreateAuditLog(orgID, req.ProjectID, &curUser.ID, "token.create", fmt.Sprintf("token:%d (%s)", tok.ID, tok.Name), clientIP(c))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"token":        secret, // returned only once
		"id":           tok.ID,
		"org_id":       tok.OrgID,
		"project_id":   tok.ProjectID,
		"name":         tok.Name,
		"token_prefix": tok.TokenPrefix,
		"scopes":       tok.Scopes,
		"expires_at":   tok.ExpiresAt,
		"created_at":   tok.CreatedAt,
	})
}

func (s *Server) deleteOrgAPIToken(c fiber.Ctx) error {
	orgID := pathID(c, "id")
	tokenID := pathID(c, "tokenId")
	if err := s.store.DeleteAPIToken(orgID, tokenID); err != nil {
		return err
	}
	curUser := currentUser(c)
	_ = s.store.CreateAuditLog(orgID, nil, &curUser.ID, "token.delete", fmt.Sprintf("token:%d", tokenID), clientIP(c))
	return c.JSON(fiber.Map{"deleted": true})
}
