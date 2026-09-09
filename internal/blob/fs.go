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
)

// FS keeps frames as files under a directory. This is the default: a single
// container with a volume needs nothing else, and the layout on disk is the same
// `<session>/<seq>.png` it has always been, so an existing data directory keeps
// working untouched.
type FS struct{ root string }

func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &FS{root: root}, nil
}

func (f *FS) Kind() string { return "fs" }
func (f *FS) Close() error { return nil }

// path resolves a key under the root and refuses to escape it. Keys are built
// from session ids that the store already validated, but a traversal check costs
// nothing and this is the one place a bad id would reach the filesystem.
func (f *FS) path(key string) (string, error) {
	p := filepath.Join(f.root, filepath.FromSlash(filepath.Clean("/"+key)))
	if !strings.HasPrefix(p, filepath.Clean(f.root)+string(os.PathSeparator)) {
		return "", errors.New("blob: key escapes the root")
	}
	return p, nil
}

func (f *FS) Put(_ context.Context, key string, data []byte) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func (f *FS) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, 0, err
	}
	file, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	st, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, st.Size(), nil
}

// DeletePrefix removes a whole session directory. A prefix here is always
// `<session>/`, so this is one RemoveAll rather than a walk.
func (f *FS) DeletePrefix(_ context.Context, prefix string) error {
	p, err := f.path(strings.TrimSuffix(prefix, "/"))
	if err != nil {
		return err
	}
	return os.RemoveAll(p)
}
