package commstyle

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ste.go holds the deterministic subset of the ASD-STE100 rules for plan
// prose. Rules that need meaning (noun clusters, one meaning per word,
// articles, condition first, warnings) are judged by the G22 review lane,
// not here. Every check is a word-level heuristic that accepts some false
// negatives to keep false positives low: PF13 is a hard gate, so a false
// hit blocks a correct plan.
//
// Exemptions: inline code is already the CODE placeholder and fenced
// blocks and table rows never reach the prose lines, so the only
// exemption applied here is Limits.TechnicalTerms (case-insensitive,
// whole word).

const (
	// steMaxDescriptionWords is the STE rule 13 limit for a description
	// sentence. No tolerance share applies.
	steMaxDescriptionWords = 25
	// steMaxParagraphSentences is the STE rule 17 limit for a paragraph.
	steMaxParagraphSentences = 6
	// steMaxHits caps the hit list; the rest are reported as one
	// "truncated" entry.
	steMaxHits = 50
	// steFirstWords is how many words of a long sentence a length hit
	// quotes.
	steFirstWords = 6
)

// steContractions is the closed contraction list. A word that ends in
// "n't" is always a contraction; any other "'s" word not listed here is a
// possessive.
var steContractions = map[string]bool{
	"it's": true, "that's": true, "what's": true, "let's": true, "who's": true,
	"there's": true, "here's": true, "he's": true, "she's": true,
	"i'm": true, "you're": true, "we're": true, "they're": true,
	"i'll": true, "you'll": true, "we'll": true, "they'll": true,
	"i've": true, "you've": true, "we've": true, "they've": true,
	"i'd": true, "you'd": true,
}

// steBeForms are the forms of "be" that make an "-ing" word a verb
// ("is running") and an instruction clause passive ("is deleted").
var steBeForms = map[string]bool{
	"be": true, "is": true, "are": true, "was": true, "were": true, "been": true,
}

// steIngVerbTriggers are the words that make the "-ing" word right after
// them a verb: prepositions ("before starting") and forms of "be"
// ("is running").
var steIngVerbTriggers = map[string]bool{
	"before": true, "after": true, "by": true, "for": true, "when": true,
	"while": true, "without": true, "on": true, "in": true, "of": true,
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
}

// steIngNonVerbs end in "-ing" but are never "-ing" verb forms.
var steIngNonVerbs = map[string]bool{
	"thing": true, "something": true, "anything": true, "nothing": true,
	"everything": true, "string": true, "spring": true, "during": true,
	"bring": true, "swing": true, "sting": true, "sibling": true,
	"ceiling": true, "morning": true, "evening": true,
}

// steIngAdjectives are "-ing" words common as adjectives before a noun
// ("of existing files", "in pending state"). After a preposition, such a
// word followed directly by a plain word is not a hit.
var steIngAdjectives = map[string]bool{
	"existing": true, "pending": true, "missing": true, "remaining": true,
	"following": true, "matching": true, "corresponding": true, "upcoming": true,
	"outstanding": true, "incoming": true, "outgoing": true, "ongoing": true,
	"underlying": true, "surrounding": true, "leading": true, "trailing": true,
	"preceding": true, "resulting": true, "failing": true, "passing": true,
	"running": true, "blocking": true, "overlapping": true, "conflicting": true,
	"competing": true, "supporting": true, "enclosing": true, "containing": true,
	"owning": true, "calling": true, "working": true,
}

// steIrregularParticiples are past participles that do not end in "-ed".
var steIrregularParticiples = map[string]bool{
	"been": true, "done": true, "gone": true, "seen": true, "taken": true,
	"given": true, "written": true, "broken": true, "chosen": true, "driven": true,
	"eaten": true, "fallen": true, "forgotten": true, "frozen": true, "gotten": true,
	"hidden": true, "known": true, "shown": true, "grown": true, "thrown": true,
	"drawn": true, "flown": true, "blown": true, "spoken": true, "stolen": true,
	"woken": true, "worn": true, "torn": true, "sworn": true, "begun": true,
	"made": true, "found": true, "built": true, "sent": true, "kept": true,
	"held": true, "brought": true, "bought": true, "thought": true, "taught": true,
	"caught": true, "sold": true, "told": true, "said": true, "paid": true,
	"left": true, "lost": true, "met": true, "meant": true, "felt": true,
	"heard": true, "understood": true, "become": true, "won": true, "got": true,
	"put": true, "set": true, "read": true, "split": true, "run": true,
}

