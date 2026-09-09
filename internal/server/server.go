// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package server is the HTTP layer: a Fiber app with the routing table, the
// auth and membership middleware, and one handler file per resource. It holds
// no SQL — everything it needs comes from internal/store.
package server

import (
	"io/fs"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/static"

	"sightpane/internal/apierr"
	"sightpane/internal/store"
)

// maxEnvelopeBytes caps one SDK envelope. Frames are base64 PNGs, so a batch of
// them is the only thing that ever approaches this.
const maxEnvelopeBytes = 32 << 20

// SourceURL satisfies AGPL §13: everyone served over the network is told where
// the source is. It is also shown in the dashboard.
const SourceURL = "https://github.com/sightpane/sightpane"

// userKey is where requireAuth stores the caller for the handlers that follow.
const userKey = "sightpane_user"

type Server struct {
	store *store.Store
	// ui serves the dashboard: a directory when SIGHTPANE_UI_DIR is set, otherwise
	// the small embedded placeholder page.
	ui fiber.Handler
}

// New builds the Fiber app. [uiDir] wins when non-empty; [embedded] is the
// fallback filesystem compiled into the binary. Either may be absent, in which
// case only the API is served.
func New(st *store.Store, uiDir string, embedded fs.FS) *fiber.App {
	s := &Server{store: st, ui: uiHandler(uiDir, embedded)}

	app := fiber.New(fiber.Config{
		AppName: "sightpane",
		// One envelope can legitimately be large; anything past this is a bug
		// or an attack, and 413 tells the SDK to stop retrying.
		BodyLimit: maxEnvelopeBytes,
		// Every handler returns an error instead of writing a status itself, so
		// the status/code/message shape lives in exactly one place.
		ErrorHandler: errorHandler,
	})

	// A panic in one request must not take the process down with it; ingest
	// runs on data we did not write.
	app.Use(recover.New())
	// The SDK and the dashboard both post from another origin.
	app.Use(cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowHeaders: []string{"Content-Type", "X-Sightpane-Key", "X-Hog-Key", "Authorization"},
		AllowMethods: []string{
			fiber.MethodGet, fiber.MethodPost, fiber.MethodPatch,
			fiber.MethodDelete, fiber.MethodOptions,
		},
	}))

	api := app.Group("/api/v1")

	// SDK ingest: authenticated by the project's API key, not a user token.
	api.Post("/envelope", s.ingest)
	api.Get("/health", s.health)

	// Identity.
	api.Post("/auth/register", s.register)
	api.Post("/auth/login", s.login)
	api.Post("/auth/logout", s.requireAuth, s.logout)
	api.Get("/auth/me", s.requireAuth, s.me)
	api.Patch("/auth/me", s.requireAuth, s.updateMe)

	// Projects. Middleware comes first in a Fiber route: every handler passed to
	// api.Get/Post runs in argument order, so putting requireAuth second would
	// run the handler with no user on the context.
	// requireProject also resolves membership, so a non-member gets 404 rather
	// than learning that the project exists.
	api.Get("/projects", s.requireAuth, s.listProjects)
	api.Post("/projects", s.requireAuth, s.createProject)
	api.Get("/projects/:id", s.requireProject(roleMember), s.getProject)
	api.Patch("/projects/:id", s.requireProject(roleOwner), s.updateProject)
	api.Delete("/projects/:id", s.requireProject(roleOwner), s.deleteProject)
	api.Post("/projects/:id/rotate-key", s.requireProject(roleOwner), s.rotateKey)
	api.Get("/projects/:id/members", s.requireProject(roleMember), s.listMembers)
	api.Post("/projects/:id/members", s.requireProject(roleOwner), s.addMember)
	api.Delete("/projects/:id/members/:uid", s.requireProject(roleOwner), s.removeMember)
	api.Get("/projects/:id/stats", s.requireProject(roleMember), s.stats)
	api.Get("/projects/:id/live", s.requireProject(roleMember), s.live)
	api.Get("/projects/:id/sessions", s.requireProject(roleMember), s.listSessions)
	api.Get("/projects/:id/issues", s.requireProject(roleMember), s.listIssues)
	api.Get("/projects/:id/events/summary", s.requireProject(roleMember), s.eventSummary)

	// Session and issue details are addressed globally, so each one resolves its
	// own project before checking membership.
	api.Get("/sessions/:id", s.requireAuth, s.getSession)
	api.Get("/sessions/:id/frames/:seq", s.requireAuth, s.getFrame)
	api.Get("/issues/:id", s.requireAuth, s.getIssue)
	api.Post("/issues/:id/resolve", s.requireAuth, s.resolveIssue)

	// The dashboard is last so it never shadows an API route.
	if s.ui != nil {
		app.Use("/", s.ui)
	}
	return app
}

func (s *Server) health(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":  "ok",
		"license": "AGPL-3.0-or-later",
		"source":  SourceURL,
		"locales": store.SupportedLocales,
	})
}

// uiHandler serves a built dashboard from [dir], falling back to [embedded].
//
// Both are single-page apps: an unknown path is a client route, not a 404, so
// anything that is not a file on disk gets index.html and the router in the
// browser takes over. The one exception is /api/, which must keep answering
// with a JSON 404 — an SDK or a fetch() that mistypes a path needs an error it
// can read, not the dashboard's HTML.
func uiHandler(dir string, embedded fs.FS) fiber.Handler {
	notAnAPIPath := func(c fiber.Ctx) error {
		if strings.HasPrefix(c.Path(), "/api/") {
			return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "not found")
		}
		return nil
	}
	cfg := static.Config{
		IndexNames: []string{"index.html"},
		Browse:     false,
	}
	if dir != "" {
		cfg.NotFoundHandler = func(c fiber.Ctx) error {
			if err := notAnAPIPath(c); err != nil {
				return err
			}
			// A path that is not a file is a client-side route, so it gets
			// index.html and a 200 — a 404 here would make the browser show an
			// error page instead of letting the router take over.
			c.Status(fiber.StatusOK)
			return c.SendFile(dir+"/index.html", fiber.SendFile{Compress: false})
		}
		return static.New(dir, cfg)
	}
	if embedded == nil {
		return nil
	}
	cfg.FS = embedded
	cfg.NotFoundHandler = func(c fiber.Ctx) error {
		if err := notAnAPIPath(c); err != nil {
			return err
		}
		b, err := fs.ReadFile(embedded, "index.html")
		if err != nil {
			return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "not found")
		}
		c.Status(fiber.StatusOK).Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		return c.Send(b)
	}
	return static.New("", cfg)
}
