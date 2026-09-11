# feat(backend,sdk,dashboard): in-app surveys & user feedback linked to session replays

## Problem

Telemetry data reveals *what* users did and *where* errors occurred, but cannot capture *why* users felt frustrated, what they expected, or whether they are satisfied with a new feature. Teams currently resort to external survey tools (Typeform, SurveyMonkey) that are completely disconnected from session replays and user metadata.

## Why it matters

PostHog's Surveys product combines qualitative user sentiment directly with quantitative telemetry. If a user submits an NPS rating of 2 out of 10 or submits an in-app bug report with "Checkout button is broken", clicking on their survey response in Sightpane immediately plays the recording of what happened right before they submitted the feedback.

## Where to look

**Code**
- `backend/internal/store/surveys.go` (new): Storage for survey definitions, targeting criteria, and response submissions.
- `backend/internal/server/survey_handlers.go` (new): Endpoints for fetching active surveys for a client and submitting responses.
- `flutter/lib/src/surveys/survey_overlay.dart` (new): Non-intrusive bottom-sheet or toast component rendered when an app triggers a survey condition.
- `react-sdk/src/react/survey.tsx` (new): Unstyled/themed survey popup component for React apps.
- `frontend/lib/features/surveys/surveys_page.dart` (new): List of active, scheduled, and concluded surveys.
- `frontend/lib/features/surveys/survey_detail_page.dart` (new): Net Promoter Score (NPS) meter (-100 to +100), Customer Satisfaction (CSAT) score, response frequency distribution, and table of individual responses with "Watch Replay" action.

**Contract / data**
- Tables `surveys` and `survey_responses`:
  ```sql
  CREATE TABLE surveys (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      name TEXT NOT NULL,
      type TEXT NOT NULL CHECK (type IN ('nps', 'csat', 'rating', 'open_text', 'single_choice')),
      question TEXT NOT NULL,
      description TEXT DEFAULT '',
      choices JSONB DEFAULT '[]', -- For single_choice
      targeting JSONB NOT NULL DEFAULT '{}', -- { "url_pattern": "/checkout/*", "event_trigger": "purchase_success", "sample_rate": 0.2 }
      active BOOLEAN NOT NULL DEFAULT true,
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );

  CREATE TABLE survey_responses (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      survey_id UUID NOT NULL REFERENCES surveys(id) ON DELETE CASCADE,
      project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      session_id UUID REFERENCES sessions(id) ON DELETE SET NULL,
      user_id TEXT,
      score INT, -- For NPS (0-10) or CSAT (1-5)
      response_text TEXT,
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  CREATE INDEX idx_survey_responses ON survey_responses(survey_id, created_at DESC);
  ```

## Fix shape

1. **SDK Targeting & Rendering**:
   - SDK requests active surveys for the project on startup.
   - When navigation hits `url_pattern` or `event_trigger` occurs, SDK verifies that the user hasn't already answered or dismissed the survey in the last 30 days.
   - SDK displays a subtle card adhering to app styling tokens.
2. **Submission**:
   - Submits answer along with `session_id`, `user_id`, and current route.
   - Emits a `breadcrumb` (`category: "survey"`, `message: "Answered NPS survey with score 9"`) so it appears in the session replay timeline.
3. **Dashboard Analytics**:
   - Computes NPS: $\% \text{Promoters (9-10)} - \% \text{Detractors (0-6)}$.
   - Table of responses with direct links to the user profile and session replay.

## Acceptance

- [ ] Product teams can configure NPS, CSAT, or free-form text feedback surveys.
- [ ] Survey displays smoothly on both Flutter mobile/web apps and React web apps without crashing the host app.
- [ ] Completed survey responses link directly to the session recording where the feedback was given.
- [ ] Users who dismiss or complete a survey are not prompted again within the configured cooldown window.
