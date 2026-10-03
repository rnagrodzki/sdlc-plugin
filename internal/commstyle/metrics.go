package commstyle

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// minMeasuredWords is the skip floor: a measured section with fewer words
// gets Status "skipped" and no failures.
const minMeasuredWords = 40

// maxLongSentenceShare is the tolerance for long sentences: a section fails
// the sentence-length check only when more than this share of its prose
// sentences is over Limits.MaxSentenceWords.
const maxLongSentenceShare = 0.10

// maxVisualListItemWords is the word ceiling for a list item to count as
// visual; a longer item is prose.
const maxVisualListItemWords = 30

// codePlaceholder replaces every inline code span in prose text, so text
// inside backticks is never split into sentences or matched as a phrase.
const codePlaceholder = "CODE"

// SectionMetrics holds the readability numbers of one measured "## "
// section and the limit checks it failed.
type SectionMetrics struct {
	Name              string   `json:"name"`
	Words             int      `json:"words"`
	ProseShare        float64  `json:"proseShare"`
	LongestParagraph  int      `json:"longestParagraph"` // sentences
	LongestList       int      `json:"longestList"`      // items
	LongSentenceShare float64  `json:"longSentenceShare"`
	JargonShare       float64  `json:"jargonShare"`
	Status            string   `json:"status"` // pass | fail | skipped
	Failures          []string `json:"failures"`
}

// BannedHit is one banned phrase found in the plan, with its 1-based line.
type BannedHit struct {
	Phrase string `json:"phrase"`
	Line   int    `json:"line"`
}

// SteHit is one strict-STE rule break: the rule id, the matched text, and
// the 1-based plan line.
type SteHit struct {
	Rule string `json:"rule"`
	Text string `json:"text"`
	Line int    `json:"line"`
}

// Report is the result of Measure. Every slice is non-nil.
type Report struct {
	Sections   []SectionMetrics `json:"sections"`
	BannedHits []BannedHit      `json:"bannedPhraseHits"`
	SteHits    []SteHit         `json:"steHits"` // filled by the strict STE check when Limits.STE is set
}

// proseLine is one prose line of the whole plan, after line classification.
// Measure builds the list once; the strict STE check reads it.
type proseLine struct {
	Line      int    // 1-based plan line
	Text      string // inline code spans replaced by "CODE"; list marker removed
	Paragraph int    // index of the prose paragraph (run of consecutive prose lines; a list item starts a new one)
	ListItem  bool   // true for a list item of more than 30 words
	Numbered  bool   // true for a "N. " or "- [ ] " item (instruction candidate)
}

// lineClass is the readability class of one plan line.
type lineClass int

const (
	classIgnored lineClass = iota // heading, blank line, horizontal rule, fence delimiter
	classVisual                   // fence body, table row, short list item
	classProse                    // any other text, long list item
)

// planLine is the classification of one plan line.
type planLine struct {
	number    int // 1-based
	class     lineClass
	inFence   bool   // fence body or fence delimiter
	text      string // inline code replaced, list marker removed
	words     int
	listItem  bool
	numbered  bool
	indent    int // leading-space count of a list item
	headLevel int // 1-6 for a "#" heading outside fences, else 0
	headText  string
}

var (
	fenceOpenLineRe = regexp.MustCompile("^[ \\t]*(`{3,})")
	listItemRe      = regexp.MustCompile(`^([ \t]*)([-*]|\d+\.)[ \t]+(\[[ xX]\][ \t]+)?`)
	orderedMarkerRe = regexp.MustCompile(`^\d+\.$`)
	checkboxRe      = regexp.MustCompile(`^\[[ xX]\]`)
	headingRe       = regexp.MustCompile(`^[ \t]*(#{1,6})(?:[ \t]+(.*?))?[ \t]*#*[ \t]*$`)
	ruleLineRe      = regexp.MustCompile(`^[ \t]*(?:-{3,}|\*{3,}|_{3,})[ \t]*$`)
	pathTokenRe     = regexp.MustCompile(`[\w.-]+/[\w./-]+\.\w+`)
	abbrevRe        = regexp.MustCompile(`(?i)(?:^|\s)(?:e\.g|i\.e|etc|vs|cf)\.$`)
)

