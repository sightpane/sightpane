// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package blob stores the replay frames.
//
// Frames are the only large, write-once, read-rarely data this service holds: a
// recorded session is tens to hundreds of PNGs, and they are read back only when
// somebody opens the player. Keeping them out of the database keeps it small
// enough to copy around, and putting them behind an interface means the same
// backend can run from a single container's disk or from a Ceph cluster without
// the ingest path knowing which.
//
// Two implementations: [FS] writes files under a directory, [S3] talks to any
// S3-compatible object store — Ceph RADOS Gateway is what this is built for, and
// MinIO or AWS S3 work unchanged.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrNotFound is returned by Get when the key is absent. Callers translate it
// into a 404; every other error is a real failure worth logging.
var ErrNotFound = errors.New("blob not found")

// Store is the whole surface the rest of the service needs. It is deliberately
// small: frames are written once, read as a stream, and deleted a whole session
// at a time when a project goes away.
type Store interface {
	// Put writes one object, overwriting any previous one with the same key.
	Put(ctx context.Context, key string, data []byte) error

	// Get opens an object for reading. The caller closes the reader. The size is
	// returned so the HTTP layer can set Content-Length instead of chunking.
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)

	// DeletePrefix removes every object under a prefix. Used when a project is
	// deleted; a partial failure is reported but must not stop the caller from
	// having already removed the rows.
	DeletePrefix(ctx context.Context, prefix string) error

	// Walk iterates over every object under prefix, calling fn for each key and size.
	// Used for sweeping orphans and frame migrations.
	Walk(ctx context.Context, prefix string, fn func(key string, size int64) error) error

	// Kind names the backend for logs and /health ("fs" or "s3").
	Kind() string

	// Close releases whatever the backend holds open.
	Close() error
}

// FrameKey is the object key for one replay frame. It is also the path layout on
// disk, so an existing `frames/<session>/<seq>.png` tree is readable by the FS
// backend without migration.
func FrameKey(sessionID string, seq int) string {
	return sessionPrefix(sessionID) + pad6(seq) + ".png"
}

// SessionPrefix is every frame of one session, for deletion.
func SessionPrefix(sessionID string) string { return sessionPrefix(sessionID) }

func sessionPrefix(sessionID string) string { return sessionID + "/" }

// pad6 keeps keys sorted lexicographically in the same order as numerically,
// which is what makes a plain object listing usable as a frame list. Sequences
// past six digits simply get longer; ordering only matters within a session and
// no session comes close.
func pad6(seq int) string { return fmt.Sprintf("%06d", seq) }

// --- Release artifacts ---
//
// Source maps share the object store with the frames: one bucket, one directory,
// one set of credentials to configure. They are namespaced under `sourcemaps/`
// while a frame key starts with a session id — 32 hex characters — so the two
// cannot collide and an existing `frames/<session>/<seq>.png` tree is untouched.

// ArtifactKey is the object key for one uploaded file of one release.
func ArtifactKey(projectID int64, release, filename string) string {
	return ReleasePrefix(projectID, release) + safeSegment(filename)
}

// ReleasePrefix is every artifact of one release, for deletion.
func ReleasePrefix(projectID int64, release string) string {
	return fmt.Sprintf("sourcemaps/%d/%s/", projectID, safeSegment(release))
}

// safeSegment keeps a caller-supplied name to one path segment. The FS backend
// already neutralises `..`, but a release named `a/b` would otherwise put its
// files in a directory nothing lists, and an S3 prefix delete would miss them.
func safeSegment(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '-' || r == '_' || r == '+':
			return r
		default:
			return '_'
		}
	}, s)
	// A name of dots only would still address a directory.
	if strings.Trim(s, ".") == "" {
		return "_"
	}
	return s
}
