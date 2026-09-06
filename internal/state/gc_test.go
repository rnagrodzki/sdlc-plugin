package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// GC — TTL expiry
// ---------------------------------------------------------------------------

func TestGC_DeletesTTLExpired(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	old := "ship-main-20250101T100000Z.json"
	recent := "ship-main-20260901T100000Z.json"

	// Old file: mtime 30 days ago.
	oldPath := filepath.Join(dir, old)
	writeFixture(t, oldPath)
	setMtime(t, oldPath, time.Now().Add(-30*24*time.Hour))

	// Recent file: mtime 1 hour ago.
	recentPath := filepath.Join(dir, recent)
	writeFixture(t, recentPath)
	setMtime(t, recentPath, time.Now().Add(-1*time.Hour))

	rpt, err := GC(root, GCOptions{TTL: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}

	// The old file should be deleted; the recent one kept.
	if !containsPath(rpt.Deleted, oldPath) {
		t.Fatalf("expected old file in Deleted, got Deleted=%v", rpt.Deleted)
	}
	if !containsPath(rpt.Kept, recentPath) {
		t.Fatalf("expected recent file in Kept, got Kept=%v", rpt.Kept)
	}
	if rpt.Buckets["ship"] != 1 {
		t.Fatalf("Buckets[ship] = %d, want 1", rpt.Buckets["ship"])
	}
}

// ---------------------------------------------------------------------------
// GC — newest file is never deleted for a live branch
// ---------------------------------------------------------------------------

func TestGC_KeepsNewestForLiveBranch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Both files are TTL-expired, but the newest must be kept.
	f1 := "execute-feat-x-20250101T100000Z.json"
	f2 := "execute-feat-x-20250201T100000Z.json"

	p1 := filepath.Join(dir, f1)
	p2 := filepath.Join(dir, f2)

	writeFixture(t, p1)
	setMtime(t, p1, time.Now().Add(-60*24*time.Hour))

	writeFixture(t, p2)
	setMtime(t, p2, time.Now().Add(-30*24*time.Hour))

	rpt, err := GC(root, GCOptions{TTL: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}

	// f2 is newer, so it must be kept despite being expired.
	if !containsPath(rpt.Kept, p2) {
		t.Fatalf("newest file should be kept, Kept=%v", rpt.Kept)
	}
	if !containsPath(rpt.Deleted, p1) {
		t.Fatalf("older file should be deleted, Deleted=%v", rpt.Deleted)
	}
}

// ---------------------------------------------------------------------------
// GC — deleted branch: all files removed
// ---------------------------------------------------------------------------

func TestGC_DeletesAllForDeletedBranch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	f1 := "ship-dead-branch-20260901T100000Z.json"
	f2 := "ship-dead-branch-20260902T100000Z.json"
	alive := "ship-alive-branch-20260901T100000Z.json"

	for _, name := range []string{f1, f2, alive} {
		fp := filepath.Join(dir, name)
		writeFixture(t, fp)
		setMtime(t, fp, time.Now().Add(-1*time.Hour))
	}

	branchExists := func(slug string) bool {
		return slug != "dead-branch"
	}

	rpt, err := GC(root, GCOptions{BranchExists: branchExists})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}

	// Both dead-branch files should be deleted.
	if !containsPath(rpt.Deleted, filepath.Join(dir, f1)) {
		t.Fatalf("dead branch f1 should be deleted")
	}
	if !containsPath(rpt.Deleted, filepath.Join(dir, f2)) {
		t.Fatalf("dead branch f2 should be deleted")
	}
	// alive-branch file should be kept.
	if !containsPath(rpt.Kept, filepath.Join(dir, alive)) {
		t.Fatalf("alive branch file should be kept")
	}
}

// ---------------------------------------------------------------------------
// GC — skips sidecar files (dot-prefixed)
// ---------------------------------------------------------------------------

