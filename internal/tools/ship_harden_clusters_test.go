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

// hcFindingWith builds a valid detail.findings entry for a.go with one extra
// key set to value, for tests that feed one field a value of the wrong type.
func hcFindingWith(key string, value any) map[string]any {
	m := hcFinding("a.go", "high", "t", "b", "agree-will-fix")
	m[key] = value
	return m
}

// hcFindingDeferred builds one detail.findings entry with a reason and the
// id of the deferred row the finding is stored in.
func hcFindingDeferred(file, severity, title, body, verdict, reason, deferredID string) map[string]any {
	m := hcFindingReason(file, severity, title, body, verdict, reason)
	m["deferredId"] = deferredID
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

// TestShipStateHardenClusters_BelowThresholdFixNow checks that a file with
// only a below-threshold finding forms no cluster, that the finding is
// reported in fixNow, and that the same finding joins the cluster of its
// file when the file has another finding that is not below-threshold.
func TestShipStateHardenClusters_BelowThresholdFixNow(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-below")

	hc, err := hardenClustersCall(t, dir, "feat/hc-below", []any{
		hcFindingDeferred("only.go", "info", "minor nit", "body", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-1"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 0 || len(hc.FixNow) != 1 {
		t.Fatalf("a below-threshold-only file must form no cluster and list one fixNow finding, got clusters=%v fixNow=%v",
			hc.Clusters, hc.FixNow)
	}
	if len(hc.Suppressed) != 0 || len(hc.LoneDisagree) != 0 {
		t.Errorf("suppressed/loneDisagree = %v/%v, want both empty", hc.Suppressed, hc.LoneDisagree)
	}
	if want := "no clusters to harden. Run one fix pass for 1 fix-now finding(s)"; hc.Next != want {
		t.Errorf("next = %q, want %q", hc.Next, want)
	}

	// Mixed with another finding on the same file: the below-threshold
	// finding joins the cluster and is listed in fixNow too.
	hc, err = hardenClustersCall(t, dir, "feat/hc-below", []any{
		hcFindingDeferred("mixed.go", "info", "minor nit", "body", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-2"),
		hcFinding("mixed.go", "high", "real issue", "body2", "agree-will-fix"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 1 || len(hc.Clusters[0].Findings) != 2 {
		t.Fatalf("clusters = %+v, want exactly 1 cluster with 2 findings", hc.Clusters)
	}
	if hc.Clusters[0].Findings[0].DeferredID != "review-deferred-x-2" || hc.Clusters[0].Findings[1].Title != "real issue" {
		t.Errorf("cluster findings = %+v, want the below-threshold finding first, then the real issue", hc.Clusters[0].Findings)
	}
	if len(hc.FixNow) != 1 || hc.FixNow[0].DeferredID != "review-deferred-x-2" {
		t.Errorf("fixNow = %+v, want the one below-threshold finding", hc.FixNow)
	}
}

// TestShipStateHardenClusters_BelowThresholdOnlyFileAmongOthers checks the
// per-file rule of the cluster build: a file with only below-threshold
// findings forms no cluster while another file in the same call still does.
func TestShipStateHardenClusters_BelowThresholdOnlyFileAmongOthers(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-below-mixed-files")

	hc, err := hardenClustersCall(t, dir, "feat/hc-below-mixed-files", []any{
		hcFindingDeferred("small.go", "info", "nit", "b", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-1"),
		hcFinding("big.go", "high", "real", "b", "agree-will-fix"),
		hcFindingDeferred("big.go", "info", "nit 2", "b", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-2"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 1 || hc.Clusters[0].Key != "big.go" || len(hc.Clusters[0].Findings) != 2 {
		t.Fatalf("clusters = %+v, want one big.go cluster with 2 findings", hc.Clusters)
	}
	if len(hc.FixNow) != 2 {
		t.Errorf("fixNow = %+v, want both below-threshold findings", hc.FixNow)
	}
	if want := "dispatch 1 cluster(s), then run one fix pass for 2 fix-now finding(s)"; hc.Next != want {
		t.Errorf("next = %q, want %q", hc.Next, want)
	}
}

// TestShipStateHardenClusters_DisagreeWithBelowThresholdClusters checks that a
// below-threshold finding joins the cluster of a file whose other finding is
// a disagree finding, in either input order. The two findings make a cluster
// of two, so the file is not a lone-disagree file. The disagree finding is
// listed in evidenceOnly and the below-threshold finding without a deferredId
// in staysDeferred.
func TestShipStateHardenClusters_DisagreeWithBelowThresholdClusters(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-disagree-below")

	disagree := hcFinding("x.go", "high", "disputed", "b1", "disagree")
	below := hcFindingReason("x.go", "info", "nit", "b2", "not-evaluated", history.ReasonBelowThreshold)

	for _, tc := range []struct {
		name  string
		input []any
		first string
	}{
		{"disagree first", []any{disagree, below}, "disputed"},
		{"below-threshold first", []any{below, disagree}, "nit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hc, err := hardenClustersCall(t, dir, "feat/hc-disagree-below", tc.input)
			if err != nil {
				t.Fatalf("harden_clusters: %v", err)
			}
			if len(hc.Clusters) != 1 || hc.Clusters[0].Key != "x.go" || len(hc.Clusters[0].Findings) != 2 {
				t.Fatalf("clusters = %+v, want one x.go cluster with 2 findings", hc.Clusters)
			}
			if got := hc.Clusters[0].Findings[0].Title; got != tc.first {
				t.Errorf("first cluster finding = %q, want %q (input order)", got, tc.first)
			}
			if len(hc.LoneDisagree) != 0 || len(hc.Suppressed) != 0 {
				t.Errorf("loneDisagree/suppressed = %v/%v, want both empty", hc.LoneDisagree, hc.Suppressed)
			}
			if len(hc.EvidenceOnly) != 1 || hc.EvidenceOnly[0].Title != "disputed" {
				t.Errorf("evidenceOnly = %+v, want the disagree finding", hc.EvidenceOnly)
			}
			if len(hc.StaysDeferred) != 1 || hc.StaysDeferred[0].Title != "nit" {
				t.Errorf("staysDeferred = %+v, want the below-threshold finding", hc.StaysDeferred)
			}
			if len(hc.FixNow) != 0 {
				t.Errorf("fixNow = %+v, want none", hc.FixNow)
			}
			if want := "dispatch 1 cluster(s) with alreadyHardened:false, one at a time"; hc.Next != want {
				t.Errorf("next = %q, want %q", hc.Next, want)
			}
		})
	}
}

// TestShipStateHardenClusters_TwoBelowThresholdNoCluster checks that a file
// whose findings are all below-threshold forms no cluster, however many there
// are, and is not a lone-disagree file. With a deferredId the finding goes to
// fixNow, and without one to staysDeferred.
func TestShipStateHardenClusters_TwoBelowThresholdNoCluster(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-two-below")

	hc, err := hardenClustersCall(t, dir, "feat/hc-two-below", []any{
		hcFindingDeferred("y.go", "info", "nit 1", "b1", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-y-1"),
		hcFindingReason("y.go", "info", "nit 2", "b2", "not-evaluated", history.ReasonBelowThreshold),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 0 {
		t.Fatalf("clusters = %+v, want none: two below-threshold findings must not form a cluster", hc.Clusters)
	}
	if len(hc.LoneDisagree) != 0 || len(hc.Suppressed) != 0 {
		t.Errorf("loneDisagree/suppressed = %v/%v, want both empty", hc.LoneDisagree, hc.Suppressed)
	}
	if len(hc.FixNow) != 1 || hc.FixNow[0].Title != "nit 1" {
		t.Errorf("fixNow = %+v, want the finding with a deferredId", hc.FixNow)
	}
	if len(hc.StaysDeferred) != 1 || hc.StaysDeferred[0].Title != "nit 2" {
		t.Errorf("staysDeferred = %+v, want the finding without a deferredId", hc.StaysDeferred)
	}
	if len(hc.EvidenceOnly) != 0 {
		t.Errorf("evidenceOnly = %+v, want none", hc.EvidenceOnly)
	}
	if want := "no clusters to harden. Run one fix pass for 1 fix-now finding(s)"; hc.Next != want {
		t.Errorf("next = %q, want %q", hc.Next, want)
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

// TestShipStateHardenClusters_AlreadyHardenedIgnoresTrailingNewline checks
// that a trigger recorded without the trailing newline still matches. A
// finding with an empty body gives a FailureText that ends in a newline,
// and rendered tool output drops it, so harden records the trigger without
// it.
func TestShipStateHardenClusters_AlreadyHardenedIgnoresTrailingNewline(t *testing.T) {
	stubCleanSurfaceStatus(t)
	branch := "feat/hc-hardened-newline"
	dir, _ := hardenClustersFixture(t, branch)

	healingCall(t, dir, branch, healingHardenedDetail(map[string]any{
		"phase": "done", "trigger": "[high] t — agree-will-fix", "applied": []any{}, "skipped": float64(0),
	}))

	hc, err := hardenClustersCall(t, dir, branch, []any{
		hcFinding("a.go", "high", "t", "", "agree-will-fix"),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 1 {
		t.Fatalf("clusters = %+v, want 1", hc.Clusters)
	}
	if !strings.HasSuffix(hc.Clusters[0].FailureText, "\n") {
		t.Fatalf("FailureText = %q, want a trailing newline (the case under test)", hc.Clusters[0].FailureText)
	}
	if !hc.Clusters[0].AlreadyHardened {
		t.Errorf("alreadyHardened = false, want true — the trigger differs only by the trailing newline")
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

	t.Run("clusters to run and fix-now findings", func(t *testing.T) {
		stubCleanSurfaceStatus(t)
		dir, _ := hardenClustersFixture(t, "feat/hc-next-pending-fixnow")
		hc, err := hardenClustersCall(t, dir, "feat/hc-next-pending-fixnow", []any{
			hcFinding("a.go", "high", "t", "b", "agree-will-fix"),
			hcFinding("b.go", "high", "t", "b", "agree-will-fix"),
			hcFindingDeferred("c.go", "info", "nit", "b", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-1"),
		})
		if err != nil {
			t.Fatalf("harden_clusters: %v", err)
		}
		if want := "dispatch 2 cluster(s), then run one fix pass for 1 fix-now finding(s)"; hc.Next != want {
			t.Errorf("next = %q, want %q", hc.Next, want)
		}
	})

	t.Run("no clusters and fix-now findings", func(t *testing.T) {
		stubCleanSurfaceStatus(t)
		dir, _ := hardenClustersFixture(t, "feat/hc-next-none-fixnow")
		hc, err := hardenClustersCall(t, dir, "feat/hc-next-none-fixnow", []any{
			hcFindingDeferred("a.go", "info", "nit", "b", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-1"),
			hcFindingDeferred("b.go", "info", "nit", "b", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-2"),
		})
		if err != nil {
			t.Fatalf("harden_clusters: %v", err)
		}
		if want := "no clusters to harden. Run one fix pass for 2 fix-now finding(s)"; hc.Next != want {
			t.Errorf("next = %q, want %q", hc.Next, want)
		}
	})

	t.Run("no clusters and only stays-deferred findings", func(t *testing.T) {
		stubCleanSurfaceStatus(t)
		dir, _ := hardenClustersFixture(t, "feat/hc-next-none-deferred")
		hc, err := hardenClustersCall(t, dir, "feat/hc-next-none-deferred", []any{
			hcFindingReason("a.go", "info", "nit", "b", "not-evaluated", history.ReasonBelowThreshold),
		})
		if err != nil {
			t.Fatalf("harden_clusters: %v", err)
		}
		if hc.Next != "no clusters — skip" {
			t.Errorf("next = %q, want %q", hc.Next, "no clusters — skip")
		}
	})

	t.Run("all already hardened and fix-now findings", func(t *testing.T) {
		stubCleanSurfaceStatus(t)
		branch := "feat/hc-next-allhardened-fixnow"
		dir, _ := hardenClustersFixture(t, branch)
		trigger := "[high] t — agree-will-fix\nb"
		healingCall(t, dir, branch, healingHardenedDetail(map[string]any{
			"phase": "done", "trigger": trigger, "applied": []any{}, "skipped": float64(0),
		}))
		hc, err := hardenClustersCall(t, dir, branch, []any{
			hcFinding("hardened.go", "high", "t", "b", "agree-will-fix"),
			hcFindingDeferred("other.go", "info", "nit", "b", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-1"),
		})
		if err != nil {
			t.Fatalf("harden_clusters: %v", err)
		}
		want := "all clusters already hardened — commit leftover edits, then run one fix pass for 1 fix-now finding(s)"
		if hc.Next != want {
			t.Errorf("next = %q, want %q", hc.Next, want)
		}
	})
}

// ---------------------------------------------------------------------------
// fixNow, staysDeferred and evidenceOnly
// ---------------------------------------------------------------------------

// TestShipStateHardenClusters_ListAssignment checks which output list each
// kind of finding lands in: exactly one of fixNow, staysDeferred or
// evidenceOnly, or none. The reason of a finding decides; with no reason the
// verdict decides.
func TestShipStateHardenClusters_ListAssignment(t *testing.T) {
	deferred := func(verdict, reason string, fixed bool) map[string]any {
		m := hcFindingDeferred("a.go", "info", "t", "b", verdict, reason, "review-deferred-x-1")
		m["fixedInRun"] = fixed
		return m
	}
	noID := func(verdict, reason string, fixed bool) map[string]any {
		m := hcFindingReason("a.go", "info", "t", "b", verdict, reason)
		m["fixedInRun"] = fixed
		return m
	}

	const (
		none          = ""
		fixNow        = "fixNow"
		staysDeferred = "staysDeferred"
		evidenceOnly  = "evidenceOnly"
	)
	cases := []struct {
		name    string
		finding map[string]any
		want    string
	}{
		{"below-threshold with deferredId, not fixed", deferred("not-evaluated", history.ReasonBelowThreshold, false), fixNow},
		{"below-threshold with deferredId, fixed in run", deferred("not-evaluated", history.ReasonBelowThreshold, true), none},
		{"below-threshold without deferredId, not fixed", noID("not-evaluated", history.ReasonBelowThreshold, false), staysDeferred},
		{"below-threshold without deferredId, fixed in run", noID("not-evaluated", history.ReasonBelowThreshold, true), staysDeferred},
		{"reason below-threshold wins over verdict disagree", deferred("disagree", history.ReasonBelowThreshold, false), fixNow},
		{"reason needs-direction", noID("needs-direction", history.ReasonNeedsDirection, false), staysDeferred},
		{"reason needs-direction with deferredId", deferred("needs-direction", history.ReasonNeedsDirection, false), staysDeferred},
		{"reason needs-direction wins over verdict agree-will-fix", noID("agree-will-fix", history.ReasonNeedsDirection, false), staysDeferred},
		{"verdict needs-direction without reason", hcFinding("a.go", "info", "t", "b", "needs-direction"), staysDeferred},
		{"reason disagree", noID("agree-will-fix", history.ReasonDisagree, false), evidenceOnly},
		{"reason wont-fix", noID("agree-will-fix", history.ReasonWontFix, false), evidenceOnly},
		{"reason wont-fix with deferredId, fixed in run", deferred("agree-will-fix", history.ReasonWontFix, true), evidenceOnly},
		{"verdict disagree without reason", hcFinding("a.go", "info", "t", "b", "disagree"), evidenceOnly},
		{"verdict agree-won't-fix without reason", hcFinding("a.go", "info", "t", "b", "agree-won't-fix"), evidenceOnly},
		{"verdict agree-will-fix without reason", hcFinding("a.go", "info", "t", "b", "agree-will-fix"), none},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubCleanSurfaceStatus(t)
			dir, _ := hardenClustersFixture(t, "feat/hc-lists")

			hc, err := hardenClustersCall(t, dir, "feat/hc-lists", []any{tc.finding})
			if err != nil {
				t.Fatalf("harden_clusters: %v", err)
			}
			got := map[string]int{
				fixNow:        len(hc.FixNow),
				staysDeferred: len(hc.StaysDeferred),
				evidenceOnly:  len(hc.EvidenceOnly),
			}
			for list, n := range got {
				want := 0
				if list == tc.want {
					want = 1
				}
				if n != want {
					t.Errorf("%s has %d finding(s), want %d (all lists: %v)", list, n, want, got)
				}
			}
		})
	}
}

// TestShipStateHardenClusters_ListsKeepInputOrderAndFields checks that each
// list holds the full finding, including the optional fields, in input order.
func TestShipStateHardenClusters_ListsKeepInputOrderAndFields(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-list-fields")

	first := hcFindingDeferred("a.go", "info", "first", "b1", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-1")
	first["line"] = float64(12)
	second := hcFindingDeferred("b.go", "info", "second", "b2", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-2")
	second["line"] = 40

	hc, err := hardenClustersCall(t, dir, "feat/hc-list-fields", []any{first, second})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.FixNow) != 2 {
		t.Fatalf("fixNow = %+v, want 2 findings", hc.FixNow)
	}
	want := []HardenClusterFinding{
		{File: "a.go", Severity: "info", Title: "first", Body: "b1", Verdict: "not-evaluated", Reason: history.ReasonBelowThreshold, Line: 12, DeferredID: "review-deferred-x-1"},
		{File: "b.go", Severity: "info", Title: "second", Body: "b2", Verdict: "not-evaluated", Reason: history.ReasonBelowThreshold, Line: 40, DeferredID: "review-deferred-x-2"},
	}
	for i := range want {
		if hc.FixNow[i] != want[i] {
			t.Errorf("fixNow[%d] = %+v, want %+v", i, hc.FixNow[i], want[i])
		}
	}
}

// TestShipStateHardenClusters_ListsDoNotChangeClusters checks that a finding
// in evidenceOnly or staysDeferred still counts as a member of its cluster,
// so the lists add information without changing the clusters.
func TestShipStateHardenClusters_ListsDoNotChangeClusters(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-lists-clusters")

	hc, err := hardenClustersCall(t, dir, "feat/hc-lists-clusters", []any{
		hcFinding("a.go", "high", "real", "b", "agree-will-fix"),
		hcFindingReason("a.go", "medium", "wrong", "b", "agree-will-fix", history.ReasonWontFix),
		hcFindingReason("a.go", "medium", "unclear", "b", "agree-will-fix", history.ReasonNeedsDirection),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 1 || len(hc.Clusters[0].Findings) != 3 {
		t.Fatalf("clusters = %+v, want one a.go cluster with 3 findings", hc.Clusters)
	}
	if len(hc.EvidenceOnly) != 1 || len(hc.StaysDeferred) != 1 || len(hc.FixNow) != 0 {
		t.Errorf("lists = evidenceOnly %d, staysDeferred %d, fixNow %d, want 1, 1, 0",
			len(hc.EvidenceOnly), len(hc.StaysDeferred), len(hc.FixNow))
	}
}

// TestShipStateHardenClusters_FailureTextSameAfterFixPass checks that a fix
// pass does not change the cluster text. The same input with fixedInRun false
// and then true gives the same failureText, so a cluster that harden already
// ran on keeps its alreadyHardened mark on a resumed run.
func TestShipStateHardenClusters_FailureTextSameAfterFixPass(t *testing.T) {
	stubCleanSurfaceStatus(t)
	branch := "feat/hc-resume"
	dir, _ := hardenClustersFixture(t, branch)

	input := func(fixed bool) []any {
		nit := hcFindingDeferred("mixed.go", "info", "nit", "nit body", "not-evaluated", history.ReasonBelowThreshold, "review-deferred-x-1")
		nit["fixedInRun"] = fixed
		return []any{nit, hcFinding("mixed.go", "high", "real", "real body", "agree-will-fix")}
	}

	before, err := hardenClustersCall(t, dir, branch, input(false))
	if err != nil {
		t.Fatalf("harden_clusters before the fix pass: %v", err)
	}
	if len(before.Clusters) != 1 || before.Clusters[0].AlreadyHardened {
		t.Fatalf("clusters = %+v, want one cluster that is not yet hardened", before.Clusters)
	}
	if len(before.FixNow) != 1 {
		t.Fatalf("fixNow before the fix pass = %+v, want 1 finding", before.FixNow)
	}

	healingCall(t, dir, branch, healingHardenedDetail(map[string]any{
		"phase": "done", "trigger": before.Clusters[0].FailureText, "applied": []any{}, "skipped": float64(0),
	}))

	after, err := hardenClustersCall(t, dir, branch, input(true))
	if err != nil {
		t.Fatalf("harden_clusters after the fix pass: %v", err)
	}
	if len(after.Clusters) != 1 {
		t.Fatalf("clusters after the fix pass = %+v, want 1", after.Clusters)
	}
	if after.Clusters[0].FailureText != before.Clusters[0].FailureText {
		t.Errorf("failureText changed after the fix pass:\nbefore %q\nafter  %q", before.Clusters[0].FailureText, after.Clusters[0].FailureText)
	}
	if !after.Clusters[0].AlreadyHardened {
		t.Error("alreadyHardened = false after the fix pass, want true")
	}
	if len(after.FixNow) != 0 {
		t.Errorf("fixNow after the fix pass = %+v, want none", after.FixNow)
	}
	if after.Next != "all clusters already hardened — commit leftover edits" {
		t.Errorf("next after the fix pass = %q, want the all-already-hardened message", after.Next)
	}
}

// ---------------------------------------------------------------------------
// Optional finding fields and the not-evaluated verdict
// ---------------------------------------------------------------------------

// TestShipStateHardenClusters_NotEvaluatedAccepted checks that the verdict
// not-evaluated is accepted with the reason below-threshold and that the
// verdict is kept on the finding.
func TestShipStateHardenClusters_NotEvaluatedAccepted(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-notevaluated")

	hc, err := hardenClustersCall(t, dir, "feat/hc-notevaluated", []any{
		hcFinding("a.go", "high", "real", "b", "agree-will-fix"),
		hcFindingReason("a.go", "info", "nit", "b", "not-evaluated", history.ReasonBelowThreshold),
	})
	if err != nil {
		t.Fatalf("harden_clusters: %v", err)
	}
	if len(hc.Clusters) != 1 || len(hc.Clusters[0].Findings) != 2 || hc.Clusters[0].Findings[1].Verdict != "not-evaluated" {
		t.Fatalf("clusters = %+v, want one cluster whose second finding has the verdict not-evaluated", hc.Clusters)
	}
	wantText := "[high] real — agree-will-fix\nb\n\n[info] nit — not-evaluated\nb"
	if hc.Clusters[0].FailureText != wantText {
		t.Errorf("failureText = %q, want %q", hc.Clusters[0].FailureText, wantText)
	}
}

// TestShipStateHardenClusters_OptionalFieldsParsed checks the accepted forms
// of line, deferredId and fixedInRun: a JSON number, a Go int, a whole
// float, an absent key and an explicit null.
func TestShipStateHardenClusters_OptionalFieldsParsed(t *testing.T) {
	cases := []struct {
		name      string
		extra     map[string]any
		wantLine  int
		wantDefID string
		wantFixed bool
	}{
		{name: "absent", extra: map[string]any{}},
		{name: "null values", extra: map[string]any{"line": nil, "deferredId": nil, "fixedInRun": nil}},
		{name: "line as float64", extra: map[string]any{"line": float64(12)}, wantLine: 12},
		{name: "line as int", extra: map[string]any{"line": 7}, wantLine: 7},
		{name: "line as whole float with zero fraction", extra: map[string]any{"line": 2.0}, wantLine: 2},
		{name: "line zero", extra: map[string]any{"line": float64(0)}, wantLine: 0},
		{name: "deferredId", extra: map[string]any{"deferredId": "review-deferred-x-1"}, wantDefID: "review-deferred-x-1"},
		{name: "deferredId empty string", extra: map[string]any{"deferredId": ""}},
		{name: "fixedInRun true", extra: map[string]any{"fixedInRun": true}, wantFixed: true},
		{name: "fixedInRun false", extra: map[string]any{"fixedInRun": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubCleanSurfaceStatus(t)
			dir, _ := hardenClustersFixture(t, "feat/hc-optional")

			f := hcFinding("a.go", "high", "t", "b", "agree-will-fix")
			for k, v := range tc.extra {
				f[k] = v
			}
			hc, err := hardenClustersCall(t, dir, "feat/hc-optional", []any{f})
			if err != nil {
				t.Fatalf("harden_clusters: %v", err)
			}
			if len(hc.Clusters) != 1 || len(hc.Clusters[0].Findings) != 1 {
				t.Fatalf("clusters = %+v, want one cluster with one finding", hc.Clusters)
			}
			got := hc.Clusters[0].Findings[0]
			if got.Line != tc.wantLine || got.DeferredID != tc.wantDefID || got.FixedInRun != tc.wantFixed {
				t.Errorf("finding line/deferredId/fixedInRun = %d/%q/%v, want %d/%q/%v",
					got.Line, got.DeferredID, got.FixedInRun, tc.wantLine, tc.wantDefID, tc.wantFixed)
			}
		})
	}
}

// TestShipStateHardenClusters_NewFieldErrorsHaveRecoverySuggestion checks the
// exact Suggestion text of the error for a not-evaluated verdict with the
// wrong reason and for a wrong type in line, deferredId and fixedInRun.
func TestShipStateHardenClusters_NewFieldErrorsHaveRecoverySuggestion(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-suggestion")

	cases := []struct {
		name       string
		finding    map[string]any
		wantMsg    string
		wantAdvice string
	}{
		{
			name:       "not-evaluated without below-threshold",
			finding:    hcFindingReason("a.go", "info", "t", "b", "not-evaluated", history.ReasonWontFix),
			wantMsg:    `harden_clusters: detail.findings[0].verdict not-evaluated needs reason below-threshold, got "wont-fix"`,
			wantAdvice: "use not-evaluated only for a below-threshold finding, then retry ship_state harden_clusters",
		},
		{
			name:       "line",
			finding:    hcFindingWith("line", "12"),
			wantMsg:    "harden_clusters: detail.findings[0].line must be a whole number, got string",
			wantAdvice: "Set detail.findings[0].line to a whole number, or omit it, then retry ship_state harden_clusters.",
		},
		{
			name:       "deferredId",
			finding:    hcFindingWith("deferredId", true),
			wantMsg:    "harden_clusters: detail.findings[0].deferredId must be a string, got bool",
			wantAdvice: "Set detail.findings[0].deferredId to a string, or omit it, then retry ship_state harden_clusters.",
		},
		{
			name:       "fixedInRun",
			finding:    hcFindingWith("fixedInRun", float64(1)),
			wantMsg:    "harden_clusters: detail.findings[0].fixedInRun must be a boolean, got float64",
			wantAdvice: "Set detail.findings[0].fixedInRun to a boolean, or omit it, then retry ship_state harden_clusters.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := hardenClustersCall(t, dir, "feat/hc-suggestion", []any{tc.finding})
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errorClassOf(err); got != "domain" {
				t.Errorf("error class = %q, want domain", got)
			}
			if err.Error() != tc.wantMsg {
				t.Errorf("error = %q, want %q", err.Error(), tc.wantMsg)
			}
			if got := suggestionOf(err); got != tc.wantAdvice {
				t.Errorf("suggestion = %q, want %q", got, tc.wantAdvice)
			}
		})
	}
}

// TestShipStateHardenClusters_WrongTypeNamesFindingIndex checks that the
// error for a wrong type names the position of the bad finding in the array.
func TestShipStateHardenClusters_WrongTypeNamesFindingIndex(t *testing.T) {
	stubCleanSurfaceStatus(t)
	dir, _ := hardenClustersFixture(t, "feat/hc-index")

	_, err := hardenClustersCall(t, dir, "feat/hc-index", []any{
		hcFinding("a.go", "high", "t", "b", "agree-will-fix"),
		hcFindingWith("line", "x"),
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "detail.findings[1].line") {
		t.Errorf("error = %q, want it to name detail.findings[1].line", err.Error())
	}
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

// TestHardenDirtySurfaces_RealGitFirstEntryModified runs the real
// hardenSurfaceStatus (no seam) against a repo whose only change is an
// unstaged edit to a tracked surface. `git status --porcelain` prints it as
// " M .sdlc-v2/config.toml" — the first line starts with a space, which
// must survive so the path is still read from column 3.
func TestHardenDirtySurfaces_RealGitFirstEntryModified(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	runGit(t, dir, "config", "commit.gpgsign", "false")
	writeRepoFile(t, dir, ".sdlc-v2/config.toml", "schemaVersion = 1\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	writeRepoFile(t, dir, ".sdlc-v2/config.toml", "schemaVersion = 2\n")

	dirty, err := hardenDirtySurfaces(dir)
	if err != nil {
		t.Fatalf("hardenDirtySurfaces: %v", err)
	}
	if strings.Join(dirty, ",") != ".sdlc-v2/config.toml" {
		t.Errorf("dirtySurfaces = %v, want [.sdlc-v2/config.toml]", dirty)
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
	for _, key := range []string{"clusters", "suppressed", "loneDisagree", "dirtySurfaces", "fixNow", "staysDeferred", "evidenceOnly"} {
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
		{
			name:    "not-evaluated without reason",
			detail:  map[string]any{"findings": []any{hcFinding("a.go", "info", "t", "b", "not-evaluated")}},
			wantMsg: "verdict not-evaluated needs reason below-threshold, got \"\"",
		},
		{
			name:    "not-evaluated with needs-direction",
			detail:  map[string]any{"findings": []any{hcFindingReason("a.go", "info", "t", "b", "not-evaluated", history.ReasonNeedsDirection)}},
			wantMsg: "verdict not-evaluated needs reason below-threshold, got \"needs-direction\"",
		},
		{
			name:    "not-evaluated with disagree",
			detail:  map[string]any{"findings": []any{hcFindingReason("a.go", "info", "t", "b", "not-evaluated", history.ReasonDisagree)}},
			wantMsg: "verdict not-evaluated needs reason below-threshold, got \"disagree\"",
		},
		{
			name:    "not-evaluated with wont-fix",
			detail:  map[string]any{"findings": []any{hcFindingReason("a.go", "info", "t", "b", "not-evaluated", history.ReasonWontFix)}},
			wantMsg: "verdict not-evaluated needs reason below-threshold, got \"wont-fix\"",
		},
		{
			name:    "line wrong type",
			detail:  map[string]any{"findings": []any{hcFindingWith("line", "12")}},
			wantMsg: "detail.findings[0].line must be a whole number, got string",
		},
		{
			name:    "line fractional",
			detail:  map[string]any{"findings": []any{hcFindingWith("line", 12.5)}},
			wantMsg: "detail.findings[0].line must be a whole number, got float64",
		},
		{
			name:    "deferredId wrong type",
			detail:  map[string]any{"findings": []any{hcFindingWith("deferredId", 7)}},
			wantMsg: "detail.findings[0].deferredId must be a string, got int",
		},
		{
			name:    "fixedInRun wrong type",
			detail:  map[string]any{"findings": []any{hcFindingWith("fixedInRun", "true")}},
			wantMsg: "detail.findings[0].fixedInRun must be a boolean, got string",
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
