// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"sightpane/internal/apierr"
)

type Org struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role,omitempty"`
	CreatedAt string `json:"created_at"`
}

type OrgMember struct {
	OrgID     int64  `json:"org_id"`
	UserID    int64  `json:"user_id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

type AuditLogEntry struct {
	ID        int64   `json:"id"`
	OrgID     int64   `json:"org_id"`
	ProjectID *int64  `json:"project_id,omitempty"`
	UserID    *int64  `json:"user_id,omitempty"`
	UserEmail *string `json:"user_email,omitempty"`
	UserName  *string `json:"user_name,omitempty"`
	Action    string  `json:"action"`
	Target    string  `json:"target"`
	IP        string  `json:"ip"`
	CreatedAt string  `json:"created_at"`
}

type APIToken struct {
	ID          int64      `json:"id"`
	OrgID       int64      `json:"org_id"`
	ProjectID   *int64     `json:"project_id,omitempty"`
	Name        string     `json:"name"`
	TokenHash   string     `json:"-"`
	TokenPrefix string     `json:"token_prefix"`
	Scopes      []string   `json:"scopes"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedBy   *int64     `json:"created_by,omitempty"`
	CreatedAt   string     `json:"created_at"`
}

func (s *Store) CreateOrg(name string, ownerID int64) (*Org, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apierr.New(400, "org_name_required", "organization name required")
	}
	now := time.Now().UTC()
	var id int64
	err := s.db.QueryRow(`INSERT INTO orgs(name, created_at) VALUES($1, $2) RETURNING id`, name, now).Scan(&id)
	if err != nil {
		return nil, err
	}
	if ownerID > 0 {
		_, err = s.db.Exec(`INSERT INTO org_members(org_id, user_id, role, created_at) VALUES($1, $2, 'owner', $3)`, id, ownerID, now)
		if err != nil {
			return nil, err
		}
	}
	return &Org{
		ID:        id,
		Name:      name,
		Role:      "owner",
		CreatedAt: now.Format(time.RFC3339Nano),
	}, nil
}

