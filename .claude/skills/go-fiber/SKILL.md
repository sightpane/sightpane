---
name: go-fiber
description: How the sightpane Go backend is built on Fiber v3 — routing, middleware order, the single error shape, request binding, the SPA fallback, custom listeners and in-memory tests. Use whenever touching anything under  — adding or changing an endpoint, middleware, error code, static serving or a Fiber test — or when asked "which Fiber middleware", "how do I add a route", "hangi middleware", "endpoint ekle". Also use when picking a package from awesome-fiber or porting a gofiber/recipes example, so the choice matches what this repo already does.
---

# Fiber in sightpane

The backend is Fiber v3 (`github.com/gofiber/fiber/v3`) on fasthttp, one binary,
SQLite behind it. This file is the house pattern; it exists because Fiber has
several sharp edges that cost real debugging time here, and because a recipe
copied from upstream usually needs adjusting before it fits.

## Layout

```

  main.go                  wiring only: config → store → seed → listener → app
  ui/index.html            embedded fallback page (go:embed needs it beside main.go)
  internal/
    config/                every environment variable, with its default
    apierr/                error codes + the Error type (below store and server, so both can use it)
    netx/                  PROXY protocol listener
    store/                 SQLite: schema, ingest, queries. Knows nothing about HTTP
    server/                Fiber: routes, middleware, one handler file per resource
```

Handlers hold no SQL and the store holds no HTTP. When something needs both — an
error that is a 409 *and* a duplicate email — it goes in `apierr` and the store
returns it, so no handler keeps a mapping table.

## The five things that bite

**1. Middleware goes first in the argument list.** `app.Get(path, a, b, c)` runs
`a`, then `b`, then `c`. There is no separate "middleware" parameter. Writing
`api.Get("/projects", s.listProjects, s.requireAuth)` runs the handler with no
user on the context and panics.

```go
api.Get("/projects/:id", s.requireProject(roleMember), s.getProject) // right
```

**2. A guard that calls `c.Next()` runs the rest of the chain inside itself.**
This one deleted a project during a test: a `requireProject` that called
`requireAuth` (which called `Next`) and *then* checked membership ran the delete
handler before the check. Resolve identity without advancing the chain, check,
then `return c.Next()` once — see `authenticate` / `requireProject` in
`internal/server/middleware.go`.

**3. `c.Bind().Body()` dispatches on Content-Type.** A JSON body sent without the
header is parsed as form data and silently comes out empty. The SDK and curl
users do not always send the header, so this repo uses `c.Bind().JSON(&in)`
everywhere, which always parses JSON. Pinned by `TestJSONBodyWithoutContentType`.

**4. Handlers return errors, they never write them.** Every failure is a
`*apierr.Error`; `errorHandler` in `internal/server/errors.go` turns it into
`{"error": "...", "code": "..."}`. The dashboard translates on `code`, so
rewording a message is free and changing a code is a breaking change.

```go
if key == "" {
    return apierr.New(fiber.StatusUnauthorized, apierr.CodeKeyRequired, "x-sightpane-key header required")
}
```

**5. `c.IP()` reads one configured header.** Deployments here sit behind
Cloudflare *and* nginx, so `clientIP` walks its own order (CDN → `Forwarded` →
`X-Forwarded-For` → `X-Real-IP` → connection). Do not replace it with Fiber's
`TrustProxy`/`ProxyHeader` without re-reading `README.md`.

## Serving the dashboard

`static.New(dir, static.Config{NotFoundHandler: ...})` with the handler sending
`index.html` **and setting status 200** — a 404 makes the browser show its own
error page instead of letting go_router take over. The fallback must still
return a JSON 404 for `/api/` paths, or a mistyped endpoint answers with HTML
that no client can parse. Both halves are pinned by
`TestDashboardIsServedAsASinglePageApp`.

Register static **last**, after the API group, so it never shadows a route.

## Custom listener

The app is served over a listener we build ourselves (`app.Listener(ln, ...)`),
because a layer-4 proxy passes the client address in a PROXY protocol header
rather than an HTTP one. `app.Listen(addr)` cannot do that. Startup goes:
`net.Listen` → optionally `netx.ProxyListener` → `app.Listener`.

