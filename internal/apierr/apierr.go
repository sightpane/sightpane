// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apierr defines the API error codes shared by the store and the HTTP
// layer. It sits below both so a known failure can carry its status and code
// from the place it is detected, instead of every handler repeating a mapping
// table.
//
// The response body is `{"error": "<English text>", "code": "<code>"}`. The
// text is for humans reading curl output; the **code** is the contract the
// dashboard translates against (frontend/lib/core/auth.dart `describeError`).
// Reword the text freely, but never repurpose a code — add a new one.
package apierr

import "fmt"

const (
	CodeInternal = "internal"
	CodeBadJSON  = "bad_json"
	CodeNotFound = "not_found"

	CodeLoginRequired      = "auth.login_required"
	CodeInvalidToken       = "auth.invalid_token"
	CodeInvalidCredentials = "auth.invalid_credentials"
	CodeEmailTaken         = "auth.email_taken"
	CodeInvalidEmail       = "auth.invalid_email"
	CodePasswordTooShort   = "auth.password_too_short"
	CodeUnsupportedLocale  = "auth.unsupported_locale"

	CodeProjectNotFound    = "project.not_found"
	CodeProjectOwnerNeeded = "project.owner_required"
	CodeProjectNameNeeded  = "project.name_required"

	CodeMemberUnknownEmail = "member.unknown_email"
	CodeMemberSelfRemove   = "member.owner_self_remove"

	CodeSessionNotFound = "session.not_found"
	CodeFrameNotFound   = "frame.not_found"
	CodeIssueNotFound   = "issue.not_found"
	CodeUserIDRequired  = "user.id_required"

	CodeReleaseRequired  = "release.required"
	CodeArtifactName     = "release.artifact_name"
	CodeArtifactNotFound = "release.artifact_not_found"

	CodeKeyRequired    = "envelope.key_required"
	CodeUnknownKey     = "envelope.unknown_key"
	CodeEnvelopeTooBig = "envelope.too_large"
	CodeEnvelopeBad    = "envelope.invalid"

	CodeAlertChannelNotFound = "alert_channel.not_found"
	CodeAlertRuleNotFound    = "alert_rule.not_found"
	CodeAlertChannelInvalid  = "alert_channel.invalid"
	CodeAlertRuleInvalid     = "alert_rule.invalid"
	CodeAlertSendFailed      = "alert.send_failed"

	CodeSearchInvalid = "search.invalid_query"
)

// Error carries an HTTP status, a stable code and an English message.
type Error struct {
	Status int
	Code   string
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func New(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Msg: msg}
}

func Newf(status int, code, format string, a ...any) *Error {
	return &Error{Status: status, Code: code, Msg: fmt.Sprintf(format, a...)}
}
