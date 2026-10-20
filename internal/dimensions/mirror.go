package dimensions

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// MirrorSuffix ends the file name of every Copilot mirror:
// .github/instructions/<name>.instructions.md.
const MirrorSuffix = ".instructions.md"

// MirrorDir returns the folder of the Copilot mirrors under the project root.
func MirrorDir(root string) string {
	return filepath.Join(root, ".github", "instructions")
}

// MirrorPath returns the path of the Copilot mirror of the dimension name under
// the project root.
func MirrorPath(root, name string) string {
	return filepath.Join(MirrorDir(root), name+MirrorSuffix)
}

// MirrorRoot returns the project root of the mirror at mirrorPath: the folder
// that holds .github/instructions.
func MirrorRoot(mirrorPath string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(mirrorPath)))
}

// Mirror marker line. Render mode ends every Copilot mirror it writes with
//
//	<!-- sdlc:generated sha256=<hash> -->
//
// where <hash> is the lowercase hex SHA-256 of the text above the marker
// line, with each "\r\n" changed to "\n". The hash lets the render tool tell
// an unedited generated mirror from hand-written content.
const (
	MirrorMarkerPrefix = "<!-- sdlc:generated sha256="
	MirrorMarkerSuffix = " -->"
)

// MirrorState is the state of one mirror file, as found by CheckMirror.
type MirrorState int

const (
	// MirrorGenerated: the last line is a marker line and its hash matches
	// the text above it.
	MirrorGenerated MirrorState = iota
	// MirrorNoMarker: the content is empty, has no final newline, or does
	// not end with a marker line.
	MirrorNoMarker
	// MirrorEdited: a marker line is present, but its hash differs from the
	// hash of the text above it, or is not a lowercase hex SHA-256 sum
	// (64 characters).
	MirrorEdited
)

// StampMirror returns content ready to write as a mirror file: "\r\n" changed
// to "\n", a final newline added when missing, and one marker line appended.
// The hash covers all text above the marker line.
func StampMirror(content string) string {
	text := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + MirrorMarkerPrefix + mirrorHash(text) + MirrorMarkerSuffix + "\n"
}

// CheckMirror reports the state of the mirror file content. A mirror stored
// with "\r\n" line endings (for example after a checkout with
// core.autocrlf) is compared as if it used "\n".
func CheckMirror(content []byte) MirrorState {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasSuffix(text, "\n") {
		return MirrorNoMarker
	}
	body := strings.TrimSuffix(text, "\n")
	lineStart := strings.LastIndex(body, "\n") + 1
	line := body[lineStart:]
	if len(line) < len(MirrorMarkerPrefix)+len(MirrorMarkerSuffix) ||
		!strings.HasPrefix(line, MirrorMarkerPrefix) ||
		!strings.HasSuffix(line, MirrorMarkerSuffix) {
		return MirrorNoMarker
	}
	hash := line[len(MirrorMarkerPrefix) : len(line)-len(MirrorMarkerSuffix)]
	if !isMirrorHash(hash) || hash != mirrorHash(text[:lineStart]) {
		return MirrorEdited
	}
	return MirrorGenerated
}

// mirrorHash returns the lowercase hex SHA-256 of text.
func mirrorHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// isMirrorHash reports whether s has the form of a mirrorHash result: the
// lowercase hex form of a SHA-256 sum.
func isMirrorHash(s string) bool {
	if len(s) != hex.EncodedLen(sha256.Size) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
