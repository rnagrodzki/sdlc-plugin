package tools

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// dashHardenSurface is a harden surface id of the test records.
const dashHardenSurface = "review-dimensions"

// dashHardenApplied returns one old-form applied entry (no rationale, no
// patch) whose targetFile ends with i.
func dashHardenApplied(i int) map[string]any {
	return map[string]any{"surface": dashHardenSurface, "action": "add", "targetFile": fmt.Sprintf("docs/f%d.md", i)}
}

// dashHardenHeld returns one held entry with reason; a pending-stored entry
// gets deferredId d1.
func dashHardenHeld(reason string) map[string]any {
	m := map[string]any{"surface": dashHardenSurface, "action": "strengthen", "targetFile": "docs/held.md", "reason": reason}
	if reason == healingHeldPendingStored {
		m["deferredId"] = "d1"
	}
	return m
}

// dashHardenRecord returns a run record. A nil held gives no held key, as in
// an old-form record.
func dashHardenRecord(trigger, phase string, applied, held []any) map[string]any {
	rec := map[string]any{
		"phase":          phase,
		"trigger":        trigger,
		"classification": "user-code",
		"applied":        applied,
		"skipped":        2,
		"recordedAt":     "2026-10-07T08:50:00Z",
	}
	if held != nil {
		rec["held"] = held
	}
	return rec
}

// dashHardenData returns ship data with healing.hardened set to hardened.
func dashHardenData(hardened any) map[string]any {
	return map[string]any{"healing": map[string]any{"hardened": hardened}}
}

// dashHardenBuild calls dashboardHardenDetail with a harden step of status.
func dashHardenBuild(data map[string]any, status string, runCompleted bool) (*DashboardHarden, []DashboardIssue) {
	return dashboardHardenDetail(data, &DashboardStep{Name: dashboardShipStepHarden, Status: status}, runCompleted)
}

// dashHardenShipData returns the join ship data with a harden step in
// progress after the review step, and edit applied.
func dashHardenShipData(edit func(map[string]any)) map[string]any {
	data := dashJoinShipData()
	steps := data["steps"].([]any)
	steps[2].(map[string]any)["status"] = StepCompleted
	data["steps"] = append(steps, map[string]any{"name": "harden", "status": StepInProgress, "startedAt": "2026-10-07T08:50:00Z"})
	if edit != nil {
		edit(data)
	}
	return data
}

// dashHardenShip writes the ship data of dashHardenShipData to a new root and
// returns its only pipeline.
func dashHardenShip(t *testing.T, edit func(map[string]any)) DashboardPipeline {
	t.Helper()
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashHardenShipData(edit), dashJoinFresh)
	return dashOne(t, root)
}

// dashHardenOf returns the harden detail of the harden step of p, or fails.
func dashHardenOf(t *testing.T, p DashboardPipeline) *DashboardHarden {
	t.Helper()
	s := dashJoinStep(t, p, "harden")
	if s.Detail == nil || s.Detail.Kind != dashboardKindHarden || s.Detail.Harden == nil {
		t.Fatalf("harden detail = %+v, want kind %q with harden", s.Detail, dashboardKindHarden)
	}
	return s.Detail.Harden
}

// dashHardenJSON returns v as a JSON object.
func dashHardenJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// dashHardenCheckLists fails when a list of h marshals to anything but a
// JSON array.
func dashHardenCheckLists(t *testing.T, h *DashboardHarden) {
	t.Helper()
	m := dashHardenJSON(t, h)
	for _, key := range []string{"edits", "clusters", "held", "timeline"} {
		if _, ok := m[key].([]any); !ok {
			t.Errorf("%s = %v, want a JSON array", key, m[key])
		}
	}
}

// dashHardenIssues returns the issues of severity in issues.
func dashHardenIssues(issues []DashboardIssue, severity string) []DashboardIssue {
	var out []DashboardIssue
	for _, is := range issues {
		if is.Severity == severity {
			out = append(out, is)
		}
	}
	return out
}

