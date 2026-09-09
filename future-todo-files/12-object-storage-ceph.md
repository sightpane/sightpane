# feat(backend,docker): keep replay frames in Ceph object storage instead of a local volume

> **Status: partly done (2026-09-09).** The pluggable frame store and its Ceph/S3
> backend shipped, and `docker compose --profile ceph up -d` runs a gateway to
> develop against. What is left is the operational half: sweeping orphaned
> objects, moving an existing `fs` deployment across, and the second replica that
> this unlocks. Those are the unticked boxes under Acceptance.

## Problem

Replay frames were files under `SIGHTPANE_DATA/frames/<session>/<seq>.png`, written by
`saveFrame` and served with a `sendfile`. That is the only data in the service
that grows without bound — a one-minute session at the default 1 s interval and
0.5 scale is 60 PNGs of 20–60 KB — and it was the one thing that made the backend
impossible to run twice: a local directory is not shared, so a second replica
would answer 404 for every frame the first one wrote. Backing it up meant copying
a directory that grows faster than the database, and there was no way to put the
frames on different hardware from the SQLite file.

## Why it matters

Everything else in the roadmap that adds volume runs into this. `03` (spans) and
`07` (record-on-error with a persistent queue) multiply the frame count; `08`
(retention) has to delete frames as well as rows; `11` (Postgres) makes more than
one backend replica possible, which is pointless while frames live on one box.
Ceph is the answer that does not add a second product to operate: it replicates
and scrubs the objects itself, grows by adding OSDs, and its RADOS Gateway speaks
S3 — the same API MinIO and AWS S3 speak, so one code path covers all three.

## Where to look

**Code**
- `backend/internal/blob/blob.go` — the `Store` interface (`Put`, `Get`,
  `DeletePrefix`, `Kind`, `Close`), `FrameKey` and `SessionPrefix`. The key
  layout is `<session>/<seq>.png` with the sequence zero-padded to six digits, so
  an object listing sorts the same way the frames play, and an existing
  `frames/` directory is readable by the `fs` backend with no migration.
- `backend/internal/blob/fs.go` — `FS`, the default. Contains a traversal inside
  its root rather than rejecting it, the way a static file server does.
- `backend/internal/blob/s3.go` — `S3`, built on `minio-go`. Creates the bucket
  if it is missing so a fresh cluster needs no setup step.
- `backend/internal/store/store.go` — `Open(dataDir, frames)`; `Store.Frames()`
  reports the backend for logs and `/health`.
- `backend/internal/store/ingest.go` — `saveFrame` decodes the base64 PNG and
  puts it *before* the row is written, so a frame that cannot be stored fails the
  whole envelope instead of leaving a row pointing at nothing.
- `backend/internal/store/query.go` — `FrameReader` checks the row first and the
  object second: the database is the record of what exists, so a missing object
  is a storage fault, not a 404 to paper over.
- `backend/internal/store/project.go` — `DeleteProject` removes the rows in one
  transaction and the objects afterwards, best-effort.
- `backend/internal/server/data_handler.go` — `getFrame` streams the object
  through the handler. Note the comment about *not* closing the reader: fasthttp
  writes the body after the handler returns and closes the stream itself.
- `backend/internal/config/config.go` — `Frames`, `S3Config`.
- `backend/main.go` — `openFrameStore`.
- `docker-compose.yml` — the `ceph` profile.

**Contract / data**
- Environment: `SIGHTPANE_FRAMES=fs|s3`; for `s3`, `SIGHTPANE_S3_ENDPOINT` (host:port, no
  scheme), `SIGHTPANE_S3_BUCKET` (default `sightpane-frames`), `SIGHTPANE_S3_ACCESS_KEY`,
  `SIGHTPANE_S3_SECRET_KEY`, `SIGHTPANE_S3_REGION`, `SIGHTPANE_S3_USE_SSL`.
- The HTTP contract did not change: `GET /api/v1/sessions/{id}/frames/{seq}.png`
  returns the same bytes with the same headers whichever backend is behind it.
