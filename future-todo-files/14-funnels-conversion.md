# feat(backend,dashboard): funnels & conversion analysis with drop-off session replay bridge

## Problem

Sightpane tracks events and user actions (`POST /api/v1/envelope` with item type `event`), but there is no mechanism to analyze multi-step conversion flows (e.g., `view_item` → `add_to_cart` → `checkout` → `purchase`). Product teams cannot see conversion rates between steps, median time-to-convert, or where drop-offs happen. Most critically, when users abandon a funnel, there is no direct link to watch the session replays of those specific dropped-off sessions to understand *why* they abandoned.

## Why it matters

Funnel analysis is the core capability of product analytics tools like PostHog. Knowing that checkout conversion is 42% is helpful, but the superpower of an integrated telemetry platform like Sightpane is watching the recordings of the 58% who abandoned right at the payment step (e.g., validation error, broken button, confusion).

## Where to look

**Code**
- `backend/internal/store/funnel.go` (new): TimescaleDB query engine computing step completion, drop-off counts, conversion percentages, and time-to-convert medians using PostgreSQL CTEs and window functions over the `events` hypertable.
- `backend/internal/server/funnel_handlers.go` (new): Endpoints for managing funnel definitions and querying aggregated results and drop-off sessions.
- `backend/internal/store/migrations/postgres/timescale/0003_funnels.sql` (new): Relational schema for saved funnel configurations.
- `frontend/lib/features/funnels/funnels_page.dart` (new): List of saved funnels and creation dialog.
- `frontend/lib/features/funnels/funnel_detail_page.dart` (new): Visualization of conversion bars, step drop-off percentages, conversion time distribution, and user list.
- `frontend/lib/features/funnels/widgets/funnel_step_chart.dart` (new): Horizontal/vertical stepped funnel chart with gradient drop-off indicators.
- `frontend/lib/features/sessions/sessions_page.dart`: Filter query bridge allowing `funnel:<id>:dropoff:<step>` to filter sessions.
- `frontend/lib/shell/app_shell.dart`: Navigation entry under Analytics.

**Contract / data**
- Table `funnels`:
  ```sql
  CREATE TABLE funnels (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      name TEXT NOT NULL,
      description TEXT DEFAULT '',
      steps JSONB NOT NULL, -- Array of [{ "name": "event_name", "filters": { "prop": "val" } }]
      conversion_window_seconds INT NOT NULL DEFAULT 86400, -- Default 1 day
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  CREATE INDEX idx_funnels_project ON funnels(project_id);
  ```
- Endpoints:
  - `GET /api/v1/projects/:id/funnels` → list of saved funnels with 7-day conversion preview.
  - `POST /api/v1/projects/:id/funnels` → create funnel `{ name, steps, conversion_window_seconds }`.
  - `GET /api/v1/projects/:id/funnels/:funnelId/results?days=14` →
    ```json
    {
      "total_entered": 1250,
      "total_converted": 520,
      "conversion_rate": 0.416,
      "median_conversion_time_seconds": 184,
      "steps": [
        { "step_index": 0, "name": "view_pricing", "count": 1250, "conversion_rate": 1.0, "drop_off_count": 450 },
        { "step_index": 1, "name": "start_checkout", "count": 800, "conversion_rate": 0.64, "drop_off_count": 280 },
        { "step_index": 2, "name": "complete_purchase", "count": 520, "conversion_rate": 0.65, "drop_off_count": 0 }
      ]
    }
    ```
  - `GET /api/v1/projects/:id/funnels/:funnelId/dropoffs?step=1&days=14&limit=20` → returns list of `session_id`s that executed step 1 but failed to reach step 2 within the conversion window.

## Fix shape

1. **Backend Query Engine**:
   - Write an efficient TimescaleDB query matching ordered sequences of events per `session_id` / `user_id`.
   - Use window functions (`ROW_NUMBER()` partitioned by user/session, conditional aggregation) within the sliding `conversion_window_seconds`.
2. **Drop-off Replay Query**:
   - Query sessions that executed step $N$ but have no matching step $N+1$ timestamp within $(t_N, t_N + W)$.
   - Return session summaries with direct deep-link tokens.
3. **Dashboard UI**:
   - Build `FunnelDetailPage` using `shadcn_flutter` components (`Card`, `Badge`, `Progress`, `DataTable`).
   - Add "Watch Drop-Off Replays" button on each step bar which links to `/projects/:id/sessions?funnel_dropoff=step_1`, pre-filtering the session replay list.

## Acceptance

- [ ] Users can create, edit and delete funnels with 2 to 8 arbitrary steps.
- [ ] Funnel computation correctly handles both strict sequence and loose sequence (other events allowed in-between).
- [ ] Time-to-convert metrics (median, 90th percentile) are calculated across converted paths.
- [ ] Clicking on any step drop-off count immediately opens the session replay list filtered to sessions that abandoned at that exact step.
- [ ] Unit tests in Go (`funnel_test.go`) pin query results against synthetic multi-step event data.
