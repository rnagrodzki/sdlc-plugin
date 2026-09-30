package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
)

// ---------------------------------------------------------------------------
// Test seams and fixtures
// ---------------------------------------------------------------------------

// useHardenSurfaceStatus points hardenSurfaceStatus at fn for one test and
// restores the previous value afterwards — the seam that keeps
// dirtySurfaces tests off real git (no-real-fs-git-in-tests guardrail).
func useHardenSurfaceStatus(t *testing.T, fn func(dir string) (string, error)) {
	t.Helper()
	prev := hardenSurfaceStatus
	hardenSurfaceStatus = fn
	t.Cleanup(func() { hardenSurfaceStatus = prev })
}

// stubCleanSurfaceStatus points hardenSurfaceStatus at an always-clean
// status, for tests that only care about cluster logic and would otherwise
// shell out to real git through activeWorktreeRootSafe's caller.
func stubCleanSurfaceStatus(t *testing.T) {
	useHardenSurfaceStatus(t, func(string) (string, error) { return "", nil })
}

// hardenClustersFixture creates a ship state for branch with no git
// fixture (shipStateInit resolves the branch from Detail directly, so no
// real git call happens) and returns the state directory.
func hardenClustersFixture(t *testing.T, branch string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	return dir, shipStateInitFixture(t, dir, branch)
}

// hcFinding builds one detail.findings entry.
func hcFinding(file, severity, title, body, verdict string) map[string]any {
	return map[string]any{"file": file, "severity": severity, "title": title, "body": body, "verdict": verdict}
}

// hcFindingReason builds one detail.findings entry with a reason.
func hcFindingReason(file, severity, title, body, verdict, reason string) map[string]any {
	m := hcFinding(file, severity, title, body, verdict)
	m["reason"] = reason
	return m
}

// hardenClustersCall runs one harden_clusters call and returns its typed
// output.
func hardenClustersCall(t *testing.T, dir, branch string, findings []any) (HardenClustersOut, error) {
	t.Helper()
	out, err := shipState(dir, dir, ShipStateIn{
		Action: "harden_clusters",
		Detail: map[string]any{"branch": branch, "findings": findings},
	}, fixedNow(time.Now()))
	if err != nil {
		return HardenClustersOut{}, err
	}
	hc, ok := out.(HardenClustersOut)
	if !ok {
		t.Fatalf("harden_clusters output = %T, want HardenClustersOut", out)
	}
	return hc, nil
}

// ---------------------------------------------------------------------------
// Clustering rules
// ---------------------------------------------------------------------------

