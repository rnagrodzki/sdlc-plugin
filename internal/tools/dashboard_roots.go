package tools

import "path/filepath"

// ResolveDisplayRoot reports whether repo names one repo the dashboard
// shows. roots are the registered repo roots. repo is cleaned, then compared
// with the display roots: one root for each repo, where a linked worktree
// root has become its main root (the same rule as the snapshot). The first
// result is the matching display root; it is empty when ok is false.
//
// The dashboard server has no working directory, so a mutating route must
// not trust a repo path from the page until this check passes.
func ResolveDisplayRoot(roots []string, repo string) (string, bool) {
	if repo == "" {
		return "", false
	}
	clean := filepath.Clean(repo)
	for _, root := range dashboardRepoRoots(roots) {
		if root == clean {
			return root, true
		}
	}
	return "", false
}
