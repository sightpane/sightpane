// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package symbol

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// --- A source map, built rather than pasted ---
//
// A real dart2js map is 20 MB of base64 VLQ, so the fixtures here are written
// out by the same encoding the format uses. It is twenty lines and it means the
// test says what it is testing instead of carrying an opaque blob.

const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// vlq encodes values the way the mappings field does: sign in the low bit, five
// bits per character, the sixth bit meaning "another character follows".
func vlq(vs ...int) string {
	var b strings.Builder
	for _, v := range vs {
		u := v << 1
		if v < 0 {
			u = (-v << 1) | 1
		}
		for {
			d := u & 31
			u >>= 5
			if u > 0 {
				d |= 32
			}
			b.WriteByte(b64[d])
			if u == 0 {
				break
			}
		}
	}
	return b.String()
}

// seg is one mapping: at generated column [genCol], the code comes from
// [source] line [line] column [col], in function [name]. All indexes are
// absolute here; segments are encoded as deltas below.
type seg struct{ genCol, source, line, col, name int }

// buildMap writes a map whose generated line i+1 carries lines[i].
func buildMap(file string, sources, names []string, lines [][]seg) []byte {
	var mappings []string
	// Every field except the generated column is a delta from the previous
	// segment across the whole file, which is what makes a real map compact.
	var prev seg
	for _, segs := range lines {
		var parts []string
		lastCol := 0
		for _, s := range segs {
			parts = append(parts, vlq(
				s.genCol-lastCol,
				s.source-prev.source,
				s.line-prev.line,
				s.col-prev.col,
				s.name-prev.name,
			))
			lastCol = s.genCol
			prev = s
		}
		mappings = append(mappings, strings.Join(parts, ","))
	}
	quoted := func(ss []string) string {
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = fmt.Sprintf("%q", s)
		}
		return "[" + strings.Join(out, ",") + "]"
	}
	return fmt.Appendf(nil, `{"version":3,"file":%q,"sources":%s,"names":%s,"mappings":%q}`,
		file, quoted(sources), quoted(names), strings.Join(mappings, ";"))
}

// appMap: generated line 1 is app code, line 2 is inside Flutter.
func appMap() []byte {
	return buildMap("main.dart.js",
		[]string{"org-dartlang-app:///lib/cashier.dart", "package:flutter/src/widgets/framework.dart"},
		[]string{"openTill", "build"},
		[][]seg{
			{{genCol: 0, source: 0, line: 119, col: 4, name: 0}},
			{{genCol: 0, source: 1, line: 4000, col: 2, name: 1}},
		})
}

// loader hands out fixed maps and counts how often it was asked, which is how
// the cache is tested.
type loader struct {
	mu    sync.Mutex
	maps  map[string][]byte
	calls int
}

func (l *loader) SourceMap(_ context.Context, projectID int64, release, filename string) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	b, ok := l.maps[fmt.Sprintf("%d/%s/%s", projectID, release, filename)]
	if !ok {
		return nil, ErrNoMap
	}
	return b, nil
}

func newLoader(m map[string][]byte) *loader { return &loader{maps: m} }

func TestResolveMapsFramesBackToSource(t *testing.T) {
	l := newLoader(map[string][]byte{"1/1.0.0/main.dart.js.map": appMap()})
	c := New(l, 0)

	got := c.Resolve(context.Background(), 1, "1.0.0", []Frame{
		{URI: "https://app.example.com/main.dart.js", Line: 1, Column: 1, Member: "aI.$2"},
		{URI: "https://app.example.com/main.dart.js", Line: 2, Column: 1, Member: "bZ.$0"},
		// A frame from something with no map at all: it survives unresolved
		// rather than being dropped, because a stack with a hole is harder to
		// read than one with a minified line in it.
		{URI: "https://app.example.com/flutter.js", Line: 9, Column: 3, Member: "x"},
	})
	if len(got) != 3 {
		t.Fatalf("want 3 frames back, got %d: %+v", len(got), got)
	}
	if got[0].File != "lib/cashier.dart" || got[0].Line != 120 || got[0].Function != "openTill" || !got[0].Resolved {
		t.Fatalf("app frame: %+v", got[0])
	}
	// The minified original is kept: a map can be wrong, and comparing the two
	// is how anyone finds that out.
	if got[0].Minified != "aI.$2 (https://app.example.com/main.dart.js:1:1)" {
		t.Fatalf("minified label: %q", got[0].Minified)
	}
	if got[1].File != "package:flutter/src/widgets/framework.dart" || got[1].Line != 4001 {
		t.Fatalf("framework frame: %+v", got[1])
	}
	if got[2].Resolved || got[2].File != "https://app.example.com/flutter.js" {
		t.Fatalf("a frame with no map must come back unresolved: %+v", got[2])
	}
}

