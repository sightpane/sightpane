# feat(backend,sdk,dashboard): feature flags & remote configuration with session correlation

## Problem

Releasing code changes in mobile apps (Flutter) or web apps (React) currently requires full deployments or app store reviews. If a bug, performance regression, or crash spike occurs, there is no remote kill-switch to disable the feature instantly. There is also no graduated canary rollout (e.g. 10% → 25% → 100%) or user property targeting (e.g. `role: beta_tester`, `version >= 2.1.0`). When errors occur or sessions are replayed, developers cannot tell which feature flags were active on that client at runtime.

## Why it matters

Feature flags are one of PostHog's most utilized products because they eliminate risky deployments. In Sightpane, embedding feature flags directly into the error tracking and session replay pipeline provides instant root-cause identification: filtering crashes by `flag:new_payment_gateway=true` immediately proves whether an issue is caused by a new feature rollout.

## Where to look

**Code**
- `backend/internal/flags/evaluator.go` (new): Deterministic evaluation engine using MurmurHash3 / SHA256 over `flag_key + distinct_id` modulo 100 for rollout percentages, and property matching for target rules.
- `backend/internal/store/feature_flags.go` (new): TimescaleDB/PostgreSQL storage for flag definitions, rollout rules, and evaluation audit logs.
- `backend/internal/server/flag_handlers.go` (new):
  - Ingest evaluation: `POST /api/v1/flags/evaluate` (called by SDKs).
  - Admin endpoints: `GET /api/v1/projects/:id/feature-flags`, `POST`, `PUT`, `DELETE`.
- `flutter/lib/src/flags.dart` (new): `Sightpane.isFeatureEnabled(key)`, `Sightpane.getFeatureFlag(key)`, `Sightpane.reloadFeatureFlags()`, caching flags in SQLite/SharedPreferences for offline startup.
- `react-sdk/src/flags.ts` (new): `useFeatureFlag(key)`, `useFeatureFlagPayload(key)`, `client.isFeatureEnabled(key)`, caching in `localStorage`.
- `frontend/lib/features/flags/feature_flags_page.dart` (new): Flag management list with toggle switches, search, and rollout percentages.
- `frontend/lib/features/flags/feature_flag_detail_page.dart` (new): Flag targeting rule editor, rollout slider (0–100%), multivariant payload editor, and evaluation traffic graph.
- `frontend/lib/features/sessions/session_detail_page.dart`: "Feature Flags" section in sidebar listing all active flags and variants for the recorded session.
- `frontend/lib/features/issues/issue_detail_page.dart`: Breakdown of active feature flags across error occurrences.

**Contract / data**
- Table `feature_flags`:
  ```sql
  CREATE TABLE feature_flags (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      key TEXT NOT NULL,
      name TEXT NOT NULL,
      description TEXT DEFAULT '',
      enabled BOOLEAN NOT NULL DEFAULT true,
      rollout_percentage INT NOT NULL DEFAULT 0 CHECK (rollout_percentage BETWEEN 0 AND 100),
      filters JSONB NOT NULL DEFAULT '[]', -- Property rules: [{"property": "role", "operator": "exact", "value": "vip"}]
      variants JSONB DEFAULT '[]', -- Optional multivariant: [{"key": "control", "rollout": 50}, {"key": "test", "rollout": 50}]
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      UNIQUE(project_id, key)
  );
  CREATE INDEX idx_flags_project ON feature_flags(project_id);
  ```
- SDK Evaluation Request (`POST /api/v1/flags/evaluate` with header `X-Sightpane-Key`):
  ```json
  {
    "distinct_id": "u_98412",
    "properties": {
      "role": "admin",
      "app_version": "2.4.0",
      "os": "Android"
    }
  }
  ```
- SDK Evaluation Response (`200 OK`):
  ```json
  {
    "flags": {
      "new_checkout_flow": true,
      "homepage_banner_v2": false,
      "pricing_experiment": "variant_b"
    }
  }
  ```

## Fix shape

1. **Evaluation Engine**:
   - Compute `hash = (MurmurHash3(key + ":" + distinct_id) % 100)`. If `hash < rollout_percentage`, flag evaluates to true.
   - If property filters are specified, test properties against user context before applying rollout percentage.
2. **SDK Integration**:
   - On SDK initialization (`Sightpane.init`), fetch flags asynchronously and cache them.
   - Provide synchronous accessor `isFeatureEnabled(key)` reading from cache so UI renders without latency or flicker.
   - Attach evaluated flag keys and values to subsequent envelope session headers and event payloads.
3. **Dashboard Management**:
   - Build CRUD interface in Flutter dashboard with instant toggle switch and percentage slider.
   - Display a live preview of whether a given user ID or property set would receive the flag.

## Acceptance

- [ ] Flags can be toggled on/off instantly without deploying code.
- [ ] Percentage rollouts (e.g. 20%) are deterministic: the same `distinct_id` always receives the same variant across app launches.
- [ ] Property rules (e.g., `role = "beta"`, `country = "TR"`) accurately include or exclude users.
- [ ] Both Flutter and React SDKs evaluate flags with zero render-blocking latency and cache evaluations locally for offline mode.
- [ ] Active feature flags appear on the session replay player and issue details for fast debugging.
