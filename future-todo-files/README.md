# future-todo-files

Features that Sentry has and sightpane does not, written as issues ready to be moved to
GitHub (the `.claude/skills/issue-writer` template: Problem · Why it matters · Where to
look · Fix shape · Acceptance). Every file is written with code coordinates; to move one over,
`env -u GITHUB_TOKEN gh issue create --title … --body-file future-todo-files/NN-….md`.

| # | File | Priority | Scope |
|---|---|---|---|
| 0a | [00a-i18n.md](00a-i18n.md) | **done** | Multi-language in the dashboard and the backend: ARB/gen-l10n, error codes, language selection |
| 0b | [00b-timescaledb-postgres.md](00b-timescaledb-postgres.md) | high | SQLite → Postgres + TimescaleDB: hypertable, time_bucket, retention; after 0a |
| 01 | [01-source-maps.md](01-source-maps.md) | high | Making `minified:Class447` stack traces readable in release builds |
| 02 | [02-alerts-notifications.md](02-alerts-notifications.md) | high | E-mail, Slack and webhooks on a new error, a regression or a rate increase |
| 03 | [03-performance.md](03-performance.md) | high | Transaction/span tracing: route load, gRPC/HTTP timings, app start, jank |
| 04 | [04-release-health.md](04-release-health.md) | medium | Crash-free sessions per release, adoption, resolved/regressed release |
| 05 | [05-issue-workflow.md](05-issue-workflow.md) | medium | Assignment, ignore/snooze, comments, merging/splitting groups, fingerprint rules |
| 06 | [06-search-and-tags.md](06-search-and-tags.md) | medium | Searching sessions/issues with tags and a query language |
| 07 | [07-replay-on-error-and-offline-queue.md](07-replay-on-error-and-offline-queue.md) | medium | Recording only when an error occurs, a persistent offline queue |
| 08 | [08-sampling-quotas-retention.md](08-sampling-quotas-retention.md) | medium | Sampling rates, ingest quota, data retention job |
| 09 | [09-pii-and-privacy.md](09-pii-and-privacy.md) | medium | PII scrubbing rules, deleting/exporting user data |
| 10 | [10-orgs-roles-audit.md](10-orgs-roles-audit.md) | low | Organizations, fine-grained roles, audit log, scoped API tokens |
| 11 | [11-storage-backend.md](11-storage-backend.md) | low | Frame/object store (S3); the SQL side moved to 0b |
| 12 | [12-object-storage-ceph.md](12-object-storage-ceph.md) | **partly done** | Replay frames in Ceph/S3 object storage instead of a local volume |
| 13 | [13-multi-platform-sdks.md](13-multi-platform-sdks.md) | high | Web/React and React Native SDKs, a DOM replay item type, and a platform-neutral name |

Deliberately left out: Sentry's DOM-based web replay (sightpane's frame-based recording was
preferred because it works on web and desktop), profiling, cron monitoring, uptime monitoring.
