package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// Classify — fixture set matching JS classify() outcomes
// ---------------------------------------------------------------------------

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		sig  Signal
		want string
	}{
		// hook-block: deny reason with R20/R21 codes
		{"deny R20", Signal{HookDenyReason: "R20: blocked by hook"}, "hook-block"},
		{"deny R21", Signal{HookDenyReason: "R21: denied"}, "hook-block"},
		// hook-block beats transport (priority order)
		{"deny R20 + 500", Signal{HookDenyReason: "R20: blocked", HTTPStatus: 500}, "hook-block"},

		// schema via deny reason
		{"deny R19", Signal{HookDenyReason: "R19: content violation"}, "schema"},
		{"deny C13", Signal{HookDenyReason: "C13: placeholder"}, "schema"},
		{"deny R18", Signal{HookDenyReason: "R18: template"}, "schema"},
		{"deny R25", Signal{HookDenyReason: "R25"}, "schema"},
		{"deny G15", Signal{HookDenyReason: "G15"}, "schema"},

		// link-verification: exact rPath equality
		{"rPath R22", Signal{RPath: "R22"}, "link-verification"},
		// rPath R22x falls through (not a prefix match)
		{"rPath R22x fallthrough", Signal{RPath: "R22x"}, "unknown"},

		// auth: 401 or message keywords
		{"http 401", Signal{HTTPStatus: 401}, "auth"},
		{"msg unauthorized", Signal{ErrorMessage: "unauthorized access"}, "auth"},
		{"msg cloudId", Signal{ErrorMessage: "cloudId not found"}, "auth"},
		{"msg namespace", Signal{ErrorMessage: "namespace error"}, "auth"},
		{"http 403", Signal{HTTPStatus: 403}, "auth"},
		// auth beats workflow when msg contains "unauthorized"
		{"400 + unauthorized transition", Signal{HTTPStatus: 400, ErrorMessage: "unauthorized transition"}, "auth"},

		// workflow: 400 with workflow keywords
		{"400 transition", Signal{HTTPStatus: 400, ErrorMessage: "bad transition"}, "workflow"},
		{"400 workflow", Signal{HTTPStatus: 400, ErrorMessage: "workflow error"}, "workflow"},
		{"400 invalid status", Signal{HTTPStatus: 400, ErrorMessage: "invalid status"}, "workflow"},
		// case insensitive
		{"400 Invalid Status", Signal{HTTPStatus: 400, ErrorMessage: "Invalid Status change"}, "workflow"},

		// schema: 400 without workflow keywords
		{"400 generic", Signal{HTTPStatus: 400, ErrorMessage: "bad request"}, "schema"},
		{"400 no msg", Signal{HTTPStatus: 400}, "schema"},

		// transport: 5xx or connection errors
		{"http 500", Signal{HTTPStatus: 500}, "transport"},
		{"http 503", Signal{HTTPStatus: 503}, "transport"},
		{"msg ECONNREFUSED", Signal{ErrorMessage: "ECONNREFUSED"}, "transport"},
		{"msg ETIMEDOUT", Signal{ErrorMessage: "ETIMEDOUT"}, "transport"},
		{"msg fetch failed", Signal{ErrorMessage: "fetch failed"}, "transport"},
		// case insensitive
		{"msg Fetch Failed", Signal{ErrorMessage: "Fetch Failed"}, "transport"},

		// unknown: no signal
		{"empty signal", Signal{}, "unknown"},
		// msg "workflow" alone (no 400) → unknown
		{"msg workflow no 400", Signal{ErrorMessage: "workflow"}, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.sig)
			if got != tt.want {
				t.Fatalf("Classify(%+v) = %q, want %q", tt.sig, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Record — append + idempotency + dir creation
// ---------------------------------------------------------------------------

func TestRecord_CreatesParentDirs(t *testing.T) {
	root := t.TempDir()
	f := Failure{Class: "auth", Tool: "test-tool", Site: "example.com", Error: "fail"}
	if err := Record(root, f); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, paths.DataDir, "learnings", "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	if !strings.Contains(string(data), "mcp-failure[auth]: test-tool") {
		t.Fatalf("unexpected content: %s", data)
	}
}

// TestRecord_HeadingNamesOnlyTheTool pins the heading format: it names the
// failing tool and class, and does not claim the failure came from Jira when
// the tool has nothing to do with Jira.
func TestRecord_HeadingNamesOnlyTheTool(t *testing.T) {
	root := t.TempDir()
	if err := Record(root, Failure{Class: "transport", Tool: "mcp__github__create_pull_request"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, paths.DataDir, "learnings", "log.md"))
	if err != nil {
		t.Fatal(err)
	}
	body, ok := strings.CutPrefix(string(data), LearningsLogHeader+"\n")
	if !ok {
		t.Fatalf("log should start with the header and a blank line, got %q", data)
	}
	heading, _, _ := strings.Cut(body, "\n")
	want := "## " + time.Now().UTC().Format("2006-01-02") + " — mcp-failure[transport]: mcp__github__create_pull_request"
	if heading != want {
		t.Errorf("heading = %q, want %q", heading, want)
	}
	if strings.Contains(strings.ToLower(heading), "jira") {
		t.Errorf("heading for a non-Jira tool must not mention jira: %q", heading)
	}
}

func TestRecord_Idempotent(t *testing.T) {
	root := t.TempDir()
	f := Failure{Class: "auth", Tool: "test-tool", Site: "example.com", Error: "fail"}

	if err := Record(root, f); err != nil {
		t.Fatal(err)
	}
	// Second identical call should be a no-op.
	if err := Record(root, f); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(root, paths.DataDir, "learnings", "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(data), "mcp-failure[auth]: test-tool")
	if count != 1 {
		t.Fatalf("expected 1 heading occurrence, got %d", count)
	}
}

func TestRecord_NoPrefixFalsePositive(t *testing.T) {
	root := t.TempDir()

	// Record "foobar" first.
	if err := Record(root, Failure{Class: "auth", Tool: "foobar", Site: "s"}); err != nil {
		t.Fatal(err)
	}
	// Then record "foo" — must append, not be suppressed by the "foobar" heading.
	if err := Record(root, Failure{Class: "auth", Tool: "foo", Site: "s"}); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(root, paths.DataDir, "learnings", "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "mcp-failure[auth]: foobar") {
		t.Fatal("foobar heading missing")
	}
	if !strings.Contains(content, "mcp-failure[auth]: foo\n") {
		t.Fatal("foo heading missing — prefix false positive")
	}
}

func TestRecord_DefaultsClassAndRecovered(t *testing.T) {
	root := t.TempDir()
	f := Failure{Tool: "x"} // Class and Recovered empty
	if err := Record(root, f); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, paths.DataDir, "learnings", "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "mcp-failure[unknown]") {
		t.Fatal("class should default to unknown")
	}
	if !strings.Contains(content, "recovered: no") {
		t.Fatal("recovered should default to no")
	}
}

func TestRecord_RedactsEmail(t *testing.T) {
	root := t.TempDir()
	f := Failure{Class: "auth", Tool: "t", Error: "failed for user@example.com"}
	if err := Record(root, f); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, paths.DataDir, "learnings", "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "user@example.com") {
		t.Fatal("email should be redacted")
	}
	if !strings.Contains(content, "[email:REDACTED]") {
		t.Fatal("email redaction marker missing")
	}
}

func TestRecord_TruncatesLongError(t *testing.T) {
	root := t.TempDir()
	longErr := strings.Repeat("a", 500)
	f := Failure{Class: "transport", Tool: "t", Error: longErr}
	if err := Record(root, f); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, paths.DataDir, "learnings", "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "error: ") {
			errField := strings.TrimPrefix(line, "error: ")
			if len(errField) > 300 {
				t.Fatalf("error field should be <=300 bytes, got %d", len(errField))
			}
		}
	}
}

