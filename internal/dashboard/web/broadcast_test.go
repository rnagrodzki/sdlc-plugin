package web

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// countingSource is a collect function of a broadcaster whose repo name the
// test sets. It counts its calls. When gate is not nil, the call whose number
// is gateCall returns only after the test closes gate; the snapshot of that
// call keeps the name that was set when the call started.
type countingSource struct {
	mu       sync.Mutex
	name     string
	calls    int
	gate     chan struct{}
	gateCall int
}

func (c *countingSource) collect() tools.DashboardSnapshot {
	c.mu.Lock()
	c.calls++
	n, name, gate := c.calls, c.name, c.gate
	c.mu.Unlock()
	if gate != nil && n == c.gateCall {
		<-gate
	}
	return tools.DashboardSnapshot{
		GeneratedAt: time.Now().Format(time.RFC3339Nano),
		Version:     testVersion,
		Repos:       []tools.DashboardRepo{{Root: "/repo/a", Name: name}},
	}
}

func (c *countingSource) setName(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.name = name
}

func (c *countingSource) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// replaceHash sets snapshotHash to fn for the test and puts the old value
// back after it.
func replaceHash(t *testing.T, fn func(tools.DashboardSnapshot) (string, error)) {
	t.Helper()
	old := snapshotHash
	snapshotHash = fn
	t.Cleanup(func() { snapshotHash = old })
}

// countHashes wraps stableSnapshotHash in a counter for the test.
func countHashes(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	replaceHash(t, func(s tools.DashboardSnapshot) (string, error) {
		n.Add(1)
		return stableSnapshotHash(s)
	})
	return &n
}

// latestOf returns the latest frame of b and whether its loop runs.
func latestOf(b *broadcaster) (latest *frame, running bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.latest, b.cancel != nil
}

// recvFrame returns the next frame on ch, or fails the test after 2s.
func recvFrame(t *testing.T, ch chan frame) frame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no frame within 2s")
		return frame{}
	}
}

// recvRepo returns the first frame on ch whose data names repo, or fails the
// test after 2s.
func recvRepo(t *testing.T, ch chan frame, repo string) frame {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case f := <-ch:
			if strings.Contains(string(f.data), `"name":"`+repo+`"`) {
				return f
			}
		case <-deadline:
			t.Fatalf("no frame for repo %q within 2s", repo)
			return frame{}
		}
	}
}

// noFrame fails the test when ch holds a frame.
func noFrame(t *testing.T, ch chan frame, what string) {
	t.Helper()
	select {
	case f := <-ch:
		t.Errorf("%s: unexpected frame %s", what, f.data)
	default:
	}
}

func TestBroadcast_TwoSubscribersShareOneCollect(t *testing.T) {
	const interval = 10 * time.Millisecond
	shortTimings(t, interval, time.Hour)
	hashes := countHashes(t)
	src := &countingSource{name: "a"}
	b := newBroadcaster(context.Background(), src.collect)

	start := time.Now()
	ch1, latest1 := b.subscribe()
	ch2, latest2 := b.subscribe()
	if latest1 != nil {
		t.Errorf("first subscribe latest = %s; want nil before the first collect", latest1.data)
	}
	recvFrame(t, ch1)
	waitFor(t, "5 collects", func() bool { return src.callCount() >= 5 })
	b.unsubscribe(ch1)
	b.unsubscribe(ch2)
	elapsed := time.Since(start)

	collects := src.callCount()
	// The loop collects once at its start and once at each timer run; a
	// ticker never runs more often than its interval.
	if limit := int(elapsed/interval) + 1; collects > limit {
		t.Errorf("collects = %d in %v; want at most %d (one for each timer run, not one for each subscriber)", collects, elapsed, limit)
	}
	if got := hashes.Load(); got != int64(collects) {
		t.Errorf("hash encodes = %d; want %d (one for each collect)", got, collects)
	}
	// With no change, the second subscriber gets exactly one frame: as the
	// latest frame at subscribe, or on its channel.
	frames := len(ch2)
	if latest2 != nil {
		frames++
	}
	if frames != 1 {
		t.Errorf("second subscriber frames = %d; want 1", frames)
	}
	noFrame(t, ch1, "first subscriber after its first frame")
}