func TestGC_SkipsSidecarFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	sidecar := ".compact-recovery-main.json"
	state := "ship-main-20260901T100000Z.json"

	writeFixture(t, filepath.Join(dir, sidecar))
	writeFixture(t, filepath.Join(dir, state))

	rpt, err := GC(root, GCOptions{})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}

	// Sidecar should not appear in Deleted or Kept.
	for _, p := range append(rpt.Deleted, rpt.Kept...) {
		if filepath.Base(p) == sidecar {
			t.Fatalf("sidecar file should not be processed by GC")
		}
	}
	// State file should be kept.
	if len(rpt.Kept) != 1 {
		t.Fatalf("expected 1 kept, got %d", len(rpt.Kept))
	}
}

// ---------------------------------------------------------------------------
// GC — empty / missing directory
// ---------------------------------------------------------------------------

func TestGC_MissingDir(t *testing.T) {
	root := t.TempDir()
	rpt, err := GC(root, GCOptions{})
	if err != nil {
		t.Fatalf("GC on missing dir: %v", err)
	}
	if len(rpt.Deleted) != 0 || len(rpt.Kept) != 0 {
		t.Fatalf("expected empty report, got Deleted=%d Kept=%d", len(rpt.Deleted), len(rpt.Kept))
	}
}

// ---------------------------------------------------------------------------
// GC — buckets have correct prefix counts
// ---------------------------------------------------------------------------

func TestGC_BucketCounts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	files := []string{
		"ship-main-20260901T100000Z.json",
		"ship-feat-a-20260901T100000Z.json",
		"execute-main-20260901T100000Z.json",
		"plan-main-20260901T100000Z.json",
	}
	for _, name := range files {
		fp := filepath.Join(dir, name)
		writeFixture(t, fp)
		setMtime(t, fp, time.Now())
	}

	rpt, err := GC(root, GCOptions{})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}

	if rpt.Buckets["ship"] != 2 {
		t.Fatalf("Buckets[ship] = %d, want 2", rpt.Buckets["ship"])
	}
	if rpt.Buckets["execute"] != 1 {
		t.Fatalf("Buckets[execute] = %d, want 1", rpt.Buckets["execute"])
	}
	if rpt.Buckets["plan"] != 1 {
		t.Fatalf("Buckets[plan] = %d, want 1", rpt.Buckets["plan"])
	}
}

// ---------------------------------------------------------------------------
// GC — gcTempdirs sweeps sdlc-explore-* directories with TTL/branch rules
// ---------------------------------------------------------------------------

func TestGCTempdirs_StaleDeadBranchDeleted(t *testing.T) {
	tmpDir := t.TempDir()

	explore := filepath.Join(tmpDir, "sdlc-explore-feat-x-abc123")
	mustMkdir(t, explore)
	setMtime(t, explore, time.Now().Add(-30*24*time.Hour))

	other := filepath.Join(tmpDir, "other-dir")
	mustMkdir(t, other)

	deleted, kept := gcTempdirs(tmpDir, 7*24*time.Hour, func(string) bool { return false })

	if !containsPath(deleted, explore) {
		t.Fatalf("expected stale dead-branch tempdir in deleted, got %v", deleted)
	}
	if containsPath(kept, explore) {
		t.Fatalf("stale dead-branch tempdir should not be kept")
	}
	if _, err := os.Stat(explore); !os.IsNotExist(err) {
		t.Fatalf("sdlc-explore dir should have been removed from disk")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("non-explore dir should be untouched: %v", err)
	}
}

func TestGCTempdirs_TTLFreshKept(t *testing.T) {
	tmpDir := t.TempDir()

	explore := filepath.Join(tmpDir, "sdlc-explore-feat-x-abc123")
	mustMkdir(t, explore)
	setMtime(t, explore, time.Now().Add(-1*time.Hour))

	// Even with a "dead" branch, a TTL-fresh dir must be kept.
	deleted, kept := gcTempdirs(tmpDir, 7*24*time.Hour, func(string) bool { return false })

	if !containsPath(kept, explore) {
		t.Fatalf("ttl-fresh tempdir should be kept, got kept=%v", kept)
	}
	if containsPath(deleted, explore) {
		t.Fatalf("ttl-fresh tempdir should not be deleted")
	}
	if _, err := os.Stat(explore); err != nil {
		t.Fatalf("ttl-fresh dir should still exist: %v", err)
	}
}

