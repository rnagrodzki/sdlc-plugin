package tools

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/telemetry"
)

// Limits of the harden step detail. The snapshot is built every 2 s, so each
// list has a cap; a cut list gives one info issue.
const (
	dashboardHardenRecordsMax   = 50                       // run records read for one run
	dashboardHardenRowsMax      = 100                      // rows in each of edits, held, timeline
	dashboardHardenRationaleMax = healingRationaleMaxRunes // runes of one rationale (the writer limit)
)

// dashboardHardenFixPassPrefix starts the harden decision that the ship
// skill records when the fix pass of the harden step starts.
const dashboardHardenFixPassPrefix = "fix pass started"

// dashboardHardenHeldLabels gives the page text of each held reason.
var dashboardHardenHeldLabels = map[string]string{
	healingHeldPendingStored:    "stored as a deferred item",
	healingHeldPendingNotStored: "not stored — copy it by hand",
	healingHeldNotProcessed:     "not processed — harden stopped",
}

// DashboardHarden is the harden detail of a ship run, built from
// data.healing.hardened, data.healing.fixed, data.deferredFindings and the
// harden entries of data.decisions. Every list is [] when empty, never null.
type DashboardHarden struct {
	Edits     []DashboardHardenEdit  `json:"edits"`             // applied[] of each valid run record
	Clusters  []DashboardHardenRun   `json:"clusters"`          // one row for each valid run record
	Funnel    *DashboardHardenFunnel `json:"funnel,omitempty"`  // absent when a source value has the wrong shape
	FixPass   *DashboardHardenFix    `json:"fixPass,omitempty"` // absent when no fix pass started
	Held      []DashboardHardenHeld  `json:"held"`              // held[] of each valid run record
	Timeline  []DashboardHardenEvent `json:"timeline"`          // harden decisions, in stored order
	Malformed int                    `json:"malformed"`         // run records that cannot be read
}

// DashboardHardenEdit is one edit that harden applied. An edit of an old
// record has no rationale and no patch: those keys are then absent.
type DashboardHardenEdit struct {
	Surface    string `json:"surface"`
	Action     string `json:"action"`
	TargetFile string `json:"targetFile"`
	Rationale  string `json:"rationale,omitempty"` // redacted, max dashboardHardenRationaleMax runes
	Patch      string `json:"patch,omitempty"`     // redacted, max healingPatchMaxBytes bytes
	PatchCut   bool   `json:"patchCut,omitempty"`  // the stored or the shown patch is cut
}

// DashboardHardenRun is one harden run record (one cluster trigger).
type DashboardHardenRun struct {
	Trigger        string `json:"trigger"` // dashboardPreview
	Phase          string `json:"phase"`   // a healingPhases value
	Classification string `json:"classification"`
	Interrupted    bool   `json:"interrupted"` // a started record of a run that is not live
	Applied        int    `json:"applied"`     // number of edits in the record
	Skipped        int    `json:"skipped"`
}

// DashboardHardenFunnel holds the counts of the harden funnel: findings that
// the fix pass fixes, that stay deferred and that are evidence only, and the
// files dropped as suppressed or as lone disagree.
type DashboardHardenFunnel struct {
	FixNow        int `json:"fixNow"`
	StaysDeferred int `json:"staysDeferred"`
	EvidenceOnly  int `json:"evidenceOnly"`
	Suppressed    int `json:"suppressed"`
	LoneDisagree  int `json:"loneDisagree"`
}

// DashboardHardenFix is the progress of the harden fix pass: Fixed is the
// number of deferredFindings entries fixed in the run, Total adds the
// below-threshold entries with a deferredId that are not fixed yet.
type DashboardHardenFix struct {
	Fixed int `json:"fixed"`
	Total int `json:"total"`
}

// DashboardHardenHeld is one proposal that harden left for a person.
type DashboardHardenHeld struct {
	Surface    string `json:"surface"`
	Action     string `json:"action"`
	TargetFile string `json:"targetFile"`
	Reason     string `json:"reason"` // a healingHeldReasons value
	Label      string `json:"label"`  // page text of Reason
	DeferredID string `json:"deferredId,omitempty"`
}

// DashboardHardenEvent is one harden decision of the ship run.
type DashboardHardenEvent struct {
	At       string `json:"at"`       // RFC 3339, or "" when the stored time does not parse
	Decision string `json:"decision"` // dashboardPreview
}