func TestBroadcast_LoopStopsAfterLastUnsubscribe(t *testing.T) {
	const interval = 5 * time.Millisecond
	shortTimings(t, interval, time.Hour)
	src := &countingSource{name: "a"}
	b := newBroadcaster(context.Background(), src.collect)

	ch1, _ := b.subscribe()
	ch2, _ := b.subscribe()
	recvFrame(t, ch1)

	b.unsubscribe(ch1)
	n := src.callCount()
	waitFor(t, "collects with one subscriber left", func() bool { return src.callCount() >= n+3 })
	if latest, running := latestOf(b); !running || latest == nil {
		t.Fatalf("after 2 -> 1 subscribers running = %v, latest = %v; want a running loop and a latest frame", running, latest)
	}

	// A channel that is not subscribed changes nothing.
	b.unsubscribe(make(chan frame, 1))
	if _, running := latestOf(b); !running {
		t.Fatal("unsubscribe of an unknown channel stopped the loop")
	}

	b.unsubscribe(ch2)
	if latest, running := latestOf(b); running || latest != nil {
		t.Errorf("after 1 -> 0 subscribers running = %v, latest = %v; want no loop and no latest frame", running, latest)
	}
	n = src.callCount()
	time.Sleep(3*interval + interval/2)
	if got := src.callCount(); got != n {
		t.Errorf("collects after the last unsubscribe = %d; want %d", got, n)
	}

	// A second unsubscribe of the same channel changes nothing.
	b.unsubscribe(ch2)
}

func TestBroadcast_NewSubscriberGetsLatest(t *testing.T) {
	shortTimings(t, time.Hour, time.Hour)
	src := &countingSource{name: "a"}
	b := newBroadcaster(context.Background(), src.collect)

	ch1, _ := b.subscribe()
	defer b.unsubscribe(ch1)
	first := recvFrame(t, ch1)

	ch2, latest := b.subscribe()
	defer b.unsubscribe(ch2)
	if latest == nil {
		t.Fatal("second subscribe latest = nil; want the current frame")
	}
	if latest.hash != first.hash || string(latest.data) != string(first.data) {
		t.Errorf("second subscribe latest = %s; want %s", latest.data, first.data)
	}
	if got := src.callCount(); got != 1 {
		t.Errorf("collects = %d; want 1 (the new subscriber does not collect)", got)
	}
	noFrame(t, ch2, "second subscriber")
}

func TestBroadcast_SlowSubscriberDoesNotDelayOthers(t *testing.T) {
	shortTimings(t, 2*time.Millisecond, time.Hour)
	src := &countingSource{name: "a"}
	b := newBroadcaster(context.Background(), src.collect)

	slow, _ := b.subscribe() // never read until the end
	defer b.unsubscribe(slow)
	fast, _ := b.subscribe()
	defer b.unsubscribe(fast)

	for _, name := range []string{"a", "b", "c", "d"} {
		src.setName(name)
		recvRepo(t, fast, name)
	}

	// The slow channel holds one frame: the newest, which replaced the
	// unread older ones.
	if n := len(slow); n != 1 {
		t.Fatalf("slow subscriber holds %d frames; want 1", n)
	}
	got := <-slow
	latest, _ := latestOf(b)
	if latest == nil || got.hash != latest.hash || !strings.Contains(string(got.data), `"name":"d"`) {
		t.Errorf("slow subscriber frame = %s; want the latest frame of repo d", got.data)
	}
}

func TestBroadcast_JoinWhileOldLoopEnds(t *testing.T) {
	shortTimings(t, time.Hour, time.Hour)
	src := &countingSource{name: "old", gate: make(chan struct{}), gateCall: 1}
	b := newBroadcaster(context.Background(), src.collect)

	ch1, _ := b.subscribe()
	waitFor(t, "the first collect to start", func() bool { return src.callCount() == 1 })
	src.setName("new")

	unsubDone := make(chan struct{})
	go func() {
		defer close(unsubDone)
		b.unsubscribe(ch1)
	}()
	waitFor(t, "the old loop to be cancelled", func() bool {
		_, running := latestOf(b)
		return !running
	})

	ch2, latest := b.subscribe()
	defer b.unsubscribe(ch2)
	if latest != nil {
		t.Errorf("subscribe while the old loop ends latest = %s; want nil", latest.data)
	}
	got := recvRepo(t, ch2, "new")

	select {
	case <-unsubDone:
		t.Fatal("unsubscribe returned before the old loop ended")
	default:
	}
	close(src.gate)
	select {
	case <-unsubDone:
	case <-time.After(2 * time.Second):
		t.Fatal("unsubscribe did not return after the old loop ended")
	}

	noFrame(t, ch2, "after the old loop ended")
	if l, _ := latestOf(b); l == nil || l.hash != got.hash {
		t.Errorf("latest after the old loop ended = %v; want the frame of the new loop", l)
	}
}

