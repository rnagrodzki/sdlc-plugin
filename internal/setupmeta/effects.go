package setupmeta

import "github.com/rnagrodzki/sdlc-plugin/internal/commstyle"

// OptionEffect tells what one option of a field does to the plugin.
type OptionEffect struct {
	Option string // one entry of Field.Options
	Text   string // what the plugin does with this option, 1 to 3 sentences
	Before string // sample text before the option applies; "" when there is no sample
	After  string // the same text after the option applies; "" when Before is ""
}

// stepEffects is the effect text of each ship step. The fields ship/steps
// and ship/quick share it.
var stepEffects = map[string]string{
	"execute":             "Runs the tasks of the plan.",
	"commit":              "Commits the changes with a generated message.",
	"review":              "Runs each review dimension on the diff and collects the findings.",
	"verify-openspec":     "Checks that the OpenSpec change files exist and pass strict validation. Ship skips it when the repo has no active change.",
	"archive-openspec":    "Syncs the task list and archives the OpenSpec change. Ship skips it when the repo has no active change.",
	"harden":              "Changes review findings into guardrail and dimension edits, and commits them.",
	"pr":                  "Pushes the branch and opens or updates the pull request.",
	"verify-pipeline":     "Waits for the CI checks of the PR. On a failure, it commits a simple fix or proposes a fix. You push the fix yourself.",
	"await-remote-review": "Waits for the automated reviewers. It handles their comments and commits the fixes.",
	"learnings-commit":    "Adds what the run learned to the learnings log. It makes no git commit.",
}