func TestGCTempdirs_StaleLiveBranchKept(t *testing.T) {
	tmpDir := t.TempDir()

	explore := filepath.Join(tmpDir, "sdlc-explore-feat-x-abc123")
	mustMkdir(t, explore)
	setMtime(t, explore, time.Now().Add(-30*24*time.Hour))

	deleted, kept := gcTempdirs(tmpDir, 7*24*time.Hour, func(slug string) bool { return slug == "feat-x" })

	if !containsPath(kept, explore) {
		t.Fatalf("stale live-branch tempdir should be kept, got kept=%v", kept)
	}
	if containsPath(deleted, explore) {
		t.Fatalf("stale live-branch tempdir should not be deleted")
	}
}

func TestGCTempdirs_NilBranchExistsAssumesLive(t *testing.T) {
	tmpDir := t.TempDir()

	explore := filepath.Join(tmpDir, "sdlc-explore-feat-x-abc123")
	mustMkdir(t, explore)
	setMtime(t, explore, time.Now().Add(-30*24*time.Hour))

	// nil branchExists => every branch treated as existing, matching
	// GCOptions.BranchExists' documented default.
	deleted, kept := gcTempdirs(tmpDir, 7*24*time.Hour, nil)

	if !containsPath(kept, explore) {
		t.Fatalf("stale tempdir with nil branchExists should be kept (assume live), got kept=%v", kept)
	}
	if containsPath(deleted, explore) {
		t.Fatalf("stale tempdir with nil branchExists should not be deleted")
	}
}

func TestGCTempdirs_UnparseableNameKept(t *testing.T) {
	tmpDir := t.TempDir()

	// No random-suffix segment after the prefix: only one '-'-delimited part.
	explore := filepath.Join(tmpDir, "sdlc-explore-onlyoneseg")
	mustMkdir(t, explore)
	setMtime(t, explore, time.Now().Add(-30*24*time.Hour))

	deleted, kept := gcTempdirs(tmpDir, 7*24*time.Hour, func(string) bool { return false })

	if !containsPath(kept, explore) {
		t.Fatalf("unparseable-name tempdir should be kept, got kept=%v", kept)
	}
	if containsPath(deleted, explore) {
		t.Fatalf("unparseable-name tempdir should not be deleted")
	}
}

func TestGC_IntegratesTempdirSweep(t *testing.T) {
	root := t.TempDir()
	tmpDir := t.TempDir()

	dead := filepath.Join(tmpDir, "sdlc-explore-dead-branch-abc123")
	mustMkdir(t, dead)
	setMtime(t, dead, time.Now().Add(-30*24*time.Hour))

	alive := filepath.Join(tmpDir, "sdlc-explore-alive-branch-def456")
	mustMkdir(t, alive)
	setMtime(t, alive, time.Now().Add(-30*24*time.Hour))

	rpt, err := GC(root, GCOptions{
		TTL:          7 * 24 * time.Hour,
		TempDir:      tmpDir,
		BranchExists: func(slug string) bool { return slug == "alive-branch" },
	})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}

	if !containsPath(rpt.TempdirsDeleted, dead) {
		t.Fatalf("expected dead-branch tempdir in TempdirsDeleted, got %v", rpt.TempdirsDeleted)
	}
	if !containsPath(rpt.TempdirsKept, alive) {
		t.Fatalf("expected alive-branch tempdir in TempdirsKept, got %v", rpt.TempdirsKept)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("dead-branch tempdir should have been removed")
	}
	if _, err := os.Stat(alive); err != nil {
		t.Fatalf("alive-branch tempdir should still exist: %v", err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// MigrateBranchSlug
// ---------------------------------------------------------------------------

func TestMigrateBranchSlug(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	files := []string{
		"ship-old-slug-20260901T100000Z.json",
		"execute-old-slug-20260902T100000Z.json",
		"plan-other-slug-20260903T100000Z.json",
	}
	for _, name := range files {
		writeFixture(t, filepath.Join(dir, name))
	}

	if err := MigrateBranchSlug(root, "old-slug", "new-slug"); err != nil {
		t.Fatalf("MigrateBranchSlug: %v", err)
	}

	// Old files should be gone.
	for _, name := range files[:2] {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("old file %s should have been renamed", name)
		}
	}

	// New files should exist.
	expected := []string{
		"ship-new-slug-20260901T100000Z.json",
		"execute-new-slug-20260902T100000Z.json",
	}
	for _, name := range expected {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("new file %s should exist: %v", name, err)
		}
	}

	// Unrelated slug should be untouched.
	if _, err := os.Stat(filepath.Join(dir, "plan-other-slug-20260903T100000Z.json")); err != nil {
		t.Fatalf("unrelated file should be untouched: %v", err)
	}
}

