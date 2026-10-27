package tools

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/telemetry"
)

// dashboardShipStepHarden is the name of the harden step of a ship pipeline.
const dashboardShipStepHarden = "harden"

// Limits of the harden step detail. The snapshot is built every 2 s, so each
// list has a cap; a cut list gives one info issue.
const (
	dashboardHardenRecordsMax   = 50  // run records read for one run
	dashboardHardenRowsMax      = 100 // rows in each of edits, held, timeline
	dashboardHardenRationaleMax = 600 // runes of one rationale
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
	Funnel    *DashboardHardenFunnel `json:"funnel,omitempty"`  // absent when a source list is not a list
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
	Applied        int    `json:"applied"`     // edits in the record
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
// A run record that cannot be read is skipped and counted in Malformed; the
// pipeline then gets one medium state issue. A healing.fixed or
// deferredFindings value that is not a list gives one medium state issue
// for the first bad field, and Funnel and FixPass are absent. A decisions
// value that is not a list gives its own medium state issue, an empty
// Timeline and no FixPass. Each cut list gives one info issue.
func dashboardHardenDetail(data map[string]any, step *DashboardStep, runCompleted bool) (*DashboardHarden, []DashboardIssue) {
	healing, _ := data["healing"].(map[string]any)
	rawHardened, hasHardened := healing["hardened"]

	decisions, decisionsOK := hardenShipList(data["decisions"])
	var hardenDecisions []map[string]any
	for _, raw := range decisions {
		m, ok := raw.(map[string]any)
		if ok && dashboardStr(m["step"]) == dashboardShipStepHarden {
			hardenDecisions = append(hardenDecisions, m)
		}
	}
	if !hasHardened && len(hardenDecisions) == 0 {
		return nil, nil
	}

	var issues []DashboardIssue
	h := &DashboardHarden{
		Edits:    []DashboardHardenEdit{},
		Clusters: []DashboardHardenRun{},
		Held:     []DashboardHardenHeld{},
		Timeline: []DashboardHardenEvent{},
	}

	// A started record is live only while the harden step runs in a live run.
	live := step.Status == StepInProgress && !runCompleted
	var records []map[string]any
	if hasHardened {
		list, ok := rawHardened.([]any)
		if !ok {
			h.Malformed = 1
		}
		for _, raw := range list {
			if rec, ok := dashboardHardenRecordValid(raw); ok {
				records = append(records, rec)
			} else {
				h.Malformed++
			}
		}
	}
	if h.Malformed > 0 {
		issues = append(issues, dashboardQueueIssue("medium", fmt.Sprintf(
			"healing.hardened of the ship state holds %d record(s) that cannot be read — they are not shown in the harden step", h.Malformed)))
	}
	records, issues = dashboardHardenCut(records, dashboardHardenRecordsMax, "run record", issues)
	for _, rec := range records {
		phase := dashboardStr(rec["phase"])
		applied, _ := rec["applied"].([]any)
		h.Clusters = append(h.Clusters, DashboardHardenRun{
			Trigger:        dashboardPreview(dashboardStr(rec["trigger"])),
			Phase:          phase,
			Classification: dashboardStr(rec["classification"]),
			Interrupted:    phase == "started" && !live,
			Applied:        len(applied),
			Skipped:        dashboardInt(rec["skipped"]),
		})
		for _, raw := range applied {
			if m, ok := raw.(map[string]any); ok {
				h.Edits = append(h.Edits, dashboardHardenEditOf(m))
			}
		}
		held, _ := rec["held"].([]any)
		for _, raw := range held {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			reason := dashboardStr(m["reason"])
			h.Held = append(h.Held, DashboardHardenHeld{
				Surface:    dashboardStr(m["surface"]),
				Action:     dashboardStr(m["action"]),
				TargetFile: dashboardStr(m["targetFile"]),
				Reason:     reason,
				Label:      dashboardHardenHeldLabels[reason],
				DeferredID: dashboardStr(m["deferredId"]),
			})
		}
	}
	h.Edits, issues = dashboardHardenCut(h.Edits, dashboardHardenRowsMax, "edit", issues)
	h.Held, issues = dashboardHardenCut(h.Held, dashboardHardenRowsMax, "held", issues)

	findings, badField, cut := hardenFindingsFromShip(healing, data["deferredFindings"])
	if badField != "" {
		issues = append(issues, dashboardHardenListIssue(badField, "funnel"))
	} else {
		if cut {
			issues = append(issues, dashboardQueueIssue("info", fmt.Sprintf(
				"the harden funnel counts only the first %d findings of the ship state", hardenFindingsMax)))
		}
		_, suppressed, loneDisagree := hardenBuildClusters(findings)
		fixNow, staysDeferred, evidenceOnly := hardenSplitFindings(findings)
		h.Funnel = &DashboardHardenFunnel{
			FixNow:        len(fixNow),
			StaysDeferred: len(staysDeferred),
			EvidenceOnly:  len(evidenceOnly),
			Suppressed:    len(suppressed),
			LoneDisagree:  len(loneDisagree),
		}
	}

	if !decisionsOK {
		issues = append(issues, dashboardHardenListIssue("decisions", "timeline"))
	} else {
		fixStarted := false
		for _, m := range hardenDecisions {
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
		h.Timeline, issues = dashboardHardenCut(h.Timeline, dashboardHardenRowsMax, "timeline", issues)
		if fixStarted && badField == "" {
			h.FixPass = dashboardHardenFixPass(data["deferredFindings"])
		}
	}
	return h, issues
}

// dashboardHardenRecordValid returns raw as a run record when every field
// that the harden detail reads has its stored form. A record is not valid
// when it is not an object; when phase is not in healingPhases or trigger is
// empty; when applied is not a list, or held is present and not a list; when
// an applied entry is not an object, lacks surface, action or targetFile, or
// has a rationale or patch that is not a string; or when a held entry is not
// an object, lacks surface, action or targetFile, or has a reason outside
// healingHeldReasons. An absent held key is valid: older records have none.
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
// action and targetFile.
func dashboardHardenHasFields(m map[string]any) bool {
	for _, field := range []string{"surface", "action", "targetFile"} {
		if strings.TrimSpace(dashboardStr(m[field])) == "" {
			return false
		}
	}
	return true
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
// deferredFindings list: Fixed is the number of entries with fixedInRun
// true; Total adds the entries with reason below-threshold (an empty reason
// counts as below-threshold), a deferredId and no fixedInRun true. The
// caller checked that deferredFindings is a list, absent or null.
func dashboardHardenFixPass(deferredFindings any) *DashboardHardenFix {
	list, _ := hardenShipList(deferredFindings)
	fix := &DashboardHardenFix{}
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if fixed, _ := m["fixedInRun"].(bool); fixed {
			fix.Fixed++
			continue
		}
		reason := dashboardStr(m["reason"])
		if reason == "" {
			reason = history.ReasonBelowThreshold
		}
		if reason == history.ReasonBelowThreshold && strings.TrimSpace(dashboardStr(m["deferredId"])) != "" {
			fix.Total++
		}
	}
	fix.Total += fix.Fixed
	return fix
}

// dashboardHardenListIssue returns the medium state issue of a source list
// that is not a list. part names the part of the harden step not shown.
func dashboardHardenListIssue(field, part string) DashboardIssue {
	return dashboardQueueIssue("medium", fmt.Sprintf("%s of the ship state is not a list — the harden %s is not shown", field, part))
}

// dashboardHardenCut returns rows cut at limit and issues with one info
// issue added when it cuts rows. list names the rows in the issue text.
func dashboardHardenCut[T any](rows []T, limit int, list string, issues []DashboardIssue) ([]T, []DashboardIssue) {
	if len(rows) <= limit {
		return rows, issues
	}
	issues = append(issues, dashboardQueueIssue("info", fmt.Sprintf(
		"%d harden %s rows not shown: the harden step shows the first %d of %d", len(rows)-limit, list, limit, len(rows))))
	return rows[:limit], issues
}