// dashboardHardenDetail builds the harden detail of a ship run. It returns
// nil when the run has no hardened key and no harden decision. It reads
// step.Status only and does not set step.Detail. runCompleted is true when
// the run has a pipelineCompletedAt.
//
// A data.healing value that is not an object (null is an empty object) gives
// one medium state issue; the run records and the funnel are then not shown.
// The run records come from dashboardHardenRuns, the funnel from
// dashboardHardenFunnel and the timeline from dashboardHardenTimeline. A
// decisions value that is not a list gives its own medium state issue, an
// empty Timeline and no FixPass. FixPass is set only when a fix pass started
// and the funnel is shown.
func dashboardHardenDetail(data map[string]any, step *DashboardStep, runCompleted bool) (*DashboardHarden, []DashboardIssue) {
	var issues []DashboardIssue
	healing, healingErr := shipDataHealing(data)
	if healingErr != nil {
		issues = append(issues, dashboardHardenShapeIssue(healingErr, "run records and funnel"))
	}
	_, hasHardened := healing["hardened"]
	decisions, decisionsErr := shipDataList(data, "decisions", "decisions")
	hardenDecisions := dashboardHardenDecisions(decisions)
	if !hasHardened && len(hardenDecisions) == 0 {
		return nil, issues
	}

	h := &DashboardHarden{
		Edits:    []DashboardHardenEdit{},
		Clusters: []DashboardHardenRun{},
		Held:     []DashboardHardenHeld{},
		Timeline: []DashboardHardenEvent{},
	}
	if hasHardened {
		// A started record is live only while the harden step runs in a live run.
		live := step.Status == StepInProgress && !runCompleted
		issues = append(issues, dashboardHardenRuns(h, healing, live)...)
	}
	if healingErr == nil {
		issues = append(issues, dashboardHardenFunnel(h, data)...)
	}
	if decisionsErr != nil {
		return h, append(issues, dashboardHardenShapeIssue(decisionsErr, "timeline"))
	}
	fixStarted, cut := dashboardHardenTimeline(h, hardenDecisions)
	issues = append(issues, cut...)
	if fixStarted && h.Funnel != nil {
		// dashboardHardenFunnel checked that deferredFindings is a list or absent.
		deferred, _ := shipDataList(data, "deferredFindings", "deferredFindings")
		h.FixPass = dashboardHardenFixPass(deferred)
	}
	return h, issues
}

// dashboardHardenDecisions returns the entries of decisions that are objects
// with step harden, in stored order.
func dashboardHardenDecisions(decisions []any) []map[string]any {
	var out []map[string]any
	for _, raw := range decisions {
		if m, ok := raw.(map[string]any); ok && dashboardStr(m["step"]) == dashboardShipStepHarden {
			out = append(out, m)
		}
	}
	return out
}

// dashboardHardenRuns fills h.Clusters, h.Edits, h.Held and h.Malformed from
// healing.hardened and returns its issues. A hardened value that is not a
// list counts as one malformed record and gives the medium shape issue of
// dashboardHardenShapeIssue. Each run record that dashboardHardenRecordValid
// refuses is skipped and counted in Malformed, and those records give one
// medium state issue. live is true while the harden step runs in a live run.
// Each cut list gives one info issue.
func dashboardHardenRuns(h *DashboardHarden, healing map[string]any, live bool) []DashboardIssue {
	var issues []DashboardIssue
	list, err := shipDataList(healing, "hardened", "healing.hardened")
	if err != nil {
		h.Malformed = 1
		issues = append(issues, dashboardHardenShapeIssue(err, "run records"))
	}
	var records []map[string]any
	bad := 0
	for _, raw := range list {
		if rec, ok := dashboardHardenRecordValid(raw); ok {
			records = append(records, rec)
		} else {
			bad++
		}
	}
	if bad > 0 {
		h.Malformed += bad
		issues = append(issues, dashboardQueueIssue("medium", fmt.Sprintf(
			"healing.hardened of the ship state holds %d record(s) that cannot be read — they are not shown in the harden step", bad)))
	}
	records, cut := dashboardCutRows(records, dashboardHardenRecordsMax, "harden run record", "the harden step")
	issues = append(issues, cut...)
	for _, rec := range records {
		applied, _ := rec["applied"].([]any)
		h.Clusters = append(h.Clusters, dashboardHardenRunOf(rec, len(applied), live))
		for _, raw := range applied {
			if m, ok := raw.(map[string]any); ok {
				h.Edits = append(h.Edits, dashboardHardenEditOf(m))
			}
		}
		held, _ := rec["held"].([]any)
		for _, raw := range held {
			if m, ok := raw.(map[string]any); ok {
				h.Held = append(h.Held, dashboardHardenHeldOf(m))
			}
		}
	}
	h.Edits, cut = dashboardCutRows(h.Edits, dashboardHardenRowsMax, "harden edit", "the harden step")
	issues = append(issues, cut...)
	h.Held, cut = dashboardCutRows(h.Held, dashboardHardenRowsMax, "harden held", "the harden step")
	return append(issues, cut...)
}

