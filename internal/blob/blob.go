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