// TestDashboardHarden_OldForm checks that an old-form record shows edits
// with action, surface and file only, and an empty held list.
func TestDashboardHarden_OldForm(t *testing.T) {
	p := dashHardenShip(t, func(d map[string]any) {
		d["healing"] = map[string]any{"hardened": []any{dashHardenRecord("t1", "done", []any{dashHardenApplied(1)}, nil)}}
	})
	h := dashHardenOf(t, p)
	want := []DashboardHardenEdit{{Surface: dashHardenSurface, Action: "add", TargetFile: "docs/f1.md"}}
	if !reflect.DeepEqual(h.Edits, want) {
		t.Errorf("edits = %+v, want %+v", h.Edits, want)
	}
	edit := dashHardenJSON(t, h.Edits[0])
	for _, key := range []string{"rationale", "patch", "patchCut"} {
		if _, has := edit[key]; has {
			t.Errorf("edit has key %q: %v", key, edit)
		}
	}
	if len(h.Held) != 0 {
		t.Errorf("held = %+v, want []", h.Held)
	}
	dashHardenCheckLists(t, h)
	wantRun := []DashboardHardenRun{{Trigger: "t1", Phase: "done", Classification: "user-code", Applied: 1, Skipped: 2}}
	if !reflect.DeepEqual(h.Clusters, wantRun) {
		t.Errorf("clusters = %+v, want %+v", h.Clusters, wantRun)
	}
	if h.Malformed != 0 || len(dashRRStateIssues(p)) != 0 {
		t.Errorf("malformed = %d, state issues = %+v, want none", h.Malformed, dashRRStateIssues(p))
	}
}

// TestDashboardHarden_NewForm checks that a new-form record shows the
// rationale, the patch and the held rows with their labels.
func TestDashboardHarden_NewForm(t *testing.T) {
	applied := dashHardenApplied(1)
	applied["rationale"] = "the plan missed a check"
	applied["patch"] = "+- check the expiry\n"
	held := []any{
		dashHardenHeld(healingHeldPendingStored),
		dashHardenHeld(healingHeldPendingNotStored),
		dashHardenHeld(healingHeldNotProcessed),
	}
	p := dashHardenShip(t, func(d map[string]any) {
		d["healing"] = map[string]any{"hardened": []any{dashHardenRecord("t1", "done", []any{applied}, held)}}
	})
	h := dashHardenOf(t, p)
	wantEdits := []DashboardHardenEdit{{
		Surface: dashHardenSurface, Action: "add", TargetFile: "docs/f1.md",
		Rationale: "the plan missed a check", Patch: "+- check the expiry\n",
	}}
	if !reflect.DeepEqual(h.Edits, wantEdits) {
		t.Errorf("edits = %+v, want %+v", h.Edits, wantEdits)
	}
	row := func(reason, label, id string) DashboardHardenHeld {
		return DashboardHardenHeld{Surface: dashHardenSurface, Action: "strengthen", TargetFile: "docs/held.md", Reason: reason, Label: label, DeferredID: id}
	}
	wantHeld := []DashboardHardenHeld{
		row(healingHeldPendingStored, "stored as a deferred item", "d1"),
		row(healingHeldPendingNotStored, "not stored — copy it by hand", ""),
		row(healingHeldNotProcessed, "not processed — harden stopped", ""),
	}
	if !reflect.DeepEqual(h.Held, wantHeld) {
		t.Errorf("held = %+v, want %+v", h.Held, wantHeld)
	}
}

