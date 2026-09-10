// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
	"sightpane/internal/store"
)

const (
	roleViewer = "viewer"
	roleMember = "member"
	roleAdmin  = "admin"
	roleOwner  = "owner"
)

const (
	apiTokenKey = "api_token"
)

func hasScope(scopes []string, target string) bool {
	for _, s := range scopes {
		if s == target || s == "*" {
			return true
		}
	}
	return false
}

// authenticate resolves the bearer token and puts the user on the context. It
// deliberately does not call c.Next(): the caller decides when the rest of the
// chain runs, so a second check can happen before the handler and not after it.
func (s *Server) authenticate(c fiber.Ctx) error {
	tok := bearer(c)
	if tok == "" {
		return apierr.New(fiber.StatusUnauthorized, apierr.CodeLoginRequired, "login required")
	}
	if strings.HasPrefix(tok, "sp_") || strings.HasPrefix(tok, "hog_") {
		apiToken, err := s.store.APITokenBySecret(tok)
		if err != nil || (apiToken.ExpiresAt != nil && time.Now().After(*apiToken.ExpiresAt)) {
			return apierr.New(fiber.StatusUnauthorized, apierr.CodeInvalidToken, "invalid or expired token")
		}
		c.Locals(apiTokenKey, apiToken)
		if apiToken.CreatedBy != nil {
			if u, err := s.store.UserByID(*apiToken.CreatedBy); err == nil {
				c.Locals(userKey, u)
			}
		}
		if c.Locals(userKey) == nil {
			c.Locals(userKey, &store.User{ID: 0, Name: apiToken.Name, Email: "token@" + apiToken.Name})
		}
		return nil
	}
	u, err := s.store.UserByToken(tok)
	if err != nil {
		return apierr.New(fiber.StatusUnauthorized, apierr.CodeInvalidToken, "invalid or expired token")
	}
	c.Locals(userKey, u)
	return nil
}

// requireAuth admits any signed-in user.
func (s *Server) requireAuth(c fiber.Ctx) error {
	if err := s.authenticate(c); err != nil {
		return err
	}
	return c.Next()
}

// requireProject demands membership of the `:id` project, and the owner role
// when [need] asks for it.
//
// A non-member gets 404, not 403: telling them "you are not a member of project
// 7" would confirm that project 7 exists.
func (s *Server) requireProject(need string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if err := s.authenticate(c); err != nil {
			return err
		}
		pid := pathID(c, "id")
		if err := s.allow(c, pid, need); err != nil {
			return err
		}
		err := c.Next()
		if err == nil {
			m := c.Method()
			if m == fiber.MethodPost || m == fiber.MethodPatch || m == fiber.MethodDelete {
				u := currentUser(c)
				if u != nil && u.ID > 0 {
					p, pErr := s.store.ProjectByID(pid)
					if pErr == nil && p != nil && p.OrgID != nil {
						_ = s.store.CreateAuditLog(*p.OrgID, &pid, &u.ID, m+" "+c.Path(), fmt.Sprintf("project:%d", pid), clientIP(c))
					}
				}
			}
		}
		return err
	}
}

func (s *Server) requireOrg(need string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if err := s.authenticate(c); err != nil {
			return err
		}
		orgID := pathID(c, "id")
		if orgID == 0 {
			orgID = pathID(c, "orgId")
		}
		u := currentUser(c)
		if u == nil || u.ID == 0 {
			return apierr.New(fiber.StatusUnauthorized, apierr.CodeLoginRequired, "login required")
		}
		role, _ := s.store.OrgMemberRole(orgID, u.ID)
		if role == "" {
			return apierr.New(fiber.StatusNotFound, apierr.CodeOrgNotFound, "organization not found")
		}
		c.Locals("org_role", role)
		method := c.Method()
		if role == roleViewer && method != fiber.MethodGet && method != fiber.MethodHead && method != fiber.MethodOptions {
			return apierr.New(fiber.StatusForbidden, apierr.CodeForbidden, "viewer role cannot perform write actions")
		}
		switch need {
		case roleOwner:
			if role != roleOwner {
				return apierr.New(fiber.StatusForbidden, "owner_role_required", "owner role required")
			}
		case roleAdmin:
			if role != roleOwner && role != roleAdmin {
				return apierr.New(fiber.StatusForbidden, "admin_role_required", "admin role required")
			}
		case roleMember:
			if role != roleOwner && role != roleAdmin && role != roleMember {
				return apierr.New(fiber.StatusForbidden, "member_role_required", "member role required")
			}
		}
		err := c.Next()
		if err == nil {
			m := c.Method()
			if m == fiber.MethodPost || m == fiber.MethodPatch || m == fiber.MethodDelete {
				_ = s.store.CreateAuditLog(orgID, nil, &u.ID, m+" "+c.Path(), fmt.Sprintf("org:%d", orgID), clientIP(c))
			}
		}
		return err
	}
}

