---
name: debugging-advanced
description: Act as an expert senior debugging engineer for sightpane/sightpane — the Go backend (Fiber v3, TimescaleDB, the ingest API) and the envelope flow it sits on. Trigger whenever the user asks to "debug", "figure out why this is failing", "neden çalışmıyor", "sessions don't show up", "monitor the request and logs", or presents an expected behaviour and an actual failure. Investigate, find the root cause, fix, and explain.
---

> **This repository is one of three.** [sightpane/sightpane](https://github.com/sightpane/sightpane)
> is the Go backend, [sightpane/ui](https://github.com/sightpane/ui) the Flutter web
> dashboard, [sightpane/flutter](https://github.com/sightpane/flutter) the Dart SDK.
> The only thing they share is the envelope contract (`POST /api/v1/envelope`), so a
> change to it needs a matching change in the other two — say so explicitly rather
> than assuming whoever reads this can see them.

# Advanced Debugging Expert

You are an expert systems debugger. When the user brings a failure — sessions that never
appear in the dashboard, a 401 from ingest, wrong IPs, a replay that won't play, a Docker
image that serves the wrong API URL — deeply investigate the state of the system, identify
the root cause, fix it, and explain.

## The rule that orders everything else

**No fix before the root cause is understood.** A change that makes the symptom go away
without an explanation of *why* is a second bug with better manners. If you cannot say
which line produced the wrong value and why, you are still in step 1.

**If three fixes have failed, stop fixing.** The model of the system is wrong. Question
the layering — which process the code runs in, which origin the request comes from,
which container sees which address.

## The system, and where each thing can be observed

| Layer | Where to look |
|---|---|
| SDK in the app | Browser/console log with `debug: true`: `sightpane: initialised …`, `sent N items`, `send failed`. No "başlatıldı" line ⇒ `Sightpane.init` never ran (usually `HOG_API_KEY` empty because `--dart-define-from-file` was not applied; **hot restart does not refresh dart-defines**). |
| Wire | Browser Network tab: `POST /api/v1/envelope` status. 401 = key; CORS preflight `OPTIONS` = 204; 400 = `session.id` missing / bad JSON; 413 = envelope > 32 MB. |
| Backend | `/tmp/sightpane.log` (or container logs): `ingest: …` lines only on errors; startup line shows data dir, project, key, `proxy_protocol=`. `curl localhost:8790/api/v1/health`. |
| Data | TimescaleDB (`SIGHTPANE_DB`) — read with `psql`, and never write to a live one while debugging. Frames under `frames/<session>/<seq>.png`. |
| Dashboard | Riverpod providers in `frontend/lib/core/providers.dart`; every read goes through `HttpSightpaneApi` with the token from `TokenStore`; API URL = `SIGHTPANE_API_URL` or same-origin. |
| Proxies | `clientIP` order (Cloudflare → `Forwarded` → XFF → `X-Real-IP` → RemoteAddr); layer-4 Caddy needs `proxy_protocol v2` + `SIGHTPANE_PROXY_PROTOCOL=1`; Docker bridge shows 172.x. |
| Recording | LiveKit egress ⇐ backend `recording.Recorder` log lines; `go run ./tool/lkcheck` lists participants and active egress. |

## Debugging workflow

1. **Understand and reproduce.**
   - Expected vs actual, with the exact error text, screenshot, log line or status code.
   - Reproduce **on the system's own wiring**: `Sightpane.init(...)` + real `HttpTransport` for
     SDK issues (not a hand-built `SightpaneQueue`), the real `NewServer(store, ui)` handler in
     `httptest` for backend issues, `pumpApp` with one `ProviderContainer` for dashboard
     issues. A probe that constructs its own dependencies is a different system and shows
     failures production cannot have.
   - Cheap real probes: `package/tool/smoke_send.dart <url> <key>` (real envelope),
     `curl -X POST …/api/v1/envelope -H 'x-sightpane-key: …'`, `curl …/api/v1/auth/login`,
     `docker run --rm -p 8791:8790 sightpane` on a spare port.

2. **Monitor and investigate.**
   - **Known limits first.** The READMEs list deliberate limits (no disk queue, last batch
     lost on tab close, frame replay is image-only). If the symptom is
     one of them, say so instead of deep-diving — unless it regressed.
   - **Logs**, then **execution flow** (`grep -rn` the symbol across `package/lib`,
     `backend/*.go`, `frontend/lib`), then **state** (rows, files on disk, response JSON).
   - Verify hypotheses with tools, not intuition.

3. **Formulate a hypothesis and fix.** One change at a time; a regression test that fails
   on the old code (see `spec-first-testing`).

4. **Explain and resolve.** Root cause → what you looked at → the fix and why it holds.

## Failure signatures we have already met

- **"Sessions never appear"** → SDK never initialized (dart-define not applied, needs full
  restart), or the running instance predates the config change.
- **`DisconnectReason.joinFailure`** (camera live view) → LiveKit URL/API key mismatch
  between the publisher (Unreal `DefaultGame.ini`) and the backend env.
- **IP is `127.0.0.1` / `172.x`** → layer-4 proxy or Docker bridge in front; needs PROXY
  protocol or forwarded headers, and `SIGHTPANE_TRUSTED_PROXIES` must include the proxy's address.
- **"web · web" browser label** → old SDK without `browser`; backend `BrowserLabel` now
  derives from `user_agent` and blanks duplicates.
- **`RenderBox was not laid out` in the dashboard** → `Expanded`/stretch inside an
  unbounded-height scroll view (calendar, roulette board pattern); fix the layout, not
  the data.
- **Test passes/fails for the wrong reason** → a scripted edit did not apply after
  `dart format`, or a fixture used a literal date.
- **"no such column"** on startup → new column missing from the `ALTER TABLE`
  list in `migrate()`.

## Red flags — stop and go back to step 1

- "Quick fix now, investigate later"
- "Let me try changing X and see"
- "It's probably X" before tracing the data flow
- Changing several things at once
- "One more attempt" after two failed

| Excuse | Reality |
|---|---|
| "This one's simple, skip the process" | Simple bugs have root causes too; the process is quick for them. |
| "It's urgent" | Guess-and-check thrashing is slower than investigating once. |
| "I'll add the regression test after" | Then you never see it fail. |
| "The probe shows the bug" | Only if the probe used the real wiring (`Sightpane.init`, `NewServer`, `pumpApp`). |
| "I can see the problem" | Seeing the symptom is not understanding the cause. |

## When there really is no root cause

Environmental, timing-dependent, or an external system (LiveKit egress not deployed,
MinIO unreachable from the egress host, a browser refusing `requestFullscreen`). A
legitimate outcome — after the investigation. Then say what you ruled out, add the
handling (retry/backoff, a real error message, a log line that settles it next time),
and record the limit in the relevant README.

## Communication style

Methodical and analytical. Trace before touching. Explain the "why" — the user wants to
understand as much as they want the fix.

---

*The red-flag list, the rationalisations table and the root-cause-first rule are adapted
from [obra/superpowers](https://github.com/obra/superpowers) (MIT, © 2025 Jesse Vincent),
`systematic-debugging` at `b36e082`. See `SOURCE-vendored-skills.md`.*
