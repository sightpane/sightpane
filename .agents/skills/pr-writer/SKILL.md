---
name: pr-writer
description: Update GitHub Pull Request titles and descriptions for sightpane/sightpane with precise context — what changed in the Go backend (Fiber v3, TimescaleDB, the ingest API), any change to the envelope wire protocol or the schema, and the verification actually run. Use whenever the user asks to "update the PR", "write a PR description", "PR aç", "PR'ı güncelle", or "add details to the pull request".
---

> **This repository is one of three.** [sightpane/sightpane](https://github.com/sightpane/sightpane)
> is the Go backend, [sightpane/ui](https://github.com/sightpane/ui) the Flutter web
> dashboard, [sightpane/flutter](https://github.com/sightpane/flutter) the Dart SDK.
> The only thing they share is the envelope contract (`POST /api/v1/envelope`), so a
> change to it needs a matching change in the other two — say so explicitly rather
> than assuming whoever reads this can see them.

# PR Writer

Authors professional pull request titles and descriptions for sightpane and applies them with the GitHub CLI (`gh`). Repo: `sightpane/sightpane` (three sub-projects in one repo: `package/` SDK, `backend/` Go server, `frontend/` Flutter web dashboard).

## Intent & Triggers
- "Update the PR description" / "PR'ı güncelle"
- "Write the PR title and body" / "PR açıklaması yaz"
- "Open a PR for this" / "PR aç" (create, then describe)

## Workflow

### 1. Gather context
Understand what was actually done — do not guess.
- Conversation history and `git log --oneline origin/main..HEAD`, `git diff --stat origin/main..HEAD`.
- Which parts changed: SDK (`package/lib/src`), backend (`store.go`, `api.go`, `auth.go`, `fingerprint.go`, `main.go`), dashboard (`frontend/lib`), Docker (`Dockerfile`, `docker-compose.yml`), docs (READMEs).
- **Contract changes** deserve their own bullet: a new envelope item type or field (`package/lib/src/models.dart` ↔ `internal/store/store.go` `itemHead`/`Ingest`), a new API endpoint (`backend/api.go` routes), a column (a new numbered file under `internal/store/migrations/`), a new `SIGHTPANE_*`/`SIGHTPANE_API_URL` env var.
- **CRITICAL:** only list things that were explicitly implemented in this branch.

### 2. Check existing PRs
```bash
env -u GITHUB_TOKEN gh pr status
env -u GITHUB_TOKEN gh pr view <number> --json title,body,url
```
`GITHUB_TOKEN` in this environment is a dummy that causes 401; always run `gh` with `env -u GITHUB_TOKEN`. If no PR exists and the user asked you to open one: `env -u GITHUB_TOKEN gh pr create --title … --body-file …`. Otherwise ask.

### 3. Draft the content
Write the body to a temp file (`/tmp/pr_description.md`); pass the title as a flag, not inside the body. Write in the language the user is using (the READMEs are Turkish; Turkish bodies are fine).

```markdown
## Özet / Description
[1–2 paragraphs: the objective and why it matters]

### 🚀 Ne yapıldı
#### 1. SDK (`package/`)
- **`lib/src/queue.dart`**: exact change and its effect on the user of the SDK.
#### 2. Backend (`backend/`)
- **`store.go`**: new column `sessions.ip` (+ ALTER for existing DBs), `clientIP` header order …
#### 3. Dashboard (`frontend/`)
- …
#### 4. Docker / config / docs
- …

### 🔌 Contract changes
- Envelope: new item type `pointer` (`events[{t,x,y,k}]`) — old backends reject it as … / accept and ignore.
- API: `GET /api/v1/projects/{id}/live` …
- Schema: `sessions.browser`, `sessions.visitor_key` (ALTER in `migrate()`).
- Env: `SIGHTPANE_PROXY_PROTOCOL`, `SIGHTPANE_TRUSTED_PROXIES`.
(Write "None" when there are none.)

### ✅ Doğrulama / Verification
- `cd package && flutter analyze && flutter test` → N tests
- `go vet ./... && go test ./...` → ok
- `cd frontend && flutter analyze && flutter test && flutter build web --dart-define-from-file=config.json`
- `docker build -t sightpane .` / `docker compose up -d --build` and the curl checks actually run (health, `/api/v1/envelope` 202, frame png 200 …)
- Live checks against a running backend (`package/tool/smoke_send.dart`, browser session in the dashboard) — say what was observed, and what was **not** verified (e.g. "real browser replay not checked").
- Breaking changes: state explicitly, or "None".

## Related
[Docs updated, follow-ups, deferred items]
```

### 4. Commit, push, apply
```bash
git add -A && git commit -m "feat(scope): concise message

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
git push origin HEAD
env -u GITHUB_TOKEN gh pr edit "$PR_NUMBER" --title "$TITLE" --body-file /tmp/pr_description.md
```
PR bodies end with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

### 5. Confirm
Report the PR link and a one-line summary of what changed in the description.

## Writing guidelines
- Conventional-commit titles with a scope: `feat(sdk): …`, `fix(backend): …`, `feat(dashboard): …`, `chore(docker): …`, `docs: …`.
- Exact file names, endpoints, env vars, item types.
- Numbers only where they change what the reader does (test counts, measured speedups).
- Never describe verification you did not run.
