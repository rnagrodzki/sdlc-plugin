package commstyle

import "fmt"

// ClassDefNew and ClassDefChanged are the plugin's only two Mermaid
// classDef lines for new and changed nodes. Both pass the WCAG 4.5:1
// contrast check against white text. Every mermaid diagram in the guide,
// in plan diagrams, and in openspec/config.yaml copies these two lines
// exactly; a diagram contrast check (internal/commstyle/diagram.go) flags
// any other hard-to-read classDef or style line.
const (
	ClassDefNew     = "classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px"
	ClassDefChanged = "classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px"
)

// mermaidContrastRule is the closing bullet of every densityPacks entry:
// the two exact classDefs, quoted for Markdown, plus the contrast rule
// for any other color line. It is built from ClassDefNew/ClassDefChanged
// (not retyped) so the guide and the diagram check never drift apart.
var mermaidContrastRule = "In Mermaid, mark new and changed nodes only with these two classDefs, copied exactly: `" + ClassDefNew + "` and `" + ClassDefChanged + "`. Any other classDef or `style` line needs `color:` and a text-to-fill contrast of 4.5:1 or more. Do not use pastel fills."

// corePack is the <layout> tag body. It does not vary with Style: every
// writing guide leads with its conclusion (BLUF) and keeps mechanism
// (names, types, code, paths) out of prose.
const corePack = `- Start each section with its conclusion (BLUF).
- Prose says what a change does, why, and its effect.
- Mechanism goes in tables, code blocks, Mermaid diagrams, and Contract blocks.`

// audiencePacks hold the <reader> tag body, one per Audiences value.
// Each pack states the reader's stance, what the prose explains, where
// mechanism goes instead, and what to do when a sentence needs one
// anyway. Ordered from most to least jargon tolerated in prose.
var audiencePacks = map[string]string{
	"technical": `- The reader builds or reviews the code. Tell the reader what changed, how it behaves, and why.
- Describe behavior and mechanism: the exact function, type, path, and condition that make the change work.
- Code names, paths, and types may appear in prose. Use the exact name, not a paraphrase.
- Put a full signature, schema, or diff in a code block, not in a sentence.`,
	"functional": `- The reader reviews the change. Tell the reader what the change does, for whom, and with what effect.
- Describe behavior and function: inputs, outputs, what the user or system sees, what fails, what improves.
- Keep code names, paths, and types out of prose. Put them in tables, code blocks, diagrams, and contracts.
- If a sentence needs a code name, move the sentence into a table row.`,
	"executive": `- The reader decides whether to approve the change. Tell the reader the impact, the cost, and the risk.
- Describe impact, cost, risk, and the decision needed. Leave out behavior detail and mechanism.
- Keep every code name, path, and type out of prose. Put them in tables, diagrams, and contracts only.
- State the decision needed in the first sentence of the section.`,
	"general": `- The reader uses the product. Tell the reader what they see, what changed, and what to do next.
- Describe only what a user sees and does, in everyday words. Leave out internal behavior and mechanism.
- Keep code names, paths, and types out of prose. Put them in tables, code blocks, and diagrams only.
- Explain any technical term the first time you use it, in plain words.`,
	"beginner": `- The reader is new to the system. Tell the reader one idea at a time, with a concrete example.
- Describe one concept in each paragraph, followed by one example of that concept in use.
- Keep code names, paths, and types out of prose. Put them in tables, code blocks, and diagrams only.
- Do not assume the reader knows a term from an earlier section. Explain it again, in the same words.`,
}

// tonePacks hold the <tone> tag body, one per Tones value.
var tonePacks = map[string]string{
	"direct": `- Be honest. State facts and problems plainly, including bad news.
- Do not praise. Do not hedge.
- Write about the subject only. No filler, no meta-comments about the document.`,
	"neutral": `- State facts plainly. Do not add opinion or emotional language.
- Do not praise and do not criticize. Report what is true.
- Write about the subject only. No filler, no meta-comments about the document.`,
}

