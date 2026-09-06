package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

const recoverySidecarTTL = time.Hour

// stopBlockCap is the consecutive-block cap for stop-pipeline-continue's
// block-count sidecar (STOP_BLOCK_CAP in the JS source): the 4th consecutive
// block on the same unchanged step is not blocked — it marks the step
// failed instead. See StepBlockCount.
const stopBlockCap = 3

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

func recoverySidecarPath(root, slug string) string {
	return filepath.Join(stateDir(root), ".compact-recovery-"+slug+".json")
}

func blockCountPath(root, slug string) string {
	return filepath.Join(stateDir(root), ".stop-block-count-"+slug+".json")
}

// ---------------------------------------------------------------------------
// Sidecar envelope types (on-disk JSON shape)
// ---------------------------------------------------------------------------

type recoverySidecarEnvelope struct {
	Data      any       `json:"data"`
	CreatedAt time.Time `json:"createdAt"`
}

type blockCountEnvelope struct {
	StepName string `json:"stepName"`
	Count    int    `json:"count"`
}

// ---------------------------------------------------------------------------
// Recovery sidecar
// ---------------------------------------------------------------------------

// WriteRecoverySidecar writes a compact-recovery sidecar file.
// File: .sdlc/execution/.compact-recovery-<slug>.json
func WriteRecoverySidecar(root, slug string, data any) error {
	dir := stateDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("state: mkdir %s: %w", dir, err)
	}

	env := recoverySidecarEnvelope{
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
	return fsx.AtomicWriteJSON(recoverySidecarPath(root, slug), env)
}

// ConsumeRecoverySidecar reads and deletes a compact-recovery sidecar
// (single-use). Returns (nil, nil) if the sidecar doesn't exist.
// Returns error if the sidecar exists but is expired (1h TTL).
func ConsumeRecoverySidecar(root, slug string) (any, error) {
	path := recoverySidecarPath(root, slug)

	var env recoverySidecarEnvelope
	if err := fsx.ReadJSON(path, &env); err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: read recovery sidecar: %w", err)
	}

	// Always delete after reading (single-use).
	_ = os.Remove(path)

	if time.Since(env.CreatedAt) > recoverySidecarTTL {
		return nil, fmt.Errorf("state: recovery sidecar expired (created %s)", env.CreatedAt.Format(time.RFC3339))
	}

	return env.Data, nil
}

// ---------------------------------------------------------------------------
// Block-count sidecar
// ---------------------------------------------------------------------------

// StepBlockCount implements stop-pipeline-continue.js's consecutive-block
// cap in one atomic call: read-reset-check-then-increment-or-delete.
// File: .sdlc/execution/.stop-block-count-<slug>.json
//
// The sidecar is read first; an absent or corrupt/unparseable file degrades
// silently to a fresh {StepName: "", Count: 0} counter (no error), mirroring
// the JS source's own try/catch around JSON.parse. If the stored step name
// differs from stepName, the counter is reset to {StepName: stepName,
// Count: 0} before the cap check — a step transition always gets a fresh
// budget.
//
// If the (possibly just-reset) count is already >= stopBlockCap, the
// sidecar is deleted and (count, true, nil) is returned WITHOUT
// incrementing or rewriting the file — this is the "already exhausted, mark
// the step failed instead of blocking again" case. Otherwise the count is
// incremented by 1, persisted, and (newCount, false, nil) is returned.
func StepBlockCount(root, slug, stepName string) (count int, capped bool, err error) {
	path := blockCountPath(root, slug)

	counter := blockCountEnvelope{StepName: "", Count: 0}
	var env blockCountEnvelope
	if readErr := fsx.ReadJSON(path, &env); readErr == nil {
		counter = env
	}

	if counter.StepName != stepName {
		counter = blockCountEnvelope{StepName: stepName, Count: 0}
	}

	if counter.Count >= stopBlockCap {
		_ = os.Remove(path)
		return counter.Count, true, nil
	}

	dir := stateDir(root)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return 0, false, fmt.Errorf("state: mkdir %s: %w", dir, mkErr)
	}

	counter.Count++
	if writeErr := fsx.AtomicWriteJSON(path, counter); writeErr != nil {
		return 0, false, fmt.Errorf("state: write block count: %w", writeErr)
	}

	return counter.Count, false, nil
}

// ---------------------------------------------------------------------------
// ClaimSession
// ---------------------------------------------------------------------------

// ClaimSession stamps sessionID into state data and writes it to disk.
func ClaimSession(st *State, sessionID string) error {
	if st.Data == nil {
		st.Data = make(map[string]any)
	}
	st.Data["sessionId"] = sessionID
	return Write(st)
}