// TestDashboardHarden_Malformed checks that each bad record form is skipped
// and counted, with exactly one medium state issue for the pipeline.
func TestDashboardHarden_Malformed(t *testing.T) {
	valid := func() map[string]any { return dashHardenRecord("good", "done", []any{dashHardenApplied(1)}, nil) }
	bad := func(edit func(map[string]any)) map[string]any {
		rec := dashHardenRecord("bad", "done", []any{dashHardenApplied(2)}, []any{dashHardenHeld(healingHeldNotProcessed)})
		edit(rec)
		return rec
	}
	// fieldEdit returns an edit that deletes key from the first entry of the
	// list list ("applied" or "held") of a record.
	fieldEdit := func(list, key string) func(map[string]any) {
		return func(r map[string]any) { delete(r[list].([]any)[0].(map[string]any), key) }
	}
	cases := []struct {
		name      string
		hardened  any
		malformed int
		clusters  int
		issue     string // "" for the issue of records that cannot be read
	}{
		{"hardened not a list", map[string]any{"x": 1}, 1, 0,
			"healing.hardened of the ship state is not a list (found object) — the harden step does not show its run records"},
		{"hardened null", nil, 1, 0,
			"healing.hardened of the ship state is not a list (found null) — the harden step does not show its run records"},
		{"entry not an object", []any{valid(), "x"}, 1, 1, ""},
		{"held entry not an object", []any{valid(), bad(func(r map[string]any) { r["held"] = []any{"x"} })}, 1, 1, ""},
		{"applied entry no surface", []any{valid(), bad(fieldEdit("applied", "surface"))}, 1, 1, ""},
		{"applied entry no action", []any{valid(), bad(fieldEdit("applied", "action"))}, 1, 1, ""},
		{"held entry no action", []any{valid(), bad(fieldEdit("held", "action"))}, 1, 1, ""},
		{"held entry no targetFile", []any{valid(), bad(fieldEdit("held", "targetFile"))}, 1, 1, ""},
		{"phase unknown", []any{valid(), bad(func(r map[string]any) { r["phase"] = "half" })}, 1, 1, ""},
		{"trigger empty", []any{valid(), bad(func(r map[string]any) { r["trigger"] = "" })}, 1, 1, ""},
		{"applied not a list", []any{valid(), bad(func(r map[string]any) { r["applied"] = "x" })}, 1, 1, ""},
		{"applied absent", []any{valid(), bad(func(r map[string]any) { delete(r, "applied") })}, 1, 1, ""},
		{"held not a list", []any{valid(), bad(func(r map[string]any) { r["held"] = map[string]any{} })}, 1, 1, ""},
		{"applied entry not an object", []any{valid(), bad(func(r map[string]any) { r["applied"] = []any{1.0} })}, 1, 1, ""},
		{"applied entry no targetFile", []any{valid(), bad(func(r map[string]any) {
			r["applied"].([]any)[0].(map[string]any)["targetFile"] = ""
		})}, 1, 1, ""},
		{"held entry no surface", []any{valid(), bad(func(r map[string]any) {
			delete(r["held"].([]any)[0].(map[string]any), "surface")
		})}, 1, 1, ""},
		{"held reason unknown", []any{valid(), bad(func(r map[string]any) {
			r["held"].([]any)[0].(map[string]any)["reason"] = "lost"
		})}, 1, 1, ""},
		{"rationale not a string", []any{valid(), bad(func(r map[string]any) {
			r["applied"].([]any)[0].(map[string]any)["rationale"] = 3.0
		})}, 1, 1, ""},
		{"patch not a string", []any{valid(), bad(func(r map[string]any) {
			r["applied"].([]any)[0].(map[string]any)["patch"] = []any{}
		})}, 1, 1, ""},
		{"two bad records", []any{"x", valid(), bad(func(r map[string]any) { r["phase"] = nil })}, 2, 1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, issues := dashHardenBuild(dashHardenData(c.hardened), StepInProgress, false)
			if h == nil {
				t.Fatal("detail = nil, want a detail")
			}
			if h.Malformed != c.malformed || len(h.Clusters) != c.clusters {
				t.Errorf("malformed = %d, clusters = %d, want %d and %d", h.Malformed, len(h.Clusters), c.malformed, c.clusters)
			}
			for _, e := range h.Edits {
				if e.TargetFile != "docs/f1.md" {
					t.Errorf("edit of a bad record shown: %+v", e)
				}
			}
			if len(h.Held) != 0 {
				t.Errorf("held = %+v, want []", h.Held)
			}
			text := c.issue
			if text == "" {
				text = fmt.Sprintf("healing.hardened of the ship state holds %d record(s) that cannot be read — they are not shown in the harden step", c.malformed)
			}
			want := []DashboardIssue{{Source: dashboardSourceState, Severity: "medium", Text: text}}
			if !reflect.DeepEqual(issues, want) {
				t.Errorf("issues = %+v, want %+v", issues, want)
			}
			dashHardenCheckLists(t, h)
		})
	}
}