// steNotParticiples end in "-ed" or "-en" but are not past participles.
var steNotParticiples = map[string]bool{
	"need": true, "speed": true, "feed": true, "seed": true, "bed": true,
	"red": true, "shed": true, "hundred": true, "sacred": true, "naked": true,
	"wicked": true, "kindred": true, "embed": true,
	"open": true, "often": true, "even": true, "ten": true, "seven": true,
	"eleven": true, "token": true, "garden": true, "green": true, "screen": true,
	"queen": true, "between": true, "children": true, "oxygen": true,
	"citizen": true, "chicken": true, "dozen": true, "women": true, "men": true,
	"kitchen": true, "listen": true, "golden": true, "wooden": true,
	"sudden": true, "hyphen": true, "linen": true, "heaven": true, "when": true,
	"then": true, "than": true,
}

// stePhrasalVerbs are the phrasal verbs STE rule 10 forbids, in base
// form. The head verb also matches its "-s", "-ed", "-ing", and
// irregular forms ("carried out", "found out").
var stePhrasalVerbs = []string{
	"set up", "carry out", "find out", "look into", "point out", "figure out",
	"come up with", "go through", "make up", "turn on", "turn off", "fill in",
	"check out", "back up", "clean up",
}

// steIrregularForms are the forms of a phrasal head verb or an avoid word
// that steInflections does not build.
var steIrregularForms = map[string][]string{
	"set":  {"setting"},
	"find": {"found"},
	"come": {"came"},
	"go":   {"goes", "went", "gone"},
	"make": {"made"},
}

// steImperativeVerbs are the verbs that make a sentence an instruction
// when the sentence starts with one. A true value marks a verb that is
// also a common noun ("Run IDs are listed", "Use cases"): it counts only
// when an object starter (an article, a pronoun, CODE) follows it.
var steImperativeVerbs = map[string]bool{
	"add": false, "append": false, "apply": false, "ask": false, "avoid": false,
	"choose": false, "confirm": false, "create": false, "define": false,
	"delete": false, "describe": false, "disable": false, "do": false,
	"don't": false, "emit": false, "enable": false, "examine": false,
	"explain": false, "find": false, "follow": false, "give": false,
	"include": false, "insert": false, "install": false, "keep": false,
	"let": false, "make": false, "move": false, "never": false, "always": false,
	"put": false, "remove": false, "rename": false, "replace": false,
	"resolve": false, "select": false, "send": false, "skip": false,
	"take": false, "tell": false, "treat": false, "trim": false, "verify": false,
	"build": true, "call": true, "change": true, "check": true, "copy": true,
	"drop": true, "edit": true, "list": true, "load": true, "mark": true,
	"open": true, "pass": true, "print": true, "read": true, "record": true,
	"report": true, "reset": true, "restore": true, "retry": true, "return": true,
	"run": true, "save": true, "set": true, "show": true, "sort": true,
	"start": true, "stop": true, "test": true, "update": true, "use": true,
	"wait": true, "write": true,
}

// steNotImperativeNext are words that, right after a sentence's first
// word, show the first word is a noun ("Test is slow").
var steNotImperativeNext = map[string]bool{
	"is": true, "are": true, "was": true, "were": true, "has": true,
	"have": true, "had": true, "can": true, "will": true, "must": true,
	"should": true, "may": true, "does": true, "did": true,
}

// steConditionWords start a condition-first instruction: "If X, do Y."
var steConditionWords = map[string]bool{
	"if": true, "when": true, "before": true, "after": true, "unless": true, "once": true,
}

// steSubordinators end the main clause of an instruction; a passive verb
// after one is in a description clause ("Run X when Y is required").
var steSubordinators = map[string]bool{
	"if": true, "when": true, "because": true, "so": true, "that": true,
	"which": true, "until": true, "unless": true, "after": true, "before": true,
	"while": true, "where": true, "once": true, "since": true,
}

