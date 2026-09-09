# feat(backend,dashboard): issue workflow — assignment, ignore/snooze, comments, merge/split, fingerprint rules

## Problem

An issue has exactly one action: resolve / reopen (`POST /api/v1/issues/{id}/resolve`).
There is no way to express who is looking at it, "this is a known framework warning,
ignore it", "tell me if it happens 100 more times", that two issues are the same error,
or that a wrongly grouped error should be split out. In Casino CRM the framework's
`RenderBox was not laid out` warning sits in the same list as real errors.

## Why it matters

As the issue list grows, noise hides the real errors; in team work the list cannot be
managed without knowing "who is on it" and "why we ignored it".

## Where to look

**Code**
- `backend/internal/store/store.go` — `migrate`, the `issues` table (fingerprint, title, count,
  resolved).
- `backend/internal/store/query.go` — `ListIssues` (the `resolved` filter), `SetIssueResolved`.
- `backend/internal/store/fingerprint.go` — `Fingerprint`: the fingerprint is fixed at ingest
  time; user-defined rules need a layer that consults a `project_fingerprint_rules` table.
- `backend/internal/store/auth.go` — `Member`; the member list needed for assignment is already
  there.
- `frontend/lib/features/issues/issues_page.dart:13`, `issue_detail_page.dart:15`.

**Contract / data**
- `issues` columns: `assignee_user_id`, `status[open|resolved|ignored|snoozed]`,
  `snooze_until`, `snooze_count_threshold`, `merged_into`.
- New tables: `issue_comments(issue_id, user_id, body, created_at)`,
  `fingerprint_rules(project_id, match{exception, message_glob, stack_contains},
  action{group_as|ignore}, priority)`.
- Endpoints: `POST /issues/{id}/assign`, `/ignore`, `/snooze`, `/merge`, `/comments`,
  `GET|POST /projects/{id}/fingerprint-rules`.

**Tests that pin current behaviour**
- `backend/internal/server/server_test.go::TestIngestAndQuery` (resolve/reopen);
  `backend/internal/store/store_test.go::TestFingerprint`.
- The issues test in `frontend/test/data_pages_test.dart`.

## Fix shape

- Move the `resolved` bit into a `status` enum (ALTER + migration: `resolved=1 →
  'resolved'`); keep the old `resolved` field in JSON as a derived value (for
  dashboard/`Issue.fromJson` compatibility).
- Ignore: ingest still counts it, but it does not appear in the list and does not trigger
  notifications (02); snooze: back to `open` automatically once `snooze_until` passes or
  the threshold is hit.
- Merge: with `merged_into`, the second issue shows the first one's occurrences; splitting
  = a new fingerprint rule for the selected occurrences.
- Rules are applied at ingest before `Fingerprint`; only future events are affected
  (regrouping history is a separate, optional "regroup" job).
- Dashboard: list filters (status, assignee), an assignment menu on the detail (members),
  a comment thread, and "Ignore / Snooze / Merge" actions.

## Acceptance

- [ ] Status transitions and threshold-based snooze covered in a backend test
- [ ] An ignored issue is counted at ingest but does not show up in the list or in notifications
- [ ] A fingerprint rule sends the framework warning into a separate group or to ignored (test)
- [ ] The assignment/comment/status flow in the dashboard has a widget test with `FakeApi`
- [ ] `Issue.fromJson` stays compatible with the old `resolved` field