func TestMigrateBranchSlug_MissingDir(t *testing.T) {
	root := t.TempDir()
	// No error when the state dir doesn't exist.
	if err := MigrateBranchSlug(root, "old", "new"); err != nil {
		t.Fatalf("MigrateBranchSlug on missing dir: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Sidecar — WriteRecoverySidecar + ConsumeRecoverySidecar
// ---------------------------------------------------------------------------

func TestRecoverySidecar_WriteAndConsume(t *testing.T) {
	root := t.TempDir()
	slug := "feat-x"
	payload := map[string]any{"key": "value", "num": float64(42)}

	if err := WriteRecoverySidecar(root, slug, payload); err != nil {
		t.Fatalf("WriteRecoverySidecar: %v", err)
	}

	// File should exist on disk.
	path := recoverySidecarPath(root, slug)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sidecar file should exist: %v", err)
	}

	// Consume should return the data.
	got, err := ConsumeRecoverySidecar(root, slug)
	if err != nil {
		t.Fatalf("ConsumeRecoverySidecar: %v", err)
	}
	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if gotMap["key"] != "value" {
		t.Fatalf("key = %v, want %q", gotMap["key"], "value")
	}
	if gotMap["num"] != float64(42) {
		t.Fatalf("num = %v, want 42", gotMap["num"])
	}

	// File should be deleted (single-use).
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("sidecar file should have been deleted after consume")
	}

	// Second consume should return (nil, nil).
	got2, err := ConsumeRecoverySidecar(root, slug)
	if err != nil {
		t.Fatalf("second ConsumeRecoverySidecar: %v", err)
	}
	if got2 != nil {
		t.Fatalf("second consume should return nil, got %v", got2)
	}
}

func TestRecoverySidecar_Expired(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	slug := "feat-exp"
	path := recoverySidecarPath(root, slug)

	// Write sidecar with createdAt 2 hours ago.
	env := recoverySidecarEnvelope{
		Data:      "old-data",
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour),
	}
	raw, _ := json.Marshal(env)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := ConsumeRecoverySidecar(root, slug)
	if err == nil {
		t.Fatalf("expected error for expired sidecar, got data=%v", got)
	}
	if got != nil {
		t.Fatalf("expected nil data for expired sidecar, got %v", got)
	}

	// File should still be deleted even if expired.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired sidecar file should have been deleted")
	}
}

