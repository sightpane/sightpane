# feat(backend,dashboard): release health (crash-free per release, adoption, resolved-in/regressed-in release)

## Problem

`release` exists only in the `sessions.release` column and as one distribution list on the
overview (`Stats.Releases`). There is no crash-free session rate per release, no adoption
curve, and no "this issue was resolved in 0.1.2 and came back in 0.1.4" information.
Marking something resolved is release-independent (`SetIssueResolved` only sets
`resolved=1`).

## Why it matters

Spotting early that a release is bad and rolling it back is one of the most used Sentry
flows; here releases cannot be compared at all.

## Where to look

**Code**
- `backend/internal/store/store.go` — `migrate`, `sessions.release`;
  `backend/internal/store/stats.go` — `Stats` → `Releases` `NameCount`.
- `backend/internal/store/store.go` — `migrate`, the `issues` table: a `resolved` bit; there is
  no `resolved_in_release`, `first_release` or `last_release`.
- `backend/internal/store/query.go` — `SetIssueResolved`;
  `backend/internal/server/data_handler.go` — `resolveIssue`.
- `backend/internal/store/ingest.go` — `Ingest` error branch: reopening happens here, and this
  is where "regressed in release X" would be detected (`d.Release > resolved_in_release`).
- `frontend/lib/features/projects/overview_page.dart:16` — the "Releases" panel
  (`_NameCounts`).
- `frontend/lib/features/issues/issue_detail_page.dart:15` — the "Resolve" button.

**Contract / data**
- New table `releases(project_id, version, first_seen, last_seen, sessions, errors,
  crash_free)`; or a view computed over `sessions`/`items` (enough for a first version).
- `issues` columns: `first_release`, `last_release`, `resolved_in_release`.
- Endpoints: `GET /api/v1/projects/{id}/releases` (list + health), `GET …/releases/{v}`
  (new/regressed issues, sessions).

**Tests that pin current behaviour**
- `backend/internal/server/server_test.go::TestIngestAndQuery` ("resolved issue still listed",
  "regressed issue").
- `frontend/test/data_pages_test.dart` "issues list toggles resolved; detail resolves and reopens".

## Fix shape

- Update release summaries incrementally during ingest (session count, error count,
  crash-free = error-free sessions / sessions).
- "Resolve" → `resolved_in_release = the last seen release`; a re-occurrence only counts
  as a "regression" if it comes from a newer release (so devices still running the old
  release do not create noise); compare releases via semver parsing, falling back to
  `first_seen` order when a version cannot be parsed.
- Dashboard: a "Releases" page (table: release, sessions, adoption %, crash-free %, new
  issues, regressions) and, on the issue, "first/last release, release it was resolved in".

## Acceptance

- [ ] A release list and a crash-free rate per release
- [ ] A repeat from an older release does not reopen a resolved issue; one from a newer release does (test)
- [ ] First/last/resolved-in release on the issue detail
- [ ] Current `Stats.Releases` behaviour is preserved