Serving runs in its own goroutine so SIGTERM can call `app.ShutdownWithTimeout`
and let `defer st.Close()` run — an ingest transaction about to commit does not
survive a hard kill.

## Testing

`app.Test(req)` runs a request in memory; no port, no goroutine. Two gotchas:

- The default timeout is **1 second**. PBKDF2 under `-race` exceeds it, so pass
  `fiber.TestConfig{Timeout: 10 * time.Second}`.
- The fake connection always reports `0.0.0.0`, so `req.RemoteAddr` is ignored.
  Send `X-Real-IP` for tests that need a client address; the connection-address
  path needs a real socket (`internal/server/proxy_test.go`).

`internal/server/server_test.go` wraps the response in a small `resp` type with
`Code`, `Body` and `Header()` so assertions read like the `httptest` ones did.

## What to take from awesome-fiber

<https://github.com/gofiber/awesome-fiber>. In use today, all from Fiber core:

| Middleware | Why it is here |
|---|---|
| `cors` | The SDK posts from an app origin and the dashboard from another port |
| `recover` | Ingest parses data we did not write; one bad envelope must not kill the process |
| `static` | Serves the built dashboard with the SPA fallback |

Worth adding when the matching roadmap item lands, and not before — each one is
a dependency and a config surface:

| Package | For | Roadmap |
|---|---|---|
| core `limiter` | Per-project ingest quota and rate limit | `future-todo-files/08` |
| core `healthcheck` | Real readiness/liveness split for k8s | ops |
| core `compress` + `etag` | Stats and session JSON are repetitive and re-fetched every second | performance |
| core `requestid` + `logger` | Correlating a dashboard complaint with a server log line | ops |
| core `sse` | Replace the dashboard's 1 s polling of `/live` and `/stats` | live view |
| core `pprof` | Profiling ingest under load | `future-todo-files/03` |
| contrib `otel` or `prometheus` | Ingest latency and queue depth as metrics | monitoring |
| contrib `testcontainers` | Postgres in tests without a hand-rolled harness | `future-todo-files/00b` |
| contrib `swaggerui` / `swaggo` | A public API surface once third parties use it | later |
| `gofiber/storage` | A shared store for quotas and alert state once there is more than one replica | `08`, `02` |

Deliberately not used: any ORM (the store writes SQL directly, and the queries
are the interesting part), `jwt` (sessions are opaque database-backed tokens,
which can be revoked), templating engines (the UI is a Flutter build).

## Recipes worth reading

<https://github.com/gofiber/recipes>. The ones that map onto this codebase:

| Recipe | Use it for |
|---|---|
| `spa` | The static + fallback pattern this repo uses for the dashboard |
| `404-handler` | Custom not-found shaping, which is what `errorHandler` does |
| `graceful-shutdown` | The signal/`ShutdownWithTimeout` loop copied into `main.go` |
| `unit-test` | `app.Test` structure (this repo asserts with the standard library, not testify) |
| `sse` | The shape of a live endpoint, when `/live` polling is replaced |
| `local-development-testcontainers` | Postgres for tests, for issue `00b` |
| `postgresql`, `sqlc` | Driver and query patterns for the same migration |
| `validation` | `go-playground/validator` — only if request shapes grow past a handful of fields |
| `clean-architecture`, `hexagonal` | Reference layouts. Deliberately heavier than this repo needs; we stopped at store/server |
| `prefork`, `multiple-ports` | Not applicable: SQLite has a single writer, so a second process would contend |

When porting a recipe, check three things: it is v3 (`func(c fiber.Ctx) error`,
not `*fiber.Ctx`), it returns errors instead of writing them, and it does not
introduce a dependency the table above rejects.

## Checklist for a new endpoint

1. Route in `internal/server/server.go`, middleware first, in the `/api/v1` group.
2. Handler in the matching `*_handler.go`; bind with `c.Bind().JSON`, return
   `apierr` values, end with `c.JSON(...)`.
3. New failure mode → a new code in `internal/apierr`, a row in the
   `README.md` table, and a branch in the dashboard's `describeError`.
4. Store method in `internal/store`, with SQL kept there.
5. Test in `internal/server/server_test.go`; add the code to `TestErrorCodes`.
6. `go vet ./... && go test ./...`, and `go test -race ./...` before committing.
