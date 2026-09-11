// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package symbol turns the stack frames of a minified release build back into
// source locations.
//
// A release web build ships one `main.dart.js` and a `main.dart.js.map` beside
// it. The SDK cannot resolve its own frames — the map is megabytes and lives on
// the server — so it sends what the browser gave it (`main.dart.js` line 4321,
// column 19) and this package maps that to `lib/features/cashier.dart:120`.
//
// The maps are large and read on the hot ingest path, so they are parsed once
// and kept in a size-bounded cache. Nothing here touches HTTP or SQL: the
// bytes come from a [Loader], which the store implements over the blob store.
package symbol

import (
	"container/list"
	"context"
	"errors"
	"path"
	"strings"
	"sync"

	"github.com/go-sourcemap/sourcemap"
)

// ErrNoMap means no map has been uploaded for that release and file. It is the
// ordinary case — most projects never upload one — so callers treat it as "leave
// the frames alone", not as a failure.
var ErrNoMap = errors.New("no source map")

// Loader hands over the bytes of one uploaded map.
type Loader interface {
	SourceMap(ctx context.Context, projectID int64, release, filename string) ([]byte, error)
}

// Frame is one line of a minified stack trace, as the SDK parsed it out of the
// browser's stack. It is the wire shape of the envelope's `frames` field.
type Frame struct {
	// URI is the script the frame is in, e.g. `http://app.example.com/main.dart.js`.
	URI string `json:"uri"`
	// Line and Column are 1-based, as every browser reports them.
	Line   int `json:"line"`
	Column int `json:"column"`
	// Member is the minified function name, kept so an unresolved frame is still
	// worth something.
	Member string `json:"member"`
}

// Resolved is one frame after the map has been applied.
type Resolved struct {
	// File is the original source, e.g. `lib/features/cashier.dart`.
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Function string `json:"function"`
	// Minified is what the frame said before resolution. It stays on the record
	// because a map can be wrong, and comparing the two is how anyone finds out.
	Minified string `json:"minified,omitempty"`
	// Resolved is false when no mapping covered this frame — a frame from a
	// script with no map, or a position the map does not describe. The rest of
	// the fields then carry the minified values, so a consumer can render the
	// list without checking every entry.
	Resolved bool `json:"resolved"`
}

// Cache parses maps once and keeps the parsed consumers until it is over
// [maxBytes], then drops the least recently used.
//
// The bound is on the *source* bytes, not on what parsing them costs: a parsed
// consumer holds more than the JSON it came from, so the real footprint is a
// multiple of the limit. It is a bound, not a budget — the point is that a
// project with forty releases cannot pin all forty maps in memory.
type Cache struct {
	loader   Loader
	maxBytes int64

	mu    sync.Mutex
	bytes int64
	order *list.List               // front = most recently used
	items map[string]*list.Element // key → element holding *entry
}

type entry struct {
	key      string
	consumer *sourcemap.Consumer
	size     int64
	// missing records that there is no map for this key, so a project without
	// maps does not hit the blob store on every single error.
	missing bool
}

// DefaultMaxBytes is what one process keeps in parsed maps. A dart2js map is
// 10–30 MB, so this is a handful of releases.
const DefaultMaxBytes = 128 << 20

func New(loader Loader, maxBytes int64) *Cache {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Cache{
		loader:   loader,
		maxBytes: maxBytes,
		order:    list.New(),
		items:    map[string]*list.Element{},
	}
}

// Resolve maps every frame it can and returns the list in the original order.
// Frames it cannot map come back with Resolved false rather than being dropped:
// a stack with a hole in it is harder to read than one with a minified line.
//
// It returns nil when nothing at all could be resolved, so the caller can tell
// "no map for this release" from "a map that resolved some of it" and store
// nothing in the first case.
func (c *Cache) Resolve(ctx context.Context, projectID int64, release string, frames []Frame) []Resolved {
	if len(frames) == 0 || release == "" {
		return nil
	}
	out := make([]Resolved, 0, len(frames))
	any := false
	for _, f := range frames {
		r := Resolved{
			File:     f.URI,
			Line:     f.Line,
			Column:   f.Column,
			Function: f.Member,
			Minified: minifiedLabel(f),
		}
		if cons := c.consumer(ctx, projectID, release, mapNameFor(f.URI)); cons != nil {
			if src, name, line, col, ok := cons.Source(f.Line, f.Column); ok {
				r.File = cleanSource(src)
				r.Line, r.Column = line, col
				if name != "" {
					r.Function = name
				}
				r.Resolved = true
				any = true
			}
		}
		out = append(out, r)
	}
	if !any {
		return nil
	}
	return out
}