// TestDashboardHarden_OtherPipelinesUnchanged checks that a malformed harden
// record changes neither a second pipeline of the same repo nor a second
// repo of the same snapshot.
func TestDashboardHarden_OtherPipelinesUnchanged(t *testing.T) {
	clean := func(branch string) map[string]any {
		return dashHardenShipData(func(d map[string]any) {
			d["branch"] = branch
			d["healing"] = map[string]any{"hardened": []any{dashHardenRecord("t1", "done", []any{dashHardenApplied(1)}, nil)}}
		})
	}
	const otherFile = "ship-feat-y-20261007T080000Z.json"

	root1 := dashRoot(t)
	dashWriteState(t, root1, dashJoinShipFile, dashHardenShipData(func(d map[string]any) {
		d["healing"] = map[string]any{"hardened": []any{"x", dashHardenRecord("t1", "started", []any{}, nil)}}
	}), dashJoinFresh)
	dashWriteState(t, root1, otherFile, clean("feat/y"), dashJoinFresh)
	root2 := dashRoot(t)
	dashWriteState(t, root2, otherFile, clean("feat/y"), dashJoinFresh)
	solo := dashRoot(t)
	dashWriteState(t, solo, otherFile, clean("feat/y"), dashJoinFresh)

	snap := CollectDashboardSnapshot([]string{root1, root2}, dashNow, "test")
	if len(snap.Repos) != 2 {
		t.Fatalf("repos = %d, want 2", len(snap.Repos))
	}
	bad := dashJoinFind(t, snap.Repos[0].Pipelines, dashJoinShipID)
	if h := dashHardenOf(t, bad); h.Malformed != 1 || len(dashRRStateIssues(bad)) != 1 {
		t.Errorf("malformed = %d, state issues = %+v, want 1 and 1", h.Malformed, dashRRStateIssues(bad))
	}

	want := dashOne(t, solo)
	got := dashJoinFind(t, snap.Repos[0].Pipelines, want.ID)
	if !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Issues, want.Issues) {
		t.Errorf("second pipeline = %+v / %+v, want %+v / %+v", got.Steps, got.Issues, want.Steps, want.Issues)
	}
	if alone := dashCollect(t, root2); !reflect.DeepEqual(snap.Repos[1].Pipelines, alone.Pipelines) {
		t.Errorf("second repo = %+v, want %+v", snap.Repos[1].Pipelines, alone.Pipelines)
	}
}

// TestDashboardHarden_TextLimits checks the rune limit of a rationale, the
// byte limit of a patch, and the redaction of both.
func TestDashboardHarden_TextLimits(t *testing.T) {
	build := func(entry map[string]any) DashboardHardenEdit {
		t.Helper()
		h, _ := dashHardenBuild(dashHardenData([]any{dashHardenRecord("t1", "done", []any{entry}, nil)}), StepInProgress, false)
		if h == nil || len(h.Edits) != 1 {
			t.Fatalf("detail = %+v, want one edit", h)
		}
		return h.Edits[0]
	}
	with := func(field string, v any) map[string]any {
		e := dashHardenApplied(1)
		e[field] = v
		return e
	}

	whole := strings.Repeat("ü", dashboardHardenRationaleMax)
	if got := build(with("rationale", whole)).Rationale; got != whole {
		t.Errorf("rationale of %d runes = %d runes, want it whole", dashboardHardenRationaleMax, len([]rune(got)))
	}
	if got := build(with("rationale", whole+"x")).Rationale; got != whole+"…" {
		t.Errorf("rationale of %d runes = %q…, want the first %d runes and …", dashboardHardenRationaleMax+1, got[:10], dashboardHardenRationaleMax)
	}

	long := build(with("patch", strings.Repeat("a", healingPatchMaxBytes+1)))
	if len(long.Patch) != healingPatchMaxBytes || !long.PatchCut {
		t.Errorf("patch = %d bytes, patchCut = %v, want %d bytes and true", len(long.Patch), long.PatchCut, healingPatchMaxBytes)
	}
	fits := build(with("patch", strings.Repeat("a", healingPatchMaxBytes)))
	if len(fits.Patch) != healingPatchMaxBytes || fits.PatchCut {
		t.Errorf("patch = %d bytes, patchCut = %v, want %d bytes and false", len(fits.Patch), fits.PatchCut, healingPatchMaxBytes)
	}
	stored := with("patch", "+x\n")
	stored["patchCut"] = true
	if got := build(stored); !got.PatchCut {
		t.Error("patchCut of a patch the writer cut = false, want true")
	}

	const secret = "Authorization: Bearer eyJhbGciOi.stuff"
	for _, field := range []string{"rationale", "patch"} {
		got := build(with(field, "see "+secret))
		text := got.Rationale
		if field == "patch" {
			text = got.Patch
		}
		if strings.Contains(text, "eyJ") || !strings.Contains(text, "[REDACTED]") {
			t.Errorf("%s = %q, want the token redacted", field, text)
		}
	}
}

