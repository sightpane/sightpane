# feat(backend,dashboard): user paths & journey flows with Sankey diagrams

## Problem

Understanding how users navigate between screens and features currently requires manually sifting through individual session recordings. There is no aggregate visual representation showing common navigation paths (e.g., "From `/home`, 60% go to `/catalog`, 25% go to `/profile`, and 15% exit"). Teams cannot trace the top 5 steps leading *up to* an error or drop-off, nor the top 5 steps following a campaign signup.

## Why it matters

User Paths are a premier feature of PostHog because user behavior rarely follows a straight line. Visualizing aggregate journey flows with Sankey diagrams reveals loops (e.g., user flipping back and forth between two tabs in confusion), dead ends, and unexpected shortcuts that funnels alone cannot depict.

## Where to look

**Code**
- `backend/internal/store/paths.go` (new): TimescaleDB query engine using `LEAD()` window functions over chronological session event logs to construct directed transition graphs ($A \xrightarrow{\text{count}} B \xrightarrow{\text{count}} C$).
- `backend/internal/server/path_handlers.go` (new): Endpoint `GET /api/v1/projects/:id/paths` with parameters for start point, end point, event types (screen navigation vs. custom events), path depth, and branch thresholds.
- `frontend/lib/features/paths/paths_page.dart` (new): User flow explorer with start/end event configuration, step depth controls, and exclusion filters.
- `frontend/lib/features/paths/widgets/sankey_diagram.dart` (new): Custom painted interactive Sankey flow diagram showing proportional flow bands, drop-off exits, and node metrics.

**Contract / data**
- Endpoint `GET /api/v1/projects/:id/paths?root_event=route:/login&direction=forward&step_limit=4&days=14`:
  ```json
  {
    "nodes": [
      { "id": "0:route:/login", "name": "route:/login", "step": 0, "count": 1000 },
      { "id": "1:route:/dashboard", "name": "route:/dashboard", "step": 1, "count": 650 },
      { "id": "1:route:/forgot-password", "name": "route:/forgot-password", "step": 1, "count": 120 },
      { "id": "1:dropoff", "name": "Exit", "step": 1, "count": 230 },
      { "id": "2:route:/settings", "name": "route:/settings", "step": 2, "count": 400 }
    ],
    "links": [
      { "source": "0:route:/login", "target": "1:route:/dashboard", "count": 650 },
      { "source": "0:route:/login", "target": "1:route:/forgot-password", "count": 120 },
      { "source": "0:route:/login", "target": "1:dropoff", "count": 230 },
      { "source": "1:route:/dashboard", "target": "2:route:/settings", "count": 400 }
    ]
  }
  ```

## Fix shape

1. **Path Traversal Algorithm**:
   - Query events filtered by type (`breadcrumb` category `navigation`, or `event`).
   - Group by `session_id`, order by `ts`, and compute consecutive transitions using PostgreSQL `LEAD()`.
   - Aggregate transitions by `(source_step, source_name, target_name)` and prune branches below a configurable noise threshold (e.g. < 2% of total traffic).
2. **Reverse Flow (Pre-Crash / Pre-Churn Analysis)**:
   - Support `direction=reverse` where the query anchors on an error or specific event and traces the preceding 3–5 steps leading into it.
3. **Interactive Sankey Visualization**:
   - Render bezier curve ribbons connecting vertical column blocks.
   - Hovering highlights the path upstream and downstream.
   - Clicking on any transition ribbon opens a session replay drawer showing matching sessions.

## Acceptance

- [ ] Users can generate forward paths from a start event or reverse paths leading into an ending event/error.
- [ ] Diagram displays up to 5 consecutive steps with clearly labeled drop-offs and transition percentages.
- [ ] Users can exclude noisy background events (e.g., heartbeats or polling calls) with filter chips.
- [ ] Clicking on any node or link offers an action to watch sample session replays that traversed that exact path.
