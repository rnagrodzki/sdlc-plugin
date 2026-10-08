// Package telemetry provides MCP failure classification, telemetry recording,
// and session-ID resolution.  Ported from scripts/lib/mcp-failure.js.
//
// Key decisions:
//
//   - KD5 (resolution order): ResolveSessionID tries param first, then the
//     on-disk marker at .sdlc-v2/state/mcp-session.id, then the SDLC_SESSION_ID
//     env var.  This is a deliberate reshuffle from the JS source (which
//     checks env first) for the MCP-server context where the param carries
//     the caller's session.  The function is read-only; it never generates
//     the marker file.
//
//   - KD6 (dedup scope): Record checks for an exact heading-line match
//     (date + class + tool) inside the target log file, per call.  Two
//     different errors for the same tool + class on the same calendar day
//     (UTC) collapse to a single entry.  This is "per-call dedup."
//
//   - Classify and Signal are additive exports beyond the contract's named
//     list (Record, ResolveSessionID, Failure) because AC1 requires fixture
//     parity and Task 30's mcp_failure_record tool will consume them.
//
//   - Legacy .claude/learnings/ fallback is dropped; the contract pins the
//     canonical path .sdlc-v2/learnings/log.md.
package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

// LearningsLogHeader is the first line of .sdlc-v2/learnings/log.md. It is a
// copy of the learnings_log tool's header (internal/tools imports this
// package, so it cannot be imported from there); a test in internal/tools
// pins that the two stay equal. learnings_log reads the file's first
// blank-line-separated block as the header, so Record writes this header
// when it is the first writer of the file.
const LearningsLogHeader = "# SDLC Execution Learnings\n"

// Classes is the closed set of failure classes (R26).
var Classes = []string{
	"transport", "auth", "schema", "workflow",
	"hook-block", "link-verification", "unknown",
}

// ---------------------------------------------------------------------------
// Signal / Classify
// ---------------------------------------------------------------------------

// Signal carries the raw inputs for failure classification.
type Signal struct {
	HTTPStatus     int
	ErrorMessage   string
	HookDenyReason string
	RPath          string
	ToolName       string
}

// Pre-compiled classification regexps.
var (
	hookBlockDenyRe = regexp.MustCompile(`R2[01]`)
	schemaDenyRe    = regexp.MustCompile(`(?:R19|C13|R18|R25|G15)`)
	authMsgRe       = regexp.MustCompile(`(?i)(?:cloudId|namespace|unauthorized)`)
	workflowMsgRe   = regexp.MustCompile(`(?i)(?:transition|workflow|invalid status)`)
	transportMsgRe  = regexp.MustCompile(`(?i)(?:ECONNREFUSED|ETIMEDOUT|fetch failed)`)
)

// Classify maps a Signal to one of the Classes.  The priority order matches
// the source classify() exactly: most-specific signal first.
func Classify(s Signal) string {
	msg := s.ErrorMessage
	deny := s.HookDenyReason

	// hook-block: deny reason contains R20 or R21.
	if deny != "" && hookBlockDenyRe.MatchString(deny) {
		return "hook-block"
	}
	// schema: deny reason matches content/placeholder/template violation codes.
	if deny != "" && schemaDenyRe.MatchString(deny) {
		return "schema"
	}
	// link-verification: exact rPath equality.
	if s.RPath == "R22" {
		return "link-verification"
	}

	// auth: 401 or message keywords.
	if s.HTTPStatus == 401 || authMsgRe.MatchString(msg) {
		return "auth"
	}
	if s.HTTPStatus == 403 {
		return "auth"
	}

	// workflow: 400 with workflow-specific message.
	if s.HTTPStatus == 400 && workflowMsgRe.MatchString(msg) {
		return "workflow"
	}
	// schema: 400 without workflow keywords.
	if s.HTTPStatus == 400 {
		return "schema"
	}

	// transport: 5xx or connection errors.
	if s.HTTPStatus >= 500 || transportMsgRe.MatchString(msg) {
		return "transport"
	}

	return "unknown"
}

// ---------------------------------------------------------------------------
// Failure / Record
// ---------------------------------------------------------------------------

// Failure is the structured record persisted by Record.
type Failure struct {
	Class     string
	Tool      string
	Site      string
	Project   string
	Error     string
	Recovered string
}