// dashHardenFunnelData returns ship data whose funnel has a different count
// in each of its five fields:
//
//	fixNow 1         b3.go: empty reason, a deferredId and fixedInRun true
//	staysDeferred 5  f2.go, f3.go: needs-direction; f4.go, b1.go, b2.go:
//	                 below-threshold or empty reason, no deferredId
//	evidenceOnly 4   f1.go: wont-fix; l1.go, l2.go, l3.go: disagree
//	suppressed 2     f1.go to f7.go each have a healing.fixed record, so 7
//	                 files cluster; the cap keeps 5 (f1.go to f4.go have 2
//	                 findings each, then f5.go), f6.go and f7.go are suppressed
//	loneDisagree 3   l1.go, l2.go, l3.go: one disagree finding each
//
// The healing.fixed record of z.go has a deferredId, so it adds no finding:
// if it did, z.go would be an 8th cluster file and suppressed would be 3.
func dashHardenFunnelData() map[string]any {
	finding := func(file, reason, id string) map[string]any {
		m := map[string]any{"file": file, "severity": "low", "title": "t " + file, "reason": reason}
		if id != "" {
			m["deferredId"] = id
		}
		return m
	}
	fixed := []any{map[string]any{"file": "z.go", "severity": "high", "title": "fixed z", "deferredId": "d9"}}
	for i := 1; i <= 7; i++ {
		fixed = append(fixed, map[string]any{"file": fmt.Sprintf("f%d.go", i), "severity": "high", "title": "fixed"})
	}
	fixedInRun := finding("b3.go", "", "d1")
	fixedInRun["fixedInRun"] = true
	return map[string]any{
		"healing": map[string]any{"hardened": []any{}, "fixed": fixed},
		"deferredFindings": []any{
			finding("f1.go", "wont-fix", ""),
			finding("f2.go", "needs-direction", ""),
			finding("f3.go", "needs-direction", ""),
			finding("f4.go", "below-threshold", ""),
			finding("b1.go", "below-threshold", ""),
			finding("b2.go", "", ""),
			fixedInRun,
			finding("l1.go", "disagree", ""),
			finding("l2.go", "disagree", ""),
			finding("l3.go", "disagree", ""),
		},
	}
}

// TestDashboardHarden_Funnel checks each funnel field against a literal
// count, and that a cut input gives one info issue. A deferredFindings entry
// with fixedInRun true still counts in fixNow: the funnel shows what harden
// saw before its fix pass.
func TestDashboardHarden_Funnel(t *testing.T) {
	h, issues := dashHardenBuild(dashHardenFunnelData(), StepInProgress, false)
	if len(issues) != 0 {
		t.Errorf("issues = %+v, want none", issues)
	}
	want := &DashboardHardenFunnel{FixNow: 1, StaysDeferred: 5, EvidenceOnly: 4, Suppressed: 2, LoneDisagree: 3}
	if !reflect.DeepEqual(h.Funnel, want) {
		t.Errorf("funnel = %+v, want %+v", h.Funnel, want)
	}

	many := make([]any, hardenFindingsMax+1)
	for i := range many {
		many[i] = map[string]any{"file": fmt.Sprintf("f%d.go", i), "severity": "low", "title": "t", "reason": "disagree"}
	}
	h, issues = dashHardenBuild(map[string]any{"healing": map[string]any{"hardened": []any{}}, "deferredFindings": many}, StepInProgress, false)
	wantCut := []DashboardIssue{{Source: dashboardSourceState, Severity: "info", Text: fmt.Sprintf(
		"The harden funnel counts only the first %d findings of the ship state", hardenFindingsMax)}}
	if h.Funnel == nil || h.Funnel.EvidenceOnly != hardenFindingsMax || !reflect.DeepEqual(issues, wantCut) {
		t.Errorf("funnel = %+v, issues = %+v, want %d evidence-only and %+v", h.Funnel, issues, hardenFindingsMax, wantCut)
	}
}

// TestDashboardHarden_FunnelSkipped checks that deferredFindings and
// healing.fixed entries that cannot be read give one medium issue with their
// count, and that the funnel still counts the other entries.
func TestDashboardHarden_FunnelSkipped(t *testing.T) {
	data := map[string]any{
		"healing": map[string]any{"hardened": []any{}, "fixed": []any{"not an object"}},
		"deferredFindings": []any{
			42.0,
			map[string]any{"file": "a.go", "severity": "low", "title": "t", "reason": "cannot-verify"},
			map[string]any{"file": "b.go", "severity": "low", "title": "t", "reason": "needs-direction"},
		},
	}
	h, issues := dashHardenBuild(data, StepInProgress, false)
	want := []DashboardIssue{{Source: dashboardSourceState, Severity: "medium", Text: "3 entries of healing.fixed and deferredFindings of the ship state cannot be read — the harden funnel does not count them"}}
	if !reflect.DeepEqual(issues, want) {
		t.Errorf("issues = %+v, want %+v", issues, want)
	}
	if h.Funnel == nil || h.Funnel.StaysDeferred != 1 {
		t.Errorf("funnel = %+v, want staysDeferred 1", h.Funnel)
	}
}

