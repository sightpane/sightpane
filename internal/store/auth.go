// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"sightpane/internal/apierr"
)

const (
	pbkdfIter   = 120000
	tokenTTL    = 30 * 24 * time.Hour
	minPassword = 6
)

// HashPassword derives the password with pbkdf2-sha256 and a random salt. The
// iteration count and the salt travel inside the stored value —
// "pbkdf2$iter$salt$hash" — so raising pbkdfIter later still verifies old rows.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdfIter, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2$%d$%s$%s", pbkdfIter, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func newToken() (plain, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	plain = hex.EncodeToString(b)
	return plain, hashToken(plain)
}

func hashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

type User struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Locale    string `json:"locale"`
	CreatedAt string `json:"created_at"`
}

// The languages the dashboard is translated into. An empty `locale` means the
// user never picked one, and the dashboard then works down its own order
// (query parameter → local storage → browser → tr).
var SupportedLocales = []string{"tr", "en"}

func localeSupported(l string) bool {
	for _, s := range SupportedLocales {
		if s == l {
			return true
		}
	}
	return false
}

// Known failures carry their status and code from the place that detects them,
// and the HTTP layer passes both through untouched, so a caller sees the same
// code whichever handler happened to run into the failure.
var (
	ErrEmailTaken       = apierr.New(409, apierr.CodeEmailTaken, "email already registered")
	ErrInvalidEmail     = apierr.New(400, apierr.CodeInvalidEmail, "valid email required")
	ErrPasswordTooShort = apierr.Newf(400, apierr.CodePasswordTooShort, "password must be at least %d characters", minPassword)
	ErrBadCredentials   = apierr.New(401, apierr.CodeInvalidCredentials, "invalid email or password")
	ErrBadLocale        = apierr.Newf(400, apierr.CodeUnsupportedLocale, "locale must be one of %s", strings.Join(SupportedLocales, ", "))
)

func (s *Store) CreateUser(email, name, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return nil, ErrInvalidEmail
	}
	if len(password) < minPassword {
		return nil, ErrPasswordTooShort
	}
	h, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var id int64
	if err := s.db.QueryRow(`INSERT INTO users(email, name, password_hash, created_at) VALUES($1,$2,$3,$4) RETURNING id`,
		email, strings.TrimSpace(name), h, now).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	return &User{ID: id, Email: email, Name: strings.TrimSpace(name), CreatedAt: now.Format(time.RFC3339Nano)}, nil
}

func (s *Store) UserByEmail(email string) (*User, string, error) {
	var u User
	var hash string
	err := s.db.QueryRow(`SELECT id, email, name, locale, password_hash, created_at FROM users WHERE email=$1`, strings.ToLower(strings.TrimSpace(email))).Scan(&u.ID, &u.Email, &u.Name, &u.Locale, &hash, tsCol{&u.CreatedAt})
	if err != nil {
		return nil, "", ErrNotFound
	}
	return &u, hash, nil
}

func (s *Store) UserByID(id int64) (*User, error) {
	var u User
	err := s.db.QueryRow(`SELECT id, email, name, locale, created_at FROM users WHERE id=$1`, id).Scan(&u.ID, &u.Email, &u.Name, &u.Locale, tsCol{&u.CreatedAt})
	if err != nil {
		return nil, ErrNotFound
	}
	return &u, nil
}

// Login issues a fresh session token when the password checks out. A wrong
// password and an unknown email return the same error on purpose, so the reply
// does not reveal which addresses have an account.
func (s *Store) Login(email, password string) (string, *User, error) {
	u, hash, err := s.UserByEmail(email)
	if err != nil || !VerifyPassword(hash, password) {
		return "", nil, ErrBadCredentials
	}
	tok, err := s.IssueToken(u.ID)
	return tok, u, err
}

func (s *Store) IssueToken(userID int64) (string, error) {
	plain, hash := newToken()
	now := time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO auth_tokens(token_hash, user_id, created_at, expires_at) VALUES($1,$2,$3,$4)`,
		hash, userID, now, now.Add(tokenTTL))
	return plain, err
}

func (s *Store) UserByToken(token string) (*User, error) {
	var userID int64
	var expires time.Time
	err := s.db.QueryRow(`SELECT user_id, expires_at FROM auth_tokens WHERE token_hash=$1`, hashToken(token)).Scan(&userID, &expires)
	if err != nil || time.Now().After(expires) {
		return nil, ErrNotFound
	}
	return s.UserByID(userID)
}

func (s *Store) RevokeToken(token string) error {
	_, err := s.db.Exec(`DELETE FROM auth_tokens WHERE token_hash=$1`, hashToken(token))
	return err
}

// SetLocale records the user's dashboard language. The empty string means "no
// choice" and clears whatever was stored, which puts the dashboard back on its
// fallback order — the browser's language rather than a language it once kept.
func (s *Store) SetLocale(userID int64, locale string) error {
	locale = strings.ToLower(strings.TrimSpace(locale))
	if locale != "" && !localeSupported(locale) {
		return ErrBadLocale
	}
	_, err := s.db.Exec(`UPDATE users SET locale=$1 WHERE id=$2`, locale, userID)
	return err
}

// --- Membership ---

type Member struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Role   string `json:"role"`
}

func (s *Store) AddMember(projectID, userID int64, role string) error {
	_, err := s.db.Exec(`INSERT INTO project_members(project_id, user_id, role) VALUES($1,$2,$3) ON CONFLICT(project_id, user_id) DO UPDATE SET role=excluded.role`, projectID, userID, role)
	return err
}

func (s *Store) RemoveMember(projectID, userID int64) error {
	_, err := s.db.Exec(`DELETE FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, userID)
	return err
}

// MemberRole returns "" rather than an error when the user is not a member, so
// a caller can treat "not a member" and "no such project" as the same answer.
func (s *Store) MemberRole(projectID, userID int64) (string, error) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, userID).Scan(&role)
	if err != nil {
		return "", nil
	}
	return role, nil
}

func (s *Store) ListMembers(projectID int64) ([]Member, error) {
	rows, err := s.db.Query(`SELECT u.id, u.email, u.name, m.role FROM project_members m JOIN users u ON u.id=m.user_id WHERE m.project_id=$1 ORDER BY m.role, u.email`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
