package tools

import (
	"errors"
	"fmt"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// dashboardDetailRepoSuggestion is the fix of a repo that a detail call cannot
// read. It covers each cause: the repo folder is gone, the repo path is a
// file, and the runs folder is not a folder that can be read.
const dashboardDetailRepoSuggestion = "Check the path that the error names: the repo path must be a folder, and " +
	paths.DataDir + "/" + paths.RunsSubdir + " in it must be a folder that you can read. Then open the detail again."

// dashboardDetailEvidenceSuggestion is the fix of session evidence that a
// detail call cannot read.
const dashboardDetailEvidenceSuggestion = "Check that the evidence file that the error names is a regular file that you can read, then open the detail again."

// DashboardRunDetailOut is the result of DashboardRunDetail: the full steps
// and the issues of one run that the dashboard shows. A run that is not shown
// gives Found false and empty lists. The lists are [] in the JSON text, never
// null.
type DashboardRunDetailOut struct {
	// Found is true when the id is the id of a run row of the repo.
	Found bool `json:"found"`
	// ID is the id that the caller asked for, also when Found is false.
	ID string `json:"id"`
	// Steps holds every step of the run with its detail. It is [] when Found is
	// false.
	Steps []DashboardStep `json:"steps"`
	// Issues holds every issue of the run. It is [] when Found is false or the
	// run has no issue.
	Issues []DashboardIssue `json:"issues"`
}

// DashboardRunDetail returns the full steps and issues of the run row of the
// repo at root whose id is id. The repo is collected again for each call, with
// the parse cache for the large source files, so the result is as new as a
// snapshot taken now.
//
// A run is shown only when it is a row of that collect. These ids give Found
// false and no error:
//   - an id that no row has, and an empty id;
//   - the id of a run nested in a ship row, because the join drops it;
//   - the id of a completed or failed run older than dashboardPipelineWindow;
//   - an id with a path part. The id is only compared with the row ids and is
//     never joined to a path.
//
// A repo that cannot be read gives the error of dashboardDetailRepo.
func DashboardRunDetail(root, id string, now time.Time) (DashboardRunDetailOut, error) {
	out := DashboardRunDetailOut{ID: id, Steps: []DashboardStep{}, Issues: []DashboardIssue{}}
	repo, err := dashboardDetailRepo(root, now)
	if err != nil {
		return out, err
	}
	row, ok := archiveFindRow(repo.Pipelines, id)
	if !ok {
		return out, nil
	}
	out.Found = true
	out.Steps = dashboardNonNilCopy(row.Steps)
	out.Issues = dashboardNonNilCopy(row.Issues)
	return out, nil
}

// DashboardSessionDetailOut is the result of DashboardSessionDetail: the
// timeline and the command groups of one session of the repo. A session that
// the repo does not hold gives Found false and empty lists. The lists are []
// in the JSON text, never null.
type DashboardSessionDetailOut struct {
	// Found is true when the id is the id of a session of the repo.
	Found bool `json:"found"`
	// ID is the id that the caller asked for, also when Found is false.
	ID string `json:"id"`
	// Timeline holds the newest dashboardSessionTimelineMax events of the
	// session, newest first. It is [] when Found is false.
	Timeline []DashboardEvent `json:"timeline"`
	// CommandGroups holds the command groups of every command of the session. It
	// is [] when Found is false or the session has no command.
	CommandGroups []DashboardCommandGroup `json:"commandGroups"`
}

// DashboardSessionDetail returns the timeline and the command groups of the
// session of the repo at root whose id is id. The repo is collected again for
// each call, with the parse cache for the evidence files.
//
// The timeline holds the newest dashboardSessionTimelineMax events. The
// command groups count every command of the session, also the ones that the
// timeline cap cut. An id that no session has, an empty id, and an id with a
// path part give Found false and no error: the id is only compared with the
// session ids. A repo that cannot be read gives the error of
// dashboardDetailRepo.
//
// An evidence file that cannot be read gives an InfraError that names the
// file, also when the session is in the lines that could be read: a miss
// would look like a gone session, and a hit could be a part of the session.
func DashboardSessionDetail(root, id string, now time.Time) (DashboardSessionDetailOut, error) {
	out := DashboardSessionDetailOut{ID: id, Timeline: []DashboardEvent{}, CommandGroups: []DashboardCommandGroup{}}
	repo, err := dashboardDetailRepo(root, now)
	if err != nil {
		return out, err
	}
	if repo.sessionsErr != nil {
		return out, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read session evidence of repo %s: %v", root, repo.sessionsErr),
			Suggestion: dashboardDetailEvidenceSuggestion,
			Cause:      repo.sessionsErr,
		}
	}
	for _, s := range repo.Sessions {
		if s.ID != id {
			continue
		}
		out.Found = true
		out.Timeline = dashboardNonNilCopy(s.Timeline)
		out.CommandGroups = dashboardNonNilCopy(s.CommandGroups)
		return out, nil
	}
	return out, nil
}

// dashboardDetailRepo collects the repo at root with collectDashboardRepo,
// the collect of the snapshot, so a row of the detail is the same row that the
// page shows. A repo with Error set gives an error that names root, because
// the lists of such a repo are empty and a miss would look like a gone item:
//   - a repo path that is not a folder gives a DomainError: no read failed;
//   - any other cause gives an InfraError with the cause of Error.
//
// Both carry dashboardDetailRepoSuggestion.
func dashboardDetailRepo(root string, now time.Time) (DashboardRepo, error) {
	repo := collectDashboardRepo(root, now)
	if repo.Error == "" {
		return repo, nil
	}
	msg := fmt.Sprintf("read repo %s: %s", root, repo.Error)
	if errors.Is(repo.err, errDashboardRepoNotFolder) {
		return repo, &mcpserver.DomainError{Msg: msg, Suggestion: dashboardDetailRepoSuggestion, Cause: repo.err}
	}
	return repo, &mcpserver.InfraError{Msg: msg, Suggestion: dashboardDetailRepoSuggestion, Cause: repo.err}
}

// dashboardNonNilCopy returns a copy of list that is never nil, so a JSON
// list encodes as [] and not as null. The copy is shallow: it keeps the caller
// from changing the list that the collect shares with the parse cache, but a
// slice inside an item (for example Programs of a command group) stays shared.
// The callers only encode the result, so they never change an item.
func dashboardNonNilCopy[T any](list []T) []T {
	return append([]T{}, list...)
}