// TestDashboardHarden_HealingNotAnObject checks that a data.healing value
// that is not an object gives one medium issue that names the field and the
// type found, and that the harden step shows no funnel. A null data.healing
// is an empty object and gives no issue.
func TestDashboardHarden_HealingNotAnObject(t *testing.T) {
	decision := []any{map[string]any{"step": "harden", "decision": "fix pass started: 1 finding(s)", "at": "2026-10-07T08:55:00Z"}}
	p := dashHardenShip(t, func(d map[string]any) {
		d["healing"] = []any{"x"}
		d["decisions"] = decision
	})
	h := dashHardenOf(t, p)
	if h.Funnel != nil || h.FixPass != nil || len(h.Clusters) != 0 || h.Malformed != 0 {
		t.Errorf("harden = %+v, want no funnel, no fixPass, no cluster and malformed 0", h)
	}
	want := "healing of the ship state is not an object (found list) — the harden step does not show its run records and funnel"
	found := 0
	for _, is := range dashRRStateIssues(p) {
		if is.Text == want && is.Severity == "medium" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("state issues = %+v, want one medium issue %q", dashRRStateIssues(p), want)
	}

	// With no harden decision the step has no detail, but the issue stays.
	p = dashHardenShip(t, func(d map[string]any) { d["healing"] = "x" })
	if s := dashJoinStep(t, p, "harden"); s.Detail != nil {
		t.Errorf("harden detail = %+v, want none", s.Detail)
	}
	if issues := dashRRStateIssues(p); len(issues) != 1 || !strings.HasPrefix(issues[0].Text, "healing of the ship state is not an object (found string)") {
		t.Errorf("state issues = %+v, want one healing issue", issues)
	}

	h2, issues := dashHardenBuild(map[string]any{"healing": nil, "decisions": decision}, StepInProgress, false)
	if h2 == nil || len(issues) != 0 || h2.Funnel == nil {
		t.Errorf("null healing: detail = %+v, issues = %+v, want a funnel and no issue", h2, issues)
	}
}

// TestDashboardHarden_BadSourceList checks the issue of each source list
// that is not a list, and that two bad fields report only the first.
func TestDashboardHarden_BadSourceList(t *testing.T) {
	fixPassDecision := []any{map[string]any{"step": "harden", "decision": "fix pass started: 1 finding(s)", "at": "2026-10-07T08:55:00Z"}}
	cases := []struct {
		name       string
		edit       func(map[string]any)
		text       string
		wantFunnel bool
		timeline   int
	}{
		{"healing.fixed", func(d map[string]any) { d["healing"].(map[string]any)["fixed"] = "x" },
			"healing.fixed of the ship state is not a list (found string) — the harden step does not show its funnel", false, 1},
		{"deferredFindings", func(d map[string]any) { d["deferredFindings"] = 5.0 },
			"deferredFindings of the ship state is not a list (found number) — the harden step does not show its funnel", false, 1},
		{"deferredFindings null", func(d map[string]any) { d["deferredFindings"] = nil },
			"deferredFindings of the ship state is not a list (found null) — the harden step does not show its funnel", false, 1},
		{"decisions", func(d map[string]any) { d["decisions"] = map[string]any{} },
			"decisions of the ship state is not a list (found object) — the harden step does not show its timeline", true, 0},
		{"healing.fixed and deferredFindings", func(d map[string]any) {
			d["healing"].(map[string]any)["fixed"] = 1.0
			d["deferredFindings"] = "x"
		}, "healing.fixed of the ship state is not a list (found number) — the harden step does not show its funnel", false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := dashHardenFunnelData()
			data["decisions"] = fixPassDecision
			c.edit(data)
			h, issues := dashHardenBuild(data, StepInProgress, false)
			if h == nil {
				t.Fatal("detail = nil, want a detail")
			}
			want := []DashboardIssue{{Source: dashboardSourceState, Severity: "medium", Text: c.text}}
			if !reflect.DeepEqual(issues, want) {
				t.Errorf("issues = %+v, want %+v", issues, want)
			}
			if (h.Funnel != nil) != c.wantFunnel {
				t.Errorf("funnel = %+v, want present %v", h.Funnel, c.wantFunnel)
			}
			if h.FixPass != nil {
				t.Errorf("fixPass = %+v, want absent", h.FixPass)
			}
			if len(h.Timeline) != c.timeline {
				t.Errorf("timeline = %+v, want %d rows", h.Timeline, c.timeline)
			}
			dashHardenCheckLists(t, h)
		})
	}
}

