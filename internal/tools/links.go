// Package tools: links_validate and mcp_failure_record tools (Task 36).
//
// links_validate is a thin wrap over internal/links.Validate: it ports
// scripts/lib/links.js's extractUrls (URL extraction + line tracking --
// internal/links does not implement this, only classification/validation
// of already-extracted URL strings), then zips links.Validate's
// order-preserving []Result back with line numbers by index.
//
// mcp_failure_record wraps internal/telemetry.{Classify,Record,
// ResolveSessionID} only, per the fact sheet's explicit scope (excludes
// recordOccurrence/analyzeForDispatch). It is placed here rather than in
// validators.go as a size/cohesion call: links.go and mcp_failure_record
// are both thin, standalone wrappers over an existing package, distinct
// from validators.go's much larger action-enum dispatcher.
package tools

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/links"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/telemetry"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// links_validate
// ---------------------------------------------------------------------------

// LinksValidateIn is the input for the "links_validate" tool.
type LinksValidateIn struct {
	File    string `json:"file" jsonschema_description:"Path to the file to extract URLs from and validate."`
	Offline bool   `json:"offline,omitempty" jsonschema_description:"When true, skips network-reachability checks (GitHub/Jira/generic HTTP) and only performs offline validation."`
}

// LinkFinding is links.Result plus the line number of the URL's first
// occurrence in the source file (an addition disclosed by the fact sheet
// as an open implementer decision -- links.Result itself carries no line).
type LinkFinding struct {
	URL    string `json:"url"`
	Line   int    `json:"line"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// LinksValidateOut is the output for the "links_validate" tool.
type LinksValidateOut struct {
	Results []LinkFinding `json:"results"`
}

var (
	urlRe           = regexp.MustCompile(`https?://[^\s)\]>"']+`)
	trailingPunctRe = regexp.MustCompile(`[.,;:!?]+$`)
)

type extractedURL struct {
	URL  string
	Line int
}

// extractURLs ports scripts/lib/links.js's extractUrls: scans line by
// line, strips trailing punctuation and a lone unmatched trailing ')',
// and tracks the 1-based line of each URL's first occurrence.
func extractURLs(text string) []extractedURL {
	lines := strings.Split(text, "\n")
	seenLine := map[string]int{}
	var order []string

	for i, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		for _, m := range urlRe.FindAllString(line, -1) {
			u := trailingPunctRe.ReplaceAllString(m, "")
			if strings.HasSuffix(u, ")") && !strings.Contains(u, "(") {
				u = u[:len(u)-1]
			}
			if _, ok := seenLine[u]; !ok {
				seenLine[u] = i + 1
				order = append(order, u)
			}
		}
	}

	out := make([]extractedURL, 0, len(order))
	for _, u := range order {
		out = append(out, extractedURL{URL: u, Line: seenLine[u]})
	}
	return out
}

func linksValidate(root string, in LinksValidateIn) (LinksValidateOut, error) {
	if in.File == "" {
		return LinksValidateOut{}, &mcpserver.DomainError{Msg: "links_validate: file is required"}
	}
	filePath := resolvePath(root, in.File)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return LinksValidateOut{}, &mcpserver.DomainError{Msg: fmt.Sprintf("links_validate: file not found: %s", filePath), Cause: err}
	}

	extracted := extractURLs(string(data))
	urls := make([]string, len(extracted))
	for i, e := range extracted {
		urls[i] = e.URL
	}

	ctx := links.Ctx{Offline: in.Offline, RepoDir: root}
	results := links.Validate(ctx, urls) // order-preserving 1:1 with urls

	out := make([]LinkFinding, len(results))
	for i, r := range results {
		line := 0
		if i < len(extracted) {
			line = extracted[i].Line
		}
		out[i] = LinkFinding{URL: r.URL, Line: line, Status: r.Status, Reason: r.Reason, Detail: r.Detail}
	}
	return LinksValidateOut{Results: out}, nil
}

