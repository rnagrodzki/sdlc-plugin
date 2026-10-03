package commstyle

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// MinContrast is the WCAG AA contrast-ratio floor for normal text.
const MinContrast = 4.5

// ContrastHit is one hard-to-read Mermaid classDef or style line.
type ContrastHit struct {
	Line   int    `json:"line"`   // 1-based line in the scanned text
	Text   string `json:"text"`   // the trimmed classDef or style line
	Reason string `json:"reason"` // "no text color" | "contrast 3.3:1 < 4.5:1"
}

var (
	// colorLineRe is the only line shape ContrastHitsInLines checks: a
	// classDef or style line that sets a fill color. Prose that merely
	// mentions "style" or "fill:" (not as the line's own keyword) never
	// matches, since the line must start with the keyword.
	colorLineRe = regexp.MustCompile(`^\s*(classDef|style)\s+\S+\s+.*fill:`)

	fillValueRe = regexp.MustCompile(`fill:\s*([^,;\s]+)`)
	colorKeyRe  = regexp.MustCompile(`color:\s*([^,;\s]+)`)
	hexColorRe  = regexp.MustCompile(`^#?([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

	// mermaidFenceOpenRe matches a fence opener line, capturing its
	// backtick run and info string (the fence's language tag). Nested
	// fence closing mirrors classifyLines in metrics.go: the closer must
	// be a run of at least as many backticks.
	mermaidFenceOpenRe = regexp.MustCompile("^[ \\t]*(`{3,})[ \\t]*([A-Za-z0-9_-]*)[ \\t]*$")
)

// MermaidContrast scans Markdown for ```mermaid fences and returns the
// hard-to-read classDef/style color lines found inside them. Lines
// outside a mermaid fence, and fences tagged with another language, are
// never checked.
func MermaidContrast(content string) []ContrastHit {
	hits := []ContrastHit{}
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		m := mermaidFenceOpenRe.FindStringSubmatch(lines[i])
		if m == nil || !strings.EqualFold(m[2], "mermaid") {
			continue
		}
		closeRe := regexp.MustCompile(fmt.Sprintf("^[ \\t]*`{%d,}[ \\t]*$", len(m[1])))
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if closeRe.MatchString(lines[j]) {
				end = j
				break
			}
		}
		for k := i + 1; k < end; k++ {
			if hit, ok := checkColorLine(k+1, lines[k]); ok {
				hits = append(hits, hit)
			}
		}
		i = end
	}
	return hits
}

// ContrastHitsInLines checks every line of text, with no fence
// requirement: only a line matching colorLineRe is checked. The
// session-start/edit hook uses it on an edit's new text, which may not
// contain the fence opener.
func ContrastHitsInLines(text string) []ContrastHit {
	hits := []ContrastHit{}
	for i, line := range strings.Split(text, "\n") {
		if hit, ok := checkColorLine(i+1, line); ok {
			hits = append(hits, hit)
		}
	}
	return hits
}

// checkColorLine checks one 1-based line against colorLineRe and, when it
// matches, against the fill/text-color contrast rule.
func checkColorLine(lineNum int, line string) (ContrastHit, bool) {
	if !colorLineRe.MatchString(line) {
		return ContrastHit{}, false
	}
	fillMatch := fillValueRe.FindStringSubmatch(line)
	if fillMatch == nil {
		return ContrastHit{}, false
	}
	trimmed := strings.TrimSpace(line)
	colorMatch := colorKeyRe.FindStringSubmatch(line)
	if colorMatch == nil {
		return ContrastHit{Line: lineNum, Text: trimmed, Reason: "no text color"}, true
	}
	ratio, ok := contrastRatio(fillMatch[1], colorMatch[1])
	if !ok || ratio >= MinContrast {
		return ContrastHit{}, false
	}
	return ContrastHit{
		Line:   lineNum,
		Text:   trimmed,
		Reason: fmt.Sprintf("contrast %.1f:1 < %.1f:1", ratio, MinContrast),
	}, true
}

// contrastRatio returns the WCAG 2.x contrast ratio of two sRGB hex
// colors (3 or 6 hex digits, with or without a leading "#"). ok is false
// when either color is not a valid hex color (e.g. a named color like
// "red" or a function like "rgb(...)").
func contrastRatio(a, b string) (float64, bool) {
	la, ok := relativeLuminance(a)
	if !ok {
		return 0, false
	}
	lb, ok := relativeLuminance(b)
	if !ok {
		return 0, false
	}
	lighter, darker := la, lb
	if darker > lighter {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05), true
}

// relativeLuminance returns the WCAG relative luminance of a hex color.
func relativeLuminance(hex string) (float64, bool) {
	full, ok := normalizeHex(hex)
	if !ok {
		return 0, false
	}
	r, _ := strconv.ParseUint(full[0:2], 16, 8)
	g, _ := strconv.ParseUint(full[2:4], 16, 8)
	b, _ := strconv.ParseUint(full[4:6], 16, 8)
	return 0.2126*linearize(float64(r)/255) +
		0.7152*linearize(float64(g)/255) +
		0.0722*linearize(float64(b)/255), true
}

// linearize converts one sRGB channel (0-1) to its linear-light value.
func linearize(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// normalizeHex validates s as a 3- or 6-digit hex color (an optional
// leading "#") and expands a 3-digit color to 6 digits, lower-cased.
func normalizeHex(s string) (string, bool) {
	m := hexColorRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	digits := strings.ToLower(m[1])
	if len(digits) == 3 {
		digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
	}
	return digits, true
}
