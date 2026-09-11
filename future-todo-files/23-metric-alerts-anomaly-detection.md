# feat(backend,dashboard): metric threshold alerts & dynamic anomaly spike detection

## Problem

Sightpane's current alert engine (issue 02) triggers only on discrete events (e.g., "new error seen", "issue reopened"). In high-traffic production environments, single errors happen all the time (transient network blips, bad client inputs). Alerting on every error causes severe alert fatigue. Sightpane currently cannot evaluate metric thresholds over time (e.g., "Alert if error rate exceeds 3% over a 5-minute window", or "Alert if p95 transaction latency exceeds 1.2s") nor detect anomalous spikes relative to historical traffic (e.g., "Traffic is 4x higher than the trailing 14-day average for Monday at 10 AM").

## Why it matters

Sentry's Metric Alerts are essential for on-call operations and SREs. Rather than receiving 500 individual error notifications during an incident, engineers receive a single incident notification when the threshold is crossed, continuous status tracking, and an automated "Resolved" notification when the error rate drops back within SLA limits.

## Where to look

**Code**
- `backend/internal/alerts/metric_worker.go` (new): Background goroutine running every 60 seconds that executes rolling metric evaluation queries against TimescaleDB hypertables.
- `backend/internal/alerts/anomaly.go` (new): Dynamic baseline calculator comparing the current time window against the identical time window over the last 14 days ($t - 7\text{d}$ and $t - 14\text{d}$) to calculate z-scores and relative multipliers ($V_{\text{current}} > k \cdot V_{\text{baseline}}$).
- `backend/internal/store/metric_alerts.go` (new): Queries for rule management, active incident tracking, and state transitions (`ok` $\leftrightarrow$ `firing`).
- `backend/internal/alerts/dispatch.go`: Send consolidated incident payloads (including link to dashboard, affected metric value, and threshold) to Slack, Discord, email, and Webhooks.
- `frontend/lib/features/settings/metric_alert_rules_page.dart` (new): Rule editor with visual preview chart showing the threshold line superimposed over historical metrics.
- `frontend/lib/features/settings/metric_alert_incidents_page.dart` (new): Incident history log showing duration, peak values, and resolution times.

**Contract / data**
- Tables `metric_alert_rules` and `metric_alert_incidents`:
  ```sql
  CREATE TABLE metric_alert_rules (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      name TEXT NOT NULL,
      metric_type TEXT NOT NULL CHECK (metric_type IN ('error_count', 'error_rate', 'transaction_duration_p95', 'unhandled_crash_count')),
      target_filter TEXT DEFAULT '', -- e.g. "route:/checkout" or "platform:android"
      comparison_operator TEXT NOT NULL CHECK (comparison_operator IN ('gt', 'gte', 'lt', 'spike_multiplier')),
      critical_threshold NUMERIC(10, 2) NOT NULL, -- e.g. 5.0 (for 5% rate) or 3.0 (for 3x spike)
      warning_threshold NUMERIC(10, 2),
      window_minutes INT NOT NULL DEFAULT 5, -- Rolling window: 1, 5, 10, 15, 30, 60
      channel_ids JSONB NOT NULL DEFAULT '[]',
      is_active BOOLEAN NOT NULL DEFAULT true,
      current_status TEXT NOT NULL DEFAULT 'ok' CHECK (current_status IN ('ok', 'warning', 'firing')),
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );

  CREATE TABLE metric_alert_incidents (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      rule_id UUID NOT NULL REFERENCES metric_alert_rules(id) ON DELETE CASCADE,
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      status TEXT NOT NULL CHECK (status IN ('firing', 'resolved')),
      triggered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      resolved_at TIMESTAMPTZ,
      peak_value NUMERIC(10, 2) NOT NULL,
      summary TEXT NOT NULL
  );
  CREATE INDEX idx_incidents ON metric_alert_incidents(rule_id, triggered_at DESC);
  ```

## Fix shape

1. **Rolling Window Aggregator**:
   - Run a single combined query every minute against TimescaleDB continuous aggregates evaluating active rules.
   - For error rate: $\frac{\text{count(errors)}}{\text{count(sessions)}} \times 100\%$.
2. **State Machine & Flapping Suppression**:
   - Transition to `firing` only when the threshold is exceeded for $M$ of $N$ evaluation ticks.
   - Transition back to `ok` (auto-resolve) only after $K$ consecutive ticks below threshold, sending a single "Incident Resolved" message.
3. **Dashboard Threshold Visualizer**:
   - In the rule builder, plot the chosen metric for the past 7 days and draw a draggable threshold line so developers can visually verify how often the alert would have fired.

## Acceptance

- [ ] Alerts trigger accurately based on rolling 1m, 5m, 15m, and 60m metric windows.
- [ ] Anomaly detection flags sudden volume surges exceeding historical baselines.
- [ ] Active incidents dispatch to Slack, Discord, and Webhooks with direct deep links to the affected transactions/errors.
- [ ] Auto-resolves and notifies channels when metric recovers below threshold.