// Measure checks the named "## " sections of content against l and scans
// the whole plan for banned phrases. A section named in sections but not
// found in content is reported with Words 0 and Status "skipped".
func Measure(content string, sections []string, l Limits) Report {
	lines := classifyLines(content)
	prose := collectProseLines(lines)

	rep := Report{
		Sections:   make([]SectionMetrics, 0, len(sections)),
		BannedHits: bannedHits(lines, l.BannedPhrases),
		SteHits:    []SteHit{},
	}
	for _, name := range sections {
		rep.Sections = append(rep.Sections, measureSection(name, lines, prose, l))
	}
	return rep
}

// classifyLines classifies every line of content. Fences mirror
// stripFences in internal/tools/validators.go (an opener of 3+ backticks,
// closed by a run at least as long; an unclosed opener is an ordinary
// line), but fence lines may be indented so fences nested in list items
// count as visual.
func classifyLines(content string) []planLine {
	raw := strings.Split(content, "\n")
	out := make([]planLine, len(raw))
	for i := range raw {
		raw[i] = strings.TrimRight(raw[i], "\r")
		out[i].number = i + 1
	}

	fence := make([]bool, len(raw))
	delim := make([]bool, len(raw))
	for i := 0; i < len(raw); i++ {
		m := fenceOpenLineRe.FindStringSubmatch(raw[i])
		if m == nil {
			continue
		}
		closeRe := regexp.MustCompile(fmt.Sprintf("^[ \\t]*`{%d,}[ \\t]*$", len(m[1])))
		for j := i + 1; j < len(raw); j++ {
			if closeRe.MatchString(raw[j]) {
				for k := i; k <= j; k++ {
					fence[k] = true
				}
				delim[i], delim[j] = true, true
				i = j
				break
			}
		}
	}

	for i, line := range raw {
		pl := &out[i]
		switch {
		case delim[i]:
			pl.inFence = true
			pl.class = classIgnored
		case fence[i]:
			pl.inFence = true
			pl.class = classVisual
			pl.text = line
			pl.words = countWords(line)
		default:
			classifyText(pl, line)
		}
	}
	return out
}

// classifyText fills pl for a line outside any fence.
func classifyText(pl *planLine, line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || ruleLineRe.MatchString(line) {
		pl.class = classIgnored
		return
	}
	if m := headingRe.FindStringSubmatch(line); m != nil {
		pl.class = classIgnored
		pl.headLevel = len(m[1])
		pl.headText = strings.TrimSpace(m[2])
		pl.text = replaceCodeSpans(trimmed)
		return
	}
	if strings.HasPrefix(trimmed, "|") {
		pl.class = classVisual
		pl.text = replaceCodeSpans(trimmed)
		pl.words = countWords(pl.text)
		return
	}
	if m := listItemRe.FindStringSubmatch(line); m != nil {
		pl.listItem = true
		pl.indent = len(strings.ReplaceAll(m[1], "\t", "    "))
		pl.numbered = orderedMarkerRe.MatchString(m[2]) || checkboxRe.MatchString(m[3])
		pl.text = replaceCodeSpans(strings.TrimSpace(line[len(m[0]):]))
		pl.words = countWords(pl.text)
		if pl.words > maxVisualListItemWords {
			pl.class = classProse
		} else {
			pl.class = classVisual
		}
		return
	}
	pl.class = classProse
	pl.text = replaceCodeSpans(trimmed)
	pl.words = countWords(pl.text)
}

// collectProseLines returns the prose lines of the whole plan. A paragraph
// is a run of consecutive prose lines; a list item always starts a new
// paragraph, so a list of long items is not read as one long paragraph.
func collectProseLines(lines []planLine) []proseLine {
	out := []proseLine{}
	para := -1
	prevProse := false
	for _, pl := range lines {
		if pl.class != classProse {
			prevProse = false
			continue
		}
		if !prevProse || pl.listItem {
			para++
		}
		prevProse = true
		out = append(out, proseLine{
			Line:      pl.number,
			Text:      pl.text,
			Paragraph: para,
			ListItem:  pl.listItem,
			Numbered:  pl.listItem && pl.numbered,
		})
	}
	return out
}

