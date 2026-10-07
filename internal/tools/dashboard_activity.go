package tools

import "time"

// collectActivity returns the sessions, learnings, and deferred items of the
// repo at root for the dashboard snapshot.
//
// Stub: it returns three empty, non-nil slices. The session, learning, and
// deferred collector replaces this body; the signature is fixed because
// CollectDashboardSnapshot calls it once for each repo.
func collectActivity(root string, now time.Time) (sessions []DashboardSession, learnings []DashboardLearning, deferred []DashboardDeferred) {
	return []DashboardSession{}, []DashboardLearning{}, []DashboardDeferred{}
}
