package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// snapshotHash encodes s with GeneratedAt cleared and returns the sha256
// hex of that JSON. The loop calls it at each timer run. Tests replace it
// to count encodes.
var snapshotHash = stableSnapshotHash

// stableSnapshotHash returns the sha256 hex of the JSON of s without its
// GeneratedAt time, which changes at every collect.
func stableSnapshotHash(s tools.DashboardSnapshot) (string, error) {
	s.GeneratedAt = ""
	stable, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(stable)
	return hex.EncodeToString(sum[:]), nil
}

// frame is one encoded snapshot event and its hash (GeneratedAt cleared).
// A frame does not change after the loop makes it, so subscribers share it.
type frame struct {
	data []byte
	hash string
}

// broadcaster runs one collect loop while at least one subscriber exists.
// The loop collects at its start and then at each snapshotPollInterval. It
// sends a frame to every subscriber only when the hash of the snapshot
// differs from the hash of latest.
type broadcaster struct {
	mu      sync.Mutex
	parent  context.Context
	subs    map[chan frame]struct{} // capacity 1, a new frame replaces an unread one
	latest  *frame
	cancel  context.CancelFunc // nil while no loop runs
	done    chan struct{}      // closed when the running loop returns; nil while no loop runs
	collect func() tools.DashboardSnapshot
}

// newBroadcaster returns a broadcaster with no subscribers and no loop. Each
// loop runs in a context derived from parent, so a cancel of parent ends it.
func newBroadcaster(parent context.Context, collect func() tools.DashboardSnapshot) *broadcaster {
	return &broadcaster{parent: parent, subs: map[chan frame]struct{}{}, collect: collect}
}

// subscribe starts the loop on 0 -> 1 subscribers. It returns the channel
// and the latest frame (nil before the first collect). The new loop reads
// snapshotPollInterval and snapshotHash here, once.
func (b *broadcaster) subscribe() (ch chan frame, latest *frame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch = make(chan frame, 1)
	b.subs[ch] = struct{}{}
	if b.cancel == nil {
		ctx, cancel := context.WithCancel(b.parent)
		done := make(chan struct{})
		b.cancel, b.done = cancel, done
		go b.run(ctx, done, snapshotPollInterval, snapshotHash)
	}
	return ch, b.latest
}

// unsubscribe stops the loop on 1 -> 0 and clears latest. It then waits
// until the stopped loop returns, so no collect of that loop runs after it.
// A channel that is not subscribed changes nothing.
func (b *broadcaster) unsubscribe(ch chan frame) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; !ok {
		b.mu.Unlock()
		return
	}
	delete(b.subs, ch)
	if len(b.subs) > 0 || b.cancel == nil {
		b.mu.Unlock()
		return
	}
	b.cancel()
	done := b.done
	b.cancel, b.done, b.latest = nil, nil, nil
	b.mu.Unlock()
	<-done
}

// run is the collect loop: one tick at its start, then one tick at each
// interval, until ctx ends. It closes done when it returns.
func (b *broadcaster) run(ctx context.Context, done chan struct{}, interval time.Duration,
	hash func(tools.DashboardSnapshot) (string, error)) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		b.tick(ctx, hash)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tick collects one snapshot and hashes it. When the hash differs from the
// hash of latest, it encodes the snapshot, stores the frame as latest, and
// sends it to every subscriber. A hash or encode error writes one log line
// and sends nothing. A tick of a loop whose ctx has ended stores and sends
// nothing.
func (b *broadcaster) tick(ctx context.Context, hash func(tools.DashboardSnapshot) (string, error)) {
	s := b.collect()
	h, err := hash(s)
	if err != nil {
		fmt.Fprintf(stderr, "sdlc dashboard: snapshot hash: %v\n", err)
		return
	}
	b.mu.Lock()
	same := b.latest != nil && b.latest.hash == h
	b.mu.Unlock()
	if same {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		fmt.Fprintf(stderr, "sdlc dashboard: snapshot encode: %v\n", err)
		return
	}
	f := &frame{data: data, hash: h}

	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	b.latest = f
	for ch := range b.subs {
		// A new frame replaces an unread one. Only the loop whose ctx is
		// live sends, and it holds mu, so after the drain the buffer of
		// capacity 1 has room; the default arm never blocks the loop.
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- *f:
		default:
		}
	}
}