// standardPacks hold the <writing_standard> tag body, one per
// WritingStandards value.
//
// The "ste" entry is the full 20-rule ASD-STE100 pack, held verbatim: it
// is a golden text compared byte-for-byte in TestGuide, and
// internal/commstyle/ste.go reads the same rule set for its deterministic
// checks. Rule 4's avoid-list pairs are the same pairs held in
// steAvoidWords below; TestGuide checks every steAvoidWords pair also
// appears in rule 4's text, so the two stay in sync without generating
// rule 4 from the map (map order is not stable across runs).
//
// The avoid list is a plugin approximation of common non-STE words, not
// the ASD-STE100 dictionary, which is copyrighted.
var standardPacks = map[string]string{
	"ste": `ASD-STE100 Simplified Technical English. Follow every rule.
Words
1. Use simple, common words. Use one word for one meaning. Use the same word for the same thing every time. Do not use synonyms.
2. Use technical names (code, commands, paths, products) exactly. Use them only as nouns or adjectives.
3. Use noun clusters of 3 words or fewer. Split a longer cluster with "of", "for", or a relative clause.
4. Do not use the words in the avoid list. Use the replacement: utilize → use, leverage → use, ensure → make sure, facilitate → help, perform → do, commence → start, terminate → stop, obtain → get, sufficient → enough, numerous → many, prior to → before, subsequent → next, in order to → to, demonstrate → show, modify → change, eliminate → remove, initiate → start, regarding → about, approximately → about, additional → more.
Verbs
5. Use only these verb forms: the infinitive, the imperative, the simple present, the simple past, and the simple future.
6. Do not use perfect tenses ("has done", "have been", "had found"). Write "did" or "is".
7. Do not use "-ing" verb forms. Write "Before you start the tool", not "Before starting the tool".
8. Use the past participle only as an adjective ("the changed file") or in a description with "is" or "are".
9. Use the active voice. Use the passive voice only in a description, and only when the doer is not known.
10. Do not use phrasal verbs ("set up", "carry out", "find out", "look into", "point out"). Use one verb: "install", "do", "find", "examine", "show".
Sentences
11. Write one instruction in each sentence, unless two actions occur at the same time.
12. Write instructions in the imperative. Put a condition first: "If X, do Y."
13. Use 20 words or fewer in an instruction sentence. Use 25 words or fewer in a description sentence.
14. Keep the articles "the", "a", "an". Do not remove words to make a sentence shorter.
15. Use a pronoun only when it has one clear referent. Otherwise, repeat the noun.
16. Write a cause and its effect as two sentences: "X fails. The cause is Y."
Paragraphs and layout
17. Write one topic in each paragraph. Use 6 sentences or fewer in each paragraph. Start with the most important fact.
18. Use a vertical list for 3 or more items, steps, or conditions.
19. Start a warning with the command, then give the reason: "Do not run X. X deletes the table."
Punctuation and style
20. Do not use contractions, semicolons, idioms, slang, or figurative words. Explain each uncommon technical term the first time you use it.`,
	"plain-language": `1. Use common, everyday words. Avoid jargon unless you explain it.
2. One idea in each sentence. Keep sentences short.
3. Use the active voice.
4. Explain each uncommon technical term the first time you use it.
5. Structure a goal or a list as bullet points, not a dense paragraph.
6. State the choice, the alternative, and why one wins, in plain words.
7. Leave blank lines between ideas and between sections.
8. Read the sentence aloud. If it is hard to say, rewrite it.`,
	"developer-docs": `1. Name the exact symbol: function, type, file, or config key.
2. State the inputs, the outputs, and the error conditions.
3. Format every identifier, path, and command as code.
4. Show one example call or config snippet for each concept.
5. State the version or file path where the behavior applies.
6. Use "returns", "accepts", and "requires" to state a contract. Do not use "might" or "could".
7. Keep one topic in each section, under its own heading.
8. Link to the source file and line when a concept has one home.`,
	"smart-brevity": `1. Start with the headline fact, in bold, in one short clause.
2. Give one line of "why it matters" right after the headline.
3. Use one idea in each sentence. Keep sentences short.
4. Use a bulleted list for detail. Do not use a long paragraph.
5. Cut every word that does not change the meaning.
6. Put the one critical number or risk right after the headline.`,
}

// densityPacks hold the <visual_rules> tag body, one per VisualDensities
// value. Each pack ends with mermaidContrastRule, so every density level
// enforces the same two classDefs.
var densityPacks = map[string]string{
	"high":     buildDensityPack(7),
	"balanced": buildDensityPack(9),
	"low":      buildDensityPack(12),
}

// buildDensityPack renders one densityPacks entry for a visualDensity
// value whose maxListItems ceiling is maxListItems (the same number
// densityLimits in style.go holds for that value).
func buildDensityPack(maxListItems int) string {
	return fmt.Sprintf("- Compare 2+ options with 2+ attributes in a table.\n- Show each changed flow as a before/after Mermaid diagram.\n- Keep lists to %d items or fewer.\n- %s", maxListItems, mermaidContrastRule)
}

// standardExample is one same-content rewrite: the text before and after
// a writing standard is applied.
type standardExample struct {
	Before string
	After  string
}

// standardExamples hold one same-content before/after pair per
// WritingStandards value, shown in the guide's <example> tag.
var standardExamples = map[string]standardExample{
	"ste": {
		"The validator, which is responsible for checking audience values, will reject values that are unknown.",
		"The validator checks each audience value. It rejects a value that is not in the list.",
	},
	"plain-language": {
		"Utilize the configuration to facilitate seamless integration across numerous environments prior to deployment.",
		"Use the setting. It helps the tool work the same way in each environment before you deploy it.",
	},
	"developer-docs": {
		"The function might sometimes return an error when the input is bad.",
		"`ValidateAudience` returns `ErrInvalidAudience` when `audience` is not in the `Audiences` list.",
	},
	"smart-brevity": {
		"We wanted to let you know that, after a lot of discussion, the audience check will change in the next release to catch fewer good values as errors.",
		"Why it matters: Fewer good values will fail the audience check. The old pattern was too strict.",
	},
}

// steAvoidWords are the avoid-list pairs behind standardPacks["ste"]
// rule 4: a plugin approximation of common non-STE words, not the
// ASD-STE100 dictionary (copyrighted). internal/commstyle/ste.go reads
// this map for its deterministic avoid-word check. TestGuide checks that
// every pair here also appears in rule 4's text, so the prose rule and
// the lookup table stay in sync.
var steAvoidWords = map[string]string{
	"utilize":       "use",
	"leverage":      "use",
	"ensure":        "make sure",
	"facilitate":    "help",
	"perform":       "do",
	"commence":      "start",
	"terminate":     "stop",
	"obtain":        "get",
	"sufficient":    "enough",
	"numerous":      "many",
	"prior to":      "before",
	"subsequent":    "next",
	"in order to":   "to",
	"demonstrate":   "show",
	"modify":        "change",
	"eliminate":     "remove",
	"initiate":      "start",
	"regarding":     "about",
	"approximately": "about",
	"additional":    "more",
}