func TestRecoverySidecar_NotFound(t *testing.T) {
	root := t.TempDir()
	got, err := ConsumeRecoverySidecar(root, "nonexistent")
	if err != nil {
		t.Fatalf("ConsumeRecoverySidecar: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for nonexistent sidecar, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// StepBlockCount
// ---------------------------------------------------------------------------

func TestStepBlockCount_AbsentStartsAtOne(t *testing.T) {
	root := t.TempDir()
	count, capped, err := StepBlockCount(root, "feat-x", "review")
	if err != nil {
		t.Fatalf("StepBlockCount: %v", err)
	}
	if capped {
		t.Fatalf("capped = true on first call, want false")
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestStepBlockCount_IncrementsThenCaps(t *testing.T) {
	root := t.TempDir()
	slug := "feat-inc"
	step := "review"

	// Calls 1-3 (consecutive, same step): increment to 1, 2, 3, never capped.
	for i := 1; i <= 3; i++ {
		count, capped, err := StepBlockCount(root, slug, step)
		if err != nil {
			t.Fatalf("StepBlockCount(%d): %v", i, err)
		}
		if capped {
			t.Fatalf("call %d: capped = true, want false", i)
		}
		if count != i {
			t.Fatalf("call %d: count = %d, want %d", i, count, i)
		}
	}

	// Call 4: already at cap (3) — reported capped, NOT incremented to 4,
	// and the sidecar is deleted.
	count, capped, err := StepBlockCount(root, slug, step)
	if err != nil {
		t.Fatalf("StepBlockCount(4): %v", err)
	}
	if !capped {
		t.Fatalf("call 4: capped = false, want true (cap exhausted)")
	}
	if count != 3 {
		t.Fatalf("call 4: count = %d, want 3 (reported pre-cap count, not incremented)", count)
	}
	if _, err := os.Stat(blockCountPath(root, slug)); !os.IsNotExist(err) {
		t.Fatalf("sidecar should have been deleted on cap-exhaustion, stat err=%v", err)
	}

	// Call 5 (retry after exhaustion): fresh budget, back to 1.
	count, capped, err = StepBlockCount(root, slug, step)
	if err != nil {
		t.Fatalf("StepBlockCount(5): %v", err)
	}
	if capped {
		t.Fatalf("call 5: capped = true, want false (fresh budget after delete)")
	}
	if count != 1 {
		t.Fatalf("call 5: count = %d, want 1 (fresh budget)", count)
	}
}

func TestStepBlockCount_ResetsOnStepNameChange(t *testing.T) {
	root := t.TempDir()
	slug := "feat-reset"

	// Two consecutive blocks on "review".
	for i := 1; i <= 2; i++ {
		if _, capped, err := StepBlockCount(root, slug, "review"); err != nil || capped {
			t.Fatalf("StepBlockCount(review, %d): capped=%v err=%v", i, capped, err)
		}
	}

	// Step transitions to "pr": must not inherit the count toward the cap —
	// starts fresh at 1, not 3.
	count, capped, err := StepBlockCount(root, slug, "pr")
	if err != nil {
		t.Fatalf("StepBlockCount(pr): %v", err)
	}
	if capped {
		t.Fatalf("StepBlockCount(pr): capped = true, want false (fresh budget on step change)")
	}
	if count != 1 {
		t.Fatalf("StepBlockCount(pr): count = %d, want 1 (reset on step change)", count)
	}
}

func TestStepBlockCount_CorruptSidecarTreatedAsAbsent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	slug := "feat-corrupt"
	path := blockCountPath(root, slug)
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	count, capped, err := StepBlockCount(root, slug, "review")
	if err != nil {
		t.Fatalf("StepBlockCount on corrupt sidecar returned error, want silent fresh counter: %v", err)
	}
	if capped {
		t.Fatalf("capped = true on corrupt-sidecar-as-fresh, want false")
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 (corrupt sidecar treated as fresh)", count)
	}
}

// ---------------------------------------------------------------------------
// ClaimSession
// ---------------------------------------------------------------------------

func TestClaimSession(t *testing.T) {
	root := t.TempDir()

	st, err := Init(root, "execute", "main", "old-session")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	if st.Data["sessionId"] != "old-session" {
		t.Fatalf("initial sessionId = %v, want %q", st.Data["sessionId"], "old-session")
	}

	if err := ClaimSession(st, "new-session"); err != nil {
		t.Fatalf("ClaimSession: %v", err)
	}

	if st.Data["sessionId"] != "new-session" {
		t.Fatalf("in-memory sessionId = %v, want %q", st.Data["sessionId"], "new-session")
	}

	// Read back from disk.
	var data map[string]any
	raw, err := os.ReadFile(st.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if data["sessionId"] != "new-session" {
		t.Fatalf("on-disk sessionId = %v, want %q", data["sessionId"], "new-session")
	}
}

func TestClaimSession_NilData(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	st := &State{
		Path:       filepath.Join(dir, "ship-main-20260901T100000Z.json"),
		Root:       root,
		Prefix:     "ship",
		BranchSlug: "main",
		Data:       nil,
	}

	if err := ClaimSession(st, "sess-1"); err != nil {
		t.Fatalf("ClaimSession: %v", err)
	}

	if st.Data["sessionId"] != "sess-1" {
		t.Fatalf("sessionId = %v, want %q", st.Data["sessionId"], "sess-1")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeFixture(t *testing.T, path string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"fixture": true})
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writeFixture %s: %v", path, err)
	}
}

func setMtime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("setMtime %s: %v", path, err)
	}
}

func containsPath(paths []string, target string) bool {
	for _, p := range paths {
		if p == target {
			return true
		}
	}
	return false
}
