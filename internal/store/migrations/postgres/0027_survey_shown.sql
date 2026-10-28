-- 0027_survey_shown.sql: when a server-scheduled survey was last shown to a person.
--
-- A server-scheduled survey is due once its trigger event is older than the
-- survey's delay. Due-ness is read from the items table at ask time; this table
-- only records that the SDK showed it, so the same trigger does not bring it
-- back. A later trigger event (newer than shown_at) makes it due again.
-- user_key is "u:<user id>", or "s:<session id>" for a person with no user id.

CREATE TABLE IF NOT EXISTS survey_shown (
    survey_id BIGINT NOT NULL REFERENCES surveys(id) ON DELETE CASCADE,
    user_key TEXT NOT NULL,
    shown_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (survey_id, user_key)
);
