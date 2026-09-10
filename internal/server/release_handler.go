// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"io"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
)

// Release artifacts: the source maps that make a minified release stack trace
// readable. Uploading one is an owner action — it is a build output, posted by
// whatever deploys the app, with a user token rather than the project key. The
// key ships inside the app and can only write envelopes.

// uploadSourceMap takes `flutter build web --source-maps` output for one
// release. Multipart, field `file`; the name comes from the part unless the form
// overrides it.
func (s *Server) uploadSourceMap(c fiber.Ctx) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON,
			`multipart form with a "file" field required`)
	}
	name := c.FormValue("filename")
	if name == "" {
		name = fh.Filename
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	// Read into memory: the blob store writes a whole object at a time, and a
	// map that does not fit here would not fit the request either — Fiber has
	// already buffered it to satisfy BodyLimit.
	data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxUploadBytes {
		return apierr.Newf(fiber.StatusRequestEntityTooLarge, apierr.CodeEnvelopeTooBig,
			"source map larger than %d MB", maxUploadBytes>>20)
	}
	a, err := s.store.PutArtifact(c.Context(), pathID(c, "id"), c.Params("release"), name, data)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(a)
}

// listReleaseArtifacts lists what has been uploaded, for the whole project or
// for one release with `?release=`.
func (s *Server) listReleaseArtifacts(c fiber.Ctx) error {
	as, err := s.store.ListArtifacts(pathID(c, "id"), c.Query("release"))
	if err != nil {
		return err
	}
	return c.JSON(as)
}

// listReleases handles GET /projects/:id/releases
// If ?artifacts=true or ?release=... is requested, it serves the release artifacts list.
// Otherwise it serves the release health list.
func (s *Server) listReleases(c fiber.Ctx) error {
	if c.Query("artifacts") == "1" || c.Query("artifacts") == "true" || c.Query("release") != "" {
		return s.listReleaseArtifacts(c)
	}
	releases, err := s.store.ListReleases(c.Context(), pathID(c, "id"))
	if err != nil {
		return err
	}
	return c.JSON(releases)
}

func (s *Server) getRelease(c fiber.Ctx) error {
	rel, err := s.store.GetReleaseHealth(c.Context(), pathID(c, "id"), c.Params("release"))
	if err != nil {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "release not found")
	}
	return c.JSON(rel)
}

func (s *Server) deleteReleaseArtifact(c fiber.Ctx) error {
	if err := s.store.DeleteArtifact(c.Context(), pathID(c, "id"), c.Params("release"), c.Params("filename")); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