func (s *Store) GetOrg(orgID int64) (*Org, error) {
	var o Org
	err := s.db.QueryRow(`SELECT id, name, created_at FROM orgs WHERE id=$1`, orgID).Scan(&o.ID, &o.Name, tsCol{&o.CreatedAt})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) ListOrgsForUser(userID int64) ([]Org, error) {
	rows, err := s.db.Query(`
		SELECT o.id, o.name, m.role, o.created_at
		FROM orgs o
		JOIN org_members m ON m.org_id = o.id
		WHERE m.user_id = $1
		ORDER BY o.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Org
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.Name, &o.Role, tsCol{&o.CreatedAt}); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) EnsureUserDefaultOrg(userID int64, defaultName string) (*Org, error) {
	orgs, err := s.ListOrgsForUser(userID)
	if err != nil {
		return nil, err
	}
	for _, o := range orgs {
		if o.Role == "owner" || o.Role == "admin" {
			return &o, nil
		}
	}
	if len(orgs) > 0 {
		return &orgs[0], nil
	}
	if strings.TrimSpace(defaultName) == "" {
		defaultName = "Default Organization"
	}
	return s.CreateOrg(defaultName, userID)
}

func (s *Store) UpdateOrg(orgID int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return apierr.New(400, "org_name_required", "organization name required")
	}
	res, err := s.db.Exec(`UPDATE orgs SET name=$1 WHERE id=$2`, name, orgID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteOrg(orgID int64) error {
	res, err := s.db.Exec(`DELETE FROM orgs WHERE id=$1`, orgID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Org Members ---

func (s *Store) AddOrgMember(orgID, userID int64, role string) error {
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "owner" && role != "admin" && role != "member" && role != "viewer" {
		role = "member"
	}
	now := time.Now().UTC()
	_, err := s.db.Exec(`
		INSERT INTO org_members(org_id, user_id, role, created_at)
		VALUES($1, $2, $3, $4)
		ON CONFLICT(org_id, user_id) DO UPDATE SET role = EXCLUDED.role
	`, orgID, userID, role, now)
	return err
}

func (s *Store) UpdateOrgMemberRole(orgID, userID int64, role string) error {
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "owner" && role != "admin" && role != "member" && role != "viewer" {
		return apierr.New(400, "invalid_role", "role must be owner, admin, member, or viewer")
	}
	res, err := s.db.Exec(`UPDATE org_members SET role=$1 WHERE org_id=$2 AND user_id=$3`, role, orgID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RemoveOrgMember(orgID, userID int64) error {
	res, err := s.db.Exec(`DELETE FROM org_members WHERE org_id=$1 AND user_id=$2`, orgID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListOrgMembers(orgID int64) ([]OrgMember, error) {
	rows, err := s.db.Query(`
		SELECT m.org_id, m.user_id, u.email, u.name, m.role, m.created_at
		FROM org_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.org_id = $1
		ORDER BY 
			CASE m.role
				WHEN 'owner' THEN 1
				WHEN 'admin' THEN 2
				WHEN 'member' THEN 3
				WHEN 'viewer' THEN 4
				ELSE 5
			END,
			u.email
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OrgMember
	for rows.Next() {
		var m OrgMember
		if err := rows.Scan(&m.OrgID, &m.UserID, &m.Email, &m.Name, &m.Role, tsCol{&m.CreatedAt}); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) OrgMemberRole(orgID, userID int64) (string, error) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM org_members WHERE org_id=$1 AND user_id=$2`, orgID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

// --- Audit Log ---

func (s *Store) CreateAuditLog(orgID int64, projectID, userID *int64, action, target, ip string) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`
		INSERT INTO audit_log(org_id, project_id, user_id, action, target, ip, created_at)
		VALUES($1, $2, $3, $4, $5, $6, $7)
	`, orgID, projectID, userID, action, target, ip, now)
	return err
}

func (s *Store) ListAuditLogs(orgID int64, limit, offset int) ([]AuditLogEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(`
		SELECT a.id, a.org_id, a.project_id, a.user_id, u.email, u.name, a.action, a.target, a.ip, a.created_at
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE a.org_id = $1
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $2 OFFSET $3
	`, orgID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditLogEntry
	for rows.Next() {
		var e AuditLogEntry
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ProjectID, &e.UserID, &e.UserEmail, &e.UserName, &e.Action, &e.Target, &e.IP, tsCol{&e.CreatedAt}); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Scoped API Tokens ---

func (s *Store) CreateAPIToken(orgID int64, projectID, userID *int64, name string, scopes []string, expiresAt *time.Time) (string, *APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "API Token"
	}
	if len(scopes) == 0 {
		scopes = []string{"read"}
	}
	rawSecretBytes := make([]byte, 24)
	if _, err := rand.Read(rawSecretBytes); err != nil {
		return "", nil, err
	}
	secret := "sp_" + hex.EncodeToString(rawSecretBytes)
	hash := hashToken(secret)
	prefix := secret[:7] + "..."

	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		return "", nil, err
	}

	now := time.Now().UTC()
	var id int64
	err = s.db.QueryRow(`
		INSERT INTO api_tokens(org_id, project_id, name, token_hash, token_prefix, scopes, expires_at, created_by, created_at)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, orgID, projectID, name, hash, prefix, string(scopesJSON), expiresAt, userID, now).Scan(&id)
	if err != nil {
		return "", nil, err
	}

	return secret, &APIToken{
		ID:          id,
		OrgID:       orgID,
		ProjectID:   projectID,
		Name:        name,
		TokenPrefix: prefix,
		Scopes:      scopes,
		ExpiresAt:   expiresAt,
		CreatedBy:   userID,
		CreatedAt:   now.Format(time.RFC3339Nano),
	}, nil
}

func (s *Store) ListAPITokens(orgID int64) ([]APIToken, error) {
	rows, err := s.db.Query(`
		SELECT id, org_id, project_id, name, token_prefix, scopes, expires_at, created_by, created_at
		FROM api_tokens
		WHERE org_id = $1
		ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []APIToken
	for rows.Next() {
		var t APIToken
		var scopesRaw string
		if err := rows.Scan(&t.ID, &t.OrgID, &t.ProjectID, &t.Name, &t.TokenPrefix, &scopesRaw, &t.ExpiresAt, &t.CreatedBy, tsCol{&t.CreatedAt}); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopesRaw), &t.Scopes)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAPIToken(orgID, tokenID int64) error {
	res, err := s.db.Exec(`DELETE FROM api_tokens WHERE org_id=$1 AND id=$2`, orgID, tokenID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) APITokenBySecret(secret string) (*APIToken, error) {
	hash := hashToken(secret)
	var t APIToken
	var scopesRaw string
	err := s.db.QueryRow(`
		SELECT id, org_id, project_id, name, token_prefix, scopes, expires_at, created_by, created_at
		FROM api_tokens
		WHERE token_hash = $1
	`, hash).Scan(&t.ID, &t.OrgID, &t.ProjectID, &t.Name, &t.TokenPrefix, &scopesRaw, &t.ExpiresAt, &t.CreatedBy, tsCol{&t.CreatedAt})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopesRaw), &t.Scopes)
	return &t, nil
}