// dashboardHardenRunOf builds the cluster row of a valid run record with
// applied edits. A started record is interrupted when the run is not live.
func dashboardHardenRunOf(rec map[string]any, applied int, live bool) DashboardHardenRun {
	phase := dashboardStr(rec["phase"])
	return DashboardHardenRun{
		Trigger:        dashboardPreview(dashboardStr(rec["trigger"])),
		Phase:          phase,
		Classification: dashboardStr(rec["classification"]),
		Interrupted:    phase == healingPhaseStarted && !live,
		Applied:        applied,
		Skipped:        dashboardInt(rec["skipped"]),
	}
}

// dashboardHardenHeldOf builds one held row from a valid held entry.
func dashboardHardenHeldOf(m map[string]any) DashboardHardenHeld {
	reason := dashboardStr(m["reason"])
	return DashboardHardenHeld{
		Surface:    dashboardStr(m["surface"]),
		Action:     dashboardStr(m["action"]),
		TargetFile: dashboardStr(m["targetFile"]),
		Reason:     reason,
		Label:      dashboardHardenHeldLabels[reason],
		DeferredID: dashboardStr(m["deferredId"]),
	}
}

// dashboardHardenFunnel sets h.Funnel from the findings of
// hardenFindingsFromShip and returns its issues. A source value with the
// wrong shape leaves h.Funnel nil and gives the medium shape issue of the
// first bad field. A cut input gives one info issue. Entries that cannot be
// read give one medium issue; the funnel is still shown without them.
func dashboardHardenFunnel(h *DashboardHarden, data map[string]any) []DashboardIssue {
	res, err := hardenFindingsFromShip(data)
	if err != nil {
		return []DashboardIssue{dashboardHardenShapeIssue(err, "funnel")}
	}
	var issues []DashboardIssue
	if res.Cut {
		issues = append(issues, dashboardQueueIssue("info", fmt.Sprintf(
			"The harden funnel counts only the first %d findings of the ship state", hardenFindingsMax)))
	}
	if res.Skipped > 0 {
		issues = append(issues, dashboardQueueIssue("medium", fmt.Sprintf(
			"%d entries of healing.fixed and deferredFindings of the ship state cannot be read — the harden funnel does not count them", res.Skipped)))
	}
	_, suppressed, loneDisagree := hardenBuildClusters(res.Findings)
	fixNow, staysDeferred, evidenceOnly := hardenSplitFindings(res.Findings)
	h.Funnel = &DashboardHardenFunnel{
		FixNow:        len(fixNow),
		StaysDeferred: len(staysDeferred),
		EvidenceOnly:  len(evidenceOnly),
		Suppressed:    len(suppressed),
		LoneDisagree:  len(loneDisagree),
	}
	return issues
}

// dashboardHardenTimeline fills h.Timeline from the harden decisions, in
// stored order, and returns whether one of them starts the fix pass, plus
// the info issue of a cut timeline. An at that does not parse as RFC 3339
// is shown as "".
func dashboardHardenTimeline(h *DashboardHarden, decisions []map[string]any) (fixStarted bool, issues []DashboardIssue) {
	for _, m := range decisions {
		decision := dashboardStr(m["decision"])
		if strings.HasPrefix(decision, dashboardHardenFixPassPrefix) {
			fixStarted = true
		}
		at := dashboardStr(m["at"])
		if _, ok := dashboardParseTime(at); !ok {
			at = ""
		}
		h.Timeline = append(h.Timeline, DashboardHardenEvent{At: at, Decision: dashboardPreview(decision)})
	}
	h.Timeline, issues = dashboardCutRows(h.Timeline, dashboardHardenRowsMax, "harden timeline", "the harden step")
	return fixStarted, issues
}