func TestBroadcast_HashErrorLogsAndSendsNothing(t *testing.T) {
	t.Run("first collect", func(t *testing.T) {
		shortTimings(t, time.Hour, time.Hour)
		logged := captureStderr(t)
		var calls atomic.Int64
		replaceHash(t, func(tools.DashboardSnapshot) (string, error) {
			calls.Add(1)
			return "", errors.New("boom")
		})
		src := &countingSource{name: "a"}
		b := newBroadcaster(context.Background(), src.collect)

		ch, _ := b.subscribe()
		waitFor(t, "one hash", func() bool { return calls.Load() == 1 })
		if latest, _ := latestOf(b); latest != nil {
			t.Errorf("latest = %s; want nil after a hash error", latest.data)
		}
		b.unsubscribe(ch)

		if got, want := logged.String(), "sdlc dashboard: snapshot hash: boom\n"; got != want {
			t.Errorf("log = %q; want %q", got, want)
		}
		noFrame(t, ch, "after a hash error")
	})

	t.Run("later collect keeps latest", func(t *testing.T) {
		shortTimings(t, 2*time.Millisecond, time.Hour)
		logged := captureStderr(t)
		var calls atomic.Int64
		replaceHash(t, func(s tools.DashboardSnapshot) (string, error) {
			if calls.Add(1) == 1 {
				return stableSnapshotHash(s)
			}
			return "", errors.New("boom")
		})
		src := &countingSource{name: "a"}
		b := newBroadcaster(context.Background(), src.collect)

		ch, _ := b.subscribe()
		first := recvFrame(t, ch)
		src.setName("b")
		waitFor(t, "4 hashes", func() bool { return calls.Load() >= 4 })
		if latest, _ := latestOf(b); latest == nil || latest.hash != first.hash {
			t.Errorf("latest = %v; want the first frame after hash errors", latest)
		}
		b.unsubscribe(ch)

		noFrame(t, ch, "after hash errors")
		lines := strings.Split(strings.TrimSuffix(logged.String(), "\n"), "\n")
		if want := int(calls.Load()) - 1; len(lines) != want {
			t.Errorf("log lines = %d; want %d (one for each failed hash)", len(lines), want)
		}
		for _, line := range lines {
			if line != "sdlc dashboard: snapshot hash: boom" {
				t.Errorf("log line = %q; want the snapshot hash line", line)
			}
		}
	})
}

func TestBroadcast_EncodeErrorLogsAndSendsNothing(t *testing.T) {
	shortTimings(t, time.Hour, time.Hour)
	logged := captureStderr(t)
	replaceHash(t, func(tools.DashboardSnapshot) (string, error) { return "fixed", nil })
	var calls atomic.Int64
	// json.Marshal refuses a NaN float, so the full encode fails.
	collect := func() tools.DashboardSnapshot {
		calls.Add(1)
		return nanSnapshot()
	}
	b := newBroadcaster(context.Background(), collect)

	ch, _ := b.subscribe()
	waitFor(t, "one collect", func() bool { return calls.Load() == 1 })
	b.unsubscribe(ch)

	got := logged.String()
	if !strings.HasPrefix(got, "sdlc dashboard: snapshot encode: ") || strings.Count(got, "\n") != 1 {
		t.Errorf("log = %q; want one snapshot encode line", got)
	}
	noFrame(t, ch, "after an encode error")
}

// nanSnapshot returns a snapshot that json.Marshal cannot encode.
func nanSnapshot() tools.DashboardSnapshot {
	return tools.DashboardSnapshot{Repos: []tools.DashboardRepo{{
		Sessions: []tools.DashboardSession{{
			CommandGroups: []tools.DashboardCommandGroup{{Share: math.NaN()}},
		}},
	}}}
}

