# feat(sdk): "record only on error" mode and a persistent offline queue

## Problem

Recording is continuous: `SightpaneReplay` takes a frame every second and queues the changed ones
immediately (`ReplayRecorder.captureNow` → `onFrame`); across a shift with 74 cameras that
is 20–60 KB per frame of bandwidth on web. There is no equivalent of Sentry's
`onErrorSampleRate`. The queue is also in memory (`SightpaneQueue._pending`); if the app closes
before it can send, the last batch is lost (`package/README.md` "Limits").

## Why it matters

Bandwidth and storage (the `frames/` directory) grow fast; what is actually wanted is
usually the 30 seconds before the error. And losing data offline can swallow exactly the
error you most want (the app crashing).

## Where to look

**Code**
- `package/lib/src/replay/recorder.dart:13` — `ReplayRecorder`; `captureNow` (110) hands the
  frame straight to `onFrame`.
- `package/lib/src/options.dart:5` — `SightpaneReplayOptions` (enabled, interval, scale,
  skipUnchanged, recordPointer); the `mode` and `bufferSeconds` fields go here.
- `package/lib/src/hog.dart:223` — `captureException`: calls `replay.captureNow()` when an
  error happens; in on-error mode "flush the buffer" belongs here.
- `package/lib/src/queue.dart:10` — `SightpaneQueue`; `_enforceLimit` (54), `_drain` (81).
- `backend/internal/store/ingest.go` — the `frame` branch of `Ingest`: `seq` is assumed to be
  monotonically increasing; ordering must be preserved when flushing a backwards-looking
  buffer (`INSERT OR REPLACE` is there).

**Contract / data**
- The frame item does not change; when the buffer is flushed the frames go out with their
  real `ts` and the backend accepts them via the `frames(session_id, seq)` PK.
- Persistent queue: a `HogStorage` interface in the SDK (`Future<void> write(List<SightpaneItem>)`,
  `read`, `clear`); IndexedDB on web (`package:web`), a file on desktop/mobile
  (`Directory.systemTemp` or a user-supplied path, without adding `path_provider`).

**Tests that pin current behaviour**
- `package/test/replay_test.dart` "captures a half-scale frame … skips unchanged frames".
- `package/test/queue_test.dart` "a failed send keeps items and retries with backoff",
  "over the queue limit frames go first".

## Fix shape

- `SightpaneReplayOptions.mode = always | onError | off`, `bufferSeconds = 30`. `onError`: frames
  and pointer packets are held in an in-memory ring buffer; `captureException` flushes the
  buffer into the queue, then live recording continues for `postErrorSeconds` (15 s), after
  which it returns to buffering.
- Persistent queue: `SightpaneQueue` writes the change to `HogStorage` on every `add`/failed `send`
  (throttled); `Sightpane.init` reads the store at startup and enqueues what it finds. Because of
  frame size only errors/breadcrumbs/events are written to the store, not frames (losing
  them at shutdown is accepted).
- On web, try one last `flush` on the `visibilitychange`/`pagehide` event (`package:web`).

## Acceptance

- [x] In `onError` mode no frames are sent when there is no error; when one happens the last 30 s of frames go out (test: fake clock)
- [x] Persistent queue: simulate a shutdown → `Sightpane.init` again → the pending error is sent
- [x] `always` mode behaves exactly as it does today (existing tests pass)
- [x] README "Limits" is updated
