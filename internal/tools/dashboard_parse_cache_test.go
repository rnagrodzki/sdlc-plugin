package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/attention"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// resetDashboardParseCache drops every key of the dashboard parse cache.
// dashRoot calls it in t.Cleanup. A test that rewrites a cached file with the
// same size also calls it first: two writes in one modification time tick
// give the same signature.
func resetDashboardParseCache() {
	dashboardParseCache.Lock()
	defer dashboardParseCache.Unlock()
	clear(dashboardParseCache.entries)
}

// dashCacheKeys returns the sorted keys that the parse cache stores now.
func dashCacheKeys() []string {
	dashboardParseCache.Lock()
	defer dashboardParseCache.Unlock()
	keys := make([]string, 0, len(dashboardParseCache.entries))
	for k := range dashboardParseCache.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// dashCacheFile writes content to a new file in dir and returns its path.
func dashCacheFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// dashCacheOverwrite writes content to path and sets its modification time to
// mtime. With the size and the time of an earlier write, the file keeps the
// signature of that write.
func dashCacheOverwrite(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// dashCacheReadBuild returns a build function that reads path and counts its
// calls in *calls.
func dashCacheReadBuild(path string, calls *int) func() (string, error) {
	return func() (string, error) {
		*calls++
		b, err := os.ReadFile(path)
		return string(b), err
	}
}

func TestDashboardParseCache_HitSkipsBuild(t *testing.T) {
	t.Cleanup(resetDashboardParseCache)
	dir := t.TempDir()
	p := dashCacheFile(t, dir, "a.log", "one")
	mtime := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	calls := 0
	build := dashCacheReadBuild(p, &calls)
	for i := 0; i < 3; i++ {
		got, err := dashboardCached(dir+"|a", []string{p}, build)
		if err != nil || got != "one" {
			t.Fatalf("call %d = (%q, %v), want (\"one\", nil)", i, got, err)
		}
	}
	if calls != 1 {
		t.Errorf("build calls = %d, want 1", calls)
	}

	// The same size and time with other content shows the stored value: the
	// signature is the only input of the hit.
	dashCacheOverwrite(t, p, "two", mtime)
	if got, _ := dashboardCached(dir+"|a", []string{p}, build); got != "one" {
		t.Errorf("same size and time = %q, want the stored \"one\"", got)
	}
	if calls != 1 {
		t.Errorf("build calls after same-signature rewrite = %d, want 1", calls)
	}
}

func TestDashboardParseCache_ChangedFileBuildsOnlyItsKey(t *testing.T) {
	t.Cleanup(resetDashboardParseCache)
	dir := t.TempDir()
	mtime := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	a := dashCacheFile(t, dir, "a.log", "aaa")
	b := dashCacheFile(t, dir, "b.log", "bbb")
	for _, p := range []string{a, b} {
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	var callsA, callsB int
	buildA, buildB := dashCacheReadBuild(a, &callsA), dashCacheReadBuild(b, &callsB)
	read := func() (string, string) {
		ga, _ := dashboardCached(dir+"|a", []string{a}, buildA)
		gb, _ := dashboardCached(dir+"|b", []string{b}, buildB)
		return ga, gb
	}
	if ga, gb := read(); ga != "aaa" || gb != "bbb" {
		t.Fatalf("first read = (%q, %q)", ga, gb)
	}

	t.Run("new size builds that key again", func(t *testing.T) {
		dashCacheOverwrite(t, a, "aaaa", mtime)
		dashCacheOverwrite(t, b, "BBB", mtime) // same size and time: stays stored
		ga, gb := read()
		if ga != "aaaa" || gb != "bbb" {
			t.Errorf("read = (%q, %q), want (\"aaaa\", \"bbb\")", ga, gb)
		}
		if callsA != 2 || callsB != 1 {
			t.Errorf("build calls = (%d, %d), want (2, 1)", callsA, callsB)
		}
	})

	t.Run("new time builds that key again", func(t *testing.T) {
		dashCacheOverwrite(t, b, "BBB", mtime.Add(time.Second)) // same size, later time
		ga, gb := read()
		if ga != "aaaa" || gb != "BBB" {
			t.Errorf("read = (%q, %q), want (\"aaaa\", \"BBB\")", ga, gb)
		}
		if callsA != 2 || callsB != 2 {
			t.Errorf("build calls = (%d, %d), want (2, 2)", callsA, callsB)
		}
	})
}

func TestDashboardParseCache_MissingFile(t *testing.T) {
	t.Cleanup(resetDashboardParseCache)
	dir := t.TempDir()
	p := filepath.Join(dir, "later.log")

	calls := 0
	build := func() (string, error) {
		calls++
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return string(b), err
	}

	t.Run("signature has size -1", func(t *testing.T) {
		sigs, total, ok := dashboardFileSigs([]string{p})
		want := []dashboardFileSig{{Path: p, Size: -1}}
		if !ok || total != 0 || !reflect.DeepEqual(sigs, want) {
			t.Errorf("dashboardFileSigs = (%+v, %d, %v), want (%+v, 0, true)", sigs, total, ok, want)
		}
	})

	t.Run("absent file is stored after a build with no error", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			if got, err := dashboardCached(dir+"|late", []string{p}, build); got != "" || err != nil {
				t.Fatalf("call %d = (%q, %v), want (\"\", nil)", i, got, err)
			}
		}
		if calls != 1 {
			t.Errorf("build calls = %d, want 1", calls)
		}
	})

	t.Run("a new file builds the key again", func(t *testing.T) {
		dashCacheFile(t, dir, "later.log", "now here")
		got, err := dashboardCached(dir+"|late", []string{p}, build)
		if got != "now here" || err != nil {
			t.Errorf("got (%q, %v), want (\"now here\", nil)", got, err)
		}
		if calls != 2 {
			t.Errorf("build calls = %d, want 2", calls)
		}
	})
}

func TestDashboardParseCache_ErrorsAreNotStored(t *testing.T) {
	t.Cleanup(resetDashboardParseCache)
	dir := t.TempDir()
	p := dashCacheFile(t, dir, "a.log", "one")
	boom := errors.New("read failed")

	t.Run("build error goes to the caller and the next call builds again", func(t *testing.T) {
		calls := 0
		build := func() (string, error) {
			calls++
			return "partial", boom
		}
		for i := 0; i < 2; i++ {
			got, err := dashboardCached(dir+"|err", []string{p}, build)
			if err != boom || got != "partial" {
				t.Fatalf("call %d = (%q, %v), want (\"partial\", boom)", i, got, err)
			}
		}
		if calls != 2 {
			t.Errorf("build calls = %d, want 2", calls)
		}
		for _, k := range dashCacheKeys() {
			if strings.HasSuffix(k, "|err") {
				t.Errorf("key %q is stored after a build error", k)
			}
		}
	})

	t.Run("stat error other than not-exist runs build and stores nothing", func(t *testing.T) {
		// A path below a regular file gives ENOTDIR from os.Stat.
		below := filepath.Join(p, "child")
		if _, _, ok := dashboardFileSigs([]string{below}); ok {
			t.Skip("os.Stat of a path below a file gives not-exist on this platform")
		}
		calls := 0
		build := func() (string, error) {
			calls++
			return "built", nil
		}
		for i := 0; i < 2; i++ {
			got, err := dashboardCached(dir+"|stat", []string{below}, build)
			if err != nil || got != "built" {
				t.Fatalf("call %d = (%q, %v), want (\"built\", nil)", i, got, err)
			}
		}
		if calls != 2 {
			t.Errorf("build calls = %d, want 2", calls)
		}
	})

	t.Run("a good build after an error is stored", func(t *testing.T) {
		calls := 0
		fail := true
		build := func() (string, error) {
			calls++
			if fail {
				return "", boom
			}
			return "ok", nil
		}
		if _, err := dashboardCached(dir+"|recover", []string{p}, build); err != boom {
			t.Fatalf("first err = %v, want boom", err)
		}
		fail = false
		for i := 0; i < 2; i++ {
			if got, err := dashboardCached(dir+"|recover", []string{p}, build); got != "ok" || err != nil {
				t.Fatalf("call %d = (%q, %v), want (\"ok\", nil)", i, got, err)
			}
		}
		if calls != 2 {
			t.Errorf("build calls = %d, want 2 (one failed, one stored)", calls)
		}
	})
}

func TestDashboardParseCache_SizeLimit(t *testing.T) {
	t.Cleanup(resetDashboardParseCache)
	dir := t.TempDir()

	// sparse creates a file of the given size that holds no data blocks.
	sparse := func(name string, size int64) string {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := f.Truncate(size); err != nil {
			t.Skipf("sparse file of %d bytes not possible: %v", size, err)
		}
		return p
	}
	build := func(calls *int) func() (int, error) {
		return func() (int, error) {
			*calls++
			return *calls, nil
		}
	}

	t.Run("a key of exactly the limit is stored", func(t *testing.T) {
		p := sparse("exact.bin", dashboardCacheMaxBytes)
		calls := 0
		for i := 0; i < 2; i++ {
			if _, err := dashboardCached(dir+"|exact", []string{p}, build(&calls)); err != nil {
				t.Fatal(err)
			}
		}
		if calls != 1 {
			t.Errorf("build calls = %d, want 1", calls)
		}
	})

	t.Run("a key of one byte more is never stored", func(t *testing.T) {
		p := sparse("over.bin", dashboardCacheMaxBytes+1)
		calls := 0
		for i := 0; i < 2; i++ {
			got, err := dashboardCached(dir+"|over", []string{p}, build(&calls))
			if err != nil || got != i+1 {
				t.Fatalf("call %d = (%d, %v), want (%d, nil)", i, got, err, i+1)
			}
		}
		if calls != 2 {
			t.Errorf("build calls = %d, want 2", calls)
		}
		for _, k := range dashCacheKeys() {
			if strings.HasSuffix(k, "|over") {
				t.Errorf("key %q is stored over the limit", k)
			}
		}
	})

	t.Run("the limit counts the total of all paths of a key", func(t *testing.T) {
		a := sparse("half-a.bin", dashboardCacheMaxBytes/2)
		b := sparse("half-b.bin", dashboardCacheMaxBytes/2+1)
		calls := 0
		for i := 0; i < 2; i++ {
			if _, err := dashboardCached(dir+"|sum", []string{a, b}, build(&calls)); err != nil {
				t.Fatal(err)
			}
		}
		if calls != 2 {
			t.Errorf("build calls = %d, want 2", calls)
		}
	})

	t.Run("a key that grows over the limit drops its stored value", func(t *testing.T) {
		p := sparse("grow.bin", 10)
		calls := 0
		if _, err := dashboardCached(dir+"|grow", []string{p}, build(&calls)); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(p, dashboardCacheMaxBytes+1); err != nil {
			t.Fatal(err)
		}
		if _, err := dashboardCached(dir+"|grow", []string{p}, build(&calls)); err != nil {
			t.Fatal(err)
		}
		for _, k := range dashCacheKeys() {
			if strings.HasSuffix(k, "|grow") {
				t.Errorf("key %q still stored after the file passed the limit", k)
			}
		}
	})
}

func TestDashboardParseCache_Prune(t *testing.T) {
	t.Cleanup(resetDashboardParseCache)
	dir := t.TempDir()
	p := dashCacheFile(t, dir, "a.log", "x")
	build := func() (string, error) { return "v", nil }

	// A root with "|" in its name keeps its keys: the root is the part
	// before the last "|".
	roots := []string{"/repo/a", "/repo/b", "/repo/with|bar"}
	for _, r := range roots {
		for _, name := range []string{"sessions", "history"} {
			if _, err := dashboardCached(dashboardCacheKey(r, name), []string{p}, build); err != nil {
				t.Fatal(err)
			}
		}
	}

	pruneDashboardParseCache([]string{"/repo/b", "/repo/with|bar"})
	want := []string{"/repo/b|history", "/repo/b|sessions", "/repo/with|bar|history", "/repo/with|bar|sessions"}
	if got := dashCacheKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("keys after prune = %v, want %v", got, want)
	}

	pruneDashboardParseCache(nil)
	if got := dashCacheKeys(); len(got) != 0 {
		t.Errorf("keys after prune with no root = %v, want none", got)
	}
}

