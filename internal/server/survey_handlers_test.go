// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"testing"

	"sightpane/internal/store"
)

func TestSurveysEndpoints(t *testing.T) {
	app, st := newTestServer(t)
	projID := int64(1)

	// Get project for its API key
	p, err := st.ProjectByID(projID)
	if err != nil {
		t.Fatalf("project by id: %v", err)
	}

	// 1. Create NPS Survey
	npsBody := map[string]any{
		"name":        "Quarterly NPS",
		"type":        "nps",
		"question":    "How likely are you to recommend Sightpane to a colleague?",
		"description": "NPS survey shown after 3 sessions",
		"targeting": map[string]any{
			"url_pattern":   "/dashboard/*",
			"event_trigger": "session_start",
			"sample_rate":   1.0,
		},
		"active": true,
	}
	rr := post(t, app, fmt.Sprintf("/api/v1/projects/%d/surveys", projID), "", npsBody)
	if rr.Code != 201 {
		t.Fatalf("POST /surveys: %d %s", rr.Code, rr.Body.String())
	}
	var npsSurvey store.Survey
	if err := json.Unmarshal(rr.Body.Bytes(), &npsSurvey); err != nil {
		t.Fatalf("unmarshal survey: %v", err)
	}
	if npsSurvey.Name != "Quarterly NPS" || npsSurvey.Type != "nps" {
		t.Fatalf("unexpected survey: %+v", npsSurvey)
	}

	// 2. Create CSAT Survey
	csatBody := map[string]any{
		"name":        "Checkout Satisfaction",
		"type":        "csat",
		"question":    "How satisfied were you with the checkout process?",
		"description": "Triggered on order completion",
		"targeting": map[string]any{
			"url_pattern": "/checkout/success",
		},
		"active": true,
	}
	rrCsat := post(t, app, fmt.Sprintf("/api/v1/projects/%d/surveys", projID), "", csatBody)
	if rrCsat.Code != 201 {
		t.Fatalf("POST csat survey: %d %s", rrCsat.Code, rrCsat.Body.String())
	}
	var csatSurvey store.Survey
	_ = json.Unmarshal(rrCsat.Body.Bytes(), &csatSurvey)

	// 3. List surveys
	var surveysList []*store.Survey
	rrList := get(t, app, fmt.Sprintf("/api/v1/projects/%d/surveys", projID), &surveysList)
	if rrList.Code != 200 {
		t.Fatalf("GET /surveys: %d %s", rrList.Code, rrList.Body.String())
	}
	if len(surveysList) != 2 {
		t.Fatalf("expected 2 surveys, got %d", len(surveysList))
	}

	// 4. Get survey by ID
	var fetched store.Survey
	rrGet := get(t, app, fmt.Sprintf("/api/v1/projects/%d/surveys/%d", projID, npsSurvey.ID), &fetched)
	if rrGet.Code != 200 {
		t.Fatalf("GET survey by id: %d %s", rrGet.Code, rrGet.Body.String())
	}
	if fetched.Question != npsSurvey.Question {
		t.Fatalf("expected question %q, got %q", npsSurvey.Question, fetched.Question)
	}

	// 5. Update survey
	updateBody := map[string]any{
		"question": "On a scale of 0 to 10, how likely are you to recommend us?",
	}
	rrPut := put(t, app, fmt.Sprintf("/api/v1/projects/%d/surveys/%d", projID, npsSurvey.ID), "", updateBody)
	if rrPut.Code != 200 {
		t.Fatalf("PUT survey: %d %s", rrPut.Code, rrPut.Body.String())
	}

	// 6. Client SDK: Get Active Surveys
	activeURL := fmt.Sprintf("/api/v1/surveys/active?key=%s", p.APIKey)
	var activeResp struct {
		Surveys []*store.Survey `json:"surveys"`
	}
	rrActive := get(t, app, activeURL, &activeResp)
	if rrActive.Code != 200 {
		t.Fatalf("GET /surveys/active: %d %s", rrActive.Code, rrActive.Body.String())
	}
	if len(activeResp.Surveys) < 2 {
		t.Fatalf("expected at least 2 active surveys, got %d", len(activeResp.Surveys))
	}

	// 7. Client SDK: Submit Responses for NPS
	// Promoter: 10
	score10 := 10
	rrResp1 := post(t, app, fmt.Sprintf("/api/v1/surveys/%d/responses", npsSurvey.ID), p.APIKey, map[string]any{
		"user_id":       "usr_1",
		"score":         score10,
		"response_text": "Love the session replay feature!",
	})
	if rrResp1.Code != 201 {
		t.Fatalf("submit response 1: %d %s", rrResp1.Code, rrResp1.Body.String())
	}

	// Promoter: 9
	score9 := 9
	rrResp2 := post(t, app, fmt.Sprintf("/api/v1/surveys/%d/responses", npsSurvey.ID), p.APIKey, map[string]any{
		"user_id":       "usr_2",
		"score":         score9,
		"response_text": "Super fast and easy to self-host.",
	})
	if rrResp2.Code != 201 {
		t.Fatalf("submit response 2: %d %s", rrResp2.Code, rrResp2.Body.String())
	}

	// Passive: 7
	score7 := 7
	post(t, app, fmt.Sprintf("/api/v1/surveys/%d/responses", npsSurvey.ID), p.APIKey, map[string]any{
		"user_id":       "usr_3",
		"score":         score7,
		"response_text": "Good, but needs more integrations.",
	})

	// Detractor: 3
	score3 := 3
	post(t, app, fmt.Sprintf("/api/v1/surveys/%d/responses", npsSurvey.ID), p.APIKey, map[string]any{
		"user_id":       "usr_4",
		"score":         score3,
		"response_text": "Checkout failed twice on mobile.",
	})

	// 8. Verify NPS Results:
	// 4 responses: 2 Promoters (50%), 1 Passive (25%), 1 Detractor (25%) -> NPS = 50 - 25 = +25.0
	var results store.SurveyResults
	rrResults := get(t, app, fmt.Sprintf("/api/v1/projects/%d/surveys/%d/results", projID, npsSurvey.ID), &results)
	if rrResults.Code != 200 {
		t.Fatalf("GET survey results: %d %s", rrResults.Code, rrResults.Body.String())
	}
	if results.TotalResponses != 4 {
		t.Fatalf("expected 4 total responses, got %d", results.TotalResponses)
	}
	if results.PromotersCount != 2 || results.PassivesCount != 1 || results.DetractorsCount != 1 {
		t.Fatalf("unexpected breakdown: promoters=%d passives=%d detractors=%d",
			results.PromotersCount, results.PassivesCount, results.DetractorsCount)
	}
	if results.NPSScore == nil || *results.NPSScore != 25.0 {
		t.Fatalf("expected NPS 25.0, got %v", results.NPSScore)
	}
	if len(results.Distribution) != 11 {
		t.Fatalf("expected 11 score buckets (0..10), got %d", len(results.Distribution))
	}

	// 9. List responses
	var responses []*store.SurveyResponse
	rrResponses := get(t, app, fmt.Sprintf("/api/v1/projects/%d/surveys/%d/responses", projID, npsSurvey.ID), &responses)
	if rrResponses.Code != 200 {
		t.Fatalf("GET survey responses: %d %s", rrResponses.Code, rrResponses.Body.String())
	}
	if len(responses) != 4 {
		t.Fatalf("expected 4 responses, got %d", len(responses))
	}

	// 10. Delete survey
	rrDel := do(t, app, "DELETE", fmt.Sprintf("/api/v1/projects/%d/surveys/%d", projID, csatSurvey.ID), "", nil)
	if rrDel.Code != 204 {
		t.Fatalf("DELETE survey: expected 204, got %d", rrDel.Code)
	}
}
