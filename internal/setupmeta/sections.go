// Package setupmeta is the Go port of scripts/lib/setup-sections.js
// (sdlc-utilities plugin). It provides the canonical SETUP_SECTIONS
// descriptor list consumed by the setup skill and the setup_prepare tool.
//
// The section IDs are frozen — skills reference them verbatim and they must
// match the source in both content and order.
package setupmeta

// Field describes one configuration field within a setup section.
// It mirrors the shape { name, label, type, options, default, description }
// from the Node.js source's field arrays (VERSION_FIELDS, JIRA_FIELDS, etc.).
//
// Validate functions and runtime-computed defaults (detectBaseBranchSafe,
// parseRemoteOwner) are deliberately omitted: validation is enforced at
// prepare time by the consuming tool, not by the descriptor.
type Field struct {
	Name        string   // config key name
	Label       string   // short human-readable label
	Type        string   // "string" | "enum" | "boolean" | "number" | "multi-select" | "multi-enum" | "list"
	Options     []string // valid values for enum/multi-select/multi-enum/boolean; nil when unconstrained
	Default     any      // default value (string, bool, int, []string, or nil)
	Description string   // one-or-two-sentence description naming the consuming skill

	// Min and Max constrain numeric fields. nil means unconstrained on that end.
	// Mirrors min/max from the Node.js source field descriptors.
	Min *int
	Max *int

	// WhenStepInActiveSteps, when non-empty, indicates this field is
	// conditionally displayed only when the named step is in the active
	// steps list. Mirrors `when: { stepInActiveSteps: '...' }` from the
	// Node.js source.
	WhenStepInActiveSteps string
}

// Section describes one setup configuration section.
// Order and IDs match the source SETUP_SECTIONS array exactly.
type Section struct {
	ID              string   // canonical section id (used by --only flag)
	Label           string   // short human-readable name (menu row)
	Purpose         string   // one-paragraph runtime explanation
	ConfigFile      string   // ".sdlc-v2/config.json" | ".sdlc-v2/local.json" | "<delegated>" | path
	ConfigPath      string   // dot-path within ConfigFile, or "" for delegated/content sections
	ConsumedBy      []string // skill ids that read this section at runtime
	FilesModified   []string // workspace artifacts created or touched
	Optional        bool     // true → safe to leave unset
	DelegatedTo     string   // sub-skill id (for content/conditional sections) or ""
	ConfirmDetected bool     // true → ask "use detected? / customize / skip"
	Fields          []Field  // configuration fields; empty for delegated sections
}

// --- Field arrays ---
// Mirrors VERSION_FIELDS, JIRA_FIELDS, REVIEW_FIELDS, RECEIVED_REVIEW_FIELDS,
// SHIP_FIELDS from the Node.js source. Ship fields are a package-level slice
// so the ship section references the same data (Go equivalent of the JS
// identity check `_shipEntry.fields === SHIP_FIELDS`).

