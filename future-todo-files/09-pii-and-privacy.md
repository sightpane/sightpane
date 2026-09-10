# feat(sdk,backend): PII scrubbing rules, user data deletion and export

## Problem

`SightpaneMask` only blacks out the rectangle of the widget it wraps; text breadcrumbs
(`tap "…"` labels, gRPC messages), error messages and `session.props` go out as they are.
The backend has no "delete/export all data for this user" endpoint; the IP is written to
every session and cannot be turned off. Sentry has server-side data scrubbing rules, an IP
storage option and user data deletion.

## Why it matters

Casino customer data (name, phone, balance) can leak into breadcrumbs and error messages;
when a KVKK/GDPR request arrives there is no way to answer it.

## Where to look

**Code**
- `package/lib/src/options.dart:53` — `beforeSend` is the only hook; enough for PII rules,
  but it leaves the work to the user.
- `package/lib/src/widgets/user_interaction.dart` — `describeTapTarget`: text labels; it
  already writes "input" for `RenderEditable`, but a phone number/name inside a `Text` still
  goes out.
- `package/lib/src/replay/mask.dart` — `SightpaneMask`/`MaskRegistry`; there is no "mask all text"
  option (`SightpaneReplayOptions.maskAllText`).
- `backend/internal/store/ingest.go` — `Ingest`: writing `ip` (`clientIP`,
  `backend/internal/server/middleware.go`), `user_json`; the scrubbing
  rules belong here.
- `backend/internal/store/project.go` — the `DeleteProject` pattern; per-user deletion is similar.

**Contract / data**
- Project setting: `store_ip[full|anonymized|none]`, `scrub_rules[{field, regex, replace}]`.
- Endpoints: `DELETE /projects/{id}/users/{userId}` (sessions, items, frames),
  `GET /projects/{id}/users/{userId}/export` (JSON + a zip of frames).

**Tests that pin current behaviour**
- `package/test/widgets_test.dart` (tap labels), `replay_test.dart` (mask).
- `backend/internal/server/server_test.go::TestClientIP`, `TestIngestAndQuery` (ip recording).

## Fix shape

- SDK: `SightpaneReplayOptions.maskAllText` (all `RenderParagraph` rectangles are blacked out, with
  `HogUnmask` as the exception), `SightpaneOptions.scrub` (a list of regexes; applied to breadcrumb
  messages, error messages and props), with default rules for email, 16-digit card numbers,
  Turkish national ID and phone numbers.
- Backend: depending on the project setting, anonymise the IP to a `/24` or do not write it;
  server-side regex rules are applied at ingest (for old SDK versions that cannot be updated).
- User deletion/export endpoints + Settings → "Privacy" in the dashboard.

## Acceptance

- [x] A pixel test for text on a frame with `maskAllText` (the replay_test pattern)
- [x] The default scrub rules work on breadcrumb and error messages (SDK test with literal examples)
- [x] With IP mode `none` `sessions.ip` is empty, with `anonymized` the last octet is zero (backend test)
- [x] After user deletion no session, item or frame file is left