// TestDashboardHarden_FixPass checks fixPass {fixed: K, total: K + M} with a
// fix pass decision, and no fixPass without it.
func TestDashboardHarden_FixPass(t *testing.T) {
	entry := func(reason, id string, fixed bool) map[string]any {
		m := map[string]any{"file": "a.go", "severity": "low", "title": "t", "reason": reason}
		if id != "" {
			m["deferredId"] = id
		}
		if fixed {
			m["fixedInRun"] = true
		}
		return m
	}
	data := func(decision string) map[string]any {
		return map[string]any{
			"healing": map[string]any{"hardened": []any{}},
			"deferredFindings": []any{
				entry("below-threshold", "d1", true),
				entry("below-threshold", "d2", true),
				entry("below-threshold", "d3", false),
				entry("", "d4", false),
				entry("below-threshold", "d5", false),
				entry("below-threshold", "", false),
				entry("needs-direction", "d6", false),
			},
			"decisions": []any{map[string]any{"step": "harden", "decision": decision, "at": "2026-10-07T08:55:00Z"}},
		}
	}
	h, _ := dashHardenBuild(data("fix pass started: 3 finding(s)"), StepInProgress, false)
	if want := (&DashboardHardenFix{Fixed: 2, Total: 5}); !reflect.DeepEqual(h.FixPass, want) {
		t.Errorf("fixPass = %+v, want %+v", h.FixPass, want)
	}
	h, _ = dashHardenBuild(data("harden result: 1 edit"), StepInProgress, false)
	if h.FixPass != nil {
		t.Errorf("fixPass = %+v, want absent", h.FixPass)
	}
	if _, has := dashHardenJSON(t, h)["fixPass"]; has {
		t.Error("fixPass key present, want absent")
	}
}

// TestDashboardHarden_Timeline checks that the timeline holds the harden
// decisions in stored order, with an at that is not RFC 3339 shown as "".
func TestDashboardHarden_Timeline(t *testing.T) {
	long := strings.Repeat("x", dashboardPreviewTextMax+5)
	data := map[string]any{"decisions": []any{
		map[string]any{"step": "harden", "decision": "first", "at": "2026-10-07T08:51:00Z"},
		map[string]any{"step": "review", "decision": "not harden", "at": "2026-10-07T08:52:00Z"},
		"not an object",
		map[string]any{"step": "harden", "decision": "second", "at": "yesterday"},
		map[string]any{"step": "harden", "decision": long, "at": "2026-10-07T08:53:00.5Z"},
	}}
	h, issues := dashHardenBuild(data, StepInProgress, false)
	if h == nil {
		t.Fatal("detail = nil, want a detail from the harden decisions")
	}
	want := []DashboardHardenEvent{
		{At: "2026-10-07T08:51:00Z", Decision: "first"},
		{At: "", Decision: "second"},
		{At: "2026-10-07T08:53:00.5Z", Decision: dashboardPreview(long)},
	}
	if !reflect.DeepEqual(h.Timeline, want) {
		t.Errorf("timeline = %+v, want %+v", h.Timeline, want)
	}
	if len(issues) != 0 || h.Malformed != 0 {
		t.Errorf("issues = %+v, malformed = %d, want none", issues, h.Malformed)
	}
	dashHardenCheckLists(t, h)
}

