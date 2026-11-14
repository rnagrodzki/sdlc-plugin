// push.go holds the best-effort push of the ship pipeline, used by
// commit_apply and the ship_state commit-check. The git work and the failure
// kinds live in internal/gitx; this file adds the skip rules and the warning
// text.

package tools

import (
	"errors"
	"fmt"

	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
)

// pushRemote is the remote that every push of the ship pipeline uses.
const pushRemote = "origin"

// pushBestEffort is the seam for the tests of commit_apply and commit-check.
var pushBestEffort = pushCurrentBranch

// pushCalls holds the git calls of the best-effort push, so tests can make
// each one fail.
type pushCalls struct {
	currentBranch func(dir string) (string, error)
	defaultBranch func(dir string) (string, error)
	hasRemote     func(dir, remote string) (bool, error)
	push          func(dir, remote string) error
}

// realPushCalls is the pushCalls of production.
var realPushCalls = pushCalls{
	currentBranch: gitx.CurrentBranch,
	defaultBranch: gitx.DefaultBranch,
	hasRemote:     gitx.HasRemote,
	push:          gitx.Push,
}

// pushCurrentBranch pushes the current branch to origin. It never fails: a
// skip or a failure becomes a warning and pushed is false. It skips the
// default branch and a repository with no origin remote. A push that runs
// with no default-branch check, because the lookup failed, gives pushed true
// and a warning that names the lookup error.
func pushCurrentBranch(dir string) (pushed bool, warning string) {
	return pushCurrentBranchWith(dir, realPushCalls)
}

// pushCurrentBranchWith is pushCurrentBranch with the git calls as a
// parameter.
func pushCurrentBranchWith(dir string, g pushCalls) (pushed bool, warning string) {
	branch, err := g.currentBranch(dir)
	if err != nil {
		return false, "push skipped: cannot read the current branch: " + gitx.SafeLine(err.Error())
	}
	// A failed default-branch lookup is not a reason to skip the push, but
	// the warning of a push that runs says that the check did not run.
	note := ""
	def, defErr := g.defaultBranch(dir)
	switch {
	case defErr != nil:
		note = "default-branch check skipped: " + gitx.SafeLine(defErr.Error()) + "."
	case def == branch:
		return false, fmt.Sprintf("push skipped: %s is the default branch", branch)
	}
	ok, err := g.hasRemote(dir, pushRemote)
	if err != nil {
		return false, "push skipped: cannot read remote " + pushRemote + ": " + gitx.SafeLine(err.Error())
	}
	if !ok {
		return false, "push skipped: no remote " + pushRemote
	}
	if err := g.push(dir, pushRemote); err != nil {
		var pe *gitx.PushError
		if errors.As(err, &pe) {
			return false, joinPushNote(note, pushFailedWarning(pe.Kind, pe.Stderr))
		}
		return false, joinPushNote(note, pushFailedWarning(gitx.PushKindOther, gitx.SafeLine(err.Error())))
	}
	if note != "" {
		return true, note + " The branch was pushed."
	}
	return true, ""
}

// joinPushNote puts the default-branch note, when there is one, in front of
// the warning.
func joinPushNote(note, warning string) string {
	if note == "" {
		return warning
	}
	return note + " " + warning
}

func pushFailedWarning(kind, stderr string) string {
	return fmt.Sprintf("push failed (%s): %s. The commit is kept and is not on the remote.", kind, stderr)
}
