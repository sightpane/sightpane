---
name: pr-reviewer
description: Senior-developer review of a sightpane/sightpane pull request against its issue, written in the repo owner's own voice and optionally posted as their review. Use whenever the user gives a PR link or number and asks to review it, "PR'ı incele", "tell me if there is anything to comment on", "how much of the issue is done", "act like a senior developer", "double verify your findings", or asks to post / label review comments on GitHub — even if they only say "read this PR and this issue". Also for the follow-up round ("the developer replied and pushed, how did he do?").
---

> **This repository is one of three.** [sightpane/sightpane](https://github.com/sightpane/sightpane)
> is the Go backend, [sightpane/ui](https://github.com/sightpane/ui) the Flutter web
> dashboard, [sightpane/flutter](https://github.com/sightpane/flutter) the Dart SDK.
> The only thing they share is the envelope contract (`POST /api/v1/envelope`), so a
> change to it needs a matching change in the other two — say so explicitly rather
> than assuming whoever reads this can see them.

# PR Reviewer

The owner reads the output and pastes it into GitHub under their name, or asks you to post
it for them. So two things matter more than usual: every finding has to be **true**, and
every comment has to **read like they typed it**.

## What the user gets back

1. **Issue completion %** — the acceptance checklist walked box by box, unmet ones named.
2. **Findings** as `file:line --> comment`, grouped BLOCKER / SHOULD FIX / NIT, in a human voice.
3. **Rollovers** — what an earlier PR deferred to this one: fixed, regressed, or still open.
4. **Deploy prerequisites** — anything that fails without an env var (`SIGHTPANE_*`, `MINIO_*`),
   a rebuilt Docker image, a Caddy change, or a schema column that only exists on fresh DBs.
5. **A verdict** in one or two sentences.

Give the list first. Post to GitHub only when asked (step 7).

## 1. Gather everything in one batch

```bash
env -u GITHUB_TOKEN gh issue view N --json title,body,state --jq '.title, .state, .body'
env -u GITHUB_TOKEN gh pr view M --json title,body,state,headRefName,baseRefName,commits,files
env -u GITHUB_TOKEN gh pr diff M > $SCRATCH/prM.diff && grep -n '^diff --git' $SCRATCH/prM.diff
git fetch -q origin <headRefName>; git merge-base origin/<base> origin/<head>
env -u GITHUB_TOKEN gh api repos/sightpane/sightpane/pulls/M/comments --paginate
```

Always `env -u GITHUB_TOKEN` — the environment carries a dummy token that 401s.

Check the merge-base: a stacked PR's diff should be only its own delta. If this is a
follow-up to an earlier PR, pull that PR's body ("deferred" list) and review comments —
they are your rollover checklist.

## 2. Read every file, in dependency order

**Contract first.** `package/lib/src/models.dart` (what the SDK sends) ↔ `internal/store/store.go`
(`itemHead`, `Ingest`, `migrate()`) ↔ `frontend/lib/core/models.dart` (what the dashboard
parses). Then: `backend/auth.go` → `api.go` routes → `main.go` wiring → SDK `hog.dart` /
`queue.dart` / `replay/` → dashboard providers → pages → tests → Dockerfile/compose → READMEs.

Read the diff in ~300-line `sed -n` ranges. Don't skip tests: the fakes
(`package/test/fake_transport.dart`, `frontend/test/helpers/test_app.dart` `FakeApi`,
`backend/api_test.go` `newTestServer`) tell you what the author believed the shapes were,
and the *missing* fixture is often the finding (no missing-`browser` case, no
untrusted-proxy case, no unchanged-frame case).

## 3. Verify against the repo, not just the diff

- **Both ends of the contract.** A new item type in the SDK with no `Ingest` case is
  silently counted as `rejected`; a new backend field with no `models.dart` parser never
  reaches the screen. `grep -rn` the JSON key on all three sides.
- **Schema.** A column used in a query must be in `CREATE TABLE` **and** in the `ALTER
  TABLE` list of `migrate()`; otherwise every existing `sightpane.db` breaks on upgrade.
- **Auth.** Every new read route wrapped in `s.project("member"|"owner", …)` or `s.auth`,
  and detail routes authorized via `SessionProject` / `IssueProject`.
- **Deploy topology.** `Dockerfile` (Flutter version pin, `SIGHTPANE_API_URL=` empty for
  same-origin), `docker-compose.yml` env, `SIGHTPANE_UI_DIR`, proxy headers / PROXY protocol,
  MinIO endpoint reachable from the egress host. Code that assumes `localhost` inside a
  container is a finding.
- **The PR body's literal claims.** "N tests", "no contract change", "verified in the
  browser" — check each. A wrong claim about verification is a finding, not a nit.
- **READMEs.** `package/`, `backend/`, `frontend/` READMEs describe behaviour; a PR that
  changes behaviour without the README line is a NIT; one that makes a README claim false
  is SHOULD FIX.

## 4. Prove each finding before it goes in the list

- **Run the branch's actual code in a throwaway worktree**, never the shared checkout:
  ```bash
  W=$(mktemp -d) && git worktree add "$W" origin/<head>
  (cd "$W/backend" && go vet ./... && go test ./... -run TestName -v)
  (cd "$W/package" && flutter test test/queue_test.dart)
  (cd "$W/frontend" && flutter analyze && flutter test test/data_pages_test.dart)
  git worktree remove --force "$W"
  ```
  For backend behaviour, `httptest` in a scratch `_test.go` or a raw socket (see
  `proxyproto_test.go`) beats reasoning. For SDK behaviour, a `fakeAsync` queue test or a
  widget test with `FakeTransport`. Never `git stash`; never point anything at a real
  `sightpane.db` or a running production backend.
- **Grep the branch file for the absence** of a guard (`git show "${B}:backend/api.go" |
  grep -n auth`). "Nothing" is evidence when the file is the one that should have it.
- **Do the arithmetic** for byte budgets, backoff sequences, payout/ratio maths, timer intervals.
- **Separate what you executed from what rests on an external system** (LiveKit egress,
  MinIO, Caddy, browser `requestFullscreen`). Hedge those in the comment itself.
- **Green suite ≠ coverage.** Ask what shape no fixture exercises. Then exercise it.
- If you can't demonstrate it, it doesn't go in the list. A question in the PR is fine; a
  confident wrong claim is not.

## 5. Classify

- **BLOCKER** — data loss or corruption (dropped errors, frames orphaned on disk, schema
  breaking existing DBs), auth bypass, a crash on the SDK user's first line, or defeats a
  guarantee the PR claims. Reproduced by execution.
- **SHOULD FIX** — real defect or a false statement rendered to users (wrong counts, wrong
  IP, wrong label), a contract mismatch with a degrade path, a deferred item that now bites.
- **NIT** — consistency, wording, README drift, a test that could pin more, a lint the
  analyzer would flag.

Issue completion: a box counts only if the behaviour exists *and* is verified. "Code
exists, not run in a browser" is half.

Deploy prerequisites are their own section — the owner merges, then deploys, and "the
backend now needs `SIGHTPANE_TRUSTED_PROXIES` or every IP becomes 172.x" is what they need
before the merge button.

## 6. Voice — this is where reviews get sent back

What reads as generated: every comment following the same claim → evidence → consequence
→ fix rhythm; chains of em-dashes; parenthetical asides everywhere; flourishes; "I ran
it:" repeated identically; a paragraph where three sentences would do.

What reads as a colleague: short sentences, one thought each. Contractions. "Tried it
locally" once and "grepped, nothing" the next time. Ask a question when it's genuinely a
decision. Vary the opening. Admit uncertainty plainly. Stop when the point is made. Write
in the language the owner writes their PRs in (Turkish is fine).

**Before:**
> The ingest path only updates `last_seen_at` on heartbeat — nothing clears `ended_at`, so
> a session that sent `session_end` and then resumed is permanently excluded from `Live`,
> which is exactly the kind of state drift the live panel exists to prevent.

**After:**
> Heartbeat bumps `last_seen_at` but never clears `ended_at`. Sent `session_end` then a
> heartbeat for the same id in a scratch test: `/live` count stays 0. Should the heartbeat
> case set `ended_at=NULL`? Otherwise a resumed web tab is invisible until a new session.

Every posted comment starts with its label in bold: `**BLOCKER:**`, `**SHOULD FIX:**`,
`**NIT:**`.

## 7. Output format, then posting only when asked

In chat, findings look like:

```
**[backend/store.go:540](backend/store.go#L540)** --> comment
```

Line numbers come from the branch (`git show "${B}:path" | grep -n`), never from diff offsets.

Don't post automatically. When asked, use `scripts/post_review.py` (input JSON with
`repo: "sightpane/sightpane"`): it anchors only lines inside diff hunks, folds the rest
into the body, pins `commit_id` to the head SHA, prefixes labels, posts as the
authenticated `gh` account with no AI attribution, verifies, and prints the URL. Run it
with `--dry-run` first. Default event `COMMENT`; `--request-changes` only when told.

## 8. The follow-up round

1. `git log --oneline origin/<base>..origin/<head>` for the new commits; diff against the
   review commit, not main.
2. Read every reply. For each "fixed in X", open the file on the branch and confirm the
   fix is there and the named test actually exercises it.
3. Run `go test ./...`, `flutter analyze`, `flutter test` on the branch in a worktree.
4. Report per comment: fixed / partially / not fixed / never posted.
5. Anything still open is a candidate for the next PR's rollover list.
