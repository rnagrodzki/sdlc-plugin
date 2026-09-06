package tools

import (
	"fmt"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Input / Output types
// ---------------------------------------------------------------------------

// ReceivedReviewIn is the input for the received_review_prepare tool.
type ReceivedReviewIn struct {
	PR int `json:"pr"`
}

// ReceivedReviewOut is the inline payload returned by received_review_prepare.
// Thread classification (outstanding/resolved/self-replied/stale) is not
// ported: the JS source relies on fetchPrReviewThreads (GraphQL), which has
// no ghx counterpart. Instead, this tool returns PRView and PRChecks output
// for downstream consumers.
type ReceivedReviewOut struct {
	Version       int              `json:"version"`
	Timestamp     string           `json:"timestamp"`
	PR            receivedReviewPR `json:"pr"`
	View          string           `json:"view"`
	Checks        string           `json:"checks"`
	PluginVersion string           `json:"plugin_version"`
}

type receivedReviewPR struct {
	Number int    `json:"number"`
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
}

// ---------------------------------------------------------------------------
// Core logic
// ---------------------------------------------------------------------------

func receivedReviewPrepare(projectRoot, activeRoot string, in ReceivedReviewIn) (ReceivedReviewOut, error) {
	// KD5 gate.
	if err := configmigrate.Verify(projectRoot); err != nil {
		return ReceivedReviewOut{}, &mcpserver.DataError{
			Msg:   fmt.Sprintf("config-version: %s", err.Error()),
			Cause: err,
		}
	}

	// Validate PR number.
	if in.PR <= 0 {
		return ReceivedReviewOut{}, &mcpserver.DomainError{
			Msg: "pr must be a positive integer",
		}
	}

	// Detect owner/repo from git remote.
	remoteURL, err := execx.Run("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: activeRoot})
	if err != nil {
		return ReceivedReviewOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("get git remote URL: %s", err.Error()),
			Cause: err,
		}
	}

	owner, repo, err := ghx.ParseRemoteOwner(remoteURL)
	if err != nil {
		return ReceivedReviewOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("parse remote owner/repo: %s", err.Error()),
			Cause: err,
		}
	}

	// Fetch PR view and checks (best effort for checks).
	view, err := ghx.PRView(activeRoot, in.PR)
	if err != nil {
		return ReceivedReviewOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("gh pr view %d: %s", in.PR, err.Error()),
			Cause: err,
		}
	}

	checks, _ := ghx.PRChecks(activeRoot, in.PR)

	return ReceivedReviewOut{
		Version:       1,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		PR:            receivedReviewPR{Number: in.PR, Owner: owner, Repo: repo},
		View:          view,
		Checks:        checks,
		PluginVersion: pluginVersion,
	}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterReceivedReviewTools registers received_review_prepare on the server.
func RegisterReceivedReviewTools(s *mcpserver.Server) {
	mcpserver.Register(s, "received_review_prepare",
		"Fetch PR view and checks for the received-review skill. Returns an inline payload with PR metadata.",
		func(ctx mcpserver.Ctx, in ReceivedReviewIn) (ReceivedReviewOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return ReceivedReviewOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
					Cause: err,
				}
			}
			activeRoot, err := worktree.ActiveRoot()
			if err != nil {
				activeRoot = root
			}
			return receivedReviewPrepare(root, activeRoot, in)
		},
	)
}
