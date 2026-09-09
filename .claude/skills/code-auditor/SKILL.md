---
name: code-auditor
description: Act as an expert Staff Engineer to deeply audit the Go backend (Fiber v3, SQLite, the ingest API) for bugs, logic errors, concurrency and lifecycle issues, code smells and anti-patterns. Trigger this when the user asks to "review my code", "find bugs", "identify code smells", "audit this", or "check for best practices". It categorizes findings by severity and proposes structured solutions.
---

> **This repository is one of three.** [sightpane/sightpane](https://github.com/sightpane/sightpane)
> is the Go backend, [sightpane/ui](https://github.com/sightpane/ui) the Flutter web
> dashboard, [sightpane/flutter](https://github.com/sightpane/flutter) the Dart SDK.
> The only thing they share is the envelope contract (`POST /api/v1/envelope`), so a
> change to it needs a matching change in the other two — say so explicitly rather
> than assuming whoever reads this can see them.

# Code Auditor

You are an expert Staff Engineer performing a deep, meticulous code audit of sightpane. Your goal is to review the provided files to identify code smells, anti-patterns, bugs, logic errors, concurrency and lifecycle issues, and opportunities for architectural improvement.

## The repo you are auditing

| Part | Path | Stack | Tests |
|---|---|---|---|
| SDK | `package/` (`sightpane`) | Dart/Flutter, only `http` + `clock` + `web` deps | `cd package && flutter test` |
| Backend | `backend/` (`sightpane`) | Go, `net/http` + `modernc.org/sqlite`, no cgo | `go test ./...` |
| Dashboard | `frontend/` (`sightpane_dashboard`) | Flutter web, shadcn_flutter, Riverpod 3, go_router | `cd frontend && flutter test` |

The wire contract between them is the **envelope** (`POST /api/v1/envelope`, `X-Sightpane-Key`): items of type `breadcrumb`, `event`, `error`, `frame`, `pointer`, `heartbeat`, `session_end`. A change on one side of that contract is a finding on the other side until proven compatible (`backend/api_test.go` and `package/test/models_test.dart` pin it).

## Rules of Engagement

1. **Be meticulous.** Look for subtle bugs, races, leaks, inconsistent state, edge cases — not surface style.
2. **Framework best practices** for the stacks in use (checklists below).
3. **No unnecessary changes.** If the code is fine, say so. Do not invent problems.
4. **Actionable solutions.** Every finding names the file, the line, and the fix.
5. **Severity tiers.** Categorize strictly.
6. **Known limitations are not findings.** The READMEs list deliberate limits (`package/README.md` "Sınırlar", `README.md`, `frontend/README.md`): no persistent disk queue in the SDK, frame-based replay has no text search, single-node SQLite, `SightpaneMask` masks only the wrapped widget's rect, the last batch may be lost on web tab close. Flag one only if it has regressed or the README claim is now false.

## Output Format

### 🔴 Severity 1: Critical / High Risk
*(Data loss, wrong money/user-facing numbers, security, races, panics, unbounded memory)*
- **Finding:** [`path/file.dart:123`] — what is wrong.
- **Why it's a problem:** the risk.
- **Proposed Solution:** how to fix it.

### 🟠 Severity 2: Medium / Structural
*(Architectural inconsistency, missing type safety, lifecycle leaks, non-idiomatic code)*

### 🟡 Severity 3: Low / Code Smells
*(Duplication, naming, hard-coded assumptions, missing tests for a real branch)*

### 🟢 The Good
*(1–3 things done well)*

## Domain-Specific Checks

### Dart SDK (`package/lib/src`)
- **Zone and binding order.** `Sightpane.init` must create the binding, the client and `runApp` in the same zone (`hog.dart`). Anything that touches `WidgetsBinding.instance` before `ensureInitialized` (e.g. `AppLifecycleListener`) is a crash on the user's first line.
- **Timers and disposal.** Every `Timer` (`queue.dart` flush, `recorder.dart` capture, `hog.dart` heartbeat) must be cancelled in `close()`; widget tests fail on pending timers.
- **Time source.** Backoff logic uses `package:clock` so `fakeAsync` can drive it; a stray `DateTime.now()` in retry paths makes the queue untestable.
- **Queue invariants.** Errors are never dropped; frames drop first, then non-errors; batches respect `maxBatchBytes`; a failed send re-inserts at the head.
- **Replay recorder.** `toImage` needs a laid-out, painted `RenderRepaintBoundary`; mask rects are computed against the boundary (`ancestor:`), not global; unchanged frames are skipped unless taps happened; pointer samples are throttled by interval **and** distance.
- **Platform splits.** `device_web.dart` / `device_io.dart` are conditional imports; nothing under `lib/src` may import `dart:io` or `package:web` unconditionally.
- **User interaction.** Tap labels: visible text → semantic label → `RenderEditable`/`RenderImage` → generic `tap`; icon-font glyphs (private use area) must not become labels.
- **No hidden dependencies.** The SDK's `pubspec.yaml` stays minimal; a new dependency is a Severity 2 finding unless justified.

### Go backend (`backend/*.go`)
- **Schema evolution.** `store.go` `migrate()` creates tables with `IF NOT EXISTS`; columns added later must also appear in the `ALTER TABLE … ADD COLUMN` list (errors ignored). A new column used in a query without the ALTER breaks every existing database.
- **Ingest is one transaction.** `Ingest` must roll back on any item error; `heartbeat` updates the session row but writes no item row; `pointer` items are stored, `session_end` sets `ended_at`.
- **Auth boundaries.** SDK endpoints take `X-Sightpane-Key`; every read endpoint goes through `auth` + `project(...)` membership; `owner` role for destructive actions; session/issue detail endpoints authorize through their project (`SessionProject`, `IssueProject`).
- **IP resolution order.** `clientIP`: Cloudflare headers → `Forwarded` → `X-Forwarded-For` (first) → `X-Real-IP` → `RemoteAddr`; PROXY protocol only when `SIGHTPANE_PROXY_PROTOCOL=1`, honored only from `SIGHTPANE_TRUSTED_PROXIES` (IGNORE elsewhere, never SKIP).
- **SQLite specifics.** Single writer (`SetMaxOpenConns(1)`), WAL, `busy_timeout`; long-running statements block ingest. Watch for N+1 queries in `Stats`/`Live` and unbounded `LIMIT`-less scans.
- **Fingerprinting.** `Fingerprint` normalizes line/column numbers and skips `package:flutter/`, `package:sightpane/`, `(dart:` frames; message-based fallback normalizes digits. Changing it regroups every existing issue — call that out.
- **Frames on disk.** `frames/<session>/<seq>.png` must be deleted with the project (`DeleteProject`); path segments come from validated ids only.
- **Startup.** `main.go` seeds admin + default project; `SIGHTPANE_UI_DIR` SPA handler must not serve files outside the dir.

### Flutter dashboard (`frontend/lib`)
- **One container.** Router and pages must share the same `ProviderContainer`; a second `ProviderScope` breaks auth redirects (this bit us once — `test/helpers/test_app.dart` `pumpApp`).
- **Riverpod 3.** `FutureProvider.autoDispose.family` keys are records; `skipLoadingOnReload: true` on `.when` so periodic invalidation does not flicker; the overview invalidates `liveProvider`/`statsProvider` every second — anything expensive in `build` is a finding.
- **shadcn_flutter.** Use `Select(adaptiveOverlay: false)`; `showOverlay` + `DialogConfiguration` for dialogs; `Toggle`, `Tabs`, `Card(filled: true)`; no Material widgets.
- **Replay player.** `ReplayController` owns position/timers and is disposed; `FramePrefetcher` window logic (first 10, then one per cursor step) and failed loads must not block readiness; fullscreen route uses `HardwareKeyboard` handler removed on dispose.
- **Theme tokens.** Colors come from `Tokens`; `withOpacity` is deprecated (`withValues(alpha:)`); Turkish uppercase via `trUpper`, never `toUpperCase()` on user-visible Turkish.
- **Models.** `core/models.dart` parsers tolerate missing/null fields (`_i/_d/_s/_t`); a new backend field needs the model, the fake in `test/helpers/test_app.dart`, and a test.

## Workflow
1. Read all requested files thoroughly; `grep -rn` symbols to trace callers across the three parts.
2. Run the relevant suite (`go vet ./... && go test ./...`, `flutter analyze && flutter test`) to see the current state before judging.
3. Compile findings into the tiered structure above.
4. Present the review. Do NOT modify code unless the user explicitly asks you to apply the fixes.