func TestBroadcast_StableSnapshotHash(t *testing.T) {
	s := tools.DashboardSnapshot{GeneratedAt: "2026-10-30T10:00:00Z", Version: "v1", Repos: []tools.DashboardRepo{{Name: "a"}}}
	h1, err := stableSnapshotHash(s)
	if err != nil {
		t.Fatalf("stableSnapshotHash: %v", err)
	}
	if s.GeneratedAt != "2026-10-30T10:00:00Z" {
		t.Errorf("stableSnapshotHash changed the GeneratedAt of the caller to %q", s.GeneratedAt)
	}

	stable := s
	stable.GeneratedAt = ""
	raw, err := json.Marshal(stable)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if want := hex.EncodeToString(sum[:]); h1 != want {
		t.Errorf("hash = %s; want the sha256 hex of the JSON without GeneratedAt %s", h1, want)
	}

	later := s
	later.GeneratedAt = "2026-10-30T10:00:02Z"
	if h2, _ := stableSnapshotHash(later); h2 != h1 {
		t.Errorf("hash with another GeneratedAt = %s; want %s", h2, h1)
	}
	changed := s
	changed.Version = "v2"
	if h3, _ := stableSnapshotHash(changed); h3 == h1 {
		t.Error("hash with another Version is the same; want a different hash")
	}

	if h, err := stableSnapshotHash(nanSnapshot()); err == nil || h != "" {
		t.Errorf("stableSnapshotHash(NaN) = %q, %v; want an empty hash and an error", h, err)
	}
}

// readLine reads one line of r, or fails the test after 2s.
func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		got <- result{line, err}
	}()
	select {
	case res := <-got:
		if res.err != nil {
			t.Fatalf("read event stream: %v", res.err)
		}
		return res.line
	case <-time.After(2 * time.Second):
		t.Fatal("no event stream line within 2s")
		return ""
	}
}

// readSnapshotData reads r up to the next snapshot event and returns its
// data line.
func readSnapshotData(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	for {
		if readLine(t, r) == "event: snapshot\n" {
			return readLine(t, r)
		}
	}
}

// openStream opens GET /api/events on the running server.
func openStream(t *testing.T, r *running) *bufio.Reader {
	t.Helper()
	resp, err := r.client.Get("http://" + testHost + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return bufio.NewReader(resp.Body)
}

func TestEvents_NewStreamGetsCurrentSnapshot(t *testing.T) {
	isolateCacheDir(t)
	shortTimings(t, time.Hour, time.Hour)
	c := &fakeCollector{repo: "a"}
	r := startServe(t, testOptions(t, c))

	first := readSnapshotData(t, openStream(t, r))
	second := openStream(t, r)
	if line := readLine(t, second); line != "retry: 3000\n" {
		t.Fatalf("second stream first line = %q; want retry: 3000", line)
	}
	if got := readSnapshotData(t, second); got != first {
		t.Errorf("second stream snapshot = %.120s; want the current snapshot %.120s", got, first)
	}
	if n := c.callCount(); n != 1 {
		t.Errorf("collects = %d; want 1 (one shared loop, no change of the hash)", n)
	}
}

func TestEvents_RetryLineBeforeFirstFrame(t *testing.T) {
	isolateCacheDir(t)
	shortTimings(t, time.Hour, time.Hour)
	gate := make(chan struct{})
	var release sync.Once
	open := func() { release.Do(func() { close(gate) }) }
	o := testOptions(t, &fakeCollector{repo: "a"})
	collect := o.Collect
	o.Collect = func(roots []string, at time.Time) tools.DashboardSnapshot {
		<-gate
		return collect(roots, at)
	}
	r := startServe(t, o)
	// Registered after startServe, so it runs first: the blocked collect
	// returns before Serve stops.
	t.Cleanup(open)

	stream := openStream(t, r)
	if line := readLine(t, stream); line != "retry: 3000\n" {
		t.Fatalf("first stream line = %q; want retry: 3000 before the first collect ends", line)
	}
	open()
	if data := readSnapshotData(t, stream); !strings.HasPrefix(data, "data: {") {
		t.Errorf("first snapshot line = %q; want data: {...}", data)
	}
}