// steObjectStarters are words that start an object: articles,
// determiners, and pronouns. The CODE placeholder also starts an object
// (see steIsObjectStart).
var steObjectStarters = map[string]bool{
	"the": true, "a": true, "an": true, "this": true, "that": true,
	"these": true, "those": true, "each": true, "every": true, "all": true,
	"any": true, "some": true, "no": true, "its": true, "their": true,
	"his": true, "her": true, "our": true, "your": true, "my": true,
	"it": true, "them": true, "him": true, "us": true, "me": true,
	"you": true, "one": true, "both": true,
}

// steFunctionWords are object starters plus prepositions, conjunctions,
// and short adverbs. A word after a participle or an "-ing" word that is
// not one of these (and not the CODE placeholder) is read as a noun the
// participle describes.
var steFunctionWords = func() map[string]bool {
	m := map[string]bool{
		"to": true, "in": true, "on": true, "for": true, "with": true,
		"by": true, "from": true, "into": true, "at": true, "as": true,
		"of": true, "after": true, "before": true, "when": true, "while": true,
		"since": true, "than": true, "then": true, "and": true, "or": true,
		"but": true, "if": true, "so": true, "also": true, "not": true,
		"only": true, "just": true, "now": true, "again": true, "here": true,
		"there": true, "out": true, "up": true,
	}
	for w := range steObjectStarters {
		m[w] = true
	}
	return m
}()

// stePerfectAdverbs may stand between "has" and the participle.
var stePerfectAdverbs = map[string]bool{
	"not": true, "never": true, "already": true, "just": true, "also": true, "now": true,
}

// steObligationModals make "<modal> be <participle>" a passive
// instruction whatever the sentence's first word is.
var steObligationModals = map[string]bool{"must": true, "should": true, "shall": true}

var (
	steTokenRe     = regexp.MustCompile(`[\p{L}\p{N}_]+(?:'[\p{L}\p{N}_]+)*`)
	steSemicolonRe = regexp.MustCompile(`\S*;[ \t]*\S*`)
	stePhrasalRes  = buildStePhraseMatchers(stePhrasalVerbs)
	steAvoidRes    = buildStePhraseMatchers(sortedKeys(steAvoidWords))
)

// steToken is one word of a sentence, with its byte span.
type steToken struct {
	text, lower string
	start, end  int
}

// steCheck scans prose lines (the line classes of Measure) and returns the
// strict STE rule breaks, sorted by line, then rule, capped at steMaxHits
// plus one "truncated" entry. It returns an empty, non-nil slice when
// l.STE is false.
func steCheck(lines []proseLine, l Limits) []SteHit {
	hits := []SteHit{}
	if !l.STE {
		return hits
	}
	terms := compileSteTerms(l.TechnicalTerms)

	type paragraph struct{ line, sentences int }
	paras := map[int]*paragraph{}
	var order []int
	for _, pl := range lines {
		text := maskSteTerms(strings.ReplaceAll(pl.Text, "’", "'"), terms)
		sentences := splitSentences(text)
		p, ok := paras[pl.Paragraph]
		if !ok {
			p = &paragraph{line: pl.Line}
			paras[pl.Paragraph] = p
			order = append(order, pl.Paragraph)
		}
		p.sentences += len(sentences)
		for i, s := range sentences {
			hits = append(hits, steSentenceHits(pl.Line, s, pl.Numbered && i == 0, l)...)
		}
	}
	for _, idx := range order {
		if p := paras[idx]; p.sentences > steMaxParagraphSentences {
			hits = append(hits, SteHit{Rule: "paragraph-length", Text: fmt.Sprintf("%d sentences", p.sentences), Line: p.line})
		}
	}
	return finishSteHits(hits)
}

