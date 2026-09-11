# feat(backend,sdk,dashboard): continuous profiling & interactive flame chart visualizer

## Problem

When transactions are slow or mobile apps experience dropped frames (UI jank), standard timing spans show that an operation took 800ms, but cannot show *which specific lines of code* or recursive functions consumed that CPU time. Developers are left guessing or attempting to reproduce issues with local CPU profilers that fail to reflect real device conditions in production.

## Why it matters

Sentry Profiling takes observability to the code execution level. By sampling the call stack at 101 Hz (once every 10ms) during active transactions, Sightpane can render interactive Flame Graphs showing the exact call trees, highlighting CPU-heavy regexes, synchronous file reads, or excessive JSON parsing without incurring perceptible client overhead.

## Where to look

**Code**
- `backend/internal/store/profiles.go` (new): Ingest and query indexing for profile summaries linked to `transaction_id`s.
- `backend/internal/blob/` (reuse S3/Ceph blob storage from issue 12): Store compressed profile call-trees (`speedscope` / `pprof` JSON format) in object storage rather than PostgreSQL to keep database sizes lean.
- `backend/internal/server/profile_handlers.go` (new): Endpoints for fetching profile call trees and top slow functions.
- `flutter/lib/src/profiling/` (new): Sampling profiler integration for Dart VM / Flutter release builds using `dart:developer` UserTags and frame budget samplers.
- `react-sdk/src/profiling.ts` (new): Web JS Self-Profiling API (`window.performance.profile`) integration for Chromium-based browsers.
- `frontend/lib/features/profiling/profiling_page.dart` (new): Profile explorer with CPU time percentiles, slowest functions list, and thread picker.
- `frontend/lib/features/profiling/widgets/flame_chart.dart` (new): Canvas-rendered interactive Flame Graph supporting panning, zooming, call stack inversion (icicle view), and text search.

**Contract / data**
- Envelope Item `profile`:
  ```json
  {
    "type": "profile",
    "ts": "2026-09-11T05:00:00.000Z",
    "transaction_name": "route:/catalog",
    "duration_ms": 650,
    "cpu_time_ms": 480,
    "thread_name": "main",
    "platform": "web",
    "profile_data": {
      "shared": {
        "frames": [
          { "name": "root", "file": "app.js", "line": 1 },
          { "name": "renderCatalog", "file": "catalog.tsx", "line": 42 },
          { "name": "sortItems", "file": "utils.ts", "line": 18 }
        ]
      },
      "samples": [
        { "elapsed_ms": 10, "stack_id": [0, 1] },
        { "elapsed_ms": 20, "stack_id": [0, 1, 2] }
      ]
    }
  }
  ```
- Table `profiles`:
  ```sql
  CREATE TABLE profiles (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      transaction_name TEXT NOT NULL,
      session_id UUID,
      duration_ms NUMERIC(10, 2) NOT NULL,
      cpu_time_ms NUMERIC(10, 2) NOT NULL,
      blob_key TEXT NOT NULL, -- Stored in Ceph/S3
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  CREATE INDEX idx_profiles_tx ON profiles(project_id, transaction_name, created_at DESC);
  ```

## Fix shape

1. **Sampling Profiler Engine**:
   - Collect profiles only during sampled transactions (e.g. `profilesSampleRate: 0.1`) to keep overhead < 1% CPU.
   - Encode frames with integer dictionary compression (`speedscope` format) before transmission.
2. **Object Storage Backend**:
   - Backend extracts metadata (`cpu_time_ms`, `transaction_name`) into PostgreSQL and writes raw profile JSON directly to Ceph/S3 object storage.
3. **Dashboard Flame Chart**:
   - Draw blocks with CustomPainter representing call frames where horizontal width equals time spent.
   - Clicking a frame zooms into its sub-tree.
   - Display a "Slowest Functions" table on the right side listing self-time vs. total-time.

## Acceptance

- [ ] CPU profiling can be sampled conditionally during transactions in Flutter and React.
- [ ] Raw profiles are stored compressed in S3/Ceph object storage with automatic TTL retention.
- [ ] Flame Graph renders smoothly in the Flutter web dashboard with fluid horizontal zooming and text filtering.
- [ ] Clicking on a slow transaction sample offers a "View Profile" action opening the flame chart.