var versionFields = []Field{
	{
		Name:        "mode",
		Label:       "Version source mode",
		Type:        "enum",
		Options:     []string{"file", "tag"},
		Default:     "file",
		Description: "Tells /version and /ship whether the canonical version lives in a file (`file`) or only in git tags (`tag`). The default `file` mode requires a versionFile path; pick `tag` for projects that derive every release from `git describe`.",
	},
	{
		Name:        "versionFile",
		Label:       "Version file path",
		Type:        "string",
		Options:     nil,
		Default:     "package.json",
		Description: "Path to the file that holds the canonical version string. /version reads and rewrites this file on each bump; setup auto-detects common paths (package.json, Cargo.toml, pyproject.toml, plugin.json) but you can override here. Ignored when mode is `tag`.",
	},
	{
		Name:        "fileType",
		Label:       "Version file format",
		Type:        "enum",
		Options:     []string{"package.json", "cargo.toml", "pyproject.toml", "pubspec.yaml", "plugin.json", "version-file"},
		Default:     "package.json",
		Description: "Format used by /version to parse and rewrite the version file. The default `package.json` reads the top-level `version` key; `version-file` is a plain-text file containing only the version string. Ignored when mode is `tag`.",
	},
	{
		Name:        "tagPrefix",
		Label:       "Git tag prefix",
		Type:        "string",
		Options:     nil,
		Default:     "v",
		Description: "Prefix prepended to the version when /version creates a release tag (e.g., prefix `v` produces `v1.2.3`). Empty string is allowed for projects that tag with bare semver. Detected from existing tags when possible.",
	},
	{
		Name:        "changelog",
		Label:       "Generate CHANGELOG on release?",
		Type:        "boolean",
		Options:     []string{"yes", "no"},
		Default:     false,
		Description: "When true, /version and /ship append a release entry to changelogFile (default `CHANGELOG.md`) on every bump. Default `no` keeps the workflow lean — enable if your project publishes release notes.",
	},
	{
		Name:        "changelogFile",
		Label:       "CHANGELOG file path",
		Type:        "string",
		Options:     nil,
		Default:     "CHANGELOG.md",
		Description: "Path to the changelog file appended by /version when changelog is enabled. Default `CHANGELOG.md` matches the conventional location at repo root. Ignored when changelog is disabled.",
	},
	{
		Name:        "preRelease",
		Label:       "Default pre-release label",
		Type:        "string",
		Options:     nil,
		Default:     "",
		Description: "When set (e.g., `rc`, `beta`, `alpha`), /version and /ship default to a pre-release bump (e.g., `1.2.4-rc.1`) on every default invocation until an explicit `major|minor|patch` graduates the release. Must match `^[a-z][a-z0-9]*$`; empty string omits the field and preserves stable-release behavior.",
	},
}

var jiraFields = []Field{
	{
		Name:        "defaultProject",
		Label:       "Default Jira project key",
		Type:        "string",
		Options:     nil,
		Default:     "",
		Description: "Project key (2–10 uppercase letters, e.g., `PROJ`) used by /jira when no explicit project is supplied. /commit and /pr also use it when extracting ticket IDs from branch names. Empty string disables Jira integration for the project.",
	},
}

var reviewFields = []Field{
	{
		Name:        "scope",
		Label:       "Default review scope",
		Type:        "enum",
		Options:     []string{"all", "committed", "staged", "working", "worktree"},
		Default:     "committed",
		Description: "Default scope for /review when no `--committed`/`--staged`/`--working`/`--worktree` flag is passed. `committed` (default) reviews commits on the current branch vs the default branch; `working` reviews staged + unstaged; `all` includes untracked.",
	},
}

var receivedReviewFields = []Field{
	{
		Name:        "alwaysFixSeverities",
		Label:       "Severities to auto-apply without consent",
		Type:        "multi-enum",
		Options:     []string{"low", "medium", "high", "critical"},
		Default:     []string{},
		Description: "Severities whose \"agree, will fix\" findings bypass the per-finding consent gate in /received-review (Step 10 / Step 12). Stored in .sdlc-v2/local.json under receivedReview.alwaysFixSeverities — per-developer, never project-wide. Default `[]` preserves the original consent-on-every-finding behavior; e.g. `[\"critical\",\"high\"]` auto-applies high-impact fixes without prompting.",
	},
}

// intPtr returns a pointer to v. Used for Field.Min / Field.Max.
func intPtr(v int) *int { return &v }

// CanonicalSteps lists the pipeline steps that may appear in ship.steps[].
// Order matters — it is the default ordering and iteration order.
// Mirrors CANONICAL_STEPS from scripts/lib/ship-fields.js.
var CanonicalSteps = []string{
	"execute", "commit", "review", "version", "verify-openspec",
	"archive-openspec", "pr", "verify-pipeline", "await-remote-review",
	"learnings-commit",
}

