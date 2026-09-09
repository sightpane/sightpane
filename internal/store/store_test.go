// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("pa55word")
	if err != nil || !strings.HasPrefix(h, "pbkdf2$") {
		t.Fatalf("hash: %v %s", err, h)
	}
	if !VerifyPassword(h, "pa55word") || VerifyPassword(h, "other") || VerifyPassword("garbage", "x") {
		t.Fatal("verify")
	}
}

func TestFingerprint(t *testing.T) {
	a, ta := Fingerprint("StateError", "Bad state: x", "#0 A.b (package:app/a.dart:1:1)\n#1 main (package:app/main.dart:5:5)")
	b, _ := Fingerprint("StateError", "Bad state: x", "#0 A.b (package:app/a.dart:9:9)\n#1 main (package:app/main.dart:7:7)")
	c, _ := Fingerprint("StateError", "Bad state: x", "#0 A.c (package:app/a.dart:1:1)")
	if a != b {
		t.Fatalf("line numbers must not change the fingerprint")
	}
	if a == c {
		t.Fatalf("different frames must differ")
	}
	if ta != "StateError: Bad state: x" {
		t.Fatalf("title %q", ta)
	}
	// The SDK's own frames never identify an error, so they are skipped under
	// both of its names. Dropping the old prefix would regroup every issue that
	// was recorded before the project was renamed.
	for _, pkg := range []string{"package:sightpane/", "package:flutter_hog/"} {
		withSDK, _ := Fingerprint("StateError", "Bad state: x",
			"#0 send ("+pkg+"queue.dart:9:9)\n#1 A.b (package:app/a.dart:1:1)\n#2 main (package:app/main.dart:5:5)")
		if withSDK != a {
			t.Fatalf("%s frames must not change the fingerprint", pkg)
		}
	}
	// With no stack trace to key on, the numbers inside the message are
	// normalised away, so the same failure about two different ids groups.
	d, _ := Fingerprint("Message", "Customer 42 not found", "")
	e, _ := Fingerprint("Message", "Customer 77 not found", "")
	if d != e {
		t.Fatalf("numbers in message must be normalized")
	}
	// A stack trace made up of framework frames only carries nothing app-specific,
	// so it has to fall back to the message as well.
	f, _ := Fingerprint("E", "m 1", "#0 x (package:flutter/src/a.dart:1:1)")
	g, _ := Fingerprint("E", "m 2", "#0 y (package:flutter/src/b.dart:2:2)")
	if f != g {
		t.Fatalf("framework-only stacks should group by message")
	}
}

// An older database is called hog.db and has no users.locale column. Open() has
// to find it by its old name and migrate() has to add the column through the
// ALTER list — adding a new column to CREATE TABLE alone would break every
// existing install, and only this test would notice.
func TestMigrateAddsLocaleToExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	// Pre-0a schema: no locale column.
	old, err := sql.Open("sqlite", filepath.Join(dir, "hog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  created_at TEXT NOT NULL
);
INSERT INTO users(email, name, password_hash, created_at) VALUES('eski@x.io','Eski','pbkdf2$1$a$b','2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	st, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("opening an old database: %v", err)
	}
	defer st.Close()
	u, _, err := st.UserByEmail("eski@x.io")
	if err != nil {
		t.Fatalf("reading an existing user: %v", err)
	}
	if u.Locale != "" {
		t.Fatalf("an existing row must start with no language: %q", u.Locale)
	}
	if err := st.SetLocale(u.ID, "en"); err != nil {
		t.Fatalf("writing the language: %v", err)
	}
	if again, _ := st.UserByID(u.ID); again.Locale != "en" {
		t.Fatalf("the language did not persist: %+v", again)
	}
}