// TestRecord_OneBlankLineBeforeBlock pins that a new block is separated from
// the existing log by exactly one blank line, whatever the log ends with.
// learnings_log splits entries on blank lines, so a missing blank line would
// merge the failure block into the previous entry. A missing or empty log gets
// the learnings_log header first, so the block is not read as the header.
func TestRecord_OneBlankLineBeforeBlock(t *testing.T) {
	heading := "## " + time.Now().UTC().Format("2006-01-02") + " — mcp-failure[auth]: test-tool"
	block := heading + "\ntool: test-tool\nsite: \nproject: \nerror: fail\nrecovered: no\n"
	cases := []struct {
		name     string
		existing string
		want     string
	}{
		{"no trailing newline", "## prev entry", "## prev entry\n\n" + block},
		{"one trailing newline", "## prev entry\n", "## prev entry\n\n" + block},
		{"already ends in a blank line", "## prev entry\n\n", "## prev entry\n\n" + block},
		{"empty file", "", LearningsLogHeader + "\n" + block},
		{"missing file", "<missing>", LearningsLogHeader + "\n" + block},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			logPath := filepath.Join(root, paths.DataDir, "learnings", "log.md")
			if tc.existing != "<missing>" {
				if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(logPath, []byte(tc.existing), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := Record(root, Failure{Class: "auth", Tool: "test-tool", Error: "fail"}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.want {
				t.Errorf("log = %q, want %q", data, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ResolveSessionID
// ---------------------------------------------------------------------------

func TestResolveSessionID_ParamFirst(t *testing.T) {
	root := t.TempDir()
	got := ResolveSessionID("explicit-id", root)
	if got != "explicit-id" {
		t.Fatalf("expected explicit-id, got %q", got)
	}
}

func TestResolveSessionID_FileSecond(t *testing.T) {
	root := t.TempDir()
	markerDir := filepath.Join(root, paths.DataDir, "state")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "mcp-session.id"), []byte("  file-id  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ResolveSessionID("", root)
	if got != "file-id" {
		t.Fatalf("expected file-id, got %q", got)
	}
}

func TestResolveSessionID_EnvThird(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SDLC_SESSION_ID", "env-id")
	got := ResolveSessionID("", root)
	if got != "env-id" {
		t.Fatalf("expected env-id, got %q", got)
	}
}

func TestResolveSessionID_FallbackPpid(t *testing.T) {
	root := t.TempDir()
	// Ensure env var is unset.
	t.Setenv("SDLC_SESSION_ID", "")
	got := ResolveSessionID("", root)
	if got == "" {
		t.Fatal("expected non-empty fallback")
	}
	// Should be a numeric string (ppid).
	for _, c := range got {
		if c < '0' || c > '9' {
			t.Fatalf("expected numeric fallback, got %q", got)
		}
	}
}

// ---------------------------------------------------------------------------
// redact (unexported, tested indirectly via Record + direct)
// ---------------------------------------------------------------------------

func TestRedact(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(string) bool // returns true if OK
	}{
		{
			"bearer token",
			"Authorization: Bearer eyJhbGciOi.stuff",
			func(s string) bool { return strings.Contains(s, "Bearer [REDACTED]") && !strings.Contains(s, "eyJ") },
		},
		{
			"jwt standalone",
			"token eyJhbGciOiJSUzI1.payload.signature",
			func(s string) bool { return strings.Contains(s, "[jwt:REDACTED]") },
		},
		{
			"cookie",
			"Cookie:session=abc123;path=/",
			func(s string) bool { return strings.Contains(s, "cookie:[REDACTED]") },
		},
		{
			"email",
			"contact dev@example.org for help",
			func(s string) bool {
				return strings.Contains(s, "[email:REDACTED]") && !strings.Contains(s, "dev@")
			},
		},
		{
			"empty",
			"",
			func(s string) bool { return s == "" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redact(tt.input)
			if !tt.check(got) {
				t.Fatalf("redact(%q) = %q", tt.input, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TruncateRunes
// ---------------------------------------------------------------------------

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"under limit", "abc", 5, "abc"},
		{"at limit", "abcde", 5, "abcde"},
		{"over limit keeps n runes plus ellipsis", "abcdef", 5, "abcde…"},
		{"multi-byte runes are not split", "zażółć", 3, "zaż…"},
		{"multi-byte at limit", "żółć", 4, "żółć"},
		{"empty input", "", 3, ""},
		{"zero n on non-empty input", "abc", 0, "…"},
		{"zero n on empty input", "", 0, ""},
		{"negative n counts as zero", "abc", -1, "…"},
		{"negative n on empty input", "", -5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateRunes(tt.s, tt.n)
			if got != tt.want {
				t.Fatalf("TruncateRunes(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}