// ShipFields mirrors SHIP_FIELDS from scripts/lib/ship-fields.js.
// The ship section references this slice directly (Go equivalent of the JS
// identity invariant `_shipEntry.fields === SHIP_FIELDS`).
var ShipFields = []Field{
	{
		Name:        "steps",
		Label:       "Pipeline steps to run",
		Type:        "multi-select",
		Options:     append([]string{}, CanonicalSteps...),
		Default:     append([]string{}, CanonicalSteps...),
		Description: "Pipeline steps to run by default. received-review and commit-fixes run conditionally based on review verdict and are not configurable here. verify-pipeline and await-remote-review are opt-in entries — add them explicitly to enable post-PR CI verification and remote-reviewer awaiting. verify-openspec is an OpenSpec-gated opt-in — add it explicitly to run `openspec validate --strict <change>` between version and archive-openspec.",
	},
	{
		Name:        "quick",
		Label:       "Optional --quick profile steps",
		Type:        "multi-select",
		Options:     append([]string{}, CanonicalSteps...),
		Default:     nil,
		Description: "Optional shortened step list used when ship is invoked with --quick. Same enum as steps. Leave unset to disable the --quick flag for this project.",
	},
	{
		Name:        "bump",
		Label:       "Default version bump level",
		Type:        "enum",
		Options:     []string{"patch", "minor", "major"},
		Default:     "patch",
		Description: "Applied by /version when no explicit bump argument is passed. The runtime value space is wider than this questionnaire presents: ship.bump in .sdlc-v2/local.json may also be a pre-release label matching `^[a-z][a-z0-9]*$` (e.g., `rc`, `beta`); enter such values via `ship-init.js --bump <label>` or by editing the config file. Schema (schemas/sdlc-local.schema.json) validates the union pattern.",
	},
	{
		Name:        "draft",
		Label:       "Open PRs as drafts?",
		Type:        "boolean",
		Options:     []string{"yes", "no"},
		Default:     false,
		Description: "Default value for the --draft flag on /pr",
	},
	{
		Name:        "auto",
		Label:       "Run pipeline non-interactively?",
		Type:        "boolean",
		Options:     []string{"yes", "no"},
		Default:     false,
		Description: "Skip interactive approval prompts throughout the ship pipeline",
	},
	{
		Name:        "rebase",
		Label:       "Rebase before shipping?",
		Type:        "enum",
		Options:     []string{"auto", "skip", "prompt"},
		Default:     "auto",
		Description: "auto (rebase automatically), skip (never rebase), prompt (ask each time). Runtime values ship.js expects; do NOT write yes/no.",
	},
	{
		Name:        "reviewThreshold",
		Label:       "Minimum severity that blocks the pipeline",
		Type:        "enum",
		Options:     []string{"critical", "high", "medium", "low"},
		Default:     "high",
		Description: "Findings at or above this severity halt the pipeline",
	},
	{
		Name:                  "verifyPipelineTimeout",
		Label:                 "verify-pipeline poll timeout (seconds)",
		Type:                  "number",
		Options:               nil,
		Default:               1200,
		Description:           "Maximum seconds verify-pipeline polls before giving up. (R57)",
		Min:                   intPtr(30),
		WhenStepInActiveSteps: "verify-pipeline",
	},
	{
		Name:                  "verifyPipelineInterval",
		Label:                 "verify-pipeline poll interval (seconds)",
		Type:                  "number",
		Options:               nil,
		Default:               60,
		Description:           "Seconds between verify-pipeline poll attempts. (R57)",
		Min:                   intPtr(10),
		WhenStepInActiveSteps: "verify-pipeline",
	},
	{
		Name:                  "verifyPipelineMaxIterations",
		Label:                 "verify-pipeline max analyze-fix iterations",
		Type:                  "number",
		Options:               nil,
		Default:               3,
		Description:           "Maximum analyze-fix-recheck iterations. (R47, R57)",
		Min:                   intPtr(1),
		Max:                   intPtr(10),
		WhenStepInActiveSteps: "verify-pipeline",
	},
	{
		Name:                  "awaitRemoteReviewTimeout",
		Label:                 "await-remote-review poll timeout (seconds)",
		Type:                  "number",
		Options:               nil,
		Default:               600,
		Description:           "Maximum seconds await-remote-review polls. (R57)",
		Min:                   intPtr(30),
		WhenStepInActiveSteps: "await-remote-review",
	},
	{
		Name:                  "awaitRemoteReviewInterval",
		Label:                 "await-remote-review poll interval (seconds)",
		Type:                  "number",
		Options:               nil,
		Default:               60,
		Description:           "Seconds between await-remote-review poll attempts. (R57)",
		Min:                   intPtr(10),
		WhenStepInActiveSteps: "await-remote-review",
	},
	{
		Name:                  "awaitRemoteReviewers",
		Label:                 "Reviewer logins satisfying await-remote-review",
		Type:                  "list",
		Options:               nil,
		Default:               []string{"copilot"},
		Description:           "Logins (case-insensitive) whose reviews satisfy the gate. (R56, R57)",
		WhenStepInActiveSteps: "await-remote-review",
	},
	{
		Name:                  "executeWaveTimeout",
		Label:                 "execute wave deadline (seconds)",
		Type:                  "number",
		Options:               nil,
		Default:               1800,
		Description:           "Maximum seconds a single wave may run before stalled tasks are terminated. (R57, R-WAVE-DEADLINE)",
		Min:                   intPtr(60),
		Max:                   intPtr(3600), // shipmeta.MaxWaveTimeoutSeconds; kept in sync by test.
		WhenStepInActiveSteps: "execute",
	},
	{
		Name:                  "executeWaveInterval",
		Label:                 "execute wave liveness poll interval (seconds)",
		Type:                  "number",
		Options:               nil,
		Default:               60,
		Description:           "Seconds between wave liveness poll attempts. (R57, R-WAVE-LIVENESS)",
		Min:                   intPtr(10),
		WhenStepInActiveSteps: "execute",
	},
}

