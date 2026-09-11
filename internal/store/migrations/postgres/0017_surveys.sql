-- 0017_surveys.sql: in-app surveys & user feedback linked to session replays

CREATE TABLE IF NOT EXISTS surveys (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('nps', 'csat', 'rating', 'open_text', 'single_choice')),
    question TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    choices JSONB NOT NULL DEFAULT '[]'::jsonb,
    targeting JSONB NOT NULL DEFAULT '{}'::jsonb,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_surveys_project ON surveys(project_id);

CREATE TABLE IF NOT EXISTS survey_responses (
    id BIGSERIAL PRIMARY KEY,
    survey_id BIGINT NOT NULL REFERENCES surveys(id) ON DELETE CASCADE,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    user_id TEXT NOT NULL DEFAULT '',
    score INT,
    response_text TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_survey_responses ON survey_responses(survey_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_survey_responses_project ON survey_responses(project_id, created_at DESC);
