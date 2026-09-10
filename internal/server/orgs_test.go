package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestViewerRoleReadOnly(t *testing.T) {
	app, st := newTestServer(t)

	// Create owner and project
	ownerUser, err := st.CreateUser("owner@test.io", "Owner", "password123")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg("Test Org", ownerUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProjectWithOrg("Viewer Proj", "flutter", "vkey1", &ownerUser.ID, &org.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Create viewer user and add to org as viewer
	viewerUser, err := st.CreateUser("viewer@test.io", "Viewer", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(org.ID, viewerUser.ID, "viewer"); err != nil {
		t.Fatal(err)
	}

	vtok, _, err := st.Login("viewer@test.io", "password123")
	if err != nil {
		t.Fatal(err)
	}

	doReq := func(method, path string, body []byte) *http.Response {
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rdr)
		req.Header.Set("Authorization", "Bearer "+vtok)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// 1. GET endpoints should succeed (200 OK)
	readEndpoints := []string{
		fmt.Sprintf("/api/v1/projects/%d", p.ID),
		fmt.Sprintf("/api/v1/projects/%d/sessions", p.ID),
		fmt.Sprintf("/api/v1/projects/%d/issues", p.ID),
		fmt.Sprintf("/api/v1/projects/%d/stats", p.ID),
		fmt.Sprintf("/api/v1/projects/%d/members", p.ID),
		fmt.Sprintf("/api/v1/projects/%d/releases", p.ID),
		fmt.Sprintf("/api/v1/projects/%d/performance", p.ID),
	}
	for _, ep := range readEndpoints {
		resp := doReq("GET", ep, nil)
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200 for viewer GET %s, got %d: %s", ep, resp.StatusCode, string(b))
		}
	}

	// 2. Write endpoints must return 403 Forbidden
	patchBody, _ := json.Marshal(map[string]any{"name": "Renamed"})
	writeCases := []struct {
		method string
		path   string
		body   []byte
	}{
		{"PATCH", fmt.Sprintf("/api/v1/projects/%d", p.ID), patchBody},
		{"DELETE", fmt.Sprintf("/api/v1/projects/%d", p.ID), nil},
		{"POST", fmt.Sprintf("/api/v1/projects/%d/rotate-key", p.ID), nil},
		{"POST", fmt.Sprintf("/api/v1/projects/%d/members", p.ID), []byte(`{"email":"other@x.io","role":"member"}`)},
		{"DELETE", fmt.Sprintf("/api/v1/projects/%d/users/user1", p.ID), nil},
		{"POST", fmt.Sprintf("/api/v1/projects/%d/fingerprint-rules", p.ID), []byte(`{"pattern":"*","fingerprint":"fp"}`)},
		{"POST", fmt.Sprintf("/api/v1/projects/%d/alert-channels", p.ID), []byte(`{"name":"ops","kind":"email","target":"a@b.com"}`)},
		{"POST", fmt.Sprintf("/api/v1/projects/%d/alerts", p.ID), []byte(`{"name":"r1","kind":"new_issue"}`)},
	}
	for _, tc := range writeCases {
		resp := doReq(tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusForbidden {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403 for viewer %s %s, got %d: %s", tc.method, tc.path, resp.StatusCode, string(b))
		}
	}
}

func TestAuditLogKeyRotationAndProjectDeletion(t *testing.T) {
	app, st := newTestServer(t)

	ownerUser, err := st.CreateUser("auditor@test.io", "Auditor", "password123")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg("Audit Org", ownerUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProjectWithOrg("Audit Proj", "flutter", "akey1", &ownerUser.ID, &org.ID)
	if err != nil {
		t.Fatal(err)
	}

	tok, _, err := st.Login("auditor@test.io", "password123")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Rotate key with IP
	rotReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%d/rotate-key", p.ID), nil)
	rotReq.Header.Set("Authorization", "Bearer "+tok)
	rotReq.Header.Set("X-Forwarded-For", "203.0.113.42")
	resp, err := app.Test(rotReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for rotate-key, got %d: %s", resp.StatusCode, string(b))
	}

	// 2. Check audit log has key.rotate
	auditReq := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/orgs/%d/audit", org.ID), nil)
	auditReq.Header.Set("Authorization", "Bearer "+tok)
	resp, err = app.Test(auditReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for audit log, got %d: %s", resp.StatusCode, string(b))
	}
	var logs []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&logs)

	foundRotate := false
	for _, l := range logs {
		if l["action"] == "key.rotate" && l["ip"] == "203.0.113.42" {
			foundRotate = true
			break
		}
	}
	if !foundRotate {
		t.Fatalf("expected key.rotate in audit log, got %+v", logs)
	}

	// 3. Delete project with IP
	delReq := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/projects/%d", p.ID), nil)
	delReq.Header.Set("Authorization", "Bearer "+tok)
	delReq.Header.Set("X-Forwarded-For", "203.0.113.99")
	resp, err = app.Test(delReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for delete project, got %d: %s", resp.StatusCode, string(b))
	}

	// 4. Check audit log has project.delete
	auditReq = httptest.NewRequest("GET", fmt.Sprintf("/api/v1/orgs/%d/audit", org.ID), nil)
	auditReq.Header.Set("Authorization", "Bearer "+tok)
	resp, err = app.Test(auditReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&logs)

	foundDelete := false
	for _, l := range logs {
		if l["action"] == "project.delete" && l["ip"] == "203.0.113.99" {
			foundDelete = true
			break
		}
	}
	if !foundDelete {
		t.Fatalf("expected project.delete in audit log, got %+v", logs)
	}
}

func TestScopedAPITokensSourceMapUploadAndIngest(t *testing.T) {
	app, st := newTestServer(t)

	ownerUser, err := st.CreateUser("ci@test.io", "CI User", "password123")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg("CI Org", ownerUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProjectWithOrg("CI Proj", "flutter", "cikey1", &ownerUser.ID, &org.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Create scoped token with sourcemaps:write
	smSecret, _, err := st.CreateAPIToken(org.ID, &p.ID, &ownerUser.ID, "Sourcemaps Token", []string{"sourcemaps:write"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create scoped token with ingest only
	ingestSecret, _, err := st.CreateAPIToken(org.ID, &p.ID, &ownerUser.ID, "Ingest Token", []string{"ingest"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create dummy sourcemap multipart body
	buildMultipartBody := func() (*bytes.Buffer, string) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, err := writer.CreateFormFile("file", "main.dart.js.map")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte(`{"version":3,"file":"main.dart.js","sources":["main.dart"],"mappings":""}`))
		_ = writer.Close()
		return body, writer.FormDataContentType()
	}

	// 1. Source map upload with sourcemaps:write token should succeed
	body, contentType := buildMultipartBody()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%d/releases/1.0.0/sourcemaps", p.ID), body)
	req.Header.Set("Authorization", "Bearer "+smSecret)
	req.Header.Set("Content-Type", contentType)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200/201 for sourcemap upload with scoped token, got %d: %s", resp.StatusCode, string(b))
	}

	// 2. Source map upload with ingest-only token (out of scope) should return 403
	body2, contentType2 := buildMultipartBody()
	req2 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%d/releases/1.0.0/sourcemaps", p.ID), body2)
	req2.Header.Set("Authorization", "Bearer "+ingestSecret)
	req2.Header.Set("Content-Type", contentType2)
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for out-of-scope sourcemap upload, got %d", resp2.StatusCode)
	}

	// 3. Ingest with ingestSecret token via Authorization header (without X-Sightpane-Key)
	env := map[string]any{
		"session": map[string]any{
			"id":         "s_ci_1",
			"started_at": "2026-03-01T12:00:00Z",
		},
		"items": []any{
			map[string]any{
				"type":    "breadcrumb",
				"ts":      "2026-03-01T12:00:01Z",
				"message": "ingested via scoped token",
			},
		},
	}
	envBytes, _ := json.Marshal(env)
	ingestReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/envelope?project_id=%d", p.ID), bytes.NewReader(envBytes))
	ingestReq.Header.Set("Content-Type", "application/json")
	ingestReq.Header.Set("Authorization", "Bearer "+ingestSecret)
	resp3, err := app.Test(ingestReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp3.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp3.Body)
		t.Fatalf("expected 202 for ingest via scoped token, got %d: %s", resp3.StatusCode, string(b))
	}
}
