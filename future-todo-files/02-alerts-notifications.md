# feat(backend,dashboard): alert rules and notification channels (email, Slack, webhook)

## Problem

A new issue, a resolved issue coming back, or a spike in the error rate is only noticed
by looking at the dashboard. The backend already knows about these events
(`Ingest` does the reopen with `INSERT INTO issues … ON CONFLICT … resolved=0`)
but emits no signal to the outside world.

## Why it matters

A monitoring tool's job is to tell you. In Casino CRM an error on the cashier/inspector
screen can go unnoticed for hours.

## Where to look

**Code**
- `backend/internal/store/ingest.go` — `Ingest`, `error` branch: the issue upsert happens here;
  whether it is new or was reopened can be read from `RowsAffected` or from a `SELECT`
  beforehand.
- `backend/internal/store/stats.go` — `Stats`: error count per day; the window query for a rate
  rule is similar.
- `backend/internal/server/server.go` — `New`, the route table; the `projects/{id}/alerts` and
  `alert-channels` endpoints go here, behind `s.requireProject(roleOwner)`.
- `frontend/lib/features/projects/settings_page.dart:14` — the member management panel;
  the rule and channel panels belong on the same page.
- `docker-compose.yml`, the env table in `backend/README.md` — SMTP and Slack settings.

**Contract / data**
- New tables: `alert_channels(project_id, kind[email|slack|webhook], target, secret)`,
  `alert_rules(project_id, kind[new_issue|regression|rate_spike|session_crash_free], params
  json, channel_ids, enabled)`, `alert_deliveries(rule_id, issue_id, sent_at, status,
  error)` (de-duplication + history).
- Env: `SMTP_HOST/PORT/USER/PASS/FROM`, optionally `SIGHTPANE_PUBLIC_URL` (the link in the notification).

**Tests that pin current behaviour**
- `backend/internal/server/server_test.go::TestIngestAndQuery` — the part where a resolved issue
  is reopened when it is seen again ("regressed issue").

**Docs**
- `backend/README.md` → "Error grouping", the env table.

## Fix shape

- Once the ingest transaction commits, drop an `IssueEvent{project, issue, kind:
  new|regressed}` onto an in-memory event channel; a separate goroutine evaluates the
  rules and sends to the channels (so it does not add to ingest latency).
- For the rate rule, a per-minute tick: if the errors / sessions ratio over the last N
  minutes crosses the threshold, notify once, with a cooldown (`cooldown_minutes`).
- Channels: email (`net/smtp`, stdlib), Slack incoming webhook (JSON POST), generic
  webhook (signed: `X-Sightpane-Signature`, HMAC-SHA256 with `secret`).
- Notification body: project, title, count, first/last seen, a deep link into the
  dashboard (`SIGHTPANE_PUBLIC_URL/projects/{id}/issues/{iid}`), and the route and browser of
  the latest occurrence.
- In the dashboard: Settings → an "Alerts" panel (rule list, add channel, "send test").

Rejected: real-time push (WebSocket) — the dashboard already refreshes once a second;
the actual need is reaching someone who is not looking at the dashboard.

## Acceptance

- [x] Slack/email notifications for a new issue and for a regression (fake SMTP/HTTP server in tests)
- [x] No duplicate send for the same event (via `alert_deliveries`), and the cooldown works
- [x] Ingest time does not change measurably (notification happens in a goroutine)
- [x] Rule/channel CRUD and "send test" on the settings page (widget test with FakeApi)
- [x] Env table in the README and a compose example