func TestShipStateHardenClusters_GroupsByFile(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-group")

	hc, err := hardenClustersCall(t, dir, "feat/hc-group", []any{
		hcFinding("a.go", "high", "issue 1", "body 1", "agree-will-fix"),
		hcFinding("a.go", "medium", "issue 2", "body 2", "needs-direction"),
		hcFinding("b.go", "low", "issue 3", "body 3", "agree-won't-fix"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 2 {
		t.Fatalf("clusters = %d, want 2: %+v", len(hc.Clusters), hc.Clusters)
	}
	byKey := map[string]HardenCluster{}
	for _, c := range hc.Clusters {
		byKey[c.Key] = c
	}
	if len(byKey["a.go"].Findings) != 2 {
		t.Errorf("a.go findings = %d, want 2", len(byKey["a.go"].Findings))
	}
	if len(byKey["b.go"].Findings) != 1 {
		t.Errorf("b.go findings = %d, want 1", len(byKey["b.go"].Findings))
	}
	if len(hc.Suppressed) != 0 || len(hc.LoneDisagree) != 0 {
		t.Errorf("suppressed/loneDisagree = %v/%v, want both empty", hc.Suppressed, hc.LoneDisagree)
	}
}

func TestShipStateHardenClusters_BelowThresholdIgnoredEntirely(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-below")

	hc, err := hardenClustersCall(t, dir, "feat/hc-below", []any{
		hcFindingReason("only.go", "info", "minor nit", "body", "agree-will-fix", history.ReasonBelowThreshold),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 0 || len(hc.Suppressed) != 0 || len(hc.LoneDisagree) != 0 {
		t.Fatalf("a below-threshold-only finding must vanish entirely, got clusters=%v suppressed=%v loneDisagree=%v",
			hc.Clusters, hc.Suppressed, hc.LoneDisagree)
	}
	if hc.Next != "no clusters — skip" {
		t.Errorf("next = %q, want %q", hc.Next, "no clusters — skip")
	}

	// Mixed with a surviving finding on the same file: only the
	// below-threshold one is dropped, the file still clusters on the rest.
	hc, err = hardenClustersCall(t, dir, "feat/hc-below", []any{
		hcFindingReason("mixed.go", "info", "minor nit", "body", "agree-will-fix", history.ReasonBelowThreshold),
		hcFinding("mixed.go", "high", "real issue", "body2", "agree-will-fix"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 1 || len(hc.Clusters[0].Findings) != 1 {
		t.Fatalf("clusters = %+v, want exactly 1 cluster with 1 surviving finding", hc.Clusters)
	}
}

func TestShipStateHardenClusters_LoneDisagreeDropped(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-lone")

	// disagree by verdict.
	hc, err := hardenClustersCall(t, dir, "feat/hc-lone", []any{
		hcFinding("byverdict.go", "high", "t", "b", "disagree"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 0 {
		t.Errorf("clusters = %+v, want none", hc.Clusters)
	}
	if len(hc.LoneDisagree) != 1 || hc.LoneDisagree[0] != "byverdict.go" {
		t.Errorf("loneDisagree = %v, want [byverdict.go]", hc.LoneDisagree)
	}

	// disagree by reason, verdict itself is not "disagree".
	hc, err = hardenClustersCall(t, dir, "feat/hc-lone", []any{
		hcFindingReason("byreason.go", "high", "t", "b", "agree-will-fix", history.ReasonDisagree),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 0 {
		t.Errorf("clusters = %+v, want none", hc.Clusters)
	}
	if len(hc.LoneDisagree) != 1 || hc.LoneDisagree[0] != "byreason.go" {
		t.Errorf("loneDisagree = %v, want [byreason.go]", hc.LoneDisagree)
	}
}

func TestShipStateHardenClusters_MultiDisagreeStillClusters(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-multidisagree")

	hc, err := hardenClustersCall(t, dir, "feat/hc-multidisagree", []any{
		hcFinding("x.go", "high", "t1", "b1", "disagree"),
		hcFinding("x.go", "medium", "t2", "b2", "disagree"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.LoneDisagree) != 0 {
		t.Errorf("loneDisagree = %v, want none — two disagree findings must still cluster", hc.LoneDisagree)
	}
	if len(hc.Clusters) != 1 || hc.Clusters[0].Key != "x.go" || len(hc.Clusters[0].Findings) != 2 {
		t.Fatalf("clusters = %+v, want one x.go cluster with 2 findings", hc.Clusters)
	}
}

func TestShipStateHardenClusters_CapAtFiveTiesAlphabetical(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-cap")

	var findings []any
	for _, f := range []string{"f.go", "e.go", "d.go", "c.go", "b.go", "a.go"} {
		findings = append(findings, hcFinding(f, "medium", "t", "b", "agree-will-fix"))
	}
	hc, err := hardenClustersCall(t, dir, "feat/hc-cap", findings)
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 5 {
		t.Fatalf("clusters = %d, want 5: %+v", len(hc.Clusters), hc.Clusters)
	}
	var gotKeys []string
	for _, c := range hc.Clusters {
		gotKeys = append(gotKeys, c.Key)
	}
	wantKeys := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Errorf("cluster keys = %v, want %v (alphabetical tie-break)", gotKeys, wantKeys)
	}
	if len(hc.Suppressed) != 1 || hc.Suppressed[0] != "f.go" {
		t.Errorf("suppressed = %v, want [f.go]", hc.Suppressed)
	}
}

func TestShipStateHardenClusters_CapPrefersMoreFindings(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-cap-count")

	var findings []any
	// five files with 1 finding each, one file with 2 — the 2-finding file
	// must survive the cap even though its name sorts last.
	for _, f := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		findings = append(findings, hcFinding(f, "medium", "t", "b", "agree-will-fix"))
	}
	findings = append(findings,
		hcFinding("z.go", "medium", "t1", "b1", "agree-will-fix"),
		hcFinding("z.go", "medium", "t2", "b2", "agree-will-fix"),
	)
	hc, err := hardenClustersCall(t, dir, "feat/hc-cap-count", findings)
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 5 {
		t.Fatalf("clusters = %d, want 5", len(hc.Clusters))
	}
	found := false
	for _, c := range hc.Clusters {
		if c.Key == "z.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("z.go (2 findings) should survive the cap over a 1-finding file; clusters = %+v", hc.Clusters)
	}
	if len(hc.Suppressed) != 1 || hc.Suppressed[0] != "e.go" {
		t.Errorf("suppressed = %v, want [e.go] (fewest findings, last alphabetically among the 1-finding files)", hc.Suppressed)
	}
}

// ---------------------------------------------------------------------------
// failureText rendering
// ---------------------------------------------------------------------------

func TestShipStateHardenClusters_FailureTextFormat(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-text")

	hc, err := hardenClustersCall(t, dir, "feat/hc-text", []any{
		hcFinding("a.go", "high", "Missing nil check", "foo() may return nil", "agree-will-fix"),
		hcFinding("a.go", "low", "Unused var", "x is never read", "agree-won't-fix"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 1 {
		t.Fatalf("clusters = %+v, want 1", hc.Clusters)
	}
	want := "[high] Missing nil check — agree-will-fix\nfoo() may return nil" +
		"\n\n" +
		"[low] Unused var — agree-won't-fix\nx is never read"
	if hc.Clusters[0].FailureText != want {
		t.Errorf("failureText =\n%q\nwant\n%q", hc.Clusters[0].FailureText, want)
	}
}

// TestShipStateHardenClusters_FailureTextQuoteSafe pins the injection guard:
// a finding whose text tries to close the quoted --failure-text argument and
// append --auto comes back with no double quote and no backslash, so it
// cannot leave the quoted value.
func TestShipStateHardenClusters_FailureTextQuoteSafe(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-quote")

	hc, err := hardenClustersCall(t, dir, "feat/hc-quote", []any{
		hcFinding("a.go", "high", `bad "title"`, `foo\" --auto --skill x "`, "agree-will-fix"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	got := hc.Clusters[0].FailureText
	if strings.ContainsAny(got, "\"\\") {
		t.Errorf("failureText = %q, want no double quote and no backslash", got)
	}
	want := "[high] bad 'title' — agree-will-fix\nfoo/' --auto --skill x '"
	if got != want {
		t.Errorf("failureText = %q, want %q", got, want)
	}
}

func TestShipStateHardenClusters_FailureTextCapped(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-cap-text")

	longBody := strings.Repeat("x", 5000)
	hc, err := hardenClustersCall(t, dir, "feat/hc-cap-text", []any{
		hcFinding("a.go", "high", "t", longBody, "agree-will-fix"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	full := "[high] t — agree-will-fix\n" + longBody
	want := string([]rune(full)[:4096])
	got := hc.Clusters[0].FailureText
	if len([]rune(got)) != 4096 {
		t.Errorf("failureText rune length = %d, want 4096", len([]rune(got)))
	}
	if got != want {
		t.Errorf("failureText was not the first 4096 runes of the untruncated text")
	}
}

// ---------------------------------------------------------------------------
// alreadyHardened
// ---------------------------------------------------------------------------

func TestShipStateHardenClusters_AlreadyHardenedNoShipState(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir := t.TempDir() // no ship_state init at all for this branch

	hc, err := hardenClustersCall(t, dir, "feat/hc-no-state", []any{
		hcFinding("a.go", "high", "t", "b", "agree-will-fix"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 1 || hc.Clusters[0].AlreadyHardened {
		t.Fatalf("clusters = %+v, want 1 cluster with alreadyHardened false", hc.Clusters)
	}
}

func TestShipStateHardenClusters_AlreadyHardenedMatchesEitherPhase(t *testing.T) {
	for _, phase := range []string{"started", "done"} {
		t.Run(phase, func(t *testing.T) {
			stubCleanSurfaceStatus(t)
			branch := "feat/hc-hardened-" + phase
			dir, _ := hardenClustersFixture(t, branch)

			trigger := "[high] t — agree-will-fix\nb"
			healingCall(t, dir, branch, healingHardenedDetail(map[string]any{
				"phase": phase, "trigger": trigger, "applied": []any{}, "skipped": float64(0),
			}))

			hc, err := hardenClustersCall(t, dir, branch, []any{
				hcFinding("a.go", "high", "t", "b", "agree-will-fix"),
			})
			if err != nil {
				t.Fatalf("harden_clusters: %v", err)
			}
			if len(hc.Clusters) != 1 || !hc.Clusters[0].AlreadyHardened {
				t.Fatalf("clusters = %+v, want 1 cluster with alreadyHardened true (phase %s)", hc.Clusters, phase)
			}
		})
	}
}

func TestShipStateHardenClusters_AlreadyHardenedNoMatch(t *testing.T) {
	stubCleanSurfaceStatus(t)
	branch := "feat/hc-hardened-nomatch"
	dir, _ := hardenClustersFixture(t, branch)

	healingCall(t, dir, branch, healingHardenedDetail(map[string]any{
		"phase": "done", "trigger": "totally different failure", "applied": []any{}, "skipped": float64(0),
	}))

	hc, err := hardenClustersCall(t, dir, branch, []any{
		hcFinding("a.go", "high", "t", "b", "agree-will-fix"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 1 || hc.Clusters[0].AlreadyHardened {
		t.Fatalf("clusters = %+v, want alreadyHardened false — trigger does not match", hc.Clusters)
	}
}

// ---------------------------------------------------------------------------
// next
// ---------------------------------------------------------------------------

func TestShipStateHardenClusters_Next(t *testing.T) {
	t.Run("no clusters", func(t *testing.T) {
		stubCleanSurfaceStatus(t)
		dir, _ := hardenClustersFixture(t, "feat/hc-next-none")
		hc, err := hardenClustersCall(t, dir, "feat/hc-next-none", []any{})
		if err != nil {
			t.Fatalf("harden_clusters: %v", err)
		}
		if hc.Next != "no clusters — skip" {
			t.Errorf("next = %q, want %q", hc.Next, "no clusters — skip")
		}
	})

	t.Run("some pending", func(t *testing.T) {
		stubCleanSurfaceStatus(t)
		branch := "feat/hc-next-pending"
		dir, _ := hardenClustersFixture(t, branch)
		trigger := "[high] t — agree-will-fix\nb"
		healingCall(t, dir, branch, healingHardenedDetail(map[string]any{
			"phase": "done", "trigger": trigger, "applied": []any{}, "skipped": float64(0),
		}))
		hc, err := hardenClustersCall(t, dir, branch, []any{
			hcFinding("hardened.go", "high", "t", "b", "agree-will-fix"),
			hcFinding("pending.go", "high", "other", "other body", "agree-will-fix"),
		})
		if err != nil {
			t.Fatalf("harden_clusters: %v", err)
		}
		if hc.Next != "dispatch 1 cluster(s) with alreadyHardened:false, one at a time" {
			t.Errorf("next = %q, want dispatch 1", hc.Next)
		}
	})

	t.Run("all already hardened", func(t *testing.T) {
		stubCleanSurfaceStatus(t)
		branch := "feat/hc-next-allhardened"
		dir, _ := hardenClustersFixture(t, branch)
		trigger := "[high] t — agree-will-fix\nb"
		healingCall(t, dir, branch, healingHardenedDetail(map[string]any{
			"phase": "done", "trigger": trigger, "applied": []any{}, "skipped": float64(0),
		}))
		hc, err := hardenClustersCall(t, dir, branch, []any{
			hcFinding("hardened.go", "high", "t", "b", "agree-will-fix"),
		})
		if err != nil {
			t.Fatalf("harden_clusters: %v", err)
		}
		if hc.Next != "all clusters already hardened — commit leftover edits" {
			t.Errorf("next = %q, want all-already-hardened message", hc.Next)
		}
	})
}

// ---------------------------------------------------------------------------
// dirtySurfaces
// ---------------------------------------------------------------------------

func TestShipStateHardenClusters_DirtySurfaces(t *testing.T) {
	useHardenSurfaceStatus(t, func(string) (string, error) {
		return strings.Join([]string{
			" M .sdlc-v2/config.toml",
			"?? .sdlc-v2/review-dimensions/new-dim.md",
			" M unrelated/file.go",
			`R  old/instructions.md -> .github/instructions/new.instructions.md`,
		}, "\n"), nil
	})
	dir, _ := hardenClustersFixture(t, "feat/hc-dirty")

	hc, err := hardenClustersCall(t, dir, "feat/hc-dirty", []any{})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	want := []string{".sdlc-v2/config.toml", ".sdlc-v2/review-dimensions", ".github/instructions"}
	if strings.Join(hc.DirtySurfaces, ",") != strings.Join(want, ",") {
		t.Errorf("dirtySurfaces = %v, want %v", hc.DirtySurfaces, want)
	}
}

func TestShipStateHardenClusters_DirtySurfacesClean(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-clean")

	hc, err := hardenClustersCall(t, dir, "feat/hc-clean", []any{})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.DirtySurfaces) != 0 {
		t.Errorf("dirtySurfaces = %v, want none", hc.DirtySurfaces)
	}
}

func TestShipStateHardenClusters_StatusErrorIsDomainError(t *testing.T) {
	useHardenSurfaceStatus(t, func(string) (string, error) {
		return "", errStubStatusFailed
	})
	dir, _ := hardenClustersFixture(t, "feat/hc-status-err")

	_, err := hardenClustersCall(t, dir, "feat/hc-status-err", []any{})
	if err == nil {
		t.Fatal("expected an error when git status fails")
	}
	if got := errorClassOf(err); got != "domain" {
		t.Errorf("error class = %q, want domain", got)
	}
	if hint := suggestionOf(err); hint == "" {
		t.Error("Suggestion is empty, want a recovery hint")
	}
}

// ---------------------------------------------------------------------------
// Never-null arrays
// ---------------------------------------------------------------------------

func TestShipStateHardenClusters_NeverNullArrays(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-nonnull")

	hc, err := hardenClustersCall(t, dir, "feat/hc-nonnull", []any{})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	raw, err := json.Marshal(hc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"clusters", "suppressed", "loneDisagree", "dirtySurfaces"} {
		if decoded[key] == nil {
			t.Errorf("%s serialized as null, want []", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Input validation
// ---------------------------------------------------------------------------

func TestShipStateHardenClusters_Rejections(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-reject")

	cases := []struct {
		name    string
		detail  map[string]any
		wantMsg string
	}{
		{
			name:    "missing findings",
			detail:  map[string]any{},
			wantMsg: "detail.findings is required",
		},
		{
			name:    "findings not an array",
			detail:  map[string]any{"findings": "nope"},
			wantMsg: "detail.findings must be an array",
		},
		{
			name:    "item not an object",
			detail:  map[string]any{"findings": []any{5}},
			wantMsg: "detail.findings[0] must be an object",
		},
		{
			name:    "empty file",
			detail:  map[string]any{"findings": []any{hcFinding("", "high", "t", "b", "agree-will-fix")}},
			wantMsg: "detail.findings[0].file is required",
		},
		{
			name:    "bad severity",
			detail:  map[string]any{"findings": []any{hcFinding("a.go", "urgent", "t", "b", "agree-will-fix")}},
			wantMsg: "not a recognised review severity",
		},
		{
			name:    "bad verdict",
			detail:  map[string]any{"findings": []any{hcFinding("a.go", "high", "t", "b", "maybe")}},
			wantMsg: "not a recognised verdict",
		},
		{
			name:    "bad reason",
			detail:  map[string]any{"findings": []any{hcFindingReason("a.go", "high", "t", "b", "agree-will-fix", "just because")}},
			wantMsg: "not a recognised deferral reason",
		},
		{
			name: "reason wrong type",
			detail: map[string]any{"findings": []any{
				func() map[string]any {
					m := hcFinding("a.go", "high", "t", "b", "agree-will-fix")
					m["reason"] = 5
					return m
				}(),
			}},
			wantMsg: "reason must be a string",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := map[string]any{"branch": "feat/hc-reject"}
			for k, v := range tc.detail {
				d[k] = v
			}
			_, err := shipState(dir, dir, ShipStateIn{Action: "harden_clusters", Detail: d}, fixedNow(time.Now()))
			if err == nil {
				t.Fatalf("expected an error for %s", tc.name)
			}
			if got := errorClassOf(err); got != "domain" {
				t.Errorf("error class = %q, want domain", got)
			}
			if hint := suggestionOf(err); hint == "" {
				t.Error("Suggestion is empty, want a recovery hint")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

// errStubStatusFailed is a fixed sentinel error for the git-status-failure
// test, so the failure is never mistaken for a real git error.
var errStubStatusFailed = &stubStatusError{}

type stubStatusError struct{}

func (*stubStatusError) Error() string { return "stub: git status failed" }
