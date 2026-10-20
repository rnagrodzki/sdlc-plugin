package dimensions

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

// testSHA256 hashes text without using the code under test.
func testSHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// testMarker builds a marker line with the given hash text.
func testMarker(hash string) string {
	return "<!-- sdlc:generated sha256=" + hash + " -->"
}

// TestStampMirror checks the line-ending change, the final newline, and the
// hash in the marker line that StampMirror appends.
func TestStampMirror(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "LF content with final newline gets one marker line",
			content: "---\napplyTo: \"**\"\n---\nbody\n",
			want:    "---\napplyTo: \"**\"\n---\nbody\n" + testMarker(testSHA256("---\napplyTo: \"**\"\n---\nbody\n")) + "\n",
		},
		{
			name:    "CRLF changes to LF before the hash",
			content: "line one\r\nline two\r\n",
			want:    "line one\nline two\n" + testMarker(testSHA256("line one\nline two\n")) + "\n",
		},
		{
			name:    "missing final newline is added before the marker line",
			content: "line one\nline two",
			want:    "line one\nline two\n" + testMarker(testSHA256("line one\nline two\n")) + "\n",
		},
		{
			name:    "hash is the SHA-256 of the text above the marker line",
			content: "a\n",
			want:    "a\n" + testMarker("87428fc522803d31065e7bce3cf03fe475096631e5e07bbd7a0fde60c4cf25c7") + "\n",
		},
		{
			name:    "empty content gets a newline and the marker line",
			content: "",
			want:    "\n" + testMarker(testSHA256("\n")) + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StampMirror(tt.content)
			if got != tt.want {
				t.Errorf("StampMirror(%q)\n got: %q\nwant: %q", tt.content, got, tt.want)
			}
			if n := strings.Count(got, MirrorMarkerPrefix); n != 1 {
				t.Errorf("StampMirror(%q) has %d marker lines, want 1", tt.content, n)
			}
		})
	}
}

// TestCheckMirror checks that CheckMirror returns the right MirrorState for
// each kind of mirror content.
func TestCheckMirror(t *testing.T) {
	const body = "---\napplyTo: \"**\"\n---\nbody text\n"
	stamped := StampMirror(body)
	wrongHash := strings.Repeat("0", 64)
	rightHash := testSHA256(body)

	tests := []struct {
		name    string
		content string
		want    MirrorState
	}{
		{
			name:    "StampMirror output is generated",
			content: stamped,
			want:    MirrorGenerated,
		},
		{
			name:    "StampMirror output with CRLF line endings is generated",
			content: strings.ReplaceAll(stamped, "\n", "\r\n"),
			want:    MirrorGenerated,
		},
		{
			name:    "empty content has no marker",
			content: "",
			want:    MirrorNoMarker,
		},
		{
			name:    "content with no final newline has no marker",
			content: strings.TrimSuffix(stamped, "\n"),
			want:    MirrorNoMarker,
		},
		{
			name:    "last line is not a marker line",
			content: body,
			want:    MirrorNoMarker,
		},
		{
			name:    "marker line followed by another line has no marker",
			content: stamped + "hand-written tail\n",
			want:    MirrorNoMarker,
		},
		{
			name:    "marker line followed by a blank line has no marker",
			content: stamped + "\n",
			want:    MirrorNoMarker,
		},
		{
			name:    "marker line cut short has no marker",
			content: body + "<!-- sdlc:generated sha256=" + rightHash + "\n",
			want:    MirrorNoMarker,
		},
		{
			name:    "one byte changed above the marker line is edited",
			content: "X" + stamped[1:],
			want:    MirrorEdited,
		},
		{
			name:    "hash that differs is edited",
			content: body + testMarker(wrongHash) + "\n",
			want:    MirrorEdited,
		},
		{
			name:    "uppercase hash is edited",
			content: body + testMarker(strings.ToUpper(rightHash)) + "\n",
			want:    MirrorEdited,
		},
		{
			name:    "hash shorter than 64 characters is edited",
			content: body + testMarker(rightHash[:63]) + "\n",
			want:    MirrorEdited,
		},
		{
			name:    "hash with a non-hex character is edited",
			content: body + testMarker(rightHash[:63]+"g") + "\n",
			want:    MirrorEdited,
		},
		{
			name:    "empty hash is edited",
			content: body + testMarker("") + "\n",
			want:    MirrorEdited,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckMirror([]byte(tt.content)); got != tt.want {
				t.Errorf("CheckMirror(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}

// TestMirrorPath checks the mirror path helpers against the fixed layout
// .github/instructions/<name>.instructions.md.
func TestMirrorPath(t *testing.T) {
	root := filepath.Join("work", "repo")
	want := filepath.Join(root, ".github", "instructions", "security.instructions.md")
	if got := MirrorPath(root, "security"); got != want {
		t.Errorf("MirrorPath = %q, want %q", got, want)
	}
	if got := MirrorRoot(want); got != root {
		t.Errorf("MirrorRoot = %q, want %q", got, root)
	}
}