// fieldEffects maps "<section id>/<field name>" to option -> effect text.
// A boolean field uses the option names "yes" and "no".
var fieldEffects = map[string]map[string]string{
	"version/tag.enabled": {
		"yes": "Each release creates a git tag. A final release also creates a GitHub Release. An RC release gets only the tag.",
		"no":  "No tag and no GitHub Release. The version file and the changelog can still run.",
	},
	"version/versionFile.enabled": {
		"yes": "Each final release writes the new version into the version file. An RC release does not write it.",
		"no":  "The release writes no version file. The version is only in the git tags. The tag must be on.",
	},
	"version/versionFile.fileType": {
		"package.json":   "A JSON file. The release reads and writes its top-level version key.",
		"cargo.toml":     "A Rust Cargo.toml file. The release reads and writes its version.",
		"pyproject.toml": "A Python pyproject.toml file. The release reads and writes its version.",
		"pubspec.yaml":   "A Dart pubspec.yaml file. The release reads and writes its version.",
		"plugin.json":    "A plugin manifest. Same parser as package.json: the top-level version key.",
		"version-file":   "A plain text file that holds only the version string.",
	},
	"version/changelog.enabled": {
		"yes": "Each final release adds a release entry at the top of the changelog file. An RC release adds no entry.",
		"no":  "No changelog write.",
	},
	"version/method": {
		"push":             "Commits the version file and the changelog and pushes them to main. Branch protection blocks this, unless the token can bypass it.",
		"pr":               "Opens a release PR with the enabled file writes. An RC release opens no PR. The tag push is direct. A protected tag still needs a bypass identity.",
		"push-with-secret": "Deprecated. The release scripts treat it as push.",
	},
	"version/preReleasePolicy": {
		"always-rc":   "/pr always suggests a release candidate. /ship always uses an RC level, also when you give --bump.",
		"default-rc":  "/pr always suggests a release candidate. /ship uses an RC level, unless you give --bump to /ship.",
		"continue-rc": "/pr suggests a release candidate only when the bump target already has RC tags.",
		"never":       "/pr never suggests a release candidate.",
	},
	"ship/steps": stepEffects,
	"ship/quick": stepEffects,
	"ship/bump": {
		"patch": "/ship gives /pr the patch level, and /pr adds a release:patch label. After the merge, CI changes 1.2.3 to 1.2.4.",
		"minor": "/ship gives /pr the minor level, and /pr adds a release:minor label. After the merge, CI changes 1.2.3 to 1.3.0.",
		"major": "/ship gives /pr the major level, and /pr adds a release:major label. After the merge, CI changes 1.2.3 to 2.0.0.",
	},
	"ship/draft": {
		"yes": "/ship opens a new PR as a draft. A PR that already exists does not change. A standalone /pr does not read this value.",
		"no":  "/ship opens a new PR ready for review.",
	},
	"ship/auto": {
		"yes": "/ship skips its confirmation prompts and passes --auto to its sub-skills.",
		"no":  "/ship asks for approval before each gated step.",
	},
	"ship/rebase": {
		"auto":   "Ship rebases when the branch is behind the base branch.",
		"skip":   "Ship never rebases. The PR can be behind the base branch.",
		"prompt": "This version has no prompt mode. Ship does not ask, and it treats the value as information only.",
	},
	"ship/reviewThreshold": {
		"critical": "Ship sends only Critical findings to received-review for a fix. High, Medium, Low, and Info findings go to the deferred list.",
		"high":     "Ship sends Critical and High findings to received-review for a fix. Medium, Low, and Info findings go to the deferred list.",
		"medium":   "Ship sends Critical, High, and Medium findings to received-review for a fix. Low and Info findings go to the deferred list.",
		"low":      "Ship sends Critical, High, Medium, and Low findings to received-review for a fix. Info findings go to the deferred list.",
		"info":     "Ship sends every finding to received-review for a fix. No finding goes to the deferred list.",
	},
	"review/scope": {
		"all":       "Reviews the commits of the branch against the base branch. Same result as committed.",
		"committed": "Reviews the commits of the branch against the base branch.",
		"staged":    "Reviews only the staged changes.",
		"working":   "Reviews the staged and unstaged changes against HEAD.",
		"worktree":  "Reviews the commits and the uncommitted changes of tracked files, against the base branch.",
	},
	"received-review/alwaysFixSeverities": {
		"critical": "An agreed Critical finding is fixed with no consent prompt.",
		"high":     "An agreed High finding is fixed with no consent prompt.",
		"medium":   "An agreed Medium finding is fixed with no consent prompt.",
		"low":      "An agreed Low finding is fixed with no consent prompt.",
		"info":     "An agreed Info finding is fixed with no consent prompt.",
	},
	"communication-style/audience": {
		"technical":  "For a reader who builds or reviews the code. Prose explains the mechanism and can name functions, types and paths. No jargon limit.",
		"functional": "For a reader who reviews the change. Prose tells what the change does and its effect. Code names stay in tables and code blocks. At most 15% of the sentences can hold jargon.",
		"executive":  "For a reader who approves the change. Prose gives the impact, the cost, the risk and the decision needed. At most 5% of the sentences can hold jargon.",
		"general":    "For a reader who uses the product. Prose tells what the user sees and does, in everyday words. No sentence can hold jargon.",
		"beginner":   "For a reader who is new to the system. Each paragraph has one concept and one example. Each term is explained again. No sentence can hold jargon.",
	},
	"communication-style/writingStandard": {
		"ste":            "ASD-STE100 Simplified Technical English, 20 rules. A sentence has 25 words at most, an instruction 20. No -ing verbs, no perfect tenses, no phrasal verbs, no contractions. A check finds the rules that it can test word by word. A review judges the other rules.",
		"plain-language": "US federal plain language. Everyday words, one idea in each sentence, active voice. A sentence has 25 words at most.",
		"developer-docs": "Google developer style. Exact symbol names in code format, with inputs, outputs and errors. One example for each concept. A sentence has 30 words at most.",
		"smart-brevity":  "The headline fact first, then one line of why it matters, then bullets. A sentence has 20 words at most.",
	},
	"communication-style/tone": {
		"direct":  "States facts and problems plainly, bad news included. No praise and no hedging. A check flags 15 filler phrases, such as \"great question\".",
		"neutral": "States facts with no opinion, no praise and no criticism. No phrase check.",
	},
	"plan-style/visualDensity": {
		"high":     "Prose is at most 30% of a plan. A paragraph has 3 sentences at most. A list has 7 items at most.",
		"balanced": "Prose is at most 50% of a plan. A paragraph has 5 sentences at most. A list has 9 items at most.",
		"low":      "Prose is at most 75% of a plan. A paragraph has 6 sentences at most. A list has 12 items at most.",
	},
	"plan-tasks/contractShape": {
		"full":    "Each task that changes a file must have a Contract block with all five keys: shape, names, mirror, decisions, sync.",
		"minimal": "Each task that changes a file must have a Contract block. /plan does not check its keys.",
		"none":    "/plan does not check the keys of the Contract block. A task that changes a file still needs the block.",
	},
	"automation/mode": {
		"supervised": "/ship reports confirm as the mode of the next step. No step acts on this value now.",
		"unattended": "/ship reports auto as the mode of the next step. The read config also holds yes for push.featureBranchAutoApprove. No step acts on these values now. Use the ship value auto to skip the prompts of /ship.",
	},
	"automation/report.enabled": {
		"yes": "/ship writes a run report to .sdlc-v2/reports/ at the end of a run.",
		"no":  "/ship writes no run report.",
	},
	"automation/report.format": {
		"md":   "A Markdown report, for a person to read.",
		"json": "A JSON report, for a script to parse.",
	},
	"automation/push.featureBranchAutoApprove": {
		"yes": pushAutoApproveEffect,
		"no":  pushAutoApproveEffect,
	},
}

// pushAutoApproveEffect is the shared text of both options of
// automation/push.featureBranchAutoApprove: no code reads the value.
const pushAutoApproveEffect = "No step reads this value now. With the mode unattended, the read config holds yes, also when the file holds no. The commit step of /ship pushes the feature branch with no question. The push skips the default branch."

// EffectsFor returns the effects of field f of section sectionID, in
// Options order. It returns nil for a field that is not enum, multi-enum,
// flag-set or boolean. It adds the sample of commstyle.StandardExample to
// each option of communication-style/writingStandard.
func EffectsFor(sectionID string, f Field) []OptionEffect {
	switch f.Type {
	case "enum", "multi-enum", "flag-set", "boolean":
	default:
		return nil
	}
	texts := fieldEffects[sectionID+"/"+f.Name]
	out := make([]OptionEffect, 0, len(f.Options))
	for _, opt := range f.Options {
		e := OptionEffect{Option: opt, Text: texts[opt]}
		if sectionID == "communication-style" && f.Name == "writingStandard" {
			if pair, ok := commstyle.StandardExample(opt); ok {
				e.Before, e.After = pair.Before, pair.After
			}
		}
		out = append(out, e)
	}
	return out
}
