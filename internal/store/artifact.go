// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"sightpane/internal/apierr"
	"sightpane/internal/blob"
	"sightpane/internal/symbol"
)

// --- Release artifacts ---
//
// The source map of a release build. The file goes to the blob store beside the
// replay frames and this table indexes it, which is the same split as the frames
// and for the same reason: a 30 MB map has no business in a row.

type ReleaseArtifact struct {
	ID         int64  `json:"id"`
	ProjectID  int64  `json:"project_id"`
	Release    string `json:"release"`
	Filename   string `json:"filename"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	UploadedAt string `json:"uploaded_at"`
}

var (
	ErrReleaseRequired  = apierr.New(400, apierr.CodeReleaseRequired, "release required")
	ErrArtifactName     = apierr.New(400, apierr.CodeArtifactName, "filename must be a source map (.map)")
	ErrArtifactNotFound = apierr.New(404, apierr.CodeArtifactNotFound, "no such artifact")
)

// PutArtifact stores one uploaded file and records it, replacing whatever was
// there for the same release and filename — which is what re-uploading after a
// rebuild has to do.
//
// The object is written before the row, so a failure halfway leaves an
// unreferenced object rather than a row pointing at nothing. The unreferenced
// one is overwritten by the next upload of the same name.
func (s *Store) PutArtifact(ctx context.Context, projectID int64, release, filename string, data []byte) (*ReleaseArtifact, error) {
	release = strings.TrimSpace(release)
	if release == "" {
		return nil, ErrReleaseRequired
	}
	filename = path.Base(strings.TrimSpace(filename))
	if filename == "" || filename == "." || filename == "/" || !strings.HasSuffix(filename, ".map") {
		return nil, ErrArtifactName
	}
	if err := s.blobs.Put(ctx, blob.ArtifactKey(projectID, release, filename), data); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	a := &ReleaseArtifact{
		ProjectID: projectID,
		Release:   release,
		Filename:  filename,
		SHA256:    hex.EncodeToString(sum[:]),
		Size:      int64(len(data)),
	}
	now := time.Now().UTC()
	err := s.db.QueryRow(`INSERT INTO release_artifacts(project_id, release, filename, sha256, size, uploaded_at)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(project_id, release, filename) DO UPDATE SET sha256=excluded.sha256, size=excluded.size, uploaded_at=excluded.uploaded_at
		RETURNING id`, projectID, release, filename, a.SHA256, a.Size, now).Scan(&a.ID)
	if err != nil {
		return nil, err
	}
	a.UploadedAt = now.Format(time.RFC3339Nano)
	// A map that was already parsed for this release is now stale.
	s.symbols.Forget(projectID, release)
	return a, nil
}

// ListArtifacts returns what has been uploaded for a project, newest first.
// An empty [release] lists every release.
func (s *Store) ListArtifacts(projectID int64, release string) ([]ReleaseArtifact, error) {
	q := `SELECT id, project_id, release, filename, sha256, size, uploaded_at FROM release_artifacts WHERE project_id=$1`
	args := []any{projectID}
	if release = strings.TrimSpace(release); release != "" {
		q += ` AND release=$2`
		args = append(args, release)
	}
	rows, err := s.db.Query(q+` ORDER BY uploaded_at DESC, filename LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReleaseArtifact{}
	for rows.Next() {
		var a ReleaseArtifact
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Release, &a.Filename, &a.SHA256, &a.Size, tsCol{&a.UploadedAt}); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteArtifact removes the row and then the object. The row goes first: an
// object nothing points at is invisible, while a row pointing at a deleted
// object would be a 500 on the next error of that release.
func (s *Store) DeleteArtifact(ctx context.Context, projectID int64, release, filename string) error {
	res, err := s.db.Exec(`DELETE FROM release_artifacts WHERE project_id=$1 AND release=$2 AND filename=$3`,
		projectID, release, filename)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrArtifactNotFound
	}
	s.symbols.Forget(projectID, release)
	return s.blobs.DeletePrefix(ctx, blob.ArtifactKey(projectID, release, filename))
}

// SourceMap implements symbol.Loader. It reports symbol.ErrNoMap when the
// project never uploaded that file, which is the ordinary case and not a
// failure — the row is checked first so a project with no maps costs one
// indexed lookup rather than a round trip to the object store.
func (s *Store) SourceMap(ctx context.Context, projectID int64, release, filename string) ([]byte, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM release_artifacts WHERE project_id=$1 AND release=$2 AND filename=$3`,
		projectID, release, filename).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, symbol.ErrNoMap
	}
	rc, _, err := s.blobs.Get(ctx, blob.ArtifactKey(projectID, release, filename))
	if errors.Is(err, blob.ErrNotFound) {
		return nil, symbol.ErrNoMap
	}
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// ArtifactProject returns the project an upload is aimed at, mirroring
// SessionProject: the HTTP layer checks membership against it.
func (s *Store) ArtifactProject(id int64) (int64, error) {
	var pid int64
	if err := s.db.QueryRow(`SELECT project_id FROM release_artifacts WHERE id=$1`, id).Scan(&pid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrArtifactNotFound
		}
		return 0, err
	}
	return pid, nil
}
