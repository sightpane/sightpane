// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
	"sightpane/internal/store"
)

// projectKey reads the project's API key. `X-Hog-Key` is the name the header had
// before the project was renamed; SDKs already in the field still send it, and an
// app is not rebuilt just because the server was upgraded, so both are accepted.
func projectKey(c fiber.Ctx) string {
	if k := c.Get("X-Sightpane-Key"); k != "" {
		return k
	}
	return c.Get("X-Hog-Key")
}

// ingest is the SDK's only endpoint. It is authenticated by the project's API
// key rather than a user token: the key ships inside the app, so it can write
// envelopes and nothing else.
//
// The reply is 202 with {accepted, rejected} — an item type this build does not
// know is counted as rejected rather than failing the batch, so an SDK that is
// newer than the backend still gets the rest of its data stored.
func (s *Server) ingest(c fiber.Ctx) error {
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
			if !hasScope(apiTok.Scopes, "ingest") && !hasScope(apiTok.Scopes, "admin") && !hasScope(apiTok.Scopes, "*") {
				return apierr.New(fiber.StatusForbidden, apierr.CodeForbidden, "api token missing ingest scope")
			}
			if apiTok.ProjectID != nil {
				p, err = s.store.ProjectByID(*apiTok.ProjectID)
			} else {
				pid := pathID(c, "id")
				if pid == 0 {
					pid = int64(queryInt(c, "project_id"))
				}
				if pid != 0 {
					p, err = s.store.ProjectByID(pid)
					if err == nil && (p.OrgID == nil || *p.OrgID != apiTok.OrgID) {
						p = nil
						err = store.ErrNotFound
					}
				} else {
					projs, pErr := s.store.ListProjectsByOrg(apiTok.OrgID)
					if pErr == nil && len(projs) > 0 {
						p = &projs[0]
					} else {
						return apierr.New(fiber.StatusBadRequest, apierr.CodeProjectNotFound, "project not found for token")
					}
				}
			}
			if p == nil || errors.Is(err, store.ErrNotFound) {
				return apierr.New(fiber.StatusNotFound, apierr.CodeProjectNotFound, "project not found")
			}
			if err != nil {
				return err
			}
		} else {
			return apierr.New(fiber.StatusUnauthorized, apierr.CodeKeyRequired,
				"x-sightpane-key header required")
		}
	}
	// The app's body limit is sized for source-map uploads; an envelope has its
	// own, smaller cap, so this is where it is enforced.
	if len(c.Body()) > maxEnvelopeBytes {
		return apierr.New(fiber.StatusRequestEntityTooLarge, apierr.CodeEnvelopeTooBig, "envelope too large")
	}
	var env store.Envelope
	if err := json.Unmarshal(c.Body(), &env); err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "invalid json: "+err.Error())
	}

	// Rate limiting / quota check
	itemCount := len(env.Items)
	if itemCount == 0 {
		itemCount = 1
	}
	if s.limiter != nil {
		allowed, retryAfter := s.limiter.Allow(p.ID, p.QuotaItemsPerMinute, itemCount)
		if !allowed {
			s.store.RecordDroppedQuota(p.ID, itemCount)
			c.Set("Retry-After", strconv.Itoa(retryAfter))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":       "quota exceeded",
				"rejected":    itemCount,
				"retry_after": retryAfter,
			})
		}
	}

	res, err := s.store.Ingest(c.Context(), p.ID, &env, clientIP(c))
	if err != nil {
		// Logged because a malformed envelope is a bug in the SDK or in us, and
		// the sender only sees the status.
		log.Printf("ingest: %v", err)
		return asStatus(err, fiber.StatusBadRequest, apierr.CodeEnvelopeBad)
	}
	return c.Status(fiber.StatusAccepted).JSON(res)
}