var planStyleFields = []Field{
	{
		Name:        "verbosity",
		Label:       "Plan narrative verbosity",
		Type:        "enum",
		Options:     []string{"terse", "standard", "verbose"},
		Default:     "standard",
		Description: "Controls how much prose /plan writes in narrative sections (Context, Research Findings, Key Decisions, Final Shape). `terse` favors bullet-dense sections; `verbose` favors fuller prose.",
	},
	{
		Name:        "audience",
		Label:       "Plan narrative audience",
		Type:        "enum",
		Options:     []string{"technical", "general"},
		Default:     "technical",
		Description: "Assumed reader background for plan narrative sections. `technical` assumes deep codebase context; `general` writes so a reader without prior context can still judge the proposed change.",
	},
	{
		Name:        "narrativeRules",
		Label:       "Custom narrative rules",
		Type:        "list",
		Options:     nil,
		Default:     nil,
		Description: "Free-form writing rules /plan enforces on narrative sections (e.g., plain-English phrasing for non-native readers, always state assumptions). One rule per line — rules may contain commas, so this field splits on newline, not comma.",
	},
}

var planTasksFields = []Field{
	{
		Name:        "contractShape",
		Label:       "Task contract shape",
		Type:        "enum",
		Options:     []string{"full", "minimal", "none"},
		Default:     "full",
		Description: "Shape of the required task contract /plan enforces on every task: `full` (Complexity, Risk, Files, Verify, Depends on), `minimal` (essential fields only), `none` (flexible, no fixed shape).",
	},
	{
		Name:        "requiredFields",
		Label:       "Additional required task fields",
		Type:        "list",
		Options:     nil,
		Default:     nil,
		Description: "Extra fields required on every plan task, beyond the five core fields /plan always guarantees (Complexity, Risk, Files, Verify, Depends on). Comma-separated; duplicates of the five core fields are dropped automatically.",
	},
}