func (s *Server) allow(c fiber.Ctx, projectID int64, need string) error {
	if apiTok, ok := c.Locals(apiTokenKey).(*store.APIToken); ok {
		p, err := s.store.ProjectByID(projectID)
		if err != nil || p == nil {
			return apierr.New(fiber.StatusNotFound, apierr.CodeProjectNotFound, "project not found")
		}
		if p.OrgID == nil || *p.OrgID != apiTok.OrgID {
			return apierr.New(fiber.StatusNotFound, apierr.CodeProjectNotFound, "project not found")
		}
		if apiTok.ProjectID != nil && *apiTok.ProjectID != projectID {
			return apierr.New(fiber.StatusNotFound, apierr.CodeProjectNotFound, "project not found")
		}
		path := c.Path()
		method := c.Method()
		if strings.Contains(path, "/sourcemaps") {
			if !hasScope(apiTok.Scopes, "sourcemaps:write") && !hasScope(apiTok.Scopes, "admin") && !hasScope(apiTok.Scopes, "*") {
				return apierr.New(fiber.StatusForbidden, apierr.CodeForbidden, "token missing sourcemaps:write scope")
			}
			return nil
		}
		if method == fiber.MethodGet || method == fiber.MethodHead || method == fiber.MethodOptions {
			if !hasScope(apiTok.Scopes, "read") && !hasScope(apiTok.Scopes, "admin") && !hasScope(apiTok.Scopes, "*") {
				return apierr.New(fiber.StatusForbidden, apierr.CodeForbidden, "token missing read scope")
			}
			return nil
		}
		if !hasScope(apiTok.Scopes, "admin") && !hasScope(apiTok.Scopes, "*") {
			return apierr.New(fiber.StatusForbidden, apierr.CodeForbidden, "token missing admin scope")
		}
		return nil
	}

	role, _ := s.store.MemberRole(projectID, currentUser(c).ID)
	if role == "" {
		return apierr.New(fiber.StatusNotFound, apierr.CodeProjectNotFound, "project not found")
	}
	method := c.Method()
	if role == roleViewer && method != fiber.MethodGet && method != fiber.MethodHead && method != fiber.MethodOptions {
		return apierr.New(fiber.StatusForbidden, apierr.CodeForbidden, "viewer role cannot perform write actions")
	}
	if need == roleOwner && role != roleOwner && role != roleAdmin {
		return apierr.New(fiber.StatusForbidden, apierr.CodeProjectOwnerNeeded, "owner role required")
	}
	return nil
}

func currentUser(c fiber.Ctx) *store.User {
	u, _ := c.Locals(userKey).(*store.User)
	return u
}

func pathID(c fiber.Ctx, name string) int64 {
	id, _ := strconv.ParseInt(c.Params(name), 10, 64)
	return id
}

func queryInt(c fiber.Ctx, name string) int {
	n, _ := strconv.Atoi(c.Query(name))
	return n
}

// bearer reads the token from the Authorization header, or from `?token=` —
// the replay player loads frame images with an <img> tag, which cannot carry a
// header.
func bearer(c fiber.Ctx) string {
	if h := c.Get(fiber.HeaderAuthorization); strings.HasPrefix(h, "Bearer ") {
		if t := strings.TrimPrefix(h, "Bearer "); t != "" {
			return t
		}
	}
	return c.Query("token")
}

// clientIP is the address recorded on a session.
//
// Order matters and is deliberate: a CDN header first (Cloudflare rewrites the
// others), then the standard Forwarded header, then the de-facto X-Forwarded-For
// whose first entry is the client, then X-Real-IP, and finally the connection
// itself. Fiber's own c.IP() is not used because it reads a single configured
// header, and a deployment behind Cloudflare *and* nginx sends several.
//
// Any of these headers can be forged by a direct client; they are only
// trustworthy when the port is reachable through the proxy alone.
func clientIP(c fiber.Ctx) string {
	for _, h := range []string{"CF-Connecting-IP", "True-Client-IP"} {
		if v := strings.TrimSpace(c.Get(h)); v != "" {
			return v
		}
	}
	if fwd := c.Get("Forwarded"); fwd != "" {
		if ip := forwardedFor(fwd); ip != "" {
			return ip
		}
	}
	if xff := c.Get(fiber.HeaderXForwardedFor); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			xff = xff[:i]
		}
		return strings.TrimSpace(xff)
	}
	if rip := c.Get("X-Real-IP"); rip != "" {
		return strings.TrimSpace(rip)
	}
	return c.IP()
}

// forwardedFor: `Forwarded: for=1.2.3.4;proto=https, for=10.0.0.1` → the first
// `for` value, with any port and quoting stripped.
func forwardedFor(v string) string {
	first := v
	if i := strings.Index(first, ","); i >= 0 {
		first = first[:i]
	}
	for _, part := range strings.Split(first, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "for") {
			ip := strings.Trim(strings.TrimSpace(kv[1]), "\"[]")
			if i := strings.LastIndex(ip, ":"); i > 0 && strings.Count(ip, ":") == 1 {
				ip = ip[:i] // IPv4:port
			}
			return ip
		}
	}
	return ""
}