// steSentenceHits applies every sentence-level rule to one sentence.
// numberedFirst is true for the first sentence of a numbered or "- [ ]"
// list item, which is always an instruction.
func steSentenceHits(line int, s string, numberedFirst bool, l Limits) []SteHit {
	var hits []SteHit
	add := func(rule, text string) {
		hits = append(hits, SteHit{Rule: rule, Text: text, Line: line})
	}
	toks := steTokens(s)

	for _, t := range toks {
		if steContractions[t.lower] || strings.HasSuffix(t.lower, "n't") {
			add("contraction", t.text)
		}
	}
	for _, m := range steSemicolonRe.FindAllString(s, -1) {
		add("semicolon", strings.TrimSpace(m))
	}
	for _, text := range steIngHits(s, toks) {
		add("ing-form", text)
	}
	for _, text := range stePerfectHits(s, toks) {
		add("perfect-tense", text)
	}
	for _, re := range stePhrasalRes {
		for _, m := range re.FindAllString(s, -1) {
			add("phrasal-verb", m)
		}
	}
	for _, re := range steAvoidRes {
		for _, m := range re.FindAllString(s, -1) {
			add("avoid-word", m)
		}
	}

	words := countWords(s)
	clause, instruction := steInstructionClause(s, toks, numberedFirst)
	switch {
	case instruction && l.MaxInstructionWords > 0:
		if words > l.MaxInstructionWords {
			add("instruction-length", steLengthText(s, words))
		}
	case words > steMaxDescriptionWords:
		add("description-length", steLengthText(s, words))
	}
	if instruction {
		for _, text := range stePassiveClauseHits(s, clause) {
			add("passive-instruction", text)
		}
	}
	for _, text := range steModalPassiveHits(s, toks) {
		add("passive-instruction", text)
	}
	return hits
}

// steIngHits returns each "-ing" word used as a verb: right after a form
// of "be", or right after a preposition unless it is a known adjective
// followed directly by a plain word ("of existing files").
func steIngHits(s string, toks []steToken) []string {
	var out []string
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		if !strings.HasSuffix(t.lower, "ing") || utf8.RuneCountInString(t.lower) < 5 || steIngNonVerbs[t.lower] {
			continue
		}
		prev := toks[i-1]
		if !steAdjacent(s, prev, t) || !steIngVerbTriggers[prev.lower] {
			continue
		}
		if !steBeForms[prev.lower] && steIngAdjectives[t.lower] && steNextIsPlainWord(s, toks, i) {
			continue
		}
		out = append(out, t.text)
	}
	return out
}

// stePerfectHits returns each "has/have/had [adverb] <participle>". A
// participle other than "been" followed directly by a plain word is read
// as an adjective ("has limited support") and is not a hit.
func stePerfectHits(s string, toks []steToken) []string {
	var out []string
	for i := 0; i+1 < len(toks); i++ {
		if h := toks[i].lower; h != "has" && h != "have" && h != "had" {
			continue
		}
		j := i + 1
		if stePerfectAdverbs[toks[j].lower] && j+1 < len(toks) && steAdjacent(s, toks[i], toks[j]) {
			j++
		}
		if !steAdjacent(s, toks[j-1], toks[j]) || !steIsParticiple(toks[j].lower) {
			continue
		}
		if toks[j].lower != "been" && steNextIsPlainWord(s, toks, j) {
			continue
		}
		out = append(out, s[toks[i].start:toks[j].end])
	}
	return out
}

// steInstructionClause reports whether s is an instruction sentence and
// returns the tokens of its main clause. An instruction is the first
// sentence of a numbered item, a sentence that starts with an imperative
// verb, or a condition-first sentence ("If X, do Y.") whose clause after
// the first comma starts with an imperative verb.
func steInstructionClause(s string, toks []steToken, numberedFirst bool) ([]steToken, bool) {
	if len(toks) == 0 {
		return nil, false
	}
	if numberedFirst || steImperativeAt(s, toks, 0) {
		return steMainClause(s, toks, 0), true
	}
	if steConditionWords[toks[0].lower] {
		comma := strings.Index(s, ",")
		if comma < 0 {
			return nil, false
		}
		for k, t := range toks {
			if t.start > comma {
				if steImperativeAt(s, toks, k) {
					return steMainClause(s, toks, k), true
				}
				break
			}
		}
	}
	return nil, false
}

// steImperativeAt reports whether toks[k] is an imperative verb that
// starts a clause.
func steImperativeAt(s string, toks []steToken, k int) bool {
	needsObject, ok := steImperativeVerbs[toks[k].lower]
	if !ok {
		return false
	}
	hasNext := k+1 < len(toks) && steAdjacent(s, toks[k], toks[k+1])
	if hasNext && steNotImperativeNext[toks[k+1].lower] {
		return false
	}
	if needsObject {
		return hasNext && steIsObjectStart(toks[k+1])
	}
	return true
}

// steIsObjectStart reports whether t starts an object: an object starter
// word or the CODE placeholder.
func steIsObjectStart(t steToken) bool {
	return steObjectStarters[t.lower] || t.text == codePlaceholder
}