// prFields holds the two flat fields on the 'pr' section. In the Node.js
// source these have runtime-computed defaults (detectBaseBranchSafe,
// parseRemoteOwner). Here the defaults are zero-valued; the consuming tool
// (Task 26: setup_prepare) computes them at prepare time.
var prFields = []Field{
	{
		Name:        "defaultBranch",
		Label:       "Target branch for PRs",
		Type:        "string",
		Options:     nil,
		Default:     "",
		Description: "Branch PRs are merged into. Auto-detected from the remote default branch; override for repos using develop, release/*, etc. When set, /pr uses this value before falling back to runtime git detection.",
	},
	{
		Name:        "expectedAccount",
		Label:       "Expected gh account",
		Type:        "string",
		Options:     nil,
		Default:     "",
		Description: "GitHub login expected to be active when /pr creates a PR. /pr halts hard if the active gh account differs from this value, preventing wrong-account PRs in multi-account setups. Default is the origin remote owner; leave blank to skip the active-account check (fall through to email-mapping or origin-owner cascade).",
	},
}

// Sections returns the ordered list of setup section descriptors.
// The order and IDs are frozen and must match the Node.js source
// (scripts/lib/setup-sections.js SETUP_SECTIONS) exactly.
func Sections() []Section {
	return []Section{
		{
			ID:              "version",
			Label:           "version",
			Purpose:         "Tells /version and /ship where the canonical version string lives (a file, or only git tags) and how releases are tagged. Without this section, version bumps and release tagging fall back to defaults that may not match your project layout.",
			ConfigFile:      ".sdlc-v2/config.json",
			ConfigPath:      "version",
			ConsumedBy:      []string{"version", "ship"},
			FilesModified:   []string{".sdlc-v2/config.json"},
			Optional:        false,
			DelegatedTo:     "",
			ConfirmDetected: true,
			Fields:          versionFields,
		},
		{
			ID:              "ship",
			Label:           "ship",
			Purpose:         "Developer-local pipeline preferences for /ship: which steps run by default, default version bump, draft-PR mode, auto-approve, workspace isolation, rebase policy, and review-failure threshold. Stored in .sdlc-v2/local.json (gitignored) so each developer can tune the pipeline without affecting teammates.",
			ConfigFile:      ".sdlc-v2/local.json",
			ConfigPath:      "ship",
			ConsumedBy:      []string{"ship"},
			FilesModified:   []string{".sdlc-v2/local.json"},
			Optional:        false,
			DelegatedTo:     "",
			ConfirmDetected: false,
			Fields:          ShipFields,
		},
		{
			ID:              "jira",
			Label:           "jira",
			Purpose:         "Default Jira project key used by /jira, /commit, and /pr when extracting or assigning ticket IDs. Without it, Jira-aware skills require an explicit project on every invocation; with it, branch names like `feat/PROJ-123-foo` resolve automatically.",
			ConfigFile:      ".sdlc-v2/config.json",
			ConfigPath:      "jira",
			ConsumedBy:      []string{"jira", "commit", "pr"},
			FilesModified:   []string{".sdlc-v2/config.json"},
			Optional:        true,
			DelegatedTo:     "",
			ConfirmDetected: false,
			Fields:          jiraFields,
		},
		{
			ID:              "review",
			Label:           "review",
			Purpose:         "Default scope for /review (committed/staged/working/worktree/all). Each developer typically prefers a different default — committed for PR-style review, working for in-progress feedback. Stored in .sdlc-v2/local.json.",
			ConfigFile:      ".sdlc-v2/local.json",
			ConfigPath:      "review",
			ConsumedBy:      []string{"review"},
			FilesModified:   []string{".sdlc-v2/local.json"},
			Optional:        true,
			DelegatedTo:     "",
			ConfirmDetected: false,
			Fields:          reviewFields,
		},
		{
			ID:              "received-review",
			Label:           "received-review",
			Purpose:         "Per-user severity allowlist for /received-review auto-apply. When set, \"agree, will fix\" findings whose severity is in the list bypass the per-finding consent gate in Step 10/12 and are auto-applied with a one-line `fixed: ...` log. Stored in .sdlc-v2/local.json under receivedReview.alwaysFixSeverities — never in project config. Default `[]` preserves the original consent-on-every-finding behavior.",
			ConfigFile:      ".sdlc-v2/local.json",
			ConfigPath:      "receivedReview",
			ConsumedBy:      []string{"received-review"},
			FilesModified:   []string{".sdlc-v2/local.json"},
			Optional:        true,
			DelegatedTo:     "",
			ConfirmDetected: false,
			Fields:          receivedReviewFields,
		},
		{
			ID:              "commit",
			Label:           "commit",
			Purpose:         "Commit message validation rules used by /commit: subject regex, allowed Conventional-Commits types/scopes, types that require a body, required trailer headers. The skill enforces these patterns when generating and validating commit messages.",
			ConfigFile:      ".sdlc-v2/config.json",
			ConfigPath:      "commit",
			ConsumedBy:      []string{"commit"},
			FilesModified:   []string{".sdlc-v2/config.json"},
			Optional:        true,
			DelegatedTo:     "inline-commit-builder",
			ConfirmDetected: false,
			Fields:          nil,
		},
		{
			ID:              "pr",
			Label:           "pr",
			Purpose:         "PR title validation rules used by /pr: title regex, allowed Conventional-Commits types/scopes, required trailers, plus expected GitHub account for active-account preflight. Mirrors commit patterns; can copy the commit config or use a different style.",
			ConfigFile:      ".sdlc-v2/config.json",
			ConfigPath:      "pr",
			ConsumedBy:      []string{"pr"},
			FilesModified:   []string{".sdlc-v2/config.json"},
			Optional:        true,
			DelegatedTo:     "inline-pr-builder",
			ConfirmDetected: false,
			Fields:          prFields,
		},
		{
			ID:              "pr-labels",
			Label:           "pr-labels",
			Purpose:         "PR label assignment policy used by /pr. Mode \"off\" (default) adds no labels except those forced via --label. Mode \"rules\" evaluates user-defined rules — each rule maps one signal (branch prefix, commit type, changed-path glob, JIRA issue type, or diff size) to one repo label. Mode \"llm\" lets the LLM suggest labels using fuzzy matching against repo labels (legacy behavior, opt-in only).",
			ConfigFile:      ".sdlc-v2/config.json",
			ConfigPath:      "pr.labels",
			ConsumedBy:      []string{"pr"},
			FilesModified:   []string{".sdlc-v2/config.json"},
			Optional:        true,
			DelegatedTo:     "setup-pr-labels",
			ConfirmDetected: false,
			Fields:          nil,
		},
		{
			ID:              "review-dimensions",
			Label:           "review-dimensions",
			Purpose:         "Review dimensions installed under .sdlc-v2/review-dimensions/*.yaml. Each dimension is a focused check set (security, performance, type safety, etc.) that /review applies as a pass over the diff. Without dimensions installed, /review has nothing to evaluate.",
			ConfigFile:      "<delegated>",
			ConfigPath:      "",
			ConsumedBy:      []string{"review"},
			FilesModified:   []string{".sdlc-v2/review-dimensions/*.yaml"},
			Optional:        true,
			DelegatedTo:     "setup-dimensions",
			ConfirmDetected: false,
			Fields:          nil,
		},
		{
			ID:              "pr-template",
			Label:           "pr-template",
			Purpose:         "PR description template at .sdlc-v2/pr-template.md, used by /pr when drafting PRs. The sub-flow scans existing GitHub PR templates, recent PRs, and Jira evidence to propose a tailored template; without it, /pr uses a built-in fallback.",
			ConfigFile:      "<delegated>",
			ConfigPath:      "",
			ConsumedBy:      []string{"pr"},
			FilesModified:   []string{".sdlc-v2/pr-template.md"},
			Optional:        true,
			DelegatedTo:     "setup-pr-template",
			ConfirmDetected: false,
			Fields:          nil,
		},
		{
			ID:              "plan-template",
			Label:           "plan-template",
			Purpose:         "Project-owned plan template at .sdlc-v2/plan-template.md, used by /plan to build the plan skeleton (Required Sections, Discovery Questions, Verification Patterns) and by validate-plan-format.js PF10 to check required-section presence. Without it, /plan falls back to the shipped default template.",
			ConfigFile:      "<delegated>",
			ConfigPath:      "",
			ConsumedBy:      []string{"plan"},
			FilesModified:   []string{".sdlc-v2/plan-template.md"},
			Optional:        true,
			DelegatedTo:     "setup-plan-template",
			ConfirmDetected: false,
			Fields:          nil,
		},
		{
			ID:              "plan-style",
			Label:           "plan-style",
			Purpose:         "Personal narrative preferences for /plan: verbosity, assumed reader audience, and custom writing rules enforced on narrative sections (Context, Research Findings, Key Decisions, Final Shape). Stored in .sdlc-v2/local.json (gitignored) so each developer can tune plan prose without affecting teammates.",
			ConfigFile:      ".sdlc-v2/local.json",
			ConfigPath:      "planStyle",
			ConsumedBy:      []string{"plan"},
			FilesModified:   []string{".sdlc-v2/local.json"},
			Optional:        true,
			DelegatedTo:     "",
			ConfirmDetected: false,
			Fields:          planStyleFields,
		},
		{
			ID:              "plan-tasks",
			Label:           "plan-tasks",
			Purpose:         "Team contract for plan task deliverables at .sdlc-v2/config.json#plan.tasks: which fields every task must carry beyond the five guaranteed core fields, and the overall contract shape /plan enforces. Shares the plan.guardrails top-level key — writes here and in plan-guardrails each read-preserve the other's sibling.",
			ConfigFile:      ".sdlc-v2/config.json",
			ConfigPath:      "plan.tasks",
			ConsumedBy:      []string{"plan"},
			FilesModified:   []string{".sdlc-v2/config.json"},
			Optional:        true,
			DelegatedTo:     "",
			ConfirmDetected: false,
			Fields:          planTasksFields,
		},
		{
			ID:              "plan-guardrails",
			Label:           "plan-guardrails",
			Purpose:         "Custom rules at .sdlc-v2/config.json#plan.guardrails evaluated by /plan during its critique phases. Each guardrail is a natural-language constraint (e.g., \"no direct DB access from controllers\") that flags drift in plans before execution.",
			ConfigFile:      ".sdlc-v2/config.json",
			ConfigPath:      "plan.guardrails",
			ConsumedBy:      []string{"plan"},
			FilesModified:   []string{".sdlc-v2/config.json"},
			Optional:        true,
			DelegatedTo:     "setup-guardrails",
			ConfirmDetected: false,
			Fields:          nil,
		},
		{
			ID:              "execution-guardrails",
			Label:           "execution-guardrails",
			Purpose:         "Runtime guardrails at .sdlc-v2/config.json#execute.guardrails evaluated by /execute and /ship before and after each wave. Error-severity violations halt execution; warning-severity violations are reported but non-blocking.",
			ConfigFile:      ".sdlc-v2/config.json",
			ConfigPath:      "execute.guardrails",
			ConsumedBy:      []string{"execute", "ship"},
			FilesModified:   []string{".sdlc-v2/config.json"},
			Optional:        true,
			DelegatedTo:     "setup-execution-guardrails",
			ConfirmDetected: false,
			Fields:          nil,
		},
		{
			ID:              "openspec-block",
			Label:           "openspec-block",
			Purpose:         "Managed block injected into openspec/config.yaml that supplies sdlc-utilities workflow guidance to OpenSpec-aware skills (/plan, /execute, /ship). Idempotent: re-running at the same plugin version is a no-op; version bumps update the block in place.",
			ConfigFile:      "openspec/config.yaml",
			ConfigPath:      "<managed-block>",
			ConsumedBy:      []string{"plan", "execute", "ship"},
			FilesModified:   []string{"openspec/config.yaml"},
			Optional:        true,
			DelegatedTo:     "setup-openspec",
			ConfirmDetected: false,
			Fields:          nil,
		},
	}
}