// dashboardHardenRecordValid returns raw as a run record when the fields that
// decide how the record shows have their stored form. A record is not valid
// when it is not an object; when phase is not in healingPhases or trigger is
// empty; when applied is not a list, or held is present and not a list; when
// an applied entry is not an object, lacks surface, action or targetFile, or
// has a rationale or patch that is not a string; or when a held entry is not
// an object, lacks surface, action or targetFile, or has a reason outside
// healingHeldReasons. An absent held key is valid: older records have none.
// The record's classification and skipped, applied[].patchCut and
// held[].deferredId are not checked: a value of the wrong type reads as its
// zero value ("", 0 or false).
func dashboardHardenRecordValid(raw any) (map[string]any, bool) {
	rec, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	if !slices.Contains(healingPhases, dashboardStr(rec["phase"])) || strings.TrimSpace(dashboardStr(rec["trigger"])) == "" {
		return nil, false
	}
	applied, ok := rec["applied"].([]any)
	if !ok {
		return nil, false
	}
	for _, raw := range applied {
		m, ok := raw.(map[string]any)
		if !ok || !dashboardHardenHasFields(m) {
			return nil, false
		}
		for _, field := range []string{"rationale", "patch"} {
			if v, has := m[field]; has {
				if _, isStr := v.(string); !isStr {
					return nil, false
				}
			}
		}
	}
	if rawHeld, has := rec["held"]; has {
		held, ok := rawHeld.([]any)
		if !ok {
			return nil, false
		}
		for _, raw := range held {
			m, ok := raw.(map[string]any)
			if !ok || !dashboardHardenHasFields(m) || !slices.Contains(healingHeldReasons, dashboardStr(m["reason"])) {
				return nil, false
			}
		}
	}
	return rec, true
}

// dashboardHardenHasFields reports whether m has a non-empty string surface,
// action and targetFile. It uses the field check of the healing_record
// writer (healingMissingEntryField), so the reader and the writer agree.
func dashboardHardenHasFields(m map[string]any) bool {
	return healingMissingEntryField(m) == ""
}

// dashboardHardenEditOf builds one edit row from a valid applied entry. The
// rationale is redacted and cut to dashboardHardenRationaleMax runes. The
// patch is redacted and cut again at healingPatchMaxBytes, as redaction can
// make it longer. PatchCut is true when the stored entry says the writer cut
// the patch, or when this read cuts it.
func dashboardHardenEditOf(m map[string]any) DashboardHardenEdit {
	edit := DashboardHardenEdit{
		Surface:    dashboardStr(m["surface"]),
		Action:     dashboardStr(m["action"]),
		TargetFile: dashboardStr(m["targetFile"]),
	}
	if s := dashboardStr(m["rationale"]); s != "" {
		edit.Rationale = telemetry.TruncateRunes(telemetry.Redact(s), dashboardHardenRationaleMax)
	}
	if s := dashboardStr(m["patch"]); s != "" {
		var cut bool
		edit.Patch, cut = healingCutPatch(telemetry.Redact(s))
		stored, _ := m["patchCut"].(bool)
		edit.PatchCut = stored || cut
	}
	return edit
}

// dashboardHardenFixPass counts the fix pass of the harden step from the
// deferredFindings list: Fixed is the number of entries that
// deferredFixedInRun reports as fixed; Total adds the entries whose
// deferredReason is below-threshold, with a deferredId and not fixed.
func dashboardHardenFixPass(deferredFindings []any) *DashboardHardenFix {
	fix := &DashboardHardenFix{}
	for _, raw := range deferredFindings {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if deferredFixedInRun(m) {
			fix.Fixed++
			continue
		}
		if deferredReason(m) == history.ReasonBelowThreshold && strings.TrimSpace(dashboardStr(m["deferredId"])) != "" {
			fix.Total++
		}
	}
	fix.Total += fix.Fixed
	return fix
}

// dashboardHardenShapeIssue returns the medium state issue of a ship state
// value with the wrong shape: the text of err, then "— the harden step does
// not show its <part>". err is a *shipDataShapeError, so the text starts with
// "<field> of the ship state is not a list" (or "an object") and names the
// type found.
func dashboardHardenShapeIssue(err error, part string) DashboardIssue {
	return dashboardQueueIssue("medium", fmt.Sprintf("%s — the harden step does not show its %s", err.Error(), part))
}
