# feat(backend,dashboard): behavioral user cohorts & N-day/week retention matrix heatmaps

## Problem

Sightpane reports raw Daily Active Users (DAU) and session counts, but cannot measure whether users actually return over time. There is no cohort retention matrix (e.g., of users who signed up in Week 1, what percentage came back in Week 2, Week 3, Week 4?). Furthermore, there is no ability to define behavioral user cohorts (e.g., "Power Users: users who completed > 5 transactions in the last 7 days", or "Churn Risk: users with 0 sessions in 14 days").

## Why it matters

Retention is the definitive benchmark of product health and user stickiness in tools like PostHog. Knowing retention curves and comparing cohorts (e.g., comparing users who experienced an error vs. users who did not) reveals the real business impact of software quality and user friction.

## Where to look

**Code**
- `backend/internal/store/retention.go` (new): TimescaleDB query calculating time-bucketed user retention matrices (first activity timestamp vs. return activity timestamps partitioned by Day, Week, or Month).
- `backend/internal/store/cohorts.go` (new): Evaluator for static lists and dynamic behavioral rules (e.g. `events[name='checkout'].count > 3` in `now - 30d`).
- `backend/internal/server/cohort_handlers.go` (new): Endpoints for cohort CRUD and retention matrix queries.
- `frontend/lib/features/retention/retention_page.dart` (new): Retention analysis interface with date range, first action / return action picker, and cohort breakdown.
- `frontend/lib/features/retention/widgets/retention_matrix_table.dart` (new): Heatmap table widget with shaded cells (darker green for high retention, light green/gray for low retention).
- `frontend/lib/features/cohorts/cohorts_page.dart` (new): Cohort management and builder interface.
- `frontend/lib/features/sessions/sessions_page.dart`: Filter sessions by `cohort:<id>`.
- `frontend/lib/features/issues/issues_page.dart`: Filter issues by `affected_cohort:<id>`.

**Contract / data**
- Table `cohorts`:
  ```sql
  CREATE TABLE cohorts (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      name TEXT NOT NULL,
      description TEXT DEFAULT '',
      is_static BOOLEAN NOT NULL DEFAULT false,
      rules JSONB NOT NULL, -- [{"event": "login", "operator": "gte", "count": 5, "window_days": 14}]
      user_count INT NOT NULL DEFAULT 0,
      last_calculated_at TIMESTAMPTZ,
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  CREATE INDEX idx_cohorts_project ON cohorts(project_id);

  CREATE TABLE cohort_members (
      cohort_id UUID NOT NULL REFERENCES cohorts(id) ON DELETE CASCADE,
      user_id TEXT NOT NULL,
      PRIMARY KEY(cohort_id, user_id)
  );
  CREATE INDEX idx_cohort_members_user ON cohort_members(user_id);
  ```
- Endpoint `GET /api/v1/projects/:id/retention?days=30&period=day&start_event=session_start&return_event=session_start`:
  ```json
  {
    "period": "day",
    "cohorts": [
      {
        "date": "2026-09-01",
        "total_users": 500,
        "activity": [
          { "period_index": 0, "count": 500, "percentage": 100.0 },
          { "period_index": 1, "count": 230, "percentage": 46.0 },
          { "period_index": 2, "count": 180, "percentage": 36.0 },
          { "period_index": 3, "count": 160, "percentage": 32.0 },
          { "period_index": 7, "count": 125, "percentage": 25.0 }
        ]
      }
    ]
  }
  ```

## Fix shape

1. **Retention Query Algorithm**:
   - Step 1: Find user cohort start timestamp ($t_{\text{first}} = \min(t)$ for $E_{\text{start}}$ in the period).
   - Step 2: Find all subsequent occurrences of $E_{\text{return}}$ for each user.
   - Step 3: Bucket $(\Delta t = t_{\text{return}} - t_{\text{first}}) / \text{period\_size}$ and count distinct users per bucket.
2. **Dynamic Cohort Refresh Worker**:
   - Run a scheduled background job in Go every hour refreshing `cohort_members` for dynamic cohorts.
3. **Dashboard Heatmap Table**:
   - Build a custom table using `shadcn_flutter` with background color opacity proportional to retention percentage (`Tokens.ok.withValues(alpha: percentage / 100)`).
   - Display overall retention curve line chart above the heatmap table.

## Acceptance

- [ ] Product teams can view daily and weekly retention matrices for any user segment.
- [ ] Customizable start event (e.g. `signup`) and return event (e.g. `feature_used` or any session).
- [ ] Users can create dynamic behavioral cohorts and inspect member lists.
- [ ] Sessions and issues can be filtered by cohort membership (e.g. `cohort:power_users`).
- [ ] Retention calculation query executes in under 500ms on TimescaleDB hypertables for datasets with 1,000,000+ events.
