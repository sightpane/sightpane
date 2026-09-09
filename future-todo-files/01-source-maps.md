# feat(backend,sdk): symbolicate stack traces with source maps in release builds

## Problem

Errors from a release (minified) web build show up in the dashboard as
`minified:Class447: Bad state: RenderBox was not laid out: minified:Class677#03a…`
(Casino CRM project, 2026-09-08, issues #1 and #2). Class and method names are
unreadable and there is no line number; the same error gets a different fingerprint in
debug and release builds because `Fingerprint` works off the minified names.

## Why it matters

The core value of error tracking in production is showing the source line; today that
only works for debug builds. Grouping also drifts from release to release (minified
names can change with every build), which means the counters for "the same error" get
split apart.

## Where to look

**Code**
- `backend/internal/store/fingerprint.go` — `Fingerprint`, `appFrames`: looks for frames
  containing `package:`; a minified stack trace has no such frame, so it falls back to
  the message.
- `backend/internal/store/ingest.go` — `Ingest`, `error` branch: `h.Stack` is stored as-is
  inside `items.body_json`; there is nowhere to put a symbolicated copy.
- `package/lib/src/hog.dart:223` — `SightpaneClient.captureException`: sends
  `stackTrace.toString()`; on web that is DDC/dart2js output, which can be parsed with
  `package:stack_trace`.
- `package/lib/src/options.dart:44` — `release`; this value has to match the build a
  source map belongs to.
- `frontend/lib/features/issues/issue_detail_page.dart:15` — the stack trace is shown
  raw in a `CodeBlock`.

**Contract / data**
- New table `release_artifacts(project_id, release, filename, sha256, uploaded_at)` plus
  `main.dart.js.map` on disk/S3.
- Next to `items.body_json.stack`, add `stack_symbolicated` (the symbolicated text) and
  `frames[]` (file, line, column, function) fields.
- New endpoint: `POST /api/v1/projects/{id}/releases/{release}/sourcemaps` (multipart, owner).

**Tests that pin current behaviour**
- `backend/internal/store/store_test.go::TestFingerprint` — grouping via `package:app/...`
  frames.
- `package/test/hog_test.dart` "captureException carries breadcrumbs, route, device and user".

**Docs**
- `backend/README.md` → "Error grouping"; `package/README.md` → "Limits".

## Fix shape

1. SDK: on web, also send the `StackTrace` text parsed through `package:stack_trace`
   `Trace.parse` as `frames[{uri, line, column, member}]` (keep the existing `stack` text).
2. An endpoint that accepts the output of `flutter build web --source-maps`
   (`build/web/main.dart.js.map`), plus a `hog-upload-sourcemap` step in the
   `frontend2`/app build script (Dart CLI:
   `package/tool/upload_sourcemap.dart <endpoint> <key> <release> <map>`).
3. In backend ingest, resolve the frames when a map exists for the `release`: source map
   v3 parsing in Go (`github.com/go-sourcemap/sourcemap`, BSD) to map
   `js line:column → dart file:line`. If resolution is slow, make it a background job:
   the error row is written raw first and updated afterwards.
4. `Fingerprint` should prefer symbolicated frames; without a map, today's behaviour.
5. In the dashboard, stack trace lines read as `lib/features/...dart:120`, with the raw
   stack trace in a collapsible section.

Difficulty: dart2js source maps are large (10–30 MB); write them to disk and load them
into memory through an LRU.
Rejected: doing symbolication in the dashboard (in the browser) — that downloads the map
to every user.

## Acceptance

- [ ] An error from a release web build shows up in the dashboard with file:line
- [ ] The same error lands in the same group in debug and release builds (test: two stack traces, one issue)
- [ ] With no map uploaded the behaviour is what it is today, and ingest latency does not increase measurably
- [ ] `go test ./...` covers the new parsing; the SDK `frames` field is covered in `models_test.dart`
- [ ] The upload step and the build flag are in the READMEs
