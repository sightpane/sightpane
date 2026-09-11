# feat(backend,dashboard): A/B testing & experimentation platform with statistical significance

## Problem

Teams building apps cannot run controlled A/B tests or multivariate experiments in Sightpane today. They must either pay for third-party platforms (PostHog, LaunchDarkly, Optimizely) or manually guess whether a change improved conversion. There is no automated variant assignment, sample size estimation, or calculation of statistical significance (p-values, confidence intervals, Bayesian probability of being the best).

## Why it matters

A/B testing is the natural evolution of Feature Flags (issue 15) and Product Analytics. Because Sightpane already ingests all user events, sessions, and errors, it has all the data needed to evaluate experiment results with zero additional tracking scripts. Furthermore, Sightpane can track guardrail metrics (e.g. "Did Variant B increase conversions, but also increase crash rates by 2%?").

## Where to look

**Code**
- `backend/internal/stats/significance.go` (new): Mathematical computation engine implementing:
  - Frequentist two-proportion z-test / chi-square test (p-value, 95% confidence intervals).
  - Bayesian beta-binomial distribution model (calculates $P(\text{Variant B} > \text{Control})$ and expected loss).
- `backend/internal/store/experiments.go` (new): Queries aggregating unique participant exposures and metric conversions per variant from the `events` hypertable.
- `backend/internal/server/experiment_handlers.go` (new): CRUD endpoints and experiment results calculation.
- `frontend/lib/features/experiments/experiments_page.dart` (new): Overview of running, drafted, and completed experiments with status badges.
- `frontend/lib/features/experiments/experiment_detail_page.dart` (new): Variant performance comparison, conversion rates, statistical confidence meters, and "Declare Winner" flow.
- `frontend/lib/features/experiments/widgets/significance_badge.dart` (new): Visual indicator ("Statistically Significant (98.4%)" vs "Needs more data (sample size: 1,420 / 5,000)").

**Contract / data**
- Table `experiments`:
  ```sql
  CREATE TABLE experiments (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      name TEXT NOT NULL,
      description TEXT DEFAULT '',
      feature_flag_key TEXT NOT NULL REFERENCES feature_flags(key) ON DELETE RESTRICT,
      status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'running', 'concluded')),
      primary_metric_event TEXT NOT NULL, -- Target event e.g. "subscription_started"
      secondary_metric_events JSONB DEFAULT '[]', -- Guardrail events e.g. ["error_rate", "churn"]
      variants JSONB NOT NULL, -- [{"key": "control", "name": "Original"}, {"key": "test_v2", "name": "New Onboarding"}]
      minimum_sample_size INT NOT NULL DEFAULT 1000,
      winner_variant TEXT,
      started_at TIMESTAMPTZ,
      concluded_at TIMESTAMPTZ,
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  CREATE INDEX idx_experiments_project ON experiments(project_id);
  ```
- Endpoint `GET /api/v1/projects/:id/experiments/:expId/results?days=14`:
  ```json
  {
    "status": "running",
    "total_participants": 8420,
    "statistical_significance": 0.982,
    "is_significant": true,
    "recommended_action": "variant_b_winning",
    "variants": [
      {
        "key": "control",
        "participants": 4210,
        "conversions": 340,
        "conversion_rate": 0.0807,
        "confidence_interval": [0.072, 0.089]
      },
      {
        "key": "variant_b",
        "participants": 4210,
        "conversions": 455,
        "conversion_rate": 0.1081,
        "confidence_interval": [0.098, 0.118],
        "relative_lift": 0.339,
        "chance_to_win": 0.982
      }
    ]
  }
  ```

## Fix shape

1. **Statistical Engine**:
   - Implement standard two-proportion z-tests for binary conversion metrics in `significance.go`.
   - Implement continuous metric evaluation (average revenue per user or session duration) using Welch's t-test.
2. **Exposure & Conversion Association**:
   - A participant is counted when the SDK requests the experiment flag or sends an event with `$feature_flag` evaluation.
   - Conversions are counted when the participant fires `primary_metric_event` strictly *after* their first exposure timestamp.
3. **Declare Winner**:
   - Clicking "Roll out winner" automatically updates the underlying feature flag rollout to 100% for the selected variant and concludes the experiment.

## Acceptance

- [ ] Experiments can be created linking directly to an automated Feature Flag variant rollout.
- [ ] Sample size calculations accurately report when enough data has been collected to reach 95% statistical power.
- [ ] The dashboard clearly presents conversion rates, relative lifts, and confidence intervals for each variant.
- [ ] Guardrail metrics (such as crash-free session rate per variant) are reported side-by-side to prevent shipping breaking variants.
- [ ] Declaring a winner seamlessly updates the flag and archives the experiment.