// TestDashboardHarden_ListLimits checks that each list at its limit has no
// cut issue, and one more row cuts it with one info issue.
func TestDashboardHarden_ListLimits(t *testing.T) {
	records := func(n int) map[string]any {
		list := make([]any, n)
		for i := range list {
			list[i] = dashHardenRecord(fmt.Sprintf("t%d", i), "done", []any{}, nil)
		}
		return dashHardenData(list)
	}
	edits := func(n int) map[string]any {
		applied := make([]any, n)
		for i := range applied {
			applied[i] = dashHardenApplied(i)
		}
		return dashHardenData([]any{dashHardenRecord("t1", "done", applied, nil)})
	}
	held := func(n int) map[string]any {
		list := make([]any, n)
		for i := range list {
			list[i] = dashHardenHeld(healingHeldNotProcessed)
		}
		return dashHardenData([]any{dashHardenRecord("t1", "done", []any{}, list)})
	}
	timeline := func(n int) map[string]any {
		list := make([]any, n)
		for i := range list {
			list[i] = map[string]any{"step": "harden", "decision": fmt.Sprintf("d%d", i), "at": "2026-10-07T08:51:00Z"}
		}
		return map[string]any{"decisions": list}
	}
	cases := []struct {
		list  string
		limit int
		data  func(int) map[string]any
		rows  func(*DashboardHarden) int
	}{
		{"run record", dashboardHardenRecordsMax, records, func(h *DashboardHarden) int { return len(h.Clusters) }},
		{"edit", dashboardHardenRowsMax, edits, func(h *DashboardHarden) int { return len(h.Edits) }},
		{"held", dashboardHardenRowsMax, held, func(h *DashboardHarden) int { return len(h.Held) }},
		{"timeline", dashboardHardenRowsMax, timeline, func(h *DashboardHarden) int { return len(h.Timeline) }},
	}
	for _, c := range cases {
		t.Run(c.list, func(t *testing.T) {
			h, issues := dashHardenBuild(c.data(c.limit), StepInProgress, false)
			if c.rows(h) != c.limit || len(dashHardenIssues(issues, "info")) != 0 {
				t.Errorf("at the limit: rows = %d, issues = %+v, want %d and no info issue", c.rows(h), issues, c.limit)
			}
			h, issues = dashHardenBuild(c.data(c.limit+1), StepInProgress, false)
			want := []DashboardIssue{{Source: dashboardSourceState, Severity: "info", Text: fmt.Sprintf(
				"1 harden %s rows not shown: the harden step shows the first %d of %d", c.list, c.limit, c.limit+1)}}
			if c.rows(h) != c.limit || !reflect.DeepEqual(dashHardenIssues(issues, "info"), want) {
				t.Errorf("over the limit: rows = %d, issues = %+v, want %d and %+v", c.rows(h), issues, c.limit, want)
			}
		})
	}
}

// TestDashboardHarden_Interrupted checks that a started record is
// interrupted unless the harden step runs and the run is not complete, and
// that a done record is never interrupted.
func TestDashboardHarden_Interrupted(t *testing.T) {
	data := dashHardenData([]any{
		dashHardenRecord("s", "started", []any{}, nil),
		dashHardenRecord("d", "done", []any{}, nil),
	})
	cases := []struct {
		status       string
		runCompleted bool
		want         bool
	}{
		{StepInProgress, false, false},
		{StepInProgress, true, true},
		{StepCompleted, false, true},
		{StepFailed, false, true},
		{StepPending, false, true},
		{StepCompleted, true, true},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s completed=%v", c.status, c.runCompleted), func(t *testing.T) {
			h, _ := dashHardenBuild(data, c.status, c.runCompleted)
			if h.Clusters[0].Interrupted != c.want {
				t.Errorf("started interrupted = %v, want %v", h.Clusters[0].Interrupted, c.want)
			}
			if h.Clusters[1].Interrupted {
				t.Error("done interrupted = true, want false")
			}
		})
	}

	p := dashHardenShip(t, func(d map[string]any) {
		d["healing"] = map[string]any{"hardened": []any{dashHardenRecord("s", "started", []any{}, nil)}}
		d["pipelineCompletedAt"] = "2026-10-07T09:00:00Z"
	})
	if !dashHardenOf(t, p).Clusters[0].Interrupted {
		t.Error("started record of a completed run: interrupted = false, want true")
	}
}

// TestDashboardHarden_NoDetail checks that a harden step with no hardened key
// and no harden decision has no detail, and that a run with no harden step
// gets no harden issue.
func TestDashboardHarden_NoDetail(t *testing.T) {
	p := dashHardenShip(t, func(d map[string]any) {
		d["decisions"] = []any{map[string]any{"step": "review", "decision": "x", "at": "2026-10-07T08:31:00Z"}}
		d["deferredFindings"] = "not a list"
	})
	if s := dashJoinStep(t, p, "harden"); s.Detail != nil {
		t.Errorf("harden detail = %+v, want none", s.Detail)
	}
	if issues := dashRRStateIssues(p); len(issues) != 0 {
		t.Errorf("state issues = %+v, want none", issues)
	}

	root := dashRoot(t)
	data := dashJoinShipData()
	data["healing"] = map[string]any{"hardened": "not a list"}
	dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)
	p = dashOne(t, root)
	for _, is := range p.Issues {
		if strings.Contains(is.Text, "harden") {
			t.Errorf("issue %+v of a run with no harden step, want none", is)
		}
	}
}
