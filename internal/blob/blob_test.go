// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both backends have to behave identically, because the rest of the service only
// sees the interface and a difference would surface as a bug that reproduces on
// one deployment and not the other. This suite runs against whichever backend it
// is handed.
func runStoreSuite(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("put then get returns the same bytes and size", func(t *testing.T) {
		key := FrameKey("sess-a", 1)
		want := []byte("\x89PNG\r\n\x1a\nnot really a png")
		if err := s.Put(ctx, key, want); err != nil {
			t.Fatal(err)
		}
		rc, size, err := s.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("bytes differ: %q", got)
		}
		if size != int64(len(want)) {
			t.Fatalf("size = %d, want %d", size, len(want))
		}
	})

	t.Run("put over an existing key replaces it", func(t *testing.T) {
		key := FrameKey("sess-a", 1)
		if err := s.Put(ctx, key, []byte("second")); err != nil {
			t.Fatal(err)
		}
		rc, _, err := s.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		got, _ := io.ReadAll(rc)
		if string(got) != "second" {
			t.Fatalf("got %q", got)
		}
	})

	// The handler turns exactly this error into a 404; anything else is a
	// storage fault and has to stay a 500.
	t.Run("a missing key is ErrNotFound", func(t *testing.T) {
		if _, _, err := s.Get(ctx, FrameKey("sess-a", 999)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("DeletePrefix removes one session and leaves the others", func(t *testing.T) {
		for _, seq := range []int{1, 2, 3} {
			if err := s.Put(ctx, FrameKey("sess-b", seq), []byte("b")); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Put(ctx, FrameKey("sess-c", 1), []byte("c")); err != nil {
			t.Fatal(err)
		}
		if err := s.DeletePrefix(ctx, SessionPrefix("sess-b")); err != nil {
			t.Fatal(err)
		}
		for _, seq := range []int{1, 2, 3} {
			if _, _, err := s.Get(ctx, FrameKey("sess-b", seq)); !errors.Is(err, ErrNotFound) {
				t.Fatalf("sess-b frame %d survived: %v", seq, err)
			}
		}
		rc, _, err := s.Get(ctx, FrameKey("sess-c", 1))
		if err != nil {
			t.Fatalf("sess-c must be untouched: %v", err)
		}
		rc.Close()
	})

	// Deleting a project twice, or one that never recorded anything, must not
	// fail — DeleteProject calls this for every session it found.
	t.Run("deleting an absent prefix is not an error", func(t *testing.T) {
		if err := s.DeletePrefix(ctx, SessionPrefix("sess-never-existed")); err != nil {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestFS(t *testing.T) {
	fs, err := NewFS(filepath.Join(t.TempDir(), "frames"))
	if err != nil {
		t.Fatal(err)
	}
	if fs.Kind() != "fs" {
		t.Fatalf("kind = %q", fs.Kind())
	}
	runStoreSuite(t, fs)
}

// The key is built from a session id, and a session id arrives from the network.
// A traversal must not reach outside the frame directory even if validation
// upstream ever slips. It is neutralised rather than rejected: `../hog.db`
// resolves to a key inside the root, the same way a static file server treats a
// request path.
func TestFSContainsTraversalInsideItsRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "frames")
	fs, err := NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	outside := filepath.Join(filepath.Dir(root), "hog.db")
	if err := os.WriteFile(outside, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fs.Put(ctx, "../hog.db", []byte("clobbered")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "database" {
		t.Fatalf("a file outside the root was written: %q", b)
	}
	if b, err := os.ReadFile(filepath.Join(root, "hog.db")); err != nil || string(b) != "clobbered" {
		t.Fatalf("the write should have landed inside the root: %v %q", err, b)
	}
	// Reading back through the same key stays inside too.
	rc, _, err := fs.Get(ctx, "../hog.db")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()
	if b, _ := io.ReadAll(rc); string(b) != "clobbered" {
		t.Fatalf("read outside the root: %q", b)
	}
}

// The frame list is served from the database, but an object listing has to sort
// the same way for a bulk delete or a manual inspection to make sense.
func TestFrameKeysSortInSequenceOrder(t *testing.T) {
	if a, b := FrameKey("s", 2), FrameKey("s", 10); !(a < b) {
		t.Fatalf("%q should sort before %q", a, b)
	}
	if got := FrameKey("s", 7); got != "s/000007.png" {
		t.Fatalf("FrameKey = %q", got)
	}
	if !strings.HasPrefix(FrameKey("s", 1), SessionPrefix("s")) {
		t.Fatal("a frame key must live under its session prefix")
	}
}

// The S3 path only proves itself against a real server; a mock would re-state
// the client library rather than test it. `docker compose --profile ceph up -d`
// brings up a Ceph RADOS Gateway, and these environment variables point at it.
func TestS3(t *testing.T) {
	endpoint := os.Getenv("SIGHTPANE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set SIGHTPANE_TEST_S3_ENDPOINT (and _ACCESS_KEY/_SECRET_KEY) to run against Ceph or MinIO")
	}
	s, err := NewS3(context.Background(), S3Config{
		Endpoint:  endpoint,
		Bucket:    envOr("SIGHTPANE_TEST_S3_BUCKET", "sightpane-frames-test"),
		AccessKey: os.Getenv("SIGHTPANE_TEST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("SIGHTPANE_TEST_S3_SECRET_KEY"),
		Region:    os.Getenv("SIGHTPANE_TEST_S3_REGION"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Kind() != "s3" {
		t.Fatalf("kind = %q", s.Kind())
	}
	// The bucket is shared between runs, so start from a clean slate.
	ctx := context.Background()
	for _, p := range []string{"sess-a", "sess-b", "sess-c"} {
		_ = s.DeletePrefix(ctx, SessionPrefix(p))
	}
	runStoreSuite(t, s)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
