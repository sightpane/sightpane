// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"

	"sightpane/internal/symbol"
)

var (
	// The line:column suffix and the "#n" frame prefix of a Dart stack trace
	// line. Both are normalised away so that the same error still groups
	// together after the code around it moved a few lines.
	lineCol   = regexp.MustCompile(`:\d+(:\d+)?\)?$`)
	framePref = regexp.MustCompile(`^#\d+\s+`)
	hexAddr   = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	digits    = regexp.MustCompile(`\d+`)
)

// Fingerprint derives the group key from the exception type plus the first
// three stack frames that belong to application code, and the title from the
// type plus the first line of the message (truncated).
func Fingerprint(exception, message, stack string) (fp string, title string) {
	return fingerprintOf(exception, message, stack, nil)
}

// fingerprintOf is Fingerprint with the frames a source map resolved, when there
// are any. It exists because a minified release stack contains no `package:`
// frame at all: appFrames finds nothing, the key falls back to the message, and
// two builds of the same code land in two groups because the minified names
// moved. Resolved frames name real files, which do not.
//
// One thing this deliberately does not do is make a release group with the debug
// build of the same error. It cannot: a debug stack says `package:myapp/a.dart`
// while a dart2js map says `lib/a.dart`, and the package name is not in the map.
// Matching them would mean keying on the basename, which regroups every issue
// ever recorded and collides two files of the same name in different
// directories — a bigger decision than this function.
func fingerprintOf(exception, message, stack string, resolved []symbol.Resolved) (fp string, title string) {
	frames := resolvedFrames(resolved, 3)
	if len(frames) == 0 {
		frames = appFrames(stack, 3)
	}
	var key string
	if len(frames) > 0 {
		key = exception + "|" + strings.Join(frames, "|")
	} else {
		// With no stack trace to key on, fall back to the message, with numbers
		// and addresses masked so that two reports of the same failure carrying
		// different ids still land in one group.
		key = exception + "|" + digits.ReplaceAllString(hexAddr.ReplaceAllString(firstLine(message), "0x?"), "N")
	}
	sum := sha1.Sum([]byte(key))
	fp = hex.EncodeToString(sum[:])
	title = strings.TrimSpace(exception)
	if m := firstLine(message); m != "" {
		if title == "" {
			title = m
		} else {
			title += ": " + m
		}
	}
	if len(title) > 200 {
		title = title[:197] + "…"
	}
	return fp, title
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// isFrameworkFrame reports whether a stack line belongs to the framework or to
// this SDK rather than to the application. `package:flutter_hog/` is the name the
// SDK had before the rename: stacks recorded then are still in the database, and
// dropping the old prefix would regroup every one of those issues.
func isFrameworkFrame(l string) bool {
	for _, p := range []string{
		"package:flutter/", "package:flutter_test/", "(dart:",
		"package:sightpane/", "package:flutter_hog/",
	} {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// resolvedFrames is appFrames over frames a source map already resolved. The
// strings it builds have the same shape appFrames produces — `member (file` —
// so both paths feed the same key, and only frames that actually resolved are
// used: an unresolved one still carries a minified name that changes with every
// build, which is the thing being fixed.
func resolvedFrames(frames []symbol.Resolved, n int) []string {
	var out []string
	for _, f := range frames {
		if !f.Resolved || f.File == "" {
			continue
		}
		if isFrameworkFrame(f.File) {
			continue
		}
		out = append(out, f.Function+" ("+f.File)
		if len(out) == n {
			break
		}
	}
	return out
}

// appFrames returns the first n stack frames that belong to the application,
// skipping the SDK's own frames and those of the Flutter and Dart core: they
// are the same for every app and would group unrelated errors together.
func appFrames(stack string, n int) []string {
	var out []string
	for _, line := range strings.Split(stack, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || !strings.Contains(l, "(") {
			continue
		}
		if isFrameworkFrame(l) {
			continue
		}
		l = framePref.ReplaceAllString(l, "")
		l = lineCol.ReplaceAllString(l, "")
		out = append(out, l)
		if len(out) == n {
			break
		}
	}
	return out
}