// dashCacheEvidenceLine returns one CLI evidence line.
func dashCacheEvidenceLine(ts, branch, command, sid string) string {
	return fmt.Sprintf(`{"ts":%q,"branch":%q,"command":%q,"sessionId":%q}`, ts, branch, command, sid)
}

// dashCacheRootKeys is the five keys that one collect of root stores.
func dashCacheRootKeys(root string) []string {
	return []string{
		dashboardCacheKey(root, "deferred"),
		dashboardCacheKey(root, "evidence-times"),
		dashboardCacheKey(root, "history"),
		dashboardCacheKey(root, "learnings"),
		dashboardCacheKey(root, "sessions"),
	}
}

func TestDashboardParseCache_SnapshotKeys(t *testing.T) {
	root := dashRoot(t)
	dashCollect(t, root)
	if got, want := dashCacheKeys(), dashCacheRootKeys(root); !reflect.DeepEqual(got, want) {
		t.Errorf("keys after a collect = %v, want %v", got, want)
	}
}

// dashCacheFixture is a repo root with one file for each cached key.
type dashCacheFixture struct {
	root     string
	cli      string
	runs     string
	learning string
	deferred string
	mtime    time.Time
}

// newDashCacheFixture writes a CLI evidence line, a runs.jsonl row, a
// learnings entry and an open deferred item for a new repo root, and fixes
// the modification time of each file to mtime.
func newDashCacheFixture(t *testing.T) dashCacheFixture {
	t.Helper()
	root := dashRoot(t)
	f := dashCacheFixture{
		root:     root,
		cli:      cliEvidencePath(root),
		runs:     history.NewFileWriter(paths.HistoryDir(root)).RunsPath(),
		learning: learningsLogPath(root),
		deferred: history.NewFileWriter(paths.HistoryDir(root)).DeferredPath(),
		mtime:    dashNow.Add(-time.Hour),
	}
	dashWriteJSONL(t, f.cli, dashCacheEvidenceLine("2026-10-07T09:50:00Z", "feat/x", "go test", "s1"))
	dashWriteJSONL(t, f.runs, `{"ts":"2026-10-07T09:00:00Z","skill":"ship","branch":"feat/x","outcome":"success","duration_ms":1000}`)
	if err := os.MkdirAll(filepath.Dir(f.learning), 0o755); err != nil {
		t.Fatal(err)
	}
	f.write(t, f.learning, "# Log\n\n## 2026-10-07 — plan: first lesson\n- detail\n")
	f.write(t, f.deferred, `[{"id":"d1","created":"2026-10-01","source":"s","priority":"high","description":"first item","status":"open"}]`)
	for _, p := range []string{f.cli, f.runs} {
		if err := os.Chtimes(p, f.mtime, f.mtime); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// write writes content to path and sets the modification time of the fixture.
func (f dashCacheFixture) write(t *testing.T, path, content string) {
	t.Helper()
	dashCacheOverwrite(t, path, content, f.mtime)
}

// writeLine writes line and a newline to path, as dashWriteJSONL does, and
// sets the modification time of the fixture.
func (f dashCacheFixture) writeLine(t *testing.T, path, line string) {
	t.Helper()
	f.write(t, path, line+"\n")
}

func TestDashboardParseCache_SnapshotSecondCollectBuildsNothing(t *testing.T) {
	f := newDashCacheFixture(t)

	first := dashCollect(t, f.root)
	if len(first.Sessions) != 1 || len(first.History) != 1 || len(first.Learnings) != 1 || len(first.Deferred) != 1 {
		t.Fatalf("first collect = %d sessions, %d runs, %d learnings, %d deferred, want 1 of each",
			len(first.Sessions), len(first.History), len(first.Learnings), len(first.Deferred))
	}

	// Each file gets other content of the same size and time. A build would
	// read the other content; a cache hit shows the first content.
	f.writeLine(t, f.cli, dashCacheEvidenceLine("2026-10-07T09:50:00Z", "feat/x", "go test", "s9"))
	f.writeLine(t, f.runs, `{"ts":"2026-10-07T09:00:00Z","skill":"ship","branch":"feat/y","outcome":"success","duration_ms":1000}`)
	f.write(t, f.learning, "# Log\n\n## 2026-10-07 — plan: other lesson\n- detail\n")
	f.write(t, f.deferred, `[{"id":"d2","created":"2026-10-01","source":"s","priority":"high","description":"other item","status":"open"}]`)

	second := dashCollect(t, f.root)
	if !reflect.DeepEqual(second.Sessions, first.Sessions) {
		t.Errorf("sessions changed without a new signature: %+v", second.Sessions)
	}
	if !reflect.DeepEqual(second.History, first.History) {
		t.Errorf("history changed without a new signature: %+v", second.History)
	}
	if !reflect.DeepEqual(second.Learnings, first.Learnings) {
		t.Errorf("learnings changed without a new signature: %+v", second.Learnings)
	}
	if !reflect.DeepEqual(second.Deferred, first.Deferred) {
		t.Errorf("deferred changed without a new signature: %+v", second.Deferred)
	}
}

func TestDashboardParseCache_SnapshotBuildsOnlyTheChangedKey(t *testing.T) {
	f := newDashCacheFixture(t)
	first := dashCollect(t, f.root)

	// The runs file gets a new size: only the history key builds again.
	// The other files get content of the same size and time: they stay stored.
	f.writeLine(t, f.runs, `{"ts":"2026-10-07T09:00:00Z","skill":"ship","branch":"feat/y","outcome":"success","duration_ms":10000}`)
	f.writeLine(t, f.cli, dashCacheEvidenceLine("2026-10-07T09:50:00Z", "feat/x", "go test", "s9"))
	f.write(t, f.deferred, `[{"id":"d2","created":"2026-10-01","source":"s","priority":"high","description":"other item","status":"open"}]`)

	second := dashCollect(t, f.root)
	if len(second.History) != 1 || second.History[0].Branch != "feat/y" {
		t.Errorf("history = %+v, want the new row on feat/y", second.History)
	}
	if !reflect.DeepEqual(second.Sessions, first.Sessions) {
		t.Errorf("sessions built again: %+v", second.Sessions)
	}
	if !reflect.DeepEqual(second.Deferred, first.Deferred) {
		t.Errorf("deferred built again: %+v", second.Deferred)
	}
}

func TestDashboardParseCache_SnapshotAppendShowsInNextCollect(t *testing.T) {
	f := newDashCacheFixture(t)
	if got := dashCollect(t, f.root); len(got.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(got.Sessions))
	}

	dashWriteJSONL(t, f.cli,
		dashCacheEvidenceLine("2026-10-07T09:50:00Z", "feat/x", "go test", "s1"),
		dashCacheEvidenceLine("2026-10-07T09:55:00Z", "feat/x", "go vet", "s2"),
	)
	got := dashCollect(t, f.root)
	if len(got.Sessions) != 2 {
		t.Errorf("sessions = %d, want 2 after an append", len(got.Sessions))
	}
}

func TestDashboardParseCache_SessionActiveUsesNowOfEachCall(t *testing.T) {
	f := newDashCacheFixture(t) // newest evidence line: 09:50:00Z

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"10 minutes after the line", dashNow, true},
		{"1 second before the 30 minute limit", time.Date(2026, 10, 7, 10, 19, 59, 0, time.UTC), true},
		{"exactly the 30 minute limit", time.Date(2026, 10, 7, 10, 20, 0, 0, time.UTC), false},
		{"hours after the line", dashNow.Add(5 * time.Hour), false},
		{"back to 10 minutes after the line", dashNow, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessions := dashboardSessionsFromEvidence(f.root, tc.now)
			if len(sessions) != 1 || sessions[0].Active != tc.want {
				t.Errorf("sessions = %+v, want one with Active %v", sessions, tc.want)
			}
		})
	}

	t.Run("the cached sessions keep Active unset", func(t *testing.T) {
		dashboardParseCache.Lock()
		defer dashboardParseCache.Unlock()
		e, ok := dashboardParseCache.entries[dashboardCacheKey(f.root, "sessions")]
		if !ok {
			t.Fatal("sessions key is not stored")
		}
		for _, s := range e.value.([]DashboardSession) {
			if s.Active {
				t.Errorf("cached session %s has Active set", s.ID)
			}
		}
	})
}

