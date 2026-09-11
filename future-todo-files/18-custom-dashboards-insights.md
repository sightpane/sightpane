# feat(backend,dashboard): custom dashboards & multi-metric insight query builder

## Problem

Sightpane's current dashboard provides a fixed Overview page with hardcoded KPI tiles and pre-baked charts. Users cannot build custom analytical queries (e.g., "Daily active users broken down by country", "Average checkout amount by platform", "Count of failed API calls by endpoint"), save them as reusable Insights, or arrange them into custom team dashboards (e.g., "Executive Overview", "Mobile App Health", "Revenue Funnels").

## Why it matters

Custom Dashboards and the Insight Builder are the cornerstone of PostHog's user experience. Different stakeholders (DevOps, PMs, Executives) need completely different metrics. Allowing teams to build, pin, and customize dashboards eliminates external BI tools (Metabase, Tableau) and brings business and technical observability into a single pane of glass.

## Where to look

**Code**
- `backend/internal/store/insights.go` (new): Dynamic SQL query builder in Go executing aggregated time-series queries over TimescaleDB `events`, `sessions`, and `spans` hypertables with arbitrary group-by breakdowns, filters, and mathematical aggregations (`count`, `count_distinct_users`, `avg(prop)`, `sum(prop)`, `p90(prop)`).
- `backend/internal/store/dashboards.go` (new): CRUD for dashboard configurations, tile coordinates, and grid layouts.
- `backend/internal/server/insight_handlers.go` (new): Endpoints for dynamic query execution and dashboard management.
- `frontend/lib/features/dashboards/dashboards_page.dart` (new): Dashboard directory with permissions and favorite pins.
- `frontend/lib/features/dashboards/dashboard_view_page.dart` (new): Responsive grid layout (`LayoutBuilder`) rendering varied widget tiles (Line charts, Bar charts, Area charts, KPI summary cards, Pie/Donut breakdown charts).
- `frontend/lib/features/insights/insight_builder_page.dart` (new): Query builder UI with event picker, filter chips, property breakdown dropdown, aggregation selector, and live chart preview.

**Contract / data**
- Tables `dashboards` and `insights`:
  ```sql
  CREATE TABLE dashboards (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      name TEXT NOT NULL,
      description TEXT DEFAULT '',
      is_default BOOLEAN NOT NULL DEFAULT false,
      layout JSONB NOT NULL DEFAULT '[]', -- Grid layout: [{"insight_id": "...", "col": 0, "row": 0, "w": 6, "h": 4}]
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );

  CREATE TABLE insights (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      dashboard_id UUID REFERENCES dashboards(id) ON DELETE SET NULL,
      name TEXT NOT NULL,
      chart_type TEXT NOT NULL DEFAULT 'line' CHECK (chart_type IN ('line', 'bar', 'area', 'number', 'donut', 'table')),
      query JSONB NOT NULL, -- { "events": [{"name": "purchase", "math": "sum", "property": "amount"}], "breakdown": "platform", "date_range": "30d" }
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  CREATE INDEX idx_insights_project ON insights(project_id);
  ```
- Dynamic Query Request (`POST /api/v1/projects/:id/insights/query`):
  ```json
  {
    "date_range": "14d",
    "interval": "day",
    "events": [
      { "name": "purchase", "math": "unique_users" }
    ],
    "breakdown": "device.browser",
    "filters": [
      { "property": "device.platform", "operator": "exact", "value": "web" }
    ]
  }
  ```
- Query Response (`200 OK`):
  ```json
  {
    "series": [
      {
        "label": "Chrome",
        "data": [
          { "time": "2026-09-01", "value": 1420 },
          { "time": "2026-09-02", "value": 1580 }
        ]
      },
      {
        "label": "Safari",
        "data": [
          { "time": "2026-09-01", "value": 820 },
          { "time": "2026-09-02", "value": 910 }
        ]
      }
    ]
  }
  ```

## Fix shape

1. **Safe Dynamic SQL Engine**:
   - Build a parameterized query builder that sanitizes JSON paths and column identifiers against SQL injection.
   - Leverage TimescaleDB `time_bucket()` and continuous aggregates for sub-100ms response times.
2. **Chart Rendering Library**:
   - Use Flutter's Grammar of Graphics (`package:graphic` or custom canvas painters) for line, area, and stacked bar charts.
   - Render compact sparklines and KPI trend cards for summary tiles.
3. **Dashboard Canvas**:
   - Implement customizable grid tile reordering and resizing.
   - Provide auto-refresh intervals (10s, 30s, 1m, off) and date range overrides for the entire dashboard.

## Acceptance

- [ ] Users can create multiple custom dashboards per project and set a default home dashboard.
- [ ] Insight query builder supports arbitrary events, math aggregations (`count`, `unique_users`, `avg`, `sum`, `p90`), and property breakdowns.
- [ ] Charts update interactively with hover tooltips and crosshairs.
- [ ] Dashboards support auto-refreshing in real time for team monitoring displays.
- [ ] Queries are cached with 60-second TTL to avoid database load spikes.
