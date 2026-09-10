// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"sightpane/internal/testdb"
)

func ingestErr(t *testing.T, st *Store, projectID int64, sessionID, exception, message string) {
	t.Helper()
	var env Envelope
	jsonStr := fmt.Sprintf(`{
		"sdk": {"name": "sp", "version": "1.0"},
		"session": {"id": %q, "started_at": %q},
		"items": [
			{"type": "error", "message": %q, "exception": %q}
		]
	}`, sessionID, time.Now().UTC().Format(time.RFC3339Nano), message, exception)
	if err := json.Unmarshal([]byte(jsonStr), &env); err != nil {
		t.Fatal(err)
	}
	_, err := st.Ingest(context.Background(), projectID, &env, "127.0.0.1")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
}

func TestIssueWorkflowAndSnooze(t *testing.T) {
	st, err := Open(Options{
		DSN:     testdb.DSN(t),
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	admin, err := st.CreateUser("admin@sightpane.local", "Admin", "pass123")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := st.CreateUser("dev@sightpane.local", "Developer", "pass123")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject("Workflow Proj", "web", "key_wf", &admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Ingest an error -> creates an open issue
	ingestErr(t, st, proj.ID, "s1", "OOMError", "out of memory")

	issues, err := st.ListIssues(proj.ID, false)
	if err != nil || len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	iss := issues[0]
	if iss.Status != "open" || iss.Resolved {
		t.Fatalf("expected open issue, got status=%s resolved=%v", iss.Status, iss.Resolved)
	}

	// 2. Assign issue to dev
	if err := st.AssignIssue(iss.ID, &dev.ID); err != nil {
		t.Fatalf("AssignIssue: %v", err)
	}
	d, err := st.GetIssue(iss.ID)
	if err != nil || d.AssigneeEmail != dev.Email {
		t.Fatalf("expected assignee %s, got %+v", dev.Email, d)
	}

	// 3. Add comment
	comment, err := st.AddIssueComment(iss.ID, dev.ID, "Investigating this crash.")
	if err != nil || comment.Body != "Investigating this crash." {
		t.Fatalf("AddIssueComment: %v", err)
	}
	comments, err := st.ListIssueComments(iss.ID)
	if err != nil || len(comments) != 1 || comments[0].UserEmail != dev.Email {
		t.Fatalf("ListIssueComments: %v, got %+v", err, comments)
	}

	// 4. Snooze issue: threshold of 2 occurrences
	if err := st.SnoozeIssue(iss.ID, nil, 2); err != nil {
		t.Fatalf("SnoozeIssue: %v", err)
	}
	d, _ = st.GetIssue(iss.ID)
	if d.Status != "snoozed" {
		t.Fatalf("expected status snoozed, got %s", d.Status)
	}

	// ListIssues without includeResolved: snoozed is not open -> should not be in default open list!
	issues, _ = st.ListIssues(proj.ID, false)
	if len(issues) != 0 {
		t.Fatalf("snoozed issue should not appear in default open list, got %d", len(issues))
	}

	// Ingest occurrence 1 since snooze (total 2) -> (2 - 1) = 1 < 2 threshold -> stays snoozed
	ingestErr(t, st, proj.ID, "s2", "OOMError", "out of memory")
	d, _ = st.GetIssue(iss.ID)
	if d.Status != "snoozed" || d.Count != 2 {
		t.Fatalf("expected snoozed with count 2, got status=%s count=%d", d.Status, d.Count)
	}

	// Ingest occurrence 2 since snooze (total 3) -> (3 - 1) = 2 >= 2 threshold -> WAKES UP to open!
	ingestErr(t, st, proj.ID, "s3", "OOMError", "out of memory")
	d, _ = st.GetIssue(iss.ID)
	if d.Status != "open" || d.Count != 3 {
		t.Fatalf("expected woken to open with count 3, got status=%s count=%d", d.Status, d.Count)
	}

	// 5. Test Ignored issue
	if err := st.SetIssueStatus(iss.ID, "ignored"); err != nil {
		t.Fatalf("SetIssueStatus ignored: %v", err)
	}
	// Occurrence continues to count, but issue stays ignored
	ingestErr(t, st, proj.ID, "s4", "OOMError", "out of memory")
	d, _ = st.GetIssue(iss.ID)
	if d.Status != "ignored" || d.Count != 4 {
		t.Fatalf("expected ignored with count 4, got status=%s count=%d", d.Status, d.Count)
	}

	// 6. Test Merge issue
	ingestErr(t, st, proj.ID, "s5", "OOMError2", "out of memory variant")
	issues, _ = st.ListIssues(proj.ID, false)
	if len(issues) != 1 {
		t.Fatalf("expected 1 open issue for variant, got %d", len(issues))
	}
	variantID := issues[0].ID
	if err := st.MergeIssue(variantID, iss.ID); err != nil {
		t.Fatalf("MergeIssue: %v", err)
	}
	d, _ = st.GetIssue(iss.ID)
	if d.Count != 5 { // 4 + 1
		t.Fatalf("expected merged count 5, got %d", d.Count)
	}
}

func TestFingerprintRules(t *testing.T) {
	st, err := Open(Options{
		DSN:     testdb.DSN(t),
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	admin, err := st.CreateUser("owner@sightpane.local", "Owner", "pass123")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject("Rules Proj", "web", "key_rules", &admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Create rule: ignore framework warning "RenderBox was not laid out"
	rule1, err := st.CreateFingerprintRule(ProjectFingerprintRule{
		ProjectID:   proj.ID,
		MessageGlob: "*RenderBox was not laid out*",
		Action:      "ignore",
		Priority:    10,
	})
	if err != nil {
		t.Fatalf("CreateFingerprintRule: %v", err)
	}

	// Create rule: group all database timeouts under a single group
	rule2, err := st.CreateFingerprintRule(ProjectFingerprintRule{
		ProjectID:        proj.ID,
		ExceptionMatch:   "DBTimeout",
		Action:           "group_as",
		GroupFingerprint: "db_timeout_common_group",
		Priority:         5,
	})
	if err != nil {
		t.Fatalf("CreateFingerprintRule: %v", err)
	}

	rules, err := st.ListFingerprintRules(proj.ID)
	if err != nil || len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	// Ingest error matching ignore rule
	ingestErr(t, st, proj.ID, "s1", "FlutterError", "A RenderBox was not laid out: RenderFlex")

	// Ignored issue must not appear in open issues list
	openIssues, _ := st.ListIssues(proj.ID, false)
	if len(openIssues) != 0 {
		t.Fatalf("expected 0 open issues for ignored warning, got %d", len(openIssues))
	}

	// Query with status:ignored
	ignoredIssues, err := st.ListIssuesWithFilter(IssueFilter{ProjectID: proj.ID, Status: "ignored"})
	if err != nil || len(ignoredIssues) != 1 {
		t.Fatalf("expected 1 ignored issue, got %d", len(ignoredIssues))
	}

	// Ingest two errors with different messages but both having DBTimeout exception
	ingestErr(t, st, proj.ID, "s2", "DBTimeoutException", "query 1 timeout")
	ingestErr(t, st, proj.ID, "s3", "DBTimeoutException", "query 2 timeout")

	// Both should be grouped into group_fingerprint "db_timeout_common_group" -> single issue with count 2!
	openIssues, _ = st.ListIssues(proj.ID, false)
	if len(openIssues) != 1 {
		t.Fatalf("expected 1 grouped issue, got %d: %+v", len(openIssues), openIssues)
	}
	if openIssues[0].Fingerprint != "db_timeout_common_group" || openIssues[0].Count != 2 {
		t.Fatalf("expected fp db_timeout_common_group with count 2, got %+v", openIssues[0])
	}

	// Delete rule
	if err := st.DeleteFingerprintRule(proj.ID, rule1.ID); err != nil {
		t.Fatalf("DeleteFingerprintRule: %v", err)
	}
	rules, _ = st.ListFingerprintRules(proj.ID)
	if len(rules) != 1 || rules[0].ID != rule2.ID {
		t.Fatalf("expected rule2 remaining, got %+v", rules)
	}
}
