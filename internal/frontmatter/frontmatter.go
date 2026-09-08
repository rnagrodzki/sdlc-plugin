// Package frontmatter extracts and decodes the YAML frontmatter block at the
// start of a Markdown file. It is a Go port of the extraction half of
// scripts/lib/yaml.js (extractFrontmatter + extractBody) in the
// sdlc-utilities plugin; the hand-rolled line-scanning YAML decoder
// (parseSimpleYaml) is replaced by gopkg.in/yaml.v3 (KD12), which is a
// strict superset of the subset that parser understood — every construct the
// regex parser accepted, yaml.v3 also accepts, and yaml.v3 additionally
// understands YAML that the regex parser could not (nested mappings, quoted
// multiline scalars, flow scalars, etc). See dimensions_test.go for the
// corpus test that is the safety net for this claim.
package frontmatter

import (
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNoFrontmatter is returned by Parse when src does not begin with a
// "---\n"-delimited YAML frontmatter block, mirroring extractFrontmatter
// returning null for the same input.
var ErrNoFrontmatter = errors.New("frontmatter: missing YAML frontmatter block (--- delimiters)")

// Parse extracts the leading "---\n...\n---" frontmatter block from src and
// decodes it as YAML, returning the decoded mapping and the trimmed
// remainder of the document as body.
//
// Extraction mirrors the source regexes exactly, including their quirks:
//   - the block must start at byte 0 with "---\n" (extractFrontmatter's
//     "^---\n" anchor has no multiline flag, so it does not match a
//     "---" line appearing after leading whitespace or blank lines)
//   - the frontmatter content runs up to the first subsequent occurrence of
//     "\n---" (a non-greedy match, not anchored to end-of-line)
//   - body is everything after that closing delimiter, with at most one
//     leading newline stripped, then trimmed of surrounding whitespace
//     (extractBody's trailing ".trim()")
//
// When src has no frontmatter block, Parse returns ErrNoFrontmatter. When
// the frontmatter block cannot be decoded as YAML, Parse returns the
// underlying yaml.v3 error.
func Parse(src []byte) (meta map[string]any, body []byte, err error) {
	const openDelim = "---\n"
	const closeDelim = "\n---"

	s := string(src)
	if !strings.HasPrefix(s, openDelim) {
		return nil, nil, ErrNoFrontmatter
	}
	rest := s[len(openDelim):]

	closeIdx := strings.Index(rest, closeDelim)
	if closeIdx == -1 {
		return nil, nil, ErrNoFrontmatter
	}
	rawFrontmatter := rest[:closeIdx]

	meta = map[string]any{}
	if strings.TrimSpace(rawFrontmatter) != "" {
		if err := yaml.Unmarshal([]byte(rawFrontmatter), &meta); err != nil {
			return nil, nil, err
		}
		if meta == nil {
			meta = map[string]any{}
		}
	}

	afterClose := rest[closeIdx+len(closeDelim):]
	afterClose = strings.TrimPrefix(afterClose, "\n")
	bodyStr := strings.TrimSpace(afterClose)

	return meta, []byte(bodyStr), nil
}