// steMainClause returns toks from k up to the first subordinator or the
// first comma, colon, or semicolon.
func steMainClause(s string, toks []steToken, k int) []steToken {
	for j := k + 1; j < len(toks); j++ {
		if steSubordinators[toks[j].lower] || strings.ContainsAny(s[toks[j-1].end:toks[j].start], ",:;") {
			return toks[k:j]
		}
	}
	return toks[k:]
}

// stePassiveClauseHits returns each "is|are|was|were|be <participle>" in
// an instruction's main clause. A "be" after an obligation modal is left
// to steModalPassiveHits.
func stePassiveClauseHits(s string, clause []steToken) []string {
	var out []string
	for i := 0; i+1 < len(clause); i++ {
		b := clause[i]
		if !steBeForms[b.lower] || b.lower == "been" || !steAdjacent(s, b, clause[i+1]) || !steIsParticiple(clause[i+1].lower) {
			continue
		}
		if b.lower == "be" && steAfterModal(clause, i) {
			continue
		}
		out = append(out, s[b.start:clause[i+1].end])
	}
	return out
}

// steModalPassiveHits returns each "must|should|shall [not] be
// <participle>" in s: an instruction in the passive voice whatever the
// sentence's first word is ("The file must be deleted.").
func steModalPassiveHits(s string, toks []steToken) []string {
	var out []string
	for i := 0; i < len(toks); i++ {
		if !steObligationModals[toks[i].lower] {
			continue
		}
		j := i + 1
		if j < len(toks) && toks[j].lower == "not" {
			j++
		}
		if j+1 >= len(toks) || toks[j].lower != "be" || !steIsParticiple(toks[j+1].lower) {
			continue
		}
		if !steAdjacent(s, toks[i], toks[i+1]) || !steAdjacent(s, toks[j-1], toks[j]) || !steAdjacent(s, toks[j], toks[j+1]) {
			continue
		}
		out = append(out, s[toks[i].start:toks[j+1].end])
	}
	return out
}

// steAfterModal reports whether the "be" at toks[i] follows an obligation
// modal, with an optional "not" between.
func steAfterModal(toks []steToken, i int) bool {
	if i >= 1 && steObligationModals[toks[i-1].lower] {
		return true
	}
	return i >= 2 && toks[i-1].lower == "not" && steObligationModals[toks[i-2].lower]
}

// steIsParticiple reports whether w is a past participle: an irregular
// form, or a word of 4+ letters ending in "-ed", or of 5+ letters ending
// in "-en", that is not in steNotParticiples.
func steIsParticiple(w string) bool {
	if steNotParticiples[w] {
		return false
	}
	if steIrregularParticiples[w] {
		return true
	}
	n := utf8.RuneCountInString(w)
	return (n >= 4 && strings.HasSuffix(w, "ed")) || (n >= 5 && strings.HasSuffix(w, "en"))
}

// steNextIsPlainWord reports whether toks[i] is followed directly (only
// spaces between) by a word that is not a function word.
func steNextIsPlainWord(s string, toks []steToken, i int) bool {
	if i+1 >= len(toks) || !steAdjacent(s, toks[i], toks[i+1]) {
		return false
	}
	next := toks[i+1]
	return !steFunctionWords[next.lower] && next.text != codePlaceholder
}

// steAdjacent reports whether only whitespace separates a and b in s.
func steAdjacent(s string, a, b steToken) bool {
	return strings.TrimSpace(s[a.end:b.start]) == ""
}

// steTokens splits s into words. An apostrophe inside a word is kept
// ("don't", "task's").
func steTokens(s string) []steToken {
	idx := steTokenRe.FindAllStringIndex(s, -1)
	out := make([]steToken, len(idx))
	for i, m := range idx {
		text := s[m[0]:m[1]]
		out[i] = steToken{text: text, lower: strings.ToLower(text), start: m[0], end: m[1]}
	}
	return out
}

// steLengthText quotes the first words of a long sentence and its word
// count, for a length hit.
func steLengthText(s string, words int) string {
	fields := strings.Fields(s)
	if len(fields) > steFirstWords {
		fields = append(fields[:steFirstWords], "...")
	}
	return fmt.Sprintf("%s (%d words)", strings.Join(fields, " "), words)
}