// maxCacheEntries caps the maximum number of distinct entries (including missing ones)
// so negative cache entries cannot grow unbounded in memory.
const maxCacheEntries = 2048

// missingEntryBytes is the nominal size assigned to negative cache entries.
const missingEntryBytes = 1024

// consumer returns the parsed map for one script, or nil when there is none.
func (c *Cache) consumer(ctx context.Context, projectID int64, release, filename string) *sourcemap.Consumer {
	if filename == "" {
		return nil
	}
	key := cacheKey(projectID, release, filename)

	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		e := el.Value.(*entry)
		c.mu.Unlock()
		if e.missing {
			return nil
		}
		return e.consumer
	}
	c.mu.Unlock()

	// Loading happens outside the lock: it is a read from disk or an object
	// store, and holding the mutex across it would serialise every ingest in
	// the process behind one slow fetch. Two goroutines can race to load the
	// same map; the loser's copy is dropped, which is cheaper than the lock.
	e := &entry{key: key}
	raw, err := c.loader.SourceMap(ctx, projectID, release, filename)
	switch {
	case errors.Is(err, ErrNoMap):
		e.missing = true
		e.size = missingEntryBytes
	case err != nil:
		// A store that is down should not be remembered as "no map": leave it
		// uncached so the next error tries again.
		return nil
	default:
		cons, perr := sourcemap.Parse(filename, raw)
		if perr != nil {
			// A map that will not parse is not going to start parsing, so it is
			// cached as missing rather than re-read for every error.
			e.missing = true
			e.size = missingEntryBytes
		} else {
			e.consumer, e.size = cons, int64(len(raw))
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok { // lost the race; keep what is already there
		c.order.MoveToFront(el)
		other := el.Value.(*entry)
		if other.missing {
			return nil
		}
		return other.consumer
	}
	c.items[key] = c.order.PushFront(e)
	c.bytes += e.size
	c.evict()
	if e.missing {
		return nil
	}
	return e.consumer
}

// evict drops least-recently-used entries until the cache is inside its bound.
// The caller holds the lock.
func (c *Cache) evict() {
	for (c.bytes > c.maxBytes || c.order.Len() > maxCacheEntries) && c.order.Len() > 1 {
		back := c.order.Back()
		e := back.Value.(*entry)
		c.order.Remove(back)
		delete(c.items, e.key)
		c.bytes -= e.size
	}
}

// Forget drops every cached map of one release, so a re-uploaded map takes
// effect without restarting the process.
func (c *Cache) Forget(projectID int64, release string) {
	prefix := cacheKey(projectID, release, "")
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, el := range c.items {
		if strings.HasPrefix(key, prefix) {
			c.order.Remove(el)
			delete(c.items, key)
			c.bytes -= el.Value.(*entry).size
		}
	}
}

func cacheKey(projectID int64, release, filename string) string {
	return itoa(projectID) + "\x00" + release + "\x00" + filename
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i, neg := len(b), n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// mapNameFor turns the script a frame came from into the map filename to look
// up: `http://host/assets/main.dart.js?v=3` → `main.dart.js.map`. A frame from
// something that is not a script — `<anonymous>`, a `blob:` URL — has no map.
func mapNameFor(uri string) string {
	if uri == "" {
		return ""
	}
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}
	base := path.Base(uri)
	if base == "" || base == "." || base == "/" || !strings.HasSuffix(base, ".js") {
		return ""
	}
	return base + ".map"
}

// cleanSource makes a map's `sources` entry readable. dart2js writes app code as
// `org-dartlang-app:///lib/main.dart` and package code as
// `package:flutter/src/widgets/framework.dart`; the second is already what a
// debug stack trace says, and the first is shortened to the path inside the
// project.
func cleanSource(src string) string {
	for _, p := range []string{"org-dartlang-app:///", "org-dartlang-sdk:///"} {
		if strings.HasPrefix(src, p) {
			return strings.TrimPrefix(src, p)
		}
	}
	// A map written relative to its own directory says `../../lib/main.dart`.
	// path.Clean keeps leading `..` — it cannot know they are meaningless here —
	// so they come off first.
	for strings.HasPrefix(src, "../") {
		src = src[3:]
	}
	src = strings.TrimPrefix(src, "./")
	return path.Clean(src)
}

// minifiedLabel is what the frame said before resolution, in the shape a Dart
// stack trace line has, so the dashboard can show the two side by side.
func minifiedLabel(f Frame) string {
	loc := f.URI
	if f.Line > 0 {
		loc += ":" + itoa(int64(f.Line))
		if f.Column > 0 {
			loc += ":" + itoa(int64(f.Column))
		}
	}
	if f.Member == "" {
		return loc
	}
	return f.Member + " (" + loc + ")"
}
