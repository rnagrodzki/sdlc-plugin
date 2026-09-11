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
	PR int `json:"pr" jsonschema_description:"Pull request number to fetch review view and checks for."`
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

// ReceivedReviewVerifyIn is the input for the received_review_verify tool.
type ReceivedReviewVerifyIn struct {
	PR    int    `json:"pr" jsonschema_description:"Pull request number to fetch review comment threads for."`
	Login string `json:"login,omitempty" jsonschema_description:"GitHub login of the PR author. Defaults to current gh auth user."`
}

// CommentThread is one PR review comment thread — a root (top-level) review
// comment plus any replies attached to it — classified by whether the PR
// author has replied.
type CommentThread struct {
	ID         int    `json:"id"`
	Path       string `json:"path"`
	Line       int    `json:"line,omitempty"`
	Reviewer   string `json:"reviewer"`
	Body       string `json:"body"`
	Status     string `json:"status" jsonschema:"enum=outstanding,enum=replied,enum=self-replied" jsonschema_description:"Thread classification relative to the PR author's replies: \"outstanding\" (no replies at all), \"self-replied\" (has replies, but none from the PR author), or \"replied\" (the PR author has replied)."`
	ReplyCount int    `json:"replyCount"`
}

// ReceivedReviewVerifyOut is the payload returned by received_review_verify.
type ReceivedReviewVerifyOut struct {
	Version     int              `json:"version"`
	Timestamp   string           `json:"timestamp"`
	PR          receivedReviewPR `json:"pr"`
	Threads     []CommentThread  `json:"threads"`
	Outstanding int              `json:"outstanding"`
	Replied     int              `json:"replied"`
	Total       int              `json:"total"`
	Next        string           `json:"next" jsonschema_description:"Actionable next-step guidance after classifying review threads."`
}

// ---------------------------------------------------------------------------
// Core logic
// ---------------------------------------------------------------------------

// classifyCommentThreads groups PR review comments into root-comment
// threads (by in_reply_to_id) and classifies each thread not authored by
// login according to whether login (the PR author) has replied to it:
//
//   - "outstanding": the root comment has no replies at all.
//   - "self-replied": the root comment has replies, but none of them is
//     from login — typically just the original reviewer replying to their
//     own comment, with the author never addressing it.
//   - "replied": at least one reply is from login.
//
// Root comments authored by login itself are skipped entirely — those are
// the author's own comments, not review feedback awaiting their response.
func classifyCommentThreads(comments []ghx.PRReviewComment, login string) []CommentThread {
	repliesByRoot := make(map[int][]ghx.PRReviewComment)
	var roots []ghx.PRReviewComment
	for _, c := range comments {
		if c.InReplyToID != 0 {
			repliesByRoot[c.InReplyToID] = append(repliesByRoot[c.InReplyToID], c)
			continue
		}
		roots = append(roots, c)
	}

	threads := make([]CommentThread, 0, len(roots))
	for _, root := range roots {
		if root.Login == login {
			continue
		}

		replies := repliesByRoot[root.ID]
		status := "outstanding"
		if len(replies) > 0 {
			status = "self-replied"
			for _, r := range replies {
				if r.Login == login {
					status = "replied"
					break
				}
			}
		}

		threads = append(threads, CommentThread{
			ID:         root.ID,
			Path:       root.Path,
			Line:       root.Line,
			Reviewer:   root.Login,
			Body:       root.Body,
			Status:     status,
			ReplyCount: len(replies),
		})
	}

	return threads
}

func receivedReviewVerify(projectRoot, activeRoot string, in ReceivedReviewVerifyIn) (ReceivedReviewVerifyOut, error) {
	// KD5 gate.
	if err := configmigrate.Verify(projectRoot); err != nil {
		return ReceivedReviewVerifyOut{}, &mcpserver.DataError{
			Msg:   fmt.Sprintf("config-version: %s", err.Error()),
			Cause: err,
		}
	}

	// Validate PR number.
	if in.PR <= 0 {
		return ReceivedReviewVerifyOut{}, &mcpserver.DomainError{
			Msg: "pr must be a positive integer",
		}
	}

	// Detect owner/repo from git remote.
	remoteURL, err := execx.Run("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: activeRoot})
	if err != nil {
		return ReceivedReviewVerifyOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("get git remote URL: %s", err.Error()),
			Cause: err,
		}
	}

	owner, repo, err := ghx.ParseRemoteOwner(remoteURL)
	if err != nil {
		return ReceivedReviewVerifyOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("parse remote owner/repo: %s", err.Error()),
			Cause: err,
		}
	}

	login := in.Login
	if login == "" {
		login, err = ghx.CurrentLogin(activeRoot)
		if err != nil {
			return ReceivedReviewVerifyOut{}, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("resolve current gh login: %s", err.Error()),
				Suggestion: "Not logged in to github.com. Run: gh auth login --hostname github.com. Alternatively, pass the \"login\" input field to skip this lookup.",
				Cause:      err,
			}
		}
	}

	// Fetch all review comments in a single batched, paginated call.
	comments, err := ghx.PRReviewComments(activeRoot, owner, repo, in.PR)
	if err != nil {
		return ReceivedReviewVerifyOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("fetch PR review comments: %s", err.Error()),
			Cause: err,
		}
	}

	threads := classifyCommentThreads(comments, login)

	outstanding := 0
	replied := 0
	for _, t := range threads {
		if t.Status == "replied" {
			replied++
		} else {
			outstanding++
		}
	}

	next := "All review threads have a reply from the PR author. Safe to proceed."
	if outstanding > 0 {
		next = "Outstanding review threads remain — reply to each and address the feedback before proceeding."
	}

	return ReceivedReviewVerifyOut{
		Version:     1,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		PR:          receivedReviewPR{Number: in.PR, Owner: owner, Repo: repo},
		Threads:     threads,
		Outstanding: outstanding,
		Replied:     replied,
		Total:       len(threads),
		Next:        next,
	}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterReceivedReviewTools registers received_review_prepare and received_review_verify on the server.
func RegisterReceivedReviewTools(s *mcpserver.Server) {
	mcpserver.Register(s, "received_review_prepare",
		"INTERNAL — called by sdlc skills only. Fetch PR view and checks for the received-review skill. Returns an inline payload with PR metadata.",
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

	mcpserver.Register(s, "received_review_verify",
		"INTERNAL — called by sdlc skills only. Fetch all review comment threads on a PR and classify each as outstanding, replied, or self-replied relative to the PR author's login. Returns per-thread status plus outstanding/replied/total counts.",
		func(ctx mcpserver.Ctx, in ReceivedReviewVerifyIn) (ReceivedReviewVerifyOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return ReceivedReviewVerifyOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
					Cause: err,
				}
			}
			activeRoot, err := worktree.ActiveRoot()
			if err != nil {
				activeRoot = root
			}
			return receivedReviewVerify(root, activeRoot, in)
		},
	)
}
