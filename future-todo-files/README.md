# future-todo-files

Features that Sentry has and sightpane does not, written as issues ready to be moved to
GitHub (the `.claude/skills/issue-writer` template: Problem · Why it matters · Where to
look · Fix shape · Acceptance). Every file is written with code coordinates; to move one over,
`env -u GITHUB_TOKEN gh issue create --title … --body-file future-todo-files/NN-….md`.

| # | File | Priority | Scope |
|---|---|---|---|
| 0a | [00a-i18n.md](00a-i18n.md) | **done** | Multi-language in the dashboard and the backend: ARB/gen-l10n, error codes, language selection |
| 0b | [00b-timescaledb-postgres.md](00b-timescaledb-postgres.md) | **done** | SQLite → Postgres + TimescaleDB: hypertable, time_bucket, retention. Frame expiry was left to 08 |
| 01 | [01-source-maps.md](01-source-maps.md) | **done** | Making `minified:Class447` stack traces readable in release builds, and stopping grouping drift between builds |
| 02 | [02-alerts-notifications.md](02-alerts-notifications.md) | **done** | E-mail, Slack and webhooks on a new error, a regression or a rate increase |
| 03 | [03-performance.md](03-performance.md) | **done** | Transaction/span tracing: route load, gRPC/HTTP timings, app start, jank |
| 04 | [04-release-health.md](04-release-health.md) | **done** | Crash-free sessions per release, adoption, resolved/regressed release |
| 05 | [05-issue-workflow.md](05-issue-workflow.md) | **done** | Assignment, ignore/snooze, comments, merging/splitting groups, fingerprint rules |
| 06 | [06-search-and-tags.md](06-search-and-tags.md) | **done** | Searching sessions/issues with tags and a query language |
| 07 | [07-replay-on-error-and-offline-queue.md](07-replay-on-error-and-offline-queue.md) | **done** | Recording only when an error occurs, a persistent offline queue |
| 08 | [08-sampling-quotas-retention.md](08-sampling-quotas-retention.md) | **done** | Sampling rates, ingest quota, data retention job |
| 09 | [09-pii-and-privacy.md](09-pii-and-privacy.md) | **done** | PII scrubbing rules, deleting/exporting user data |
| 10 | [10-orgs-roles-audit.md](10-orgs-roles-audit.md) | **done** | Organizations, fine-grained roles, audit log, scoped API tokens |
| 11 | [11-storage-backend.md](11-storage-backend.md) | **done** | Frame/object store (S3); the SQL side moved to 0b |
| 12 | [12-object-storage-ceph.md](12-object-storage-ceph.md) | **done** | Replay frames in Ceph/S3 object storage instead of a local volume |
| 13 | [13-multi-platform-sdks.md](13-multi-platform-sdks.md) | **done** | Web/React and React Native SDKs, a DOM replay item type, and a platform-neutral name |
| 14 | [14-funnels-conversion.md](14-funnels-conversion.md) | **done** | Multi-step conversion funnels, drop-off analysis, and direct link to session replays |
| 15 | [15-feature-flags.md](15-feature-flags.md) | **done** | Feature flags & remote configuration, percentage rollouts, user targeting, offline caching |
| 16 | [16-ab-testing-experiments.md](16-ab-testing-experiments.md) | **done** | A/B testing & experiments, variant assignment, statistical significance & confidence intervals |
| 17 | [17-cohorts-retention.md](17-cohorts-retention.md) | **done** | Behavioral user cohorts and N-day/week retention matrix heatmaps |
| 18 | [18-custom-dashboards-insights.md](18-custom-dashboards-insights.md) | **done** | Custom multi-metric dashboards, dynamic insight query builder, formula metrics, custom tiles |
| 19 | [19-user-paths-flows.md](19-user-paths-flows.md) | **done** | User paths & journey flows with interactive Sankey diagrams before/after key events |
| 20 | [20-surveys-user-feedback.md](20-surveys-user-feedback.md) | **done** | In-app micro-surveys (NPS, CSAT, bug reports) targeted by route/event and linked to replays |
| 21 | [21-cron-job-monitoring.md](21-cron-job-monitoring.md) | **done** | Cron job and scheduled task heartbeat monitoring, crontab parser, missed run alerts |
| 22 | [22-distributed-tracing-spans.md](22-distributed-tracing-spans.md) | **done** | Distributed tracing, W3C traceparent propagation across services, and waterfall span flame chart |
| 23 | [23-metric-alerts-anomaly-detection.md](23-metric-alerts-anomaly-detection.md) | **done** | Metric threshold alert engine, rolling window evaluators, and anomaly spike detection |
| 24 | [24-profiling-cpu-memory.md](24-profiling-cpu-memory.md) | **done** | Continuous CPU & memory profiling, call trees, and interactive flame graph visualizer |
| 25 | [25-uptime-synthetic-monitoring.md](25-uptime-synthetic-monitoring.md) | **done** | Exterior uptime monitoring, synthetic HTTP/TCP ping health checks, and SSL expiry warnings |