// RegisterLinksTools registers the "links_validate" MCP tool.
func RegisterLinksTools(s *mcpserver.Server) {
	mcpserver.Register(s, "links_validate",
		"Extract URLs from a file and validate each: GitHub issue/PR identity+existence, Atlassian Jira host match, generic HTTP(S) reachability.",
		func(ctx mcpserver.Ctx, in LinksValidateIn) (LinksValidateOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return LinksValidateOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve project root: %s", err.Error()), Cause: err}
			}
			return linksValidate(root, in)
		},
	)
}

// ---------------------------------------------------------------------------
// mcp_failure_record
// ---------------------------------------------------------------------------

// MCPFailureRecordIn is the input for the "mcp_failure_record" tool. Fields
// map directly onto telemetry.Signal and telemetry.Failure; SessionID is
// the optional explicit override for telemetry.ResolveSessionID's KD5
// resolution chain (param first).
type MCPFailureRecordIn struct {
	Tool           string `json:"tool" jsonschema_description:"Name of the MCP tool call that failed. Required."`
	HTTPStatus     int    `json:"httpStatus,omitempty" jsonschema_description:"HTTP status code of the failed call, if any (e.g. 401/403 classify as auth)."`
	ErrorMessage   string `json:"errorMessage,omitempty" jsonschema_description:"Error message text from the failed call, used for keyword-based classification (auth/workflow/transport)."`
	HookDenyReason string `json:"hookDenyReason,omitempty" jsonschema_description:"Deny reason text from a hook that blocked the call, if the failure was a hook block (classifies as hook-block or schema based on the guardrail code it contains)."`
	RPath          string `json:"rPath,omitempty" jsonschema_description:"Guardrail/rule code associated with the failure (e.g. \"R22\" classifies as link-verification)."`
	Site           string `json:"site,omitempty" jsonschema_description:"Site identifier (e.g. Jira host) associated with the failed call, recorded on the failure entry."`
	Project        string `json:"project,omitempty" jsonschema_description:"Project identifier associated with the failed call, recorded on the failure entry."`
	Recovered      string `json:"recovered,omitempty" jsonschema_description:"Description of how the failure was recovered from, if it was, recorded on the failure entry."`
	SessionID      string `json:"sessionId,omitempty" jsonschema_description:"Claude Code session ID. Explicit override for session-ID resolution (tried before the on-disk marker and env var)."`
}

// MCPFailureRecordOut is the output for the "mcp_failure_record" tool.
type MCPFailureRecordOut struct {
	Class     string `json:"class"`
	SessionID string `json:"sessionId"`
	Recorded  bool   `json:"recorded"`
}

func mcpFailureRecord(root string, in MCPFailureRecordIn) (MCPFailureRecordOut, error) {
	if in.Tool == "" {
		return MCPFailureRecordOut{}, &mcpserver.DomainError{Msg: "mcp_failure_record: tool is required"}
	}

	class := telemetry.Classify(telemetry.Signal{
		HTTPStatus:     in.HTTPStatus,
		ErrorMessage:   in.ErrorMessage,
		HookDenyReason: in.HookDenyReason,
		RPath:          in.RPath,
		ToolName:       in.Tool,
	})

	failure := telemetry.Failure{
		Class:     class,
		Tool:      in.Tool,
		Site:      in.Site,
		Project:   in.Project,
		Error:     in.ErrorMessage,
		Recovered: in.Recovered,
	}
	if err := telemetry.Record(root, failure); err != nil {
		return MCPFailureRecordOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("record mcp failure: %s", err.Error()), Cause: err}
	}

	sessionID := telemetry.ResolveSessionID(in.SessionID, root)
	return MCPFailureRecordOut{Class: class, SessionID: sessionID, Recorded: true}, nil
}

// RegisterMCPFailureTools registers the "mcp_failure_record" MCP tool.
func RegisterMCPFailureTools(s *mcpserver.Server) {
	mcpserver.Register(s, "mcp_failure_record",
		"INTERNAL — called by sdlc skills only. Classify an MCP tool-call failure and record it to .sdlc-v2/learnings/log.md for later analysis.",
		func(ctx mcpserver.Ctx, in MCPFailureRecordIn) (MCPFailureRecordOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return MCPFailureRecordOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve project root: %s", err.Error()), Cause: err}
			}
			if in.SessionID == "" {
				in.SessionID = ctx.SessionID
			}
			return mcpFailureRecord(root, in)
		},
	)
}