// buildStePhraseMatchers compiles one case-insensitive whole-word matcher
// per phrase. The first word also matches its inflected forms.
func buildStePhraseMatchers(phrases []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(phrases))
	for _, p := range phrases {
		words := strings.Fields(strings.ToLower(p))
		if len(words) == 0 {
			continue
		}
		heads := steInflections(words[0])
		for i, h := range heads {
			heads[i] = regexp.QuoteMeta(h)
		}
		parts := []string{"(?:" + strings.Join(heads, "|") + ")"}
		for _, w := range words[1:] {
			parts = append(parts, regexp.QuoteMeta(w))
		}
		out = append(out, regexp.MustCompile(`(?i)\b`+strings.Join(parts, `\s+`)+`\b`))
	}
	return out
}

// steInflections returns w with its regular "-s", "-ed", and "-ing"
// forms and its steIrregularForms entries. Forms that are not real words
// ("numerouses") are harmless: they never match.
func steInflections(w string) []string {
	forms := []string{w}
	switch {
	case strings.HasSuffix(w, "e"):
		forms = append(forms, w+"s", w+"d", w[:len(w)-1]+"ing")
	case strings.HasSuffix(w, "y") && len(w) > 1 && !strings.ContainsRune("aeiou", rune(w[len(w)-2])):
		stem := w[:len(w)-1]
		forms = append(forms, stem+"ies", stem+"ied", w+"ing")
	case strings.HasSuffix(w, "s") || strings.HasSuffix(w, "sh") || strings.HasSuffix(w, "ch") || strings.HasSuffix(w, "x"):
		forms = append(forms, w+"es", w+"ed", w+"ing")
	default:
		forms = append(forms, w+"s", w+"ed", w+"ing")
	}
	return append(forms, steIrregularForms[w]...)
}

// compileSteTerms builds one case-insensitive matcher per technical term,
// longest term first so a multi-word term is masked before a term inside
// it.
func compileSteTerms(terms []string) []*regexp.Regexp {
	clean := make([]string, 0, len(terms))
	for _, t := range terms {
		if t = strings.TrimSpace(t); t != "" {
			clean = append(clean, t)
		}
	}
	sort.SliceStable(clean, func(i, j int) bool { return len(clean[i]) > len(clean[j]) })
	out := make([]*regexp.Regexp, len(clean))
	for i, t := range clean {
		out[i] = regexp.MustCompile(`(?i)` + regexp.QuoteMeta(t))
	}
	return out
}

// maskSteTerms replaces each whole-word match of a technical term with
// the CODE placeholder, so no STE word rule can hit it. A match counts as
// whole-word when no letter, digit, or underscore touches either end.
func maskSteTerms(s string, terms []*regexp.Regexp) string {
	for _, re := range terms {
		var b strings.Builder
		last := 0
		for _, m := range re.FindAllStringIndex(s, -1) {
			if !steWordBoundary(s, m[0], m[1]) {
				continue
			}
			b.WriteString(s[last:m[0]])
			b.WriteString(codePlaceholder)
			last = m[1]
		}
		if last == 0 {
			continue
		}
		b.WriteString(s[last:])
		s = b.String()
	}
	return s
}

func steWordBoundary(s string, start, end int) bool {
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	if start > 0 {
		if r, _ := utf8.DecodeLastRuneInString(s[:start]); isWord(r) {
			return false
		}
	}
	if end < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[end:]); isWord(r) {
			return false
		}
	}
	return true
}

// finishSteHits drops duplicate hits (same line, rule, and text, ignoring
// case), sorts by line, then rule, and caps the list at steMaxHits plus
// one "truncated" entry whose Line is the first left-out hit's line.
func finishSteHits(hits []SteHit) []SteHit {
	out := []SteHit{}
	seen := map[string]bool{}
	for _, h := range hits {
		key := fmt.Sprintf("%d\x00%s\x00%s", h.Line, h.Rule, strings.ToLower(h.Text))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Rule < out[j].Rule
	})
	if len(out) <= steMaxHits {
		return out
	}
	rest := out[steMaxHits:]
	return append(out[:steMaxHits:steMaxHits], SteHit{Rule: "truncated", Text: fmt.Sprintf("%d more", len(rest)), Line: rest[0].Line})
}

// sortedKeys returns the keys of m in sorted order, so matchers built
// from a map are in a stable order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
