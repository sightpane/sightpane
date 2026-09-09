# feat(sdk,backend,dashboard): make this a platform-neutral product — web/React and React Native SDKs

> **The rename is done (2026-09-09).** `flutter-hog` is now **sightpane**: the Go
> module, the Dart package and its `Sightpane*` classes, the dashboard package,
> the Docker image and every document. The old names stay accepted where somebody
> else's deployment depends on them — the `X-Hog-Key` ingest header, the `HOG_*`
> environment prefix, a `hog.db` in the data directory, the browser's stored
> session keys, and `package:flutter_hog/` in stack-trace grouping. Each has a
> test. What is left here is the actual multi-platform work.

## Problem

The name no longer says Flutter, but the product still assumes it. The dashboard
tells a user with no frames that "SightpaneReplay is not wrapped", which means
nothing outside Dart; error grouping skips `package:flutter/` and `(dart:` frames
and has no idea what a JavaScript stack looks like; and the setup snippet the
dashboard hands out on project creation is Dart source.

Underneath that, the wire protocol is already platform-neutral. An envelope is
`{sdk{name,version}, session{id, started_at, user, device, props}, items[]}` and
nothing in it is Dart-specific: `device.platform` already carries `web`, `linux`,
`android`; breadcrumbs, events, errors, pointer samples and heartbeats are
generic. A JavaScript client could speak it today. Two things stand in the way:
replay, which assumes a PNG per frame, and the fact that `sdk.name` is parsed and
then thrown away, so the backend cannot tell which client sent what.

## Why it matters

The product is "self-hosted error tracking, product analytics and session replay
that you run yourself". Nothing about that is Flutter-specific, and the teams most
likely to adopt it have a React web app and a React Native app next to the Flutter
one. Every day the name and the copy stay Flutter-shaped, the decision to widen
gets more expensive: a rename touches the module path, the package names on
pub.dev/npm, the Docker image tag, the AGPL §13 source URL and every document.
Doing it before there is a second SDK is much cheaper than after.

## Where to look

**Code — the contract, which mostly already fits**
- `backend/internal/store/ingest.go` — `Envelope` has an `SDK{Name,Version}` field
  that `Ingest` never reads and no column stores. That is the hook a second client
  needs: which SDK sent a session decides how its stacks are parsed and what the
  dashboard tells the user about missing frames.
- `backend/internal/store/store.go` — `migrate`: `sessions` has `platform` and
  `release` but no `sdk_name` / `sdk_version`. Adding them means the `CREATE
  TABLE` **and** the `ALTER TABLE … ADD COLUMN` list.
