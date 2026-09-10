# feat(backend,dashboard): organisations, fine-grained roles, audit log and scoped API tokens

## Problem

The permission model is per-project `owner | member` (`project_members.role`,
`Server.allow`). There is no concept of an organisation (every project has its own
invitations), there is no view-only role, nothing records who rotated a key or deleted a
project and when, and apart from the user token there is no machine token (uploading source
maps from CI, 01).

## Why it matters

In the casino CRM, operators need to see the dashboard but must not be able to rotate keys;
without an audit log, destructive actions cannot be traced.

## Where to look

**Code**
- `backend/internal/store/auth.go` — `Member`, `AddMember`, `MemberRole`.
- `backend/internal/server/server.go` — `New`, the `requireProject(roleOwner|roleMember)`
  wrappers; `backend/internal/server/middleware.go` — `allow`.
- `backend/internal/store/store.go` — `migrate`, `projects.created_by`; there is no org table.
- `frontend/lib/features/projects/settings_page.dart:14` — the member panel (role badge).

**Contract / data**
- Tables: `orgs`, `org_members(role: owner|admin|member|viewer)`, `projects.org_id`,
  `audit_log(org_id, user_id, action, target, at, ip)`, `api_tokens(org_id, name,
  hash, scopes[ingest|sourcemaps:write|read], expires_at)`.
- Endpoints: `/orgs`, `/orgs/{id}/members`, `/orgs/{id}/audit`, `/orgs/{id}/tokens`.

**Tests that pin current behaviour**
- `backend/internal/server/server_test.go::TestUsersProjectsMembersStats` (owner/member
  permissions).
- `frontend/test/projects_test.dart` "member (non-owner) sees settings read-only".

## Fix shape

- Migration: create an org named after each existing project and make the project's members
  org members; keep the per-project `project_members` around for a while (compatibility),
  with `allow` looking at the org role first.
- Audit log: in the `requireAuth` middleware, record write requests (POST/PATCH/DELETE) after the
  commit; Settings → "Audit" in the dashboard.
- API tokens: distinguish them by the `Authorization: Bearer hog_…` prefix; scope checking
  can also be used in place of `X-Sightpane-Key` for `ingest`.

## Acceptance

- [x] The `viewer` role can read and no write endpoint works (test)
- [x] Key rotation/project deletion appear in the audit log with the user + IP
- [x] Source map upload works with a scoped token, out-of-scope returns 403
- [x] The existing owner/member tests keep passing
