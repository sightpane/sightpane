// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"os"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
)

// sdkScript serves the script-tag build of the browser SDK, so a plain HTML
// page loads the SDK from the same origin it reports to:
//
//	<script src="https://sightpane.example.com/js/sightpane.js" data-key="dev"></script>
//
// The file is SIGHTPANE_SDK_JS; the Docker image builds it from sightpane/ts-sdk.
// A short max-age keeps a bundle upgrade from waiting on every visitor's cache
// while still letting a busy site fetch it once per minute per browser.
func (s *Server) sdkScript(c fiber.Ctx) error {
	if s.sdkJS == "" {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "browser sdk not bundled: set SIGHTPANE_SDK_JS")
	}
	if st, err := os.Stat(s.sdkJS); err != nil || st.IsDir() {
		return apierr.New(fiber.StatusNotFound, apierr.CodeNotFound, "browser sdk file missing: "+s.sdkJS)
	}
	c.Set(fiber.HeaderContentType, "text/javascript; charset=utf-8")
	c.Set(fiber.HeaderCacheControl, "public, max-age=300")
	// Not compressed: fasthttp writes the gzip copy next to the source file,
	// and in the image that directory is a read-only mount. 16 KB is fine.
	return c.SendFile(s.sdkJS, fiber.SendFile{Compress: false})
}
