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

const pushRemote = "origin"

// pushBestEffort is the seam for the tests of commit_apply and commit-check.
var pushBestEffort = pushBranchBestEffort

// pushBranchBestEffort pushes the current branch to origin. It never fails:
// a skip or a failure becomes a warning and pushed is false. It skips the
// default branch and a repository with no origin remote.
func pushBranchBestEffort(dir string) (pushed bool, warning string) {
	return pushBestEffortWith(dir, gitx.Push)
}

// pushBestEffortWith is pushBranchBestEffort with the push call as a
// parameter, so tests can make an auth or network failure.
func pushBestEffortWith(dir string, push func(dir, remote string) error) (pushed bool, warning string) {
	branch, err := gitx.CurrentBranch(dir)
	if err != nil {
		return false, pushFailedWarning(gitx.PushKindOther, err.Error())
	}
	// A failed default-branch lookup is not a reason to skip the push.
	if def, defErr := gitx.DefaultBranch(dir); defErr == nil && def != "" && def == branch {
		return false, fmt.Sprintf("push skipped: %s is the default branch", branch)
	}
	ok, err := gitx.HasRemote(dir, pushRemote)
	if err != nil {
		return false, pushFailedWarning(gitx.PushKindOther, err.Error())
	}
	if !ok {
		return false, "push skipped: no remote " + pushRemote
	}
	if err := push(dir, pushRemote); err != nil {
		var pe *gitx.PushError
		if errors.As(err, &pe) {
			return false, pushFailedWarning(pe.Kind, pe.Stderr)
		}
		return false, pushFailedWarning(gitx.PushKindOther, err.Error())
	}
	return true, ""
}

func pushFailedWarning(kind, stderr string) string {
	return fmt.Sprintf("push failed (%s): %s. The pr step pushes again.", kind, stderr)
}