func TestDashboardParseCache_LearningsWindowUsesNowOfEachCall(t *testing.T) {
	f := newDashCacheFixture(t) // one entry dated 2026-10-07

	cases := []struct {
		name string
		now  time.Time
		want int
	}{
		{"same day", dashNow, 1},
		{"next day", dashNow.Add(24 * time.Hour), 1},
		{"two days later", dashNow.Add(48 * time.Hour), 0},
		{"same day again", dashNow, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dashboardRecentLearnings(f.root, tc.now); len(got) != tc.want {
				t.Errorf("learnings = %+v, want %d", got, tc.want)
			}
		})
	}
}

// dashSetAttentionTimes sets the modification time of every wait record of
// root to mtime.
func dashSetAttentionTimes(t *testing.T, root string, mtime time.Time) {
	t.Helper()
	entries, err := os.ReadDir(attention.Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(attention.Dir(root), e.Name()), mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDashboardParseCache_StateAndWaitChangesShowWithSameSizeAndTime(t *testing.T) {
	root := dashRoot(t)
	mtime := dashNow.Add(-time.Minute)
	state := func(branch string) map[string]any {
		return map[string]any{"branch": branch, "steps": dashSteps(StepCompleted, StepInProgress)}
	}

	dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", state("feat/aaa"), mtime)
	dashWriteAttention(t, root, dashQuestionRecord("s1", "tu1", "feat/aaa", "first question", dashNow.Add(-5*time.Minute)))
	dashSetAttentionTimes(t, root, mtime)
	first := dashCollect(t, root)
	if len(first.Pipelines) != 1 || first.Pipelines[0].Branch != "feat/aaa" {
		t.Fatalf("first pipelines = %+v, want one on feat/aaa", first.Pipelines)
	}
	if len(first.LoneWaits) != 1 || first.LoneWaits[0].Text != "first question" {
		t.Fatalf("first lone waits = %+v, want one \"first question\"", first.LoneWaits)
	}

	// The state file and the wait record get other content of the same size
	// and time.
	dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", state("feat/bbb"), mtime)
	dashWriteAttention(t, root, dashQuestionRecord("s1", "tu1", "feat/aaa", "other question", dashNow.Add(-5*time.Minute)))
	dashSetAttentionTimes(t, root, mtime)
	second := dashCollect(t, root)
	if len(second.Pipelines) != 1 || second.Pipelines[0].Branch != "feat/bbb" {
		t.Errorf("second pipelines = %+v, want one on feat/bbb", second.Pipelines)
	}
	if len(second.LoneWaits) != 1 || second.LoneWaits[0].Text != "other question" {
		t.Errorf("second lone waits = %+v, want one \"other question\"", second.LoneWaits)
	}
}

func TestDashboardParseCache_LinkedWorktreeSecondCollectBuildsNothing(t *testing.T) {
	main := newDashCacheFixture(t)
	linked := t.TempDir()
	if err := os.MkdirAll(filepath.Join(linked, paths.DataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(main.root, paths.DataDir, paths.RunsSubdir), filepath.Join(linked, paths.DataDir, paths.RunsSubdir)); err != nil {
		t.Fatal(err)
	}

	first := CollectDashboardSnapshot([]string{linked}, dashNow, "test")
	if len(first.Repos) != 1 || len(first.Repos[0].History) != 1 {
		t.Fatalf("first collect = %+v, want one repo with one run", first.Repos)
	}

	main.writeLine(t, main.runs, `{"ts":"2026-10-07T09:00:00Z","skill":"ship","branch":"feat/y","outcome":"success","duration_ms":1000}`)
	second := CollectDashboardSnapshot([]string{linked}, dashNow, "test")
	if len(second.Repos) != 1 || !reflect.DeepEqual(second.Repos[0].History, first.Repos[0].History) {
		t.Errorf("history of the linked collect changed without a new signature: %+v", second.Repos)
	}
}

func TestDashboardParseCache_CollectWithoutRootDropsItsKeys(t *testing.T) {
	a, b := newDashCacheFixture(t), newDashCacheFixture(t)
	CollectDashboardSnapshot([]string{a.root, b.root}, dashNow, "test")
	if got, want := dashCacheKeys(), append(dashCacheRootKeys(a.root), dashCacheRootKeys(b.root)...); len(got) != len(want) {
		t.Fatalf("keys after two roots = %v, want %d keys", got, len(want))
	}

	CollectDashboardSnapshot([]string{b.root}, dashNow, "test")
	if got, want := dashCacheKeys(), dashCacheRootKeys(b.root); !reflect.DeepEqual(got, want) {
		t.Errorf("keys after a collect of b only = %v, want %v", got, want)
	}

	CollectDashboardSnapshot(nil, dashNow, "test")
	if got := dashCacheKeys(); len(got) != 0 {
		t.Errorf("keys after a collect with no root = %v, want none", got)
	}
}

func TestDashboardParseCache_ConcurrentCollects(t *testing.T) {
	f := newDashCacheFixture(t)
	want := dashCollect(t, f.root)

	var wg sync.WaitGroup
	results := make([]DashboardRepo, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap := CollectDashboardSnapshot([]string{f.root}, dashNow, "test")
			if len(snap.Repos) == 1 {
				results[i] = snap.Repos[0]
			}
		}()
	}
	wg.Wait()

	for i, got := range results {
		if !reflect.DeepEqual(got.Sessions, want.Sessions) || !reflect.DeepEqual(got.History, want.History) ||
			!reflect.DeepEqual(got.Learnings, want.Learnings) || !reflect.DeepEqual(got.Deferred, want.Deferred) {
			t.Errorf("collect %d differs from the first collect: %+v", i, got)
		}
	}
}

func TestDashboardParseCache_ReadErrorIsNotStored(t *testing.T) {
	f := newDashCacheFixture(t)
	// A directory in place of the runs file: os.Stat works, the read fails.
	if err := os.Remove(f.runs); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.runs, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := dashCollect(t, f.root); len(got.History) != 0 {
		t.Errorf("history = %+v, want none while runs.jsonl is a folder", got.History)
	}
	for _, k := range dashCacheKeys() {
		if k == dashboardCacheKey(f.root, "history") {
			t.Errorf("key %q is stored after a read error", k)
		}
	}

	// The next collect builds again and shows the file once it is readable.
	if err := os.Remove(f.runs); err != nil {
		t.Fatal(err)
	}
	f.writeLine(t, f.runs, `{"ts":"2026-10-07T09:00:00Z","skill":"ship","branch":"feat/x","outcome":"success","duration_ms":1000}`)
	if got := dashCollect(t, f.root); len(got.History) != 1 {
		t.Errorf("history = %+v, want 1 row after the file is readable", got.History)
	}
}