// sectionRange returns the 0-based line index range [start, end) of the
// body of the first "## name" heading, or ok=false. The body ends at the
// next "#" or "##" heading outside fences.
func sectionRange(name string, lines []planLine) (start, end int, ok bool) {
	want := strings.TrimSpace(name)
	for i, pl := range lines {
		if pl.headLevel != 2 || !strings.EqualFold(pl.headText, want) {
			continue
		}
		end = len(lines)
		for j := i + 1; j < len(lines); j++ {
			if lv := lines[j].headLevel; lv == 1 || lv == 2 {
				end = j
				break
			}
		}
		return i + 1, end, true
	}
	return 0, 0, false
}

// measureSection computes the metrics of one named section and checks them
// against l.
func measureSection(name string, lines []planLine, prose []proseLine, l Limits) SectionMetrics {
	sm := SectionMetrics{Name: name, Status: "skipped", Failures: []string{}}
	start, end, ok := sectionRange(name, lines)
	if !ok {
		return sm
	}

	proseWords := 0
	for _, pl := range lines[start:end] {
		sm.Words += pl.words
		if pl.class == classProse {
			proseWords += pl.words
		}
	}
	sm.LongestList = longestList(lines[start:end])

	firstLine, lastLine := 0, -1 // empty body: no prose line matches
	if end > start {
		firstLine, lastLine = lines[start].number, lines[end-1].number
	}
	sentences, longSentences, jargonSentences := 0, 0, 0
	paraSentences := map[int]int{}
	for _, p := range prose {
		if p.Line < firstLine || p.Line > lastLine {
			continue
		}
		for _, s := range splitSentences(p.Text) {
			sentences++
			paraSentences[p.Paragraph]++
			if l.MaxSentenceWords > 0 && countWords(s) > l.MaxSentenceWords {
				longSentences++
			}
			if isJargonSentence(s) {
				jargonSentences++
			}
		}
	}
	for _, n := range paraSentences {
		sm.LongestParagraph = max(sm.LongestParagraph, n)
	}
	sm.ProseShare = share(proseWords, sm.Words)
	sm.LongSentenceShare = share(longSentences, sentences)
	sm.JargonShare = share(jargonSentences, sentences)

	if sm.Words < minMeasuredWords {
		return sm
	}
	if l.MaxProseShare > 0 && sm.ProseShare > l.MaxProseShare {
		sm.Failures = append(sm.Failures, fmt.Sprintf("prose share %.2f > %.2f", sm.ProseShare, l.MaxProseShare))
	}
	if l.MaxParagraphSentences > 0 && sm.LongestParagraph > l.MaxParagraphSentences {
		sm.Failures = append(sm.Failures, fmt.Sprintf("longest paragraph %d sentences > %d", sm.LongestParagraph, l.MaxParagraphSentences))
	}
	if l.MaxListItems > 0 && sm.LongestList > l.MaxListItems {
		sm.Failures = append(sm.Failures, fmt.Sprintf("longest list %d items > %d", sm.LongestList, l.MaxListItems))
	}
	if l.MaxSentenceWords > 0 && sm.LongSentenceShare > maxLongSentenceShare {
		sm.Failures = append(sm.Failures, fmt.Sprintf("long sentence share %.2f > %.2f (sentences over %d words)", sm.LongSentenceShare, maxLongSentenceShare, l.MaxSentenceWords))
	}
	if l.MaxJargonShare < 1 && sm.JargonShare > l.MaxJargonShare {
		sm.Failures = append(sm.Failures, fmt.Sprintf("jargon share %.2f > %.2f", sm.JargonShare, l.MaxJargonShare))
	}
	if len(sm.Failures) > 0 {
		sm.Status = "fail"
	} else {
		sm.Status = "pass"
	}
	return sm
}

