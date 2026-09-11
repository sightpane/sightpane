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

	"sightpane/internal/alert"
	"sightpane/internal/apierr"
	"sightpane/internal/config"
	"sightpane/internal/store"
)

// maxEnvelopeBytes caps one SDK envelope. Frames are base64 PNGs, so a batch of
// them is the only thing that ever approaches this. It is checked in the ingest
// handler, because the body limit below has to be larger for source maps.
const maxEnvelopeBytes = 32 << 20

// maxUploadBytes caps a release artifact. A dart2js source map for a real app
// runs 10–30 MB, and the whole file has to arrive in one request because a map
// is only usable complete.
const maxUploadBytes = 96 << 20

// SourceURL satisfies AGPL §13: everyone served over the network is told where
// the source is. It is also shown in the dashboard.
const SourceURL = "https://github.com/sightpane/sightpane"

// userKey is where requireAuth stores the caller for the handlers that follow.
const userKey = "sightpane_user"

type Server struct {
	store    *store.Store
	notifier *alert.Notifier
	limiter  *IngestLimiter
	// ui serves the dashboard: a directory when SIGHTPANE_UI_DIR is set, otherwise
	// the small embedded placeholder page.
	ui fiber.Handler
}

// New builds the Fiber app. [uiDir] wins when non-empty; [embedded] is the
// fallback filesystem compiled into the binary. Either may be absent, in which
// case only the API is served.
func New(st *store.Store, notifier *alert.Notifier, uiDir string, embedded fs.FS, defaultIngestRate ...int) *fiber.App {
	if notifier == nil {
		notifier = alert.NewNotifier(st, config.Config{PublicURL: "http://localhost:8790"})
	}
	st.OnIssueEvent(notifier.Enqueue)
	defaultRate := 0
	if len(defaultIngestRate) > 0 {
		defaultRate = defaultIngestRate[0]
	}
	s := &Server{
		store:    st,
		notifier: notifier,
		limiter:  NewIngestLimiter(defaultRate),
		ui:       uiHandler(uiDir, embedded),
	}

	app := fiber.New(fiber.Config{
		AppName: "sightpane",
		// Sized for the largest thing anyone posts, which is a source map, not
		// an envelope; the ingest handler holds envelopes to their own, smaller
		// limit. 413 tells the SDK to stop retrying either way.
		BodyLimit: maxUploadBytes,
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
	api.Post("/flags/evaluate", s.evaluateFlags)
	api.Get("/surveys/active", s.activeSurveys)
	api.Post("/surveys/:surveyId/responses", s.submitSurveyResponse)
	api.Post("/crons/:slug/checkin", s.cronCheckin)
	api.Get("/crons/:slug/checkin", s.cronCheckin)
	api.Get("/health", s.health)

	// Identity.
	api.Post("/auth/register", s.register)
	api.Post("/auth/login", s.login)
	api.Post("/auth/logout", s.requireAuth, s.logout)
	api.Get("/auth/me", s.requireAuth, s.me)
	api.Patch("/auth/me", s.requireAuth, s.updateMe)

	// Projects. Middleware comes first in a Fiber route: every handler passed to
	// api.Get/Post runs in argument order, so putting requireAuth second would
	// Organizations, fine-grained roles, audit log and scoped tokens
	api.Get("/orgs", s.requireAuth, s.listOrgs)
	api.Post("/orgs", s.requireAuth, s.createOrg)
	api.Get("/orgs/:id", s.requireOrg(roleViewer), s.getOrg)
	api.Patch("/orgs/:id", s.requireOrg(roleAdmin), s.updateOrg)
	api.Delete("/orgs/:id", s.requireOrg(roleOwner), s.deleteOrg)

	api.Get("/orgs/:id/members", s.requireOrg(roleViewer), s.listOrgMembers)
	api.Post("/orgs/:id/members", s.requireOrg(roleAdmin), s.addOrgMember)
	api.Patch("/orgs/:id/members/:uid", s.requireOrg(roleAdmin), s.updateOrgMemberRole)
	api.Delete("/orgs/:id/members/:uid", s.requireOrg(roleAdmin), s.removeOrgMember)

	api.Get("/orgs/:id/audit", s.requireOrg(roleMember), s.listOrgAuditLogs)

	api.Get("/orgs/:id/tokens", s.requireOrg(roleAdmin), s.listOrgAPITokens)
	api.Post("/orgs/:id/tokens", s.requireOrg(roleAdmin), s.createOrgAPIToken)
	api.Delete("/orgs/:id/tokens/:tokenId", s.requireOrg(roleAdmin), s.deleteOrgAPIToken)

	// Projects. Middleware comes first in a Fiber route: every handler passed to
	// api.Get/Post runs in argument order, so putting requireAuth second would
	// run the handler with no user on the context.
	// requireProject also resolves membership, so a non-member gets 404 rather
	// than learning that the project exists.
	api.Get("/projects", s.requireAuth, s.listProjects)
	api.Post("/projects", s.requireAuth, s.createProject)
	api.Get("/projects/:id", s.requireProject(roleViewer), s.getProject)
	api.Patch("/projects/:id", s.requireProject(roleOwner), s.updateProject)
	api.Delete("/projects/:id", s.requireProject(roleOwner), s.deleteProject)
	api.Post("/projects/:id/rotate-key", s.requireProject(roleOwner), s.rotateKey)
	api.Get("/projects/:id/members", s.requireProject(roleViewer), s.listMembers)
	api.Post("/projects/:id/members", s.requireProject(roleOwner), s.addMember)
	api.Delete("/projects/:id/members/:uid", s.requireProject(roleOwner), s.removeMember)
	api.Get("/projects/:id/stats", s.requireProject(roleViewer), s.stats)
	api.Get("/projects/:id/live", s.requireProject(roleViewer), s.live)
	api.Get("/projects/:id/sessions", s.requireProject(roleViewer), s.listSessions)
	api.Get("/projects/:id/issues", s.requireProject(roleViewer), s.listIssues)
	api.Get("/projects/:id/events/summary", s.requireProject(roleViewer), s.eventSummary)
	api.Get("/projects/:id/users", s.requireProject(roleViewer), s.listProjectUsers)
	api.Delete("/projects/:id/users/:userId", s.requireProject(roleOwner), s.deleteUserData)
	api.Get("/projects/:id/users/:userId/export", s.requireProject(roleViewer), s.exportUserData)

	// Alerts and notification channels (owner only).
	api.Get("/projects/:id/alert-channels", s.requireProject(roleOwner), s.listAlertChannels)
	api.Post("/projects/:id/alert-channels", s.requireProject(roleOwner), s.createAlertChannel)
	api.Patch("/projects/:id/alert-channels/:cid", s.requireProject(roleOwner), s.updateAlertChannel)
	api.Delete("/projects/:id/alert-channels/:cid", s.requireProject(roleOwner), s.deleteAlertChannel)
	api.Post("/projects/:id/alert-channels/:cid/test", s.requireProject(roleOwner), s.testAlertChannel)
	api.Get("/projects/:id/alerts", s.requireProject(roleOwner), s.listAlertRules)
	api.Post("/projects/:id/alerts", s.requireProject(roleOwner), s.createAlertRule)
	api.Patch("/projects/:id/alerts/:rid", s.requireProject(roleOwner), s.updateAlertRule)
	api.Delete("/projects/:id/alerts/:rid", s.requireProject(roleOwner), s.deleteAlertRule)

	// Releases and release artifacts.
	api.Get("/projects/:id/releases", s.requireProject(roleViewer), s.listReleases)
	api.Get("/projects/:id/releases/:release", s.requireProject(roleViewer), s.getRelease)
	api.Get("/projects/:id/release-artifacts", s.requireProject(roleViewer), s.listReleaseArtifacts)
	api.Post("/projects/:id/releases/:release/sourcemaps", s.requireProject(roleOwner), s.uploadSourceMap)
	api.Delete("/projects/:id/releases/:release/artifacts/:filename", s.requireProject(roleOwner), s.deleteReleaseArtifact)

	// Performance monitoring.
	api.Get("/projects/:id/performance", s.requireProject(roleViewer), s.getPerformanceSummary)
	api.Get("/projects/:id/performance/detail", s.requireProject(roleViewer), s.getTransactionDetail)
	api.Get("/projects/:id/performance/transactions/*", s.requireProject(roleViewer), s.getTransactionDetail)


	// Session and issue details are addressed globally, so each one resolves its
	// own project before checking membership.
	api.Get("/sessions/:id", s.requireAuth, s.getSession)
	api.Get("/sessions/:id/frames/:seq", s.requireAuth, s.getFrame)
	api.Get("/issues/:id", s.requireAuth, s.getIssue)
	api.Post("/issues/:id/resolve", s.requireAuth, s.resolveIssue)
	api.Post("/issues/:id/assign", s.requireAuth, s.assignIssue)
	api.Post("/issues/:id/status", s.requireAuth, s.setIssueStatus)
	api.Post("/issues/:id/snooze", s.requireAuth, s.snoozeIssue)
	api.Post("/issues/:id/merge", s.requireAuth, s.mergeIssue)
	api.Get("/issues/:id/comments", s.requireAuth, s.listIssueComments)
	api.Post("/issues/:id/comments", s.requireAuth, s.addIssueComment)

	// Fingerprint rules
	api.Get("/projects/:id/fingerprint-rules", s.requireProject(roleViewer), s.listFingerprintRules)
	api.Post("/projects/:id/fingerprint-rules", s.requireProject(roleOwner), s.createFingerprintRule)
	api.Delete("/projects/:id/fingerprint-rules/:ruleId", s.requireProject(roleOwner), s.deleteFingerprintRule)

	// Funnels
	api.Get("/projects/:id/funnels", s.requireProject(roleViewer), s.listFunnels)
	api.Post("/projects/:id/funnels", s.requireProject(roleMember), s.createFunnel)
	api.Get("/projects/:id/funnels/:funnelId", s.requireProject(roleViewer), s.getFunnel)
	api.Patch("/projects/:id/funnels/:funnelId", s.requireProject(roleMember), s.updateFunnel)
	api.Delete("/projects/:id/funnels/:funnelId", s.requireProject(roleMember), s.deleteFunnel)
	api.Get("/projects/:id/funnels/:funnelId/results", s.requireProject(roleViewer), s.getFunnelResults)
	api.Get("/projects/:id/funnels/:funnelId/dropoffs", s.requireProject(roleViewer), s.getFunnelDropoffs)

	// Cohorts & Retention
	api.Get("/projects/:id/cohorts", s.requireProject(roleViewer), s.listCohorts)
	api.Post("/projects/:id/cohorts", s.requireProject(roleMember), s.createCohort)
	api.Get("/projects/:id/cohorts/:cohortId", s.requireProject(roleViewer), s.getCohort)
	api.Patch("/projects/:id/cohorts/:cohortId", s.requireProject(roleMember), s.updateCohort)
	api.Delete("/projects/:id/cohorts/:cohortId", s.requireProject(roleMember), s.deleteCohort)
	api.Get("/projects/:id/cohorts/:cohortId/members", s.requireProject(roleViewer), s.listCohortMembers)
	api.Post("/projects/:id/cohorts/:cohortId/refresh", s.requireProject(roleMember), s.refreshCohort)
	api.Get("/projects/:id/retention", s.requireProject(roleViewer), s.getRetentionMatrix)

	// User Paths / Flows
	api.Get("/projects/:id/paths", s.requireProject(roleViewer), s.getPathFlow)
	api.Get("/projects/:id/paths/sessions", s.requireProject(roleViewer), s.getPathSessions)

	// Feature Flags
	api.Get("/projects/:id/feature-flags", s.requireProject(roleViewer), s.listFeatureFlags)
	api.Post("/projects/:id/feature-flags", s.requireProject(roleMember), s.createFeatureFlag)
	api.Get("/projects/:id/feature-flags/:fid", s.requireProject(roleViewer), s.getFeatureFlag)
	api.Put("/projects/:id/feature-flags/:fid", s.requireProject(roleMember), s.updateFeatureFlag)
	api.Delete("/projects/:id/feature-flags/:fid", s.requireProject(roleMember), s.deleteFeatureFlag)
	api.Post("/projects/:id/feature-flags/:fid/test", s.requireProject(roleViewer), s.testFeatureFlag)

	// A/B Testing & Experiments
	api.Get("/projects/:id/experiments", s.requireProject(roleViewer), s.listExperiments)
	api.Post("/projects/:id/experiments", s.requireProject(roleMember), s.createExperiment)
	api.Get("/projects/:id/experiments/:expId", s.requireProject(roleViewer), s.getExperiment)
	api.Put("/projects/:id/experiments/:expId", s.requireProject(roleMember), s.updateExperiment)
	api.Delete("/projects/:id/experiments/:expId", s.requireProject(roleMember), s.deleteExperiment)
	api.Get("/projects/:id/experiments/:expId/results", s.requireProject(roleViewer), s.getExperimentResults)
	api.Post("/projects/:id/experiments/:expId/winner", s.requireProject(roleMember), s.declareExperimentWinner)

	// Surveys & User Feedback
	api.Get("/projects/:id/surveys", s.requireProject(roleViewer), s.listSurveys)
	api.Post("/projects/:id/surveys", s.requireProject(roleMember), s.createSurvey)
	api.Get("/projects/:id/surveys/:surveyId", s.requireProject(roleViewer), s.getSurvey)
	api.Put("/projects/:id/surveys/:surveyId", s.requireProject(roleMember), s.updateSurvey)
	api.Delete("/projects/:id/surveys/:surveyId", s.requireProject(roleMember), s.deleteSurvey)
	api.Get("/projects/:id/surveys/:surveyId/results", s.requireProject(roleViewer), s.getSurveyResults)
	api.Get("/projects/:id/surveys/:surveyId/responses", s.requireProject(roleViewer), s.listSurveyResponses)

	// Cron Job & Heartbeat Monitoring
	api.Get("/projects/:id/crons", s.requireProject(roleViewer), s.listCronMonitors)
	api.Post("/projects/:id/crons", s.requireProject(roleMember), s.createCronMonitor)
	api.Get("/projects/:id/crons/:monitorId", s.requireProject(roleViewer), s.getCronMonitor)
	api.Put("/projects/:id/crons/:monitorId", s.requireProject(roleMember), s.updateCronMonitor)
	api.Delete("/projects/:id/crons/:monitorId", s.requireProject(roleMember), s.deleteCronMonitor)
	api.Get("/projects/:id/crons/:monitorId/checkins", s.requireProject(roleViewer), s.listCronCheckins)

	// Uptime & Synthetic Monitoring
	api.Get("/projects/:id/uptime", s.requireProject(roleViewer), s.listUptimeMonitors)
	api.Post("/projects/:id/uptime", s.requireProject(roleMember), s.createUptimeMonitor)
	api.Get("/projects/:id/uptime/:monitorId", s.requireProject(roleViewer), s.getUptimeMonitor)
	api.Put("/projects/:id/uptime/:monitorId", s.requireProject(roleMember), s.updateUptimeMonitor)
	api.Delete("/projects/:id/uptime/:monitorId", s.requireProject(roleMember), s.deleteUptimeMonitor)
	api.Post("/projects/:id/uptime/:monitorId/check", s.requireProject(roleMember), s.triggerUptimeCheck)

	// Metric Alerts & Anomaly Detection
	api.Get("/projects/:id/metric-alerts/rules", s.requireProject(roleViewer), s.listMetricAlertRules)
	api.Post("/projects/:id/metric-alerts/rules", s.requireProject(roleMember), s.createMetricAlertRule)
	api.Get("/projects/:id/metric-alerts/rules/:ruleId", s.requireProject(roleViewer), s.getMetricAlertRule)
	api.Put("/projects/:id/metric-alerts/rules/:ruleId", s.requireProject(roleMember), s.updateMetricAlertRule)
	api.Delete("/projects/:id/metric-alerts/rules/:ruleId", s.requireProject(roleMember), s.deleteMetricAlertRule)
	api.Get("/projects/:id/metric-alerts/rules/:ruleId/preview", s.requireProject(roleViewer), s.getMetricAlertRulePreview)
	api.Get("/projects/:id/metric-alerts/preview", s.requireProject(roleViewer), s.getMetricAlertPreview)
	api.Get("/projects/:id/metric-alerts/incidents", s.requireProject(roleViewer), s.listMetricAlertIncidents)
	api.Post("/projects/:id/metric-alerts/rules/:ruleId/test", s.requireProject(roleMember), s.testMetricAlertRule)

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