- Frames are streamed, not redirected to a presigned URL. Access to a frame is
  decided per project on every request, and a presigned URL would outlive that
  decision. The cost is that frame traffic passes through the backend.

**Tests that pin current behaviour**
- `backend/internal/blob/blob_test.go` — `runStoreSuite` runs the identical suite
  against both backends, because a difference between them would be a bug that
  reproduces on one deployment and not the other. `TestS3` skips unless
  `SIGHTPANE_TEST_S3_ENDPOINT` is set.
- `backend/internal/server/server_test.go` — `TestIngestAndQuery` round-trips a
  frame through the API and compares the bytes.

**Docs / prior art**
- `backend/README.md` environment table; the root `README.md` Docker section.
- `11-storage-backend.md`, whose S3 half this closes; `08` (retention must delete
  objects too); `00b` (Postgres, the other half of running two replicas).

## Fix shape

The remaining work, in the order it becomes necessary:

1. **Sweep orphans.** `DeleteProject` deletes objects best-effort and logs a
   failure, so a gateway hiccup leaves unreachable objects behind. The retention
   job from `08` should also walk `SessionPrefix` for sessions that no longer
   have rows. A Ceph lifecycle rule is not enough on its own — it expires by age,
   not by whether a row still points at the object.
2. **Migrate an existing deployment.** `go run . migrate-frames --from fs --to s3`
   walking `frames/` and putting each object, resumable, verifying by size. Until
   that exists, switching `SIGHTPANE_FRAMES` on a live install hides the old frames.
3. **Bucket hygiene.** One bucket per deployment, not per project: Ceph's bucket
   index is a bottleneck at high object counts, and project ids are already the
   first path segment through the session. Consider bucket sharding
   (`radosgw-admin bucket reshard`) once a bucket passes a few million objects.
4. **Credentials.** `SIGHTPANE_S3_SECRET_KEY` is read from the environment, which puts
   it in `docker inspect`. Support `SIGHTPANE_S3_SECRET_KEY_FILE` for a Docker/k8s
   secret, matching what the Postgres DSN will need in `00b`.
5. **Second replica.** Only after `00b`: with Postgres and S3 there is no local
   state left, so the backend can be scaled horizontally. The in-memory pieces
   (quota counters in `08`, alert state in `02`) have to move to a shared store
   first — that is the real blocker, not storage.

Rejected: presigned URLs for frames (they outlive the authorisation check);
storing frames as SQLite blobs (the database has to stay small enough to copy);
CephFS or RBD instead of the gateway (a POSIX mount reintroduces the shared-volume
problem the S3 API avoids); a second object-store abstraction for anything other
than frames (nothing else is large enough to justify it).

## Acceptance

- [x] `SIGHTPANE_FRAMES=s3` with `SIGHTPANE_S3_*` pointed at a Ceph RADOS Gateway ingests a
      frame and serves it back byte-identical through
      `GET /api/v1/sessions/{id}/frames/{seq}.png`
- [x] with `s3` selected, nothing is written under `SIGHTPANE_DATA` except `sightpane.db`
- [x] deleting a project removes its objects from the bucket
- [x] `SIGHTPANE_FRAMES` unset behaves exactly as before, and an existing
      `SIGHTPANE_DATA/frames` tree keeps working with no migration
- [x] `backend/internal/blob/blob_test.go` runs the same suite against both
      backends; the S3 half passes against a real gateway
      (`docker compose --profile ceph up -d`)
- [x] `docker compose --profile ceph up -d` brings up a gateway that the backend
      can be pointed at with four environment variables
- [ ] the retention job removes objects whose rows are gone, and a failed
      `DeleteProject` sweep is picked up on the next run
- [ ] `migrate-frames` moves an existing `fs` deployment to `s3`, resumably, and
      reports what it verified
- [ ] `SIGHTPANE_S3_SECRET_KEY_FILE` is supported so the secret is not in the process
      environment
- [ ] two backend replicas behind one gateway serve each other's frames (needs
      `00b` first)
