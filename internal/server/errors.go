// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/apierr"
	"sightpane/internal/store"
)

// errorHandler turns whatever a handler returned into the one response shape
// the dashboard knows: `{"error": "<English text>", "code": "<code>"}`.
//
// Handlers therefore never write an error themselves — they return one, and the
// status/code mapping lives here and in the place the error was created.
func errorHandler(c fiber.Ctx, err error) error {
	var he *apierr.Error
	switch {
	case errors.As(err, &he):
		return writeErr(c, he.Status, he.Code, he.Msg)
	case errors.Is(err, store.ErrNotFound):
		return writeErr(c, fiber.StatusNotFound, apierr.CodeNotFound, "not found")
	}
	// Fiber raises this for a body over BodyLimit before any handler runs.
	var fe *fiber.Error
	if errors.As(err, &fe) && fe.Code == fiber.StatusRequestEntityTooLarge {
		return writeErr(c, fe.Code, apierr.CodeEnvelopeTooBig, "envelope too large")
	}
	return writeErr(c, fiber.StatusInternalServerError, apierr.CodeInternal, err.Error())
}

func writeErr(c fiber.Ctx, status int, code, msg string) error {
	return c.Status(status).JSON(fiber.Map{"error": msg, "code": code})
}

// badJSON is returned whenever a body will not parse. The parser's own message
// is not included: it describes our struct, not the caller's mistake.
func badJSON() error {
	return apierr.New(fiber.StatusBadRequest, apierr.CodeBadJSON, "invalid json")
}

// asStatus re-labels an error that carries no code of its own. Store errors that
// do carry one pass through untouched, so the code always wins over the caller's
// guess (an unknown database failure stays a 500, it does not become a 400).
func asStatus(err error, status int, code string) error {
	var he *apierr.Error
	if errors.As(err, &he) {
		return he
	}
	if errors.Is(err, store.ErrNotFound) {
		return err
	}
	return apierr.New(status, code, err.Error())
}
