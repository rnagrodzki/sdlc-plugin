package tools

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
)

// dashboardCacheMaxBytes is the largest total file size of one cache key.
// A larger key is built on each call and never stored.
const dashboardCacheMaxBytes = 32 << 20

// dashboardFileSig is one source file of a cache key. Size is -1 when the file is missing.
type dashboardFileSig struct {
	Path    string
	Size    int64
	ModNano int64
}

// dashboardCacheEntry is the stored value of one cache key with the file
// signatures that the value was built from.
type dashboardCacheEntry struct {
	sigs  []dashboardFileSig
	value any
}

// dashboardParseCache holds the parsed content of the large append-only
// dashboard source files. Its key is "<root>|<name>" (dashboardCacheKey). The
// mutex guards entries, and dashboardCached holds it while it builds a value.
var dashboardParseCache = struct {
	sync.Mutex
	entries map[string]dashboardCacheEntry
}{entries: map[string]dashboardCacheEntry{}}

// dashboardCacheKey returns the cache key of the data set name of the repo at
// root. dashboardCacheKeyRoot reads the root back as the part before the last
// "|", so name must not hold a "|".
func dashboardCacheKey(root, name string) string {
	return root + "|" + name
}

// dashboardCacheKeyRoot returns the root part of a key that dashboardCacheKey
// built. ok is false when key holds no "|".
func dashboardCacheKeyRoot(key string) (root string, ok bool) {
	i := strings.LastIndexByte(key, '|')
	if i < 0 {
		return "", false
	}
	return key[:i], true
}

// dashboardCached returns the cached value of key when the signature of
// files is equal to the stored one. Else it calls build. It stores the
// result only when err is nil. build runs under the cache lock.
//
// build is called, and its value and error are returned unchanged, when
// os.Stat fails for a file with an error other than "not exist", and when the
// total size of files is over dashboardCacheMaxBytes. In both cases the
// result is not stored. A file that does not exist has size -1.
//
// The value is shared between calls. A caller that changes a value of a
// cached slice or map copies it first.
func dashboardCached[T any](key string, files []string, build func() (T, error)) (T, error) {
	sigs, total, ok := dashboardFileSigs(files)

	dashboardParseCache.Lock()
	defer dashboardParseCache.Unlock()

	if ok {
		if e, hit := dashboardParseCache.entries[key]; hit && slices.Equal(e.sigs, sigs) {
			if v, isT := e.value.(T); isT {
				return v, nil
			}
		}
	}
	// A stale value is useless from here on: drop it so a key that is no
	// longer stored does not keep old content in memory.
	delete(dashboardParseCache.entries, key)

	v, err := build()
	if ok && err == nil && total <= dashboardCacheMaxBytes {
		dashboardParseCache.entries[key] = dashboardCacheEntry{sigs: sigs, value: v}
	}
	return v, err
}

// dashboardFileSigs returns the signature of each of files and the sum of
// their sizes. The signatures are taken before the build, so a file that
// changes during the build gives a different signature on the next call and
// the key is built again. ok is false when os.Stat fails for a path with an
// error other than "not exist".
func dashboardFileSigs(files []string) (sigs []dashboardFileSig, total int64, ok bool) {
	sigs = make([]dashboardFileSig, 0, len(files))
	for _, p := range files {
		info, err := os.Stat(p)
		switch {
		case err == nil:
			sigs = append(sigs, dashboardFileSig{Path: p, Size: info.Size(), ModNano: info.ModTime().UnixNano()})
			total += info.Size()
		case errors.Is(err, fs.ErrNotExist):
			sigs = append(sigs, dashboardFileSig{Path: p, Size: -1})
		default:
			return nil, 0, false
		}
	}
	return sigs, total, true
}

// pruneDashboardParseCache drops each key whose root is not in roots, and
// each key that has no root part.
func pruneDashboardParseCache(roots []string) {
	keep := make(map[string]bool, len(roots))
	for _, r := range roots {
		keep[r] = true
	}

	dashboardParseCache.Lock()
	defer dashboardParseCache.Unlock()
	for key := range dashboardParseCache.entries {
		if root, ok := dashboardCacheKeyRoot(key); !ok || !keep[root] {
			delete(dashboardParseCache.entries, key)
		}
	}
}
