// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// This program is free software: you can redistribute it and/or modify it under
// the terms of the GNU Affero General Public License as published by the Free
// Software Foundation, either version 3 of the License, or (at your option) any
// later version. This program is distributed WITHOUT ANY WARRANTY; see the GNU
// Affero General Public License for more details: <https://www.gnu.org/licenses/>.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command sightpane receives errors, events, breadcrumbs and replay frames from
// applications, keeps them in SQLite plus files on disk, and serves the dashboard
// and its API.
//
// This file is wiring only: configuration, storage, the first-run seed, the
// listener and the HTTP app. The parts live in internal/{config,store,server,netx}.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"

	"sightpane/internal/blob"
	"sightpane/internal/config"
	"sightpane/internal/netx"
	"sightpane/internal/server"
	"sightpane/internal/store"
)

// shutdownGrace is how long in-flight requests get to finish on SIGTERM. An
// envelope can carry megabytes of frames, so cutting it short would lose data
// the SDK already considers delivered.
const shutdownGrace = 10 * time.Second

// A placeholder page so a bare binary explains itself. A real dashboard build
// is served from SIGHTPANE_UI_DIR instead. Embedding happens here because go:embed
// can only read the directory tree of the file that declares it.
//
//go:embed ui/*
var uiFS embed.FS

func main() {
	cfg := config.Load()

	frames, err := openFrameStore(cfg)
	if err != nil {
		log.Fatalf("frames: %v", err)
	}

	st, err := store.Open(cfg.DataDir, frames)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	if err := seed(st, cfg); err != nil {
		log.Fatalf("seed: %v", err)
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	if cfg.ProxyProtocol {
		ln = netx.ProxyListener(ln, cfg.TrustedProxies)
	}

	embedded, _ := fs.Sub(uiFS, "ui")
	app := server.New(st, cfg.UIDir, embedded)

	log.Printf("sightpane listening on %s (data %s, frames %s, project %q key %q, admin %s, proxy_protocol=%v)",
		cfg.Addr, cfg.DataDir, st.Frames(), cfg.DefaultProject, cfg.DefaultKey, cfg.AdminEmail, cfg.ProxyProtocol)

	// Serve from another goroutine so the signal handler below can shut the app
	// down instead of the process being killed mid-write. SQLite in WAL mode
	// survives a hard kill, but an ingest transaction that was about to commit
	// does not, and `defer st.Close()` would never run.
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			log.Fatalf("serve: %v", err)
		}
	case sig := <-stop:
		log.Printf("%s received, finishing in-flight requests (up to %s)", sig, shutdownGrace)
		if err := app.ShutdownWithTimeout(shutdownGrace); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
	log.Print("stopped")
}

// openFrameStore picks where replay frames live. The default is a directory
// under SIGHTPANE_DATA, which is all a single container needs. SIGHTPANE_FRAMES=s3
// points at
// an S3-compatible object store — Ceph's RADOS Gateway is the intended one — and
// is what makes more than one backend replica possible, since a local directory
// is not shared between them.
func openFrameStore(cfg config.Config) (blob.Store, error) {
	switch cfg.Frames {
	case "", "fs":
		return blob.NewFS(filepath.Join(cfg.DataDir, "frames"))
	case "s3":
		// A short timeout: reaching the object store is a startup precondition,
		// and hanging here would look like a stuck process.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return blob.NewS3(ctx, blob.S3Config{
			Endpoint:  cfg.S3.Endpoint,
			Bucket:    cfg.S3.Bucket,
			AccessKey: cfg.S3.AccessKey,
			SecretKey: cfg.S3.SecretKey,
			Region:    cfg.S3.Region,
			UseSSL:    cfg.S3.UseSSL,
		})
	default:
		return nil, fmt.Errorf("SIGHTPANE_FRAMES=%q, want fs or s3", cfg.Frames)
	}
}

// seed makes a fresh install usable without a setup step: an admin account to
// sign in with and one project whose key can go straight into Hog.init. Both
// are only created when missing, so restarting never overwrites real data.
func seed(st *store.Store, cfg config.Config) error {
	admin, _, err := st.UserByEmail(cfg.AdminEmail)
	if errors.Is(err, store.ErrNotFound) {
		if admin, err = st.CreateUser(cfg.AdminEmail, "Admin", cfg.AdminPassword); err != nil {
			return err
		}
		log.Printf("created admin user %s", cfg.AdminEmail)
	} else if err != nil {
		return err
	}
	p, err := st.EnsureProject(cfg.DefaultProject, cfg.DefaultKey)
	if err != nil {
		return err
	}
	if role, _ := st.MemberRole(p.ID, admin.ID); role == "" {
		return st.AddMember(p.ID, admin.ID, "owner")
	}
	return nil
}