// longestList returns the item count of the longest list in lines. Items
// are counted per indent level; a shallower item ends the deeper lists
// under it. Blank lines do not end a list; any other non-list line does.
func longestList(lines []planLine) int {
	longest := 0
	counts := map[int]int{}
	for _, pl := range lines {
		if pl.listItem && !pl.inFence {
			for indent := range counts {
				if indent > pl.indent {
					delete(counts, indent)
				}
			}
			counts[pl.indent]++
			longest = max(longest, counts[pl.indent])
			continue
		}
		if pl.class == classIgnored && pl.headLevel == 0 && !pl.inFence {
			continue // blank line or rule
		}
		clear(counts)
	}
	return longest
}

// splitSentences splits prose text at ".", "!" or "?" followed by
// whitespace or the end of text. Common abbreviations ("e.g.", "i.e.",
// "etc.", "vs.", "cf.") do not end a sentence. Sentences with no word are
// dropped. The text is expected to have inline code already replaced.
func splitSentences(text string) []string {
	var out []string
	runes := []rune(text)
	start := 0
	for i, r := range runes {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
			continue
		}
		if r == '.' && abbrevRe.MatchString(string(runes[start:i+1])) {
			continue
		}
		out = appendSentence(out, string(runes[start:i+1]))
		start = i + 1
	}
	return appendSentence(out, string(runes[start:]))
}

func appendSentence(out []string, s string) []string {
	s = strings.TrimSpace(s)
	if countWords(s) == 0 {
		return out
	}
	return append(out, s)
}

// isJargonSentence reports whether a sentence holds an inline code span
// (the CODE placeholder) or a file-path-like token.
func isJargonSentence(s string) bool {
	return strings.Contains(s, codePlaceholder) || pathTokenRe.MatchString(s)
}

// bannedHits scans every line outside fences, with inline code removed,
// for the phrases (case-insensitive, whole words). The result is sorted by
// line, then phrase, with one hit per phrase per line.
func bannedHits(lines []planLine, phrases []string) []BannedHit {
	hits := []BannedHit{}
	type matcher struct {
		phrase string
		re     *regexp.Regexp
	}
	var ms []matcher
	for _, p := range phrases {
		words := strings.Fields(strings.ToLower(p))
		if len(words) == 0 {
			continue
		}
		for i, w := range words {
			words[i] = regexp.QuoteMeta(w)
		}
		ms = append(ms, matcher{
			phrase: strings.Join(strings.Fields(strings.ToLower(p)), " "),
			re:     regexp.MustCompile(`(?i)\b` + strings.Join(words, `\s+`) + `\b`),
		})
	}
	if len(ms) == 0 {
		return hits
	}
	for _, pl := range lines {
		if pl.inFence || pl.text == "" {
			continue
		}
		text := strings.ReplaceAll(pl.text, "’", "'")
		for _, m := range ms {
			if m.re.MatchString(text) {
				hits = append(hits, BannedHit{Phrase: m.phrase, Line: pl.number})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Line != hits[j].Line {
			return hits[i].Line < hits[j].Line
		}
		return hits[i].Phrase < hits[j].Phrase
	})
	return hits
}

// replaceCodeSpans replaces each inline code span (a run of N backticks
// closed by the next run of exactly N backticks) with "CODE". An
// unclosed run is kept as plain text.
func replaceCodeSpans(s string) string {
	if !strings.Contains(s, "`") {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != '`' {
			b.WriteByte(s[i])
			i++
			continue
		}
		n := runLen(s, i)
		closeAt := -1
		for j := i + n; j < len(s); {
			if s[j] != '`' {
				j++
				continue
			}
			m := runLen(s, j)
			if m == n {
				closeAt = j
				break
			}
			j += m
		}
		if closeAt < 0 {
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		b.WriteString(codePlaceholder)
		i = closeAt + n
	}
	return b.String()
}

func runLen(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

// countWords counts whitespace-separated tokens that hold at least one
// letter or digit (table pipes and separators count 0).
func countWords(s string) int {
	n := 0
	for _, tok := range strings.Fields(s) {
		if strings.IndexFunc(tok, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			n++
		}
	}
	return n
}

// share returns part/total rounded to 2 decimals, or 0 when total is 0.
// Checks compare the rounded value, so a failure message never reads
// "0.30 > 0.30".
func share(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(part)/float64(total)*100) / 100
}