- `backend/internal/store/fingerprint.go` — `appFrames` skips
  `package:flutter/`, `package:sightpane/`, `(dart:` and `package:flutter_test/`.
  A JS stack (`at fn (webpack://app/src/x.tsx:12:9)`) has none of those and needs
  its own skip list (`node_modules/`, `webpack-internal:`, the SDK's own frames)
  plus the same line/column normalisation. Changing this regroups existing issues,
  so it has to key off the SDK name rather than replace the current rules.

**Code — replay, the one real gap**
- `package/lib/src/replay/recorder.dart` — frames are PNGs from a
  `RepaintBoundary`, and `backend/internal/blob` stores them as such. That is the
  right design for Flutter, which paints to a canvas with no DOM.
- On the web a DOM recorder (the rrweb model: an initial snapshot plus mutation,
  scroll and input events) is one to two orders of magnitude smaller than a PNG
  per second, and it gives text selection and correct rendering at any viewport.
  So the web SDK should **not** send `frame` items. It needs a new envelope item
  type — `dom` — carrying `{kind: "snapshot"|"mutation", t, data}`.
- `frontend/lib/features/sessions/session_detail_page.dart` — `ReplayPlayer` and
  `FramePrefetcher` assume a list of image URLs. A DOM session needs a different
  player (a sandboxed iframe replaying mutations) selected by what the session
  actually contains.
- React Native has no DOM but does have `react-native-view-shot`, so it maps onto
  the existing `frame` item with no protocol change. It is the cheaper of the two
  to ship and proves the contract is portable before the harder web work.

**Code — the Flutter-shaped surface**
- `frontend/tool/gen_arb.py` — `replayNoFrames` mentions `SightpaneReplay`; it has to
  become platform-aware, driven by the session's SDK.
- `frontend/lib/features/projects/projects_page.dart` — `SetupSnippet.code` is
  hardcoded Dart. It needs one snippet per SDK, chosen by the project's
  `platform` field (which already exists and already offers `web`, `mobile`,
  `desktop`).
- `package/pubspec.yaml`, `backend/go.mod` (`module sightpane`),
  `frontend/pubspec.yaml` (`sightpane_dashboard`), `Dockerfile`,
  `docker-compose.yml` image tag, and `backend/internal/server/server.go`
  `SourceURL` — all carry the name.

**Contract / data**
- New envelope item `dom`: `{type: "dom", ts, kind, data}` where `data` is the
  rrweb-shaped payload. Old backends count an unknown type as `rejected`, which
  is the existing forward-compatibility rule, so a new SDK against an old backend
  degrades rather than fails.
- `sessions.sdk_name` / `sessions.sdk_version`, filled from the envelope's `sdk`
  block. Sessions recorded before this stay empty and are treated as Flutter,
  which is what they are.
- Package names, once the project is renamed: `<name>` on pub.dev,
  `@<name>/browser` and `@<name>/react` on npm, `@<name>/react-native`. The five
  candidate names below were checked and are free on npm, pub.dev, PyPI,
  crates.io and as a GitHub owner.

**Tests that pin current behaviour**
- `backend/internal/server/server_test.go` — `TestIngestAndQuery` sends an
  envelope with a `frame` item and a Dart stack; `TestFingerprint` in
  `backend/internal/store/store_test.go` pins the Dart skip rules. Both must keep
  passing unchanged: a second SDK adds paths, it does not change the first one.
- `package/test/models_test.dart` pins the envelope shape from the Dart side.

**Docs / prior art**
- Every `README.md`, `CLAUDE.md`, and `.claude/skills/*`.
- Related: `01` (source maps — a JS SDK needs them far more urgently than Flutter
  does, since minified JS is the normal case), `03` (spans are platform-neutral
  and would land in all SDKs at once), `09` (PII scrubbing has to run in each SDK).

## Fix shape

**The name, for the record.** `sightpane` was picked from a shortlist checked on
2026-09-09 — `.com`, `.dev`, `.io` and `.sh` unregistered, and unused on npm,
pub.dev, PyPI, crates.io and as a GitHub owner. The runners-up were `tellglass`,
`keenglass`, `seenglass` and `kaskope`, all equally free. Anything containing
`vue`, `react` or another framework name was ruled out: this is a product several
frameworks talk to, and a name that claims one of them misleads.

The npm scope is still to be claimed: `@sightpane/browser`,
`@sightpane/react`, `@sightpane/react-native`.

**The work, in order.**

1. **Record which SDK sent a session.** `sdk_name` / `sdk_version` columns, filled
   from the envelope. Nothing changes for existing clients; this is the hook
   everything below hangs on.
2. **Make fingerprinting SDK-aware.** Keep the Dart rules for `flutter`, add a
   JavaScript rule set. Key off `sdk_name`, so no existing group moves.
3. **React Native SDK.** The cheapest proof that the contract is portable: it
   reuses `frame` unchanged, and only errors, breadcrumbs and navigation need
   platform work. Ship it before touching the protocol.
4. **The `dom` item type and a DOM player.** The real work. A browser SDK
   (`@<name>/browser`) with a React wrapper on top of it, and a second player in
   the dashboard chosen by session content. This is where the effort is; do not
   start it until 1–3 are in and the naming is settled.
5. **Per-SDK setup snippets and copy.** The dashboard stops assuming Dart.

Rejected: making the Flutter SDK send DOM events (there is no DOM to record);
making the web SDK send PNGs via `html2canvas` (seconds per frame, wrong
rendering, and a fraction of the fidelity of DOM replay); a single "universal"
SDK package (each platform's integration points differ enough that one package
would be a lowest common denominator); keeping the Flutter name and adding others
under it (it misleads about what the product is).

## Acceptance

- [x] a name is chosen and applied: Go module, Dart package and class prefix,
      dashboard package, Docker image, `SourceURL`, every document
- [x] an existing deployment keeps working across the rename — `X-Hog-Key`,
      `HOG_*`, `hog.db`, the browser's stored session, and `package:flutter_hog/`
      stack frames — each pinned by a test
- [ ] the npm scope `@sightpane` is registered
- [ ] `sessions.sdk_name` / `sdk_version` arrive via the `ALTER` list and are
      filled from the envelope; sessions recorded before are treated as Flutter
- [ ] `TestFingerprint` still passes unchanged, and a new case pins that a
      JavaScript stack groups by its own rules
- [ ] a React Native SDK sends errors, events, breadcrumbs, navigation and frames
      to an unmodified backend, and its sessions play in the dashboard
- [ ] a browser SDK sends `dom` items; an old backend counts them as `rejected`
      without failing the envelope
- [ ] the dashboard picks the player from what the session contains, and shows a
      setup snippet for the SDK the project actually uses
- [ ] `go test ./...`, `flutter test` in `package/` and `frontend/` all green;
      the Flutter SDK's behaviour is untouched throughout