// Nothing resolved means nothing to store: the caller writes no symbolicated
// column at all, rather than a list of frames that only repeat the stack.
func TestResolveReturnsNothingWithoutAMap(t *testing.T) {
	c := New(newLoader(nil), 0)
	if got := c.Resolve(context.Background(), 1, "1.0.0", []Frame{
		{URI: "https://x/main.dart.js", Line: 1, Column: 1},
	}); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
	// No release means no map can be looked up; this is the common case for a
	// native build and must not cost a lookup.
	if got := c.Resolve(context.Background(), 1, "", []Frame{{URI: "https://x/main.dart.js", Line: 1}}); got != nil {
		t.Fatalf("want nil for an empty release, got %+v", got)
	}
}

// Ingest resolves on the hot path, so a map is parsed once and a project with
// no maps must not hit the store on every error.
func TestCacheParsesOnceAndRemembersAbsence(t *testing.T) {
	l := newLoader(map[string][]byte{"1/1.0.0/main.dart.js.map": appMap()})
	c := New(l, 0)
	frame := []Frame{{URI: "https://x/main.dart.js", Line: 1, Column: 1}}

	for i := 0; i < 5; i++ {
		c.Resolve(context.Background(), 1, "1.0.0", frame)
	}
	if l.calls != 1 {
		t.Fatalf("map loaded %d times, want 1", l.calls)
	}
	for i := 0; i < 5; i++ {
		c.Resolve(context.Background(), 2, "9.9.9", frame) // no map for this one
	}
	if l.calls != 2 {
		t.Fatalf("absence looked up %d times, want it remembered after 1", l.calls-1)
	}

	// A re-uploaded map has to take effect without restarting the process.
	c.Forget(1, "1.0.0")
	c.Resolve(context.Background(), 1, "1.0.0", frame)
	if l.calls != 3 {
		t.Fatalf("Forget did not drop the parsed map: %d loads", l.calls)
	}
}

// The bound is what keeps a project with forty releases from pinning forty maps
// in memory.
func TestCacheEvictsTheLeastRecentlyUsed(t *testing.T) {
	m := appMap()
	l := newLoader(map[string][]byte{
		"1/a/main.dart.js.map": m,
		"1/b/main.dart.js.map": m,
	})
	// Room for one map and not two.
	c := New(l, int64(len(m)))
	frame := []Frame{{URI: "https://x/main.dart.js", Line: 1, Column: 1}}

	c.Resolve(context.Background(), 1, "a", frame)
	c.Resolve(context.Background(), 1, "b", frame)
	if l.calls != 2 {
		t.Fatalf("setup: %d loads", l.calls)
	}
	// "a" should be gone now, so asking for it again reloads.
	c.Resolve(context.Background(), 1, "a", frame)
	if l.calls != 3 {
		t.Fatalf("the older map was not evicted: %d loads", l.calls)
	}
}

func TestMapNameFor(t *testing.T) {
	for in, want := range map[string]string{
		"https://app.example.com/main.dart.js":     "main.dart.js.map",
		"https://app.example.com/a/b/main.dart.js": "main.dart.js.map",
		"https://app.example.com/main.dart.js?v=3": "main.dart.js.map",
		"https://app.example.com/main.dart.js#x":   "main.dart.js.map",
		"main.dart.js":                             "main.dart.js.map",
		"":                                         "",
		"<anonymous>":                              "",
		"https://app.example.com/style.css":        "",
		"blob:https://app.example.com/1234-5678":   "",
	} {
		if got := mapNameFor(in); got != want {
			t.Fatalf("mapNameFor(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCleanSource(t *testing.T) {
	for in, want := range map[string]string{
		"org-dartlang-app:///lib/main.dart":          "lib/main.dart",
		"org-dartlang-sdk:///lib/core/errors.dart":   "lib/core/errors.dart",
		"package:flutter/src/widgets/framework.dart": "package:flutter/src/widgets/framework.dart",
		"../../lib/main.dart":                        "lib/main.dart",
		"lib/main.dart":                              "lib/main.dart",
	} {
		if got := cleanSource(in); got != want {
			t.Fatalf("cleanSource(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCacheEvictsMissingEntries(t *testing.T) {
	l := newLoader(nil) // no maps at all
	// Cap byte budget so 2 missing entries (at 1024 bytes each) fit, but a 3rd evicts the 1st
	c := New(l, 2*missingEntryBytes)
	frame := []Frame{{URI: "https://x/main.dart.js", Line: 1, Column: 1}}

	c.Resolve(context.Background(), 1, "rel1", frame)
	c.Resolve(context.Background(), 1, "rel2", frame)
	if l.calls != 2 {
		t.Fatalf("setup: %d loads, want 2", l.calls)
	}
	// rel3 should cause rel1 to be evicted
	c.Resolve(context.Background(), 1, "rel3", frame)
	if l.calls != 3 {
		t.Fatalf("rel3 load: %d loads, want 3", l.calls)
	}
	// rel1 should now reload since it was evicted
	c.Resolve(context.Background(), 1, "rel1", frame)
	if l.calls != 4 {
		t.Fatalf("rel1 was not evicted: %d loads, want 4", l.calls)
	}
}

