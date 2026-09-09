---
name: issue-writer
description: Write GitHub issues for sightpane/sightpane that a developer or agent can start on without a second conversation — every issue names the exact files and symbols in the Go backend (Fiber v3, TimescaleDB, the ingest API), the envelope/API contract involved, the tests that pin current behaviour, and a fix shape. Use whenever the user asks to "create an issue", "file this", "open a ticket", "issue aç", "turn these findings into issues", or asks to convert audit results, a PR review comment, or a bug report into GitHub issues — the house default is always this level of detail.
---

> **This repository is one of three.** [sightpane/sightpane](https://github.com/sightpane/sightpane)
> is the Go backend, [sightpane/ui](https://github.com/sightpane/ui) the Flutter web
> dashboard, [sightpane/flutter](https://github.com/sightpane/flutter) the Dart SDK.
> The only thing they share is the envelope contract (`POST /api/v1/envelope`), so a
> change to it needs a matching change in the other two — say so explicitly rather
> than assuming whoever reads this can see them.

# Issue Writer

An issue in this repo is read by someone with **zero context** — often an agent with a
fresh window that sees the issue text and nothing else. The biggest failure is an issue
that describes a problem correctly but leaves the reader to find where it lives.

So: **an issue must name its coordinates.** Files with line numbers, which of the three
sub-projects, the table/column, the envelope item or API endpoint, the tests that
pin today's behaviour, the README section that explains the subsystem. If you cannot name
them, you have not researched enough to file yet.

## When this triggers

Any request to create, file, or open issues — including "turn these findings into
issues", "file the top N", "issue aç". When the user hands you a list and says nothing
about depth, use the full template below, not a one-paragraph stub.

## Workflow

### 1. Research before you write

- **Code coordinates** — `grep -rn` the symbol across `package/lib`, `backend/*.go`,
  `frontend/lib`; open the file and confirm the line still says what you think.
- **Contract coordinates** — which envelope item type/field (`package/lib/src/models.dart`
  `SightpaneItem.*` ↔ `internal/store/store.go` `itemHead` + `Ingest` switch), which endpoint
  (`backend/api.go` route table), which column (a new file under `internal/store/migrations/` +
  the `ALTER TABLE … ADD COLUMN` list), which env var (`main.go` `env(...)`,
  `frontend/lib/core/config.dart`, `package/lib/src/options.dart`).
- **Blast radius** — every caller; a helper used by both the SDK and the dashboard
  models is a different issue than one used once.
- **Existing pins** — the tests that assert the current behaviour:
  `package/test/*.dart` (queue, replay, pointer, widgets, hog), `backend/*_test.go`
  (api, proxyproto, fingerprint), `frontend/test/*.dart` (auth, projects, data pages,
  prefetcher). These are the files the fix must change and the reader's best spec.
- **Already filed?** `env -u GITHUB_TOKEN gh issue list --search "…" --state all`, and
  the local backlog `future-todo-files/NN-*.md` (Sentry-parity items written to this
  template, not yet on GitHub). Link or move, don't duplicate; when a backlog file is
  filed, add the issue number at its top and keep the file until the issue closes.

Verify, don't trust. A README line or an old chat note can be stale.

### 2. Decide the grouping

Fewer, meatier issues. Combine items that share a file, a subsystem, or a single
decision; keep separate what needs different reviewers or risk, or is blocked.
State the grouping back to the user.

### 3. Write each issue to the template

```markdown
## Problem

What is wrong, in two or three sentences, with measured evidence — counts, session ids,
frame sizes, timings, the exact log line. Lead with the user-visible symptom.

## Why it matters

The consequence and its bounds ("only web", "only when SIGHTPANE_PROXY_PROTOCOL=1", "every
session since the schema change").

## Where to look

**Code**
- `backend/store.go:540` — `Ingest`, the `heartbeat` case; why it is the site
- `package/lib/src/queue.dart:88` — `_drain`, the retry path
- `frontend/lib/features/sessions/session_detail_page.dart` — the consumer

**Contract / data**
- Envelope item `pointer` — `events[{t,x,y,k}]`, ms offsets from item `ts`
- `sessions.visitor_key` — sha1(user|ip|browser) first 8 bytes, set on every envelope
- `GET /api/v1/projects/{id}/live?window=` — response shape

**Tests that pin current behaviour**
- `backend/api_test.go::TestVisitorsAndLive`
- `package/test/queue_test.dart` "a failed send keeps items and retries with backoff"

**Docs / prior art**
- `README.md` → "Ziyaretçi" / "Canlı"
- Related: #12 (the other half), #7 (superseded)

## Fix shape

A sketch, not a mandate. Name the approach considered and rejected, and why. Flag what
makes it harder than it looks (e.g. "prefix is fixed at egress start, so this needs a
rotation loop", "keep the object-store round trip out of the ingest transaction").

## Acceptance

- [ ] Concrete, checkable outcomes
- [ ] The verification step, not just the change (which command, what it prints)
```

### 4. Repo-specific things to get right

- **Three parts, one contract.** If the fix changes an envelope item or field, the issue
  covers both `package/lib/src/models.dart` and `internal/store/store.go` (and the dashboard
  model in `frontend/lib/core/models.dart` when it is displayed). Old SDKs keep sending
  the old shape — say whether the backend must stay backward compatible.
- **Schema changes are code.** A new column goes into `migrate()` twice: the `CREATE
  TABLE` and the `ALTER TABLE … ADD COLUMN` list for existing databases. An issue that
  adds a column must say so.
- **Env vars are config.** Name the variable, its default, where it is read
  (`main.go`, `internal/store/store.go`, `frontend/lib/core/config.dart`,
  `package/lib/src/options.dart`), and update `docker-compose.yml` / READMEs.
- **Tests must fail first.** When the fix needs a test, require reverting the fix and
  watching the new test go red. A green suite is not coverage.
- **Widget-test hygiene.** Flutter tests in this repo must end with `await Sightpane.close()`
  (SDK) or share one `ProviderContainer` via `pumpApp` (dashboard); an issue touching
  tests should say which harness it uses.
- **Docker.** If the change affects the image (Flutter version pin in `Dockerfile`, a
  new build arg, a new port), the acceptance list includes `docker build` and a run
  check.
- **Do not file known, documented limits** (README "Sınırlar" / "Bilinen sınırlar")
  as bugs unless the request is to lift the limit.

### 5. Create them

```bash
env -u GITHUB_TOKEN gh issue create \
  --title "fix(backend): heartbeat does not reopen a session marked ended" \
  --body-file /tmp/issue-1.md \
  --assignee <login>
```

Titles use conventional-commit prefixes with a scope (`sdk`, `backend`, `dashboard`,
`docker`, `docs`) and name the defect, not the area. Write the body to a file so
backticks and newlines survive. Labels: only ones that exist (`gh label list`).
`--assignee`: confirm the login with `gh api user --jq .login` when the user says "me".

### 6. Report back

Issue numbers with one line each, plus: what you **combined** and why; what you **did
not file** because the claim no longer held; what is **blocked** outside the repo (an
egress service, MinIO credentials, a Caddy config the owner must change).

## Good vs bad

**Bad** — the reader has to redo your investigation:

> Visitor count is wrong when the same user logs in twice.

**Good** — the reader starts at the right file with the right facts:

> `stats.users` counts `COUNT(DISTINCT visitor_key)` (`backend/store.go:Stats`), and
> `visitor_key` is `sha1(user_id|ip|browser)` set on every envelope (`Ingest`). The
> browser label falls back to `device.os` when the SDK sends no `browser`
> (`BrowserLabel`), so pre-0.1.0 SDKs report `"web"` for every browser and Chrome +
> Firefox from one account collapse into one visitor. The dashboard shows this as
> "web · web" (`frontend/lib/features/sessions/sessions_page.dart` `platformLabel`).
> `TestVisitorsAndLive` pins the current behaviour with `browser` present; there is no
> case for a missing `browser` + present `user_agent`.
