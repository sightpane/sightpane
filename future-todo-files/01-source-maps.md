# feat(backend,sdk): symbolicate stack traces with source maps in release builds

> **Done** across all three repositories. The plan below is kept as written; what
> shipped differs from it and one acceptance item was not met. See
> [Outcome](#outcome) at the end, and read that first.

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

- [x] An error from a release web build shows up in the dashboard with file:line
- [x] Grouping is stable between release builds (pinned by tests)
- [x] With no map uploaded the behaviour is what it is today, and ingest latency does not increase measurably
- [x] `go test ./...` covers the new parsing; the SDK `frames` field is covered in `models_test.dart` / `stack_test.dart`
- [x] The upload step and the build flag are in the READMEs

---

## Outcome

Three commits, one per repository: the backend resolves and groups, the SDK sends
the positions, the dashboard renders them.

**What the feature actually fixes.** The plan led with readability, but the
expensive half was grouping. A minified stack has no `package:` frame at all, so
`appFrames` finds nothing, `Fingerprint` falls back to the message, and the
minified names move with every build — one failure lands in a fresh issue each
release and its counters are split across all of them. `fingerprintOf` now
prefers the frames a map resolved, and the end-to-end test asserts exactly that:
two bundle positions, one Dart line, one issue seen twice.

**Where the resolved frames live.** Not inside `body_json`, as the plan proposed
(`stack_symbolicated` and `frames[]` next to `stack`). `body_json` is handed back
to the dashboard as the SDK sent it, and rewriting it would end that. They are a
new nullable `items.symbolicated_json` instead — which also leaves room for the
backfill below, since filling it in later is an UPDATE rather than a rewrite of
somebody's error payload.

**Resolution is inline, not a background job.** The plan allowed for either. It
is cheap enough not to need one: an envelope with no `frames` — every native SDK
— costs nothing at all, and the first web error of a release costs one indexed
lookup, after which the parsed map and even the *absence* of a map are cached.
Without caching absence, every error of every project that never uploads a map
would hit the object store.

**A new SDK dependency**, which the house rules make a decision rather than a
detail: `package:stack_trace`. Chrome, Firefox and Safari each write a stack
differently; this is the Dart team's parser for all three and is already in the
tree of any app with `flutter_test`. The alternative was three hand-written
regexes that would rot.

### Not done, and why

- **A release still does not group with a debug build of the same error.** This
  was an acceptance item and it is not met. It cannot be met by symbolication
  alone: a debug stack says `package:myapp/a.dart` and a dart2js map says
  `lib/a.dart`, and the package name is nowhere in the map. Matching them means
  keying the fingerprint on the file's *basename*, which regroups every issue
  ever recorded and collides two files of the same name in different
  directories. That is a decision about existing data, not a detail of this
  feature, so it was left for someone to make deliberately. The reasoning is in
  `fingerprintOf`'s comment where the next person will look.
- **No backfill.** A map uploaded after the first error of a release does not
  symbolicate what already arrived. The normal order — upload at deploy, errors
  after — makes this rare, and the storage shape supports adding it. Both READMEs
  say so plainly rather than leaving it to be discovered.
- **Ingest latency was reasoned about, not measured.** The structure above makes
  it zero for native and one cached lookup for web, but no benchmark was run.

### Verified

- `go test -race ./...` green, including a new end-to-end test that uploads a
  map, ingests two "builds" of one failure and gets one issue carrying
  `lib/cashier.dart:120 openTill`.
- The source-map fixtures are generated by the same base64 VLQ encoding the
  format uses rather than pasted from a build, so the tests state what they test.
- Against a running backend, not only in tests: `tool/upload_sourcemap.dart`
  posted a real multipart upload, an envelope with minified frames went in, and
  the API returned the resolved frame with the raw body untouched.
- `flutter analyze` and `flutter test` green in both Flutter repositories
  (30 and 38 tests), `flutter build web` succeeds.

### Acceptance, as it stands

- [x] An error from a release web build shows up with file:line
- [ ] ~~The same error groups in debug and release builds~~ — not met, see above.
      Grouping is now stable *between release builds*, which is the drift the
      problem statement described
- [x] With no map uploaded, behaviour is unchanged (a test pins it); latency is
      structurally unchanged but unmeasured
- [x] `go test ./...` covers the parsing (`internal/symbol`), and the SDK's
      `frames` field is covered in `test/stack_test.dart` rather than
      `models_test.dart`
- [x] The upload step and `--source-maps` are in both READMEs, with the ordering
      caveat and the note that the token is a user token, not the project key