// Pre-compiled redactor regexps (order matches JS source).
var (
	bearerRe  = regexp.MustCompile(`Bearer [A-Za-z0-9._-]+`)
	jwtRe     = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`)
	cookieRe  = regexp.MustCompile(`(?i)cookie:[^;\n]+`)
	cloudIdRe = regexp.MustCompile(`(?i)(?:cloudId|cloud_id)[=:\s"']+([0-9a-f-]{30,})`)
	emailRe   = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
)

// Redact applies the failure-log redactors (bearer, JWT, cookie, cloudId,
// email) to s. Exported for internal/tools' user-input evidence.
func Redact(s string) string { return redact(s) }

// TruncateRunes cuts s to at most max runes, appending a trailing "…" when
// truncation actually happens. Operates on runes (not bytes) so a multi-byte
// character is never split mid-sequence.
func TruncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// redact applies all redactor patterns to s in source order.
func redact(s string) string {
	if s == "" {
		return s
	}
	r := bearerRe.ReplaceAllString(s, "Bearer [REDACTED]")
	r = jwtRe.ReplaceAllString(r, "[jwt:REDACTED]")
	r = cookieRe.ReplaceAllString(r, "cookie:[REDACTED]")
	r = cloudIdRe.ReplaceAllStringFunc(r, func(match string) string {
		sub := cloudIdRe.FindStringSubmatch(match)
		if len(sub) > 1 && len(sub[1]) >= 6 {
			return fmt.Sprintf("cloudId=[REDACTED:%s…]", sub[1][:6])
		}
		return "cloudId=[REDACTED]"
	})
	r = emailRe.ReplaceAllString(r, "[email:REDACTED]")
	return r
}

// Record appends a structured failure block to .sdlc-v2/learnings/log.md under
// root.  It creates parent directories as needed, and writes
// LearningsLogHeader before the block when the log is missing or empty.
//
// Idempotency (KD6): if a heading line with the same date, class, and tool
// already exists in the log, the call is a no-op and returns nil.  Two
// different errors for the same tool+class on the same UTC day therefore
// collapse to one entry.
func Record(root string, f Failure) error {
	today := time.Now().UTC().Format("2006-01-02")
	cls := f.Class
	if cls == "" {
		cls = "unknown"
	}
	tool := redact(f.Tool)
	site := redact(f.Site)
	project := redact(f.Project)

	errMsg := redact(strings.ReplaceAll(f.Error, "\n", " "))
	if len(errMsg) > 300 {
		errMsg = errMsg[:300]
	}

	recovered := f.Recovered
	if recovered == "" {
		recovered = "no"
	}

	logPath := filepath.Join(root, paths.DataDir, paths.LearningsSubdir, "log.md")
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("telemetry: mkdir %s: %w", dir, err)
	}

	// Heading uses the em dash (U+2014) matching JS source. It names only the
	// failing tool: any skill records failures here, not just the jira skill,
	// so the heading must not hard-code a source.
	heading := fmt.Sprintf("## %s — mcp-failure[%s]: %s", today, cls, tool)

	// Idempotency: skip if heading line already present.
	existing, _ := os.ReadFile(logPath)
	if len(existing) > 0 && strings.Contains(string(existing), heading+"\n") {
		return nil
	}

	block := strings.Join([]string{
		heading,
		"tool: " + tool,
		"site: " + site,
		"project: " + project,
		"error: " + errMsg,
		"recovered: " + recovered,
		"",
	}, "\n")

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("telemetry: open %s: %w", logPath, err)
	}
	defer file.Close()

	if _, err := file.WriteString(blockSeparator(existing) + block); err != nil {
		return fmt.Errorf("telemetry: write %s: %w", logPath, err)
	}
	return nil
}

// blockSeparator returns what must go between the existing log content and a
// new block so that exactly one blank line separates them. learnings_log
// splits entries on blank lines, so without it the block would merge into the
// previous entry. A missing or empty log gets the learnings_log header and a
// blank line first — otherwise learnings_log would read the block itself as
// the header and never count it. A log that already ends in a blank line
// needs nothing more.
func blockSeparator(existing []byte) string {
	s := string(existing)
	switch {
	case s == "":
		return LearningsLogHeader + "\n"
	case strings.HasSuffix(s, "\n\n"):
		return ""
	case strings.HasSuffix(s, "\n"):
		return "\n"
	default:
		return "\n\n"
	}
}

// ---------------------------------------------------------------------------
// ResolveSessionID
// ---------------------------------------------------------------------------

// ResolveSessionID returns a stable session identifier following the KD5
// resolution chain:
//
//  1. param — explicit caller-supplied value (highest priority).
//  2. .sdlc-v2/state/mcp-session.id — on-disk marker file (trimmed).
//  3. SDLC_SESSION_ID env var.
//  4. Fallback to the parent process ID (best-effort, mirrors JS ppid).
//
// The function is read-only; it never creates the marker file.
func ResolveSessionID(param, root string) string {
	if param != "" {
		return param
	}

	markerPath := filepath.Join(root, paths.DataDir, paths.StateArtifactsSubdir, "mcp-session.id")
	if data, err := os.ReadFile(markerPath); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id
		}
	}

	if env := os.Getenv("SDLC_SESSION_ID"); env != "" {
		return env
	}

	return strconv.Itoa(os.Getppid())
}
