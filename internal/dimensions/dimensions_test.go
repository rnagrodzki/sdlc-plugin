package dimensions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/frontmatter"
)

// ---------------------------------------------------------------------------
// Corpus fixtures — verbatim content of every review-dimension Markdown file
// under .sdlc/review-dimensions/ in the sdlc-utilities plugin (the source
// repo this package ports scripts/lib/dimensions.js and scripts/lib/yaml.js
// from). Confirmed via the actual JS reference implementation
// (validateDimensionFile) that all 14 currently produce zero errors and zero
// warnings — that "clean corpus" property is what TestCorpusFrontmatterParity
// and TestCorpusValidatesClean assert against the yaml.v3-backed port.
// ---------------------------------------------------------------------------

const corpusCICDPipeline = "---\nname: ci-cd-pipeline\ndescription: \"Reviews GitHub Actions workflows and CI scripts for permissions, secret handling, job ordering, and script-to-workflow contract alignment\"\ntriggers:\n  - \".github/workflows/*.yml\"\n  - \".github/scripts/*.js\"\nskip-when:\n  - \"**/node_modules/**\"\nseverity: high\nmodel: sonnet\n---\n\n# CI/CD Pipeline Review\n\nReview GitHub Actions workflows and their companion CI scripts for security, correctness, and contract alignment. This project ships workflows that invoke Node.js scripts under `.github/scripts/`; both sides of that boundary must agree on environment variable names, exit codes, and permissions. Past issues include over-broad `permissions:` blocks and env variable mismatches between YAML definitions and `process.env` reads in scripts.\n\n## Checklist\n\n- [ ] Every `permissions:` block is scoped to the minimum required — use `contents: read` unless the job explicitly pushes tags or commits, which requires `contents: write`\n- [ ] `retag-release.yml` carries `contents: write` permission (required for tag push operations)\n- [ ] `check-version-bump.yml` correctly passes PR base context so the script can resolve the comparison ref\n- [ ] No secrets or tokens are hardcoded in workflow YAML — all sensitive values are referenced via `${{ secrets.NAME }}` or `${{ github.token }}`\n- [ ] Job `needs:` declarations match actual execution dependencies — no job starts before its required predecessor has completed\n- [ ] Workflows that mutate shared state (tags, releases, branches) define a `concurrency:` group to prevent parallel runs from racing\n- [ ] Action references are pinned to full SHA hashes (e.g., `uses: actions/checkout@abc1234...`), not mutable version tags like `@v4`\n- [ ] Workflow `env:` variable names (e.g., `BASE_REF`, `PR_NUMBER`) match exactly what CI scripts read from `process.env` — no casing or naming drift\n- [ ] CI scripts use `process.exit(0)`, `process.exit(1)`, and `process.exit(2)` consistently per the documented exit code contract (`0` = success, `1` = handled error, `2` = unexpected crash)\n- [ ] Version comments in workflow YAML headers match the version exported or logged by the corresponding CI script\n- [ ] Workflow steps that invoke CI scripts check `$?` after execution and fail the job explicitly on non-zero exit rather than silently continuing\n- [ ] No `pull_request_target` trigger is used without verifying that it cannot be exploited to expose secrets to untrusted forks\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| Hardcoded secret or token in YAML | critical |\n| Permission scope too broad (e.g., `contents: write` when read suffices) | high |\n| Missing `needs:` causing job race condition | high |\n| Action pinned to mutable tag instead of SHA | high |\n| Env variable mismatch between workflow and script | high |\n| Version comment mismatch between workflow and script | medium |\n| Missing concurrency group for racing workflows | medium |\n| CI script exit code inconsistency | medium |\n| Minor YAML formatting or documentation gap | low |\n"

const corpusCodeQuality = "---\nname: code-quality\ndescription: \"Reviews Node.js scripts for error handling, async patterns, consistent CLI conventions, and common code smells\"\ntriggers:\n  - \"**/*.js\"\nskip-when:\n  - \"**/node_modules/**\"\n  - \"**/dist/**\"\n  - \"**/build/**\"\n  - \"**/vendor/**\"\nseverity: medium\nmodel: sonnet\n---\n\n# Code Quality Review\n\nReview Node.js scripts for clarity, correctness, and maintainability. This project uses standalone Node.js scripts (no package.json / no bundler) in `plugins/*/scripts/` and `.github/scripts/`.\n\n## Checklist\n\n- [ ] Functions and variables use clear, intention-revealing names\n- [ ] Functions have single responsibility — not doing too many things\n- [ ] Error cases are handled explicitly — no silent `catch {}` blocks that swallow errors\n- [ ] Scripts use correct exit codes: `process.exit(0)` for success, `process.exit(1)` for user errors, `process.exit(2)` for script errors — both plugin scripts (`scripts/*.js`) and CI scripts (`.github/scripts/*.js`) follow this convention\n- [ ] Error messages go to `stderr` (`process.stderr.write` or `console.error`), normal output to `stdout`\n- [ ] File paths use `path.join()` or `path.resolve()` — no string concatenation for paths\n- [ ] `child_process` calls (execSync, spawnSync) handle errors and check exit codes\n- [ ] No magic numbers or strings — use named constants\n- [ ] No dead code or commented-out code blocks\n- [ ] `fs` operations check for file/directory existence before access where appropriate\n- [ ] Consistent patterns across lib modules (e.g., similar error handling, similar function signatures)\n- [ ] YAML/JSON parsing has proper error handling for malformed input\n- [ ] No unnecessary complexity — prefer simple, direct code over abstractions\n- [ ] Migration, initialization, and preflight-check logic is idempotent — the same check must not re-trigger after state has been successfully transitioned; guard conditions prevent re-execution on already-migrated state\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| Silent error swallowing / lost error context | high |\n| Wrong exit code (success on error, or vice versa) | high |\n| Missing error handling on fs/child_process operations | high |\n| Path string concatenation instead of path.join | medium |\n| Inconsistent/misleading naming that could cause bugs | medium |\n| Dead code | low |\n| Magic number without explanation | low |\n| Commented-out code blocks | info |\n| Migration/preflight check re-triggers on already-migrated state | high |\n| Missing guard condition preventing double-execution of state transition | medium |\n"

const corpusDependencyManagement = "---\nname: dependency-management\ndescription: \"Reviews dependency changes in site/package.json and pnpm lockfile for version pinning, lockfile consistency, and unintended bumps\"\ntriggers:\n  - \"site/package.json\"\n  - \"site/pnpm-lock.yaml\"\nskip-when:\n  - \"**/node_modules/**\"\nseverity: medium\nmodel: haiku\nmax-files: 10\n---\n\n# Dependency Management Review\n\nReview dependency changes for the Astro documentation site managed with pnpm.\n\n## Checklist\n\n- [ ] Lockfile (`pnpm-lock.yaml`) is updated consistently with `package.json` changes — no divergence\n- [ ] New dependencies use a narrow version range (`^` or `~`), not `*` or `latest`\n- [ ] Major version bumps are intentional — check if Astro or Tailwind CSS migration notes apply\n- [ ] Dev dependencies are not in the production `dependencies` list\n- [ ] No duplicate packages solving the same problem added alongside existing deps\n- [ ] `pnpm.onlyBuiltDependencies` allowlist is updated if a new native dependency is added (currently: esbuild, sharp)\n- [ ] Transitive dependency changes in lockfile are reviewed for unexpected major bumps\n- [ ] New dependencies are compatible with the project's license (MIT)\n- [ ] Astro plugin dependencies (`@astrojs/*`) are version-compatible with the installed Astro version\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| Lockfile diverges from package.json | high |\n| Package with known critical CVE added | critical |\n| Unintended major version bump in lockfile | medium |\n| `*` or `latest` version specifier | medium |\n| Dev dependency in production list | medium |\n| Duplicate dependency solving same problem | low |\n| Missing `onlyBuiltDependencies` entry for native dep | low |\n"

const corpusDocsSkillSync = "---\nname: docs-skill-sync\ndescription: \"Reviews that skill changes are reflected in docs/skills/ markdown, site/src/data/skills-meta.ts, and the README skills table\"\ntriggers:\n  - \"**/skills/**/SKILL.md\"\n  - \"docs/skills/*.md\"\n  - \"site/src/data/skills-meta.ts\"\n  - \"site/src/content.config.ts\"\n  - \"README.md\"\nskip-when:\n  - \"**/node_modules/**\"\n  - \"tests/**\"\n  - \".claude/skills/**\"\nseverity: high\nmodel: haiku\nrequires-full-diff: true\n---\n\n# Documentation–Skill Sync Review\n\nReview that every skill definition change is propagated to all three downstream documentation surfaces: the `docs/skills/` reference doc, the `site/src/data/skills-meta.ts` site metadata, and the `README.md` skills table. This project uses an Astro site (`site/`) that reads skill docs via a content collection loader (`site/src/content.config.ts` → `../docs/skills`), and renders pipeline diagrams and SkillCard tiles from `skills-meta.ts`.\n\n## Checklist\n\n### 1:1 existence checks\n- [ ] Every skill directory under `plugins/sdlc-utilities/skills/<name>/` has a matching `docs/skills/<name>.md`\n- [ ] Every user-invocable skill has a row in the `README.md` Skills table (non-user-invocable skills like `error-report` still need a doc but not a README row)\n- [ ] Every user-invocable skill has an entry in `site/src/data/skills-meta.ts` `skillsMeta` array with matching `slug`\n\n### Content consistency — docs/skills/*.md\n- [ ] The doc's Overview matches the skill's actual purpose — if the SKILL.md workflow changed, the doc Overview must reflect the new behavior\n- [ ] The doc's Flags table lists every flag the SKILL.md documents (and no removed flags)\n- [ ] The doc's Prerequisites list matches the SKILL.md's actual tool/config requirements\n- [ ] The doc's \"What It Creates or Modifies\" section reflects the current artifacts the skill produces\n- [ ] The doc's Related Skills section references correct skill names and paths\n- [ ] The doc follows the template structure from `docs/skill-doc-template.md`\n\n### Content consistency — site/src/data/skills-meta.ts\n- [ ] The `tagline` field accurately summarizes the skill's current behavior — stale taglines that describe old workflows are high-severity\n- [ ] The `pipeline` array reflects the SKILL.md's current workflow steps in order — added/removed/renamed steps must be updated\n- [ ] The `connections` array reflects the skill's actual See Also / Related Skills — broken or missing connections produce dead links on the site\n- [ ] The `category` field is correct for the skill's current function (planning, review, gitops, integrations)\n- [ ] The `userInvocable` field matches the skill's frontmatter `user-invocable` value\n\n### Content consistency — README.md\n- [ ] The skill description in the README table matches the skill's actual purpose (doesn't need to be identical to tagline, but must be accurate)\n- [ ] The README table link points to the correct `docs/skills/<name>.md` path\n\n### Template and structural changes\n- [ ] If `docs/skill-doc-template.md` changed, check whether existing skill docs need to adopt the new structure\n- [ ] If `site/src/content.config.ts` changed (e.g., loader base path), verify that docs are still being picked up correctly\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| Skill exists but has no docs/skills/ doc file | critical |\n| SKILL.md workflow changed but pipeline array in skills-meta.ts is stale | high |\n| Flags added/removed in SKILL.md but doc Flags table not updated | high |\n| Tagline in skills-meta.ts describes old behavior | high |\n| Broken connection in skills-meta.ts (references non-existent slug) | high |\n| Missing README table row for user-invocable skill | medium |\n| README description materially inaccurate | medium |\n| Related Skills section references wrong skill name | medium |\n| Doc Prerequisites list doesn't match actual requirements | medium |\n| Minor wording drift between doc and skill (not materially wrong) | low |\n| Template structure deviation in older doc | info |\n"

const corpusHookReadiness = "---\nname: hook-readiness\ndescription: \"Reviews skills and scripts for reactive patterns better served as Claude Code harness hooks, and validates hooks.json structural correctness\"\ntriggers:\n  - \"**/skills/**/SKILL.md\"\n  - \"**/hooks/hooks.json\"\n  - \"**/scripts/*.js\"\nskip-when:\n  - \"**/node_modules/**\"\n  - \"docs/**\"\n  - \"tests/**\"\nseverity: medium\nmodel: sonnet\n---\n\n# Hook Readiness Review\n\nReview skills, hooks config, and scripts for patterns that belong in the Claude Code harness hook system rather than inline skill logic. Claude Code harness hooks fire automatically on lifecycle events (SessionStart, PreToolUse, PostToolUse, etc.) and can be type `command` (shell), `prompt` (LLM judgment), or `agent` (subagent). Exit code 0 = proceed, exit code 2 = block action. Moving recurring reactive patterns into hooks makes them automatic and eliminates duplication across skills.\n\n## A. Hook Opportunity Detection\n\nCheck skills and scripts for patterns that should instead be hooks:\n\n- [ ] Reactive validation patterns (e.g., \"after editing, run lint/validate\") that fire on every tool use → should be a `PostToolUse` hook rather than inline skill instructions\n- [ ] Session initialization logic (e.g., \"check tool availability on startup\", \"verify environment on start\") → should be a `SessionStart` hook rather than a pre-flight block repeated in each skill\n- [ ] File protection patterns (e.g., \"don't edit files matching X\", \"never modify Y\") → should be a `PreToolUse` hook with exit code 2 to block the action automatically\n- [ ] Post-write validation (e.g., \"validate dimension files after writing\", \"lint after saving\") → should be a `PostToolUse` hook scoped to `Edit|Write` tool matches\n- [ ] Notification patterns (e.g., \"alert when permission needed\", \"notify on completion\") → should use the `Notification` hook event rather than inline skill output\n- [ ] Pre-flight checks repeated across 2 or more skills (e.g., checking for a required binary, confirming a config file exists) → centralize as a `SessionStart` or `PreToolUse` hook\n\n## B. hooks.json Structural Correctness\n\nWhen `hooks.json` is among the changed files, verify:\n\n- [ ] Hook events use only valid event names: `SessionStart`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `UserPromptSubmit`, `Notification`, `SubagentStart`, `SubagentStop`, `Stop`, `TaskCompleted`, `ConfigChange`, `WorktreeCreate`, `WorktreeRemove`, `SessionEnd`, `PreCompact`, `PermissionRequest`, `TeammateIdle`\n- [ ] Matchers are valid regex patterns — no unescaped characters that make an invalid regex\n- [ ] Hook types are one of: `command`, `prompt`, `agent` — no other values\n- [ ] Commands referenced by hooks exist on disk and are executable (check scripts referenced by path)\n- [ ] No overly broad matchers (e.g., `.*`) on `PreToolUse` — a catch-all `PreToolUse` matcher fires on every tool invocation and will block or slow everything\n- [ ] Hook commands include appropriate error handling (`|| true`, `2>/dev/null`) where failure should not abort the triggering action\n- [ ] Hook commands that invoke potentially slow operations (network calls, large file scans, full test suites) include an explicit timeout or are documented as acceptable to block\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| File protection logic inline in skill instead of `PreToolUse` hook | high |\n| Invalid hook event name in hooks.json | high |\n| Hook command references non-existent script | high |\n| Overly broad matcher on `PreToolUse` (blocks all tools) | high |\n| Reactive validation pattern in skill instead of `PostToolUse` hook | medium |\n| Session setup duplicated across skills instead of `SessionStart` hook | medium |\n| Missing error handling in hook command (no `\\|\\| true`) | medium |\n| Hook command without timeout on potentially slow operation | medium |\n| Pre-flight check repeated in 2+ skills (hook candidate) | low |\n| Minor hook structural issue (e.g., unnecessary whitespace, redundant matcher) | low |\n"

const corpusReleaseConsistency = "---\nname: release-consistency\ndescription: \"Reviews version and changelog changes for consistency across plugin.json, CHANGELOG.md, version.json, and git tag references\"\ntriggers:\n  - \"plugins/sdlc-utilities/.claude-plugin/plugin.json\"\n  - \"CHANGELOG.md\"\n  - \".claude/version.json\"\n  - \".claude-plugin/marketplace.json\"\nskip-when:\n  - \"**/node_modules/**\"\n  - \"tests/**\"\nseverity: high\nmodel: haiku\nrequires-full-diff: true\n---\n\n# Release Consistency Review\n\nReview version and changelog changes for consistency across all release artifacts. Version numbers must agree between `plugin.json`, `CHANGELOG.md`, and `marketplace.json`. The `version.json` configuration must reference valid paths and tag prefixes. A mismatch between any of these artifacts will cause release tooling to produce incorrect tags, publish the wrong version, or leave users with a broken changelog.\n\n## Checklist\n\n- [ ] The version string in `plugin.json` `\"version\"` field matches the most recent heading in `CHANGELOG.md` — e.g., `\"version\": \"1.2.3\"` corresponds to `## [1.2.3]`\n- [ ] The CHANGELOG entry for the released version includes a date in `YYYY-MM-DD` format and that date is not in the future\n- [ ] CHANGELOG follows Keep a Changelog format: sections use exactly the headings `### Added`, `### Changed`, `### Fixed`, or `### Removed` — no ad-hoc section names\n- [ ] `marketplace.json` plugin `name` field matches the `name` field in `plugin.json` — they must reference the same plugin identity\n- [ ] `.claude/version.json` `versionFile` path still resolves to the correct `plugin.json` (the file must exist at that path relative to the project root)\n- [ ] `tagPrefix` in `version.json` is consistent with existing git tags — e.g., if existing tags are `sdlc-utilities-v1.0.0` then `tagPrefix` must be `sdlc-utilities-v`, not `v` or `sdlc-v`\n- [ ] No version downgrade: the new version in `plugin.json` is greater than or equal to the previous version in semver terms — patch, minor, and major increments are valid; a lower version is not\n- [ ] When `plugin.json` version changes, there must be a corresponding CHANGELOG entry for that exact version — a version bump without a changelog entry is incomplete\n- [ ] If a release tag is retargeted to a new SHA at the same version (e.g., via `/version --retag`), `CHANGELOG.md` is unchanged AND `plugin.json` version is unchanged AND the SHA being tagged is reachable from the default branch — retag must not silently rewrite release history across version boundaries. **Note:** `/version --retag` is user-initiated; `retag-release.yml` is CI-automated squash-drift fix — they are orthogonal. Do not conflate.\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| Version in plugin.json and CHANGELOG heading don't match | critical |\n| plugin.json version bumped but no CHANGELOG entry | high |\n| CHANGELOG date missing or in the future | high |\n| Version downgrade detected | high |\n| versionFile path in version.json points to wrong file | high |\n| marketplace.json name mismatch with plugin.json | medium |\n| tagPrefix inconsistent with existing tags | medium |\n| CHANGELOG section not following Keep a Changelog format | medium |\n| Minor formatting deviation in CHANGELOG | low |\n| Tag SHA changed without corresponding plugin.json/CHANGELOG update | high |\n"

const corpusRuntimeContract = "---\nname: runtime-contract\ndescription: \"Reviews the command-to-script-to-skill execution pipeline for temp file lifecycle, exit code semantics, argument passing, JSON schema agreement, and version skew resilience\"\ntriggers:\n  - \"**/commands/*.md\"\n  - \"**/skills/**/SKILL.md\"\n  - \"**/scripts/*.js\"\nskip-when:\n  - \"**/node_modules/**\"\n  - \"docs/**\"\nseverity: high\nmodel: opus\n---\n\n# Runtime Contract Review\n\nReview the command→script→skill execution pipeline for correctness and resilience. Every command in this project follows the pattern: resolve script → run to temp file → read JSON → delegate to skill. A documented version skew bug in `pr` (installed `pr-prepare.js` silently omitting `customTemplate`) illustrates how contract violations surface only at runtime. This dimension checks that all parties in the pipeline — command, script, and skill — agree on the interface.\n\n## Checklist\n\n- [ ] Commands that invoke scripts capture output via `--output-file` flag — the script writes JSON to a crypto-random temp file and prints its path to stdout. Never use `mktemp` in the bash block\n- [ ] The temp file variable name is unique per command (e.g., `PR_CONTEXT_FILE`, `MANIFEST_FILE`, `VERSION_CONTEXT_FILE`) and not a generic name like `TMPFILE` that could shadow across steps\n- [ ] Every temp file created by a command has a corresponding `rm -f` cleanup that executes on all exit paths — success, error, and user cancellation (look for cleanup noted in the workflow, not just in the \"happy path\")\n- [ ] When a task creates state files under multiple prefixes (e.g., `plan-*`, `ship-*`, `execute-*`), the cleanup / GC logic must enumerate and sweep ALL created prefixes — not a subset. Asymmetric cleanup (sweeping `ship-*` and `execute-*` but omitting `plan-*`) leaves permanent orphans. Each new prefix must be added to the GC sweep at the same time it is introduced.\n- [ ] Exit code handling matches script semantics: `0` = success with usable output, `1` = errors captured in JSON `errors` array, `2` = script crash — the command checks both `$?` and the `errors` array in the JSON\n- [ ] `$ARGUMENTS` is passed to `node \"$SCRIPT\"` so that CLI flags reach the script as individual arguments — not concatenated into a single string\n- [ ] JSON field names that the skill reads from the script output match the fields the script actually produces — no field name mismatches (e.g., skill reads `customTemplate` but script outputs `custom_template`)\n- [ ] When a skill documents a version skew workaround (e.g., reading `.claude/pr-template.md` directly when `customTemplate` is null), the workaround handles both \"field absent\" and \"field explicitly set to null\"\n- [ ] Scripts that accept `--project-root` default to `process.cwd()` — commands either pass `--project-root .` explicitly or correctly rely on the default\n- [ ] The `diff_dir` temp directory created by `review-prepare.js` is cleaned up by the orchestrating skill (`rm -rf {manifest.diff_dir}`) after the review completes — not left to the command\n- [ ] Commands delegate to exactly one skill and pass the parsed JSON context as the primary input — they do not partially process JSON fields or add derived fields before delegation\n- [ ] Scripts write valid JSON to `stdout` and all error/diagnostic messages to `stderr` — commands capture `stdout` only (e.g., `node \"$SCRIPT\" ... > \"$TEMP_FILE\"`) and use `$?` for error detection\n- [ ] When a command specifies `allowed-tools` in its frontmatter, `Skill` is listed (needed for delegation) and `Bash` is listed (needed for script execution)\n- [ ] When a script resolves a flag from CLI + config inputs (e.g., `flags.X = config.X === true || args.X === true`), every SKILL.md decision site that gates on that concept references the resolved field (`flags.X`) — no SKILL.md site re-derives via `config.X`, raw `$ARGUMENTS`, or the original CLI string after Step 1 (CONSUME). Carve-outs that legitimately depend on persistent project state (e.g., CI scaffold install sites) must include an inline rationale comment explaining the divergence.\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| Temp file not cleaned up — leaked on error path | high |\n| Exit code not checked after script invocation | high |\n| JSON field name mismatch between script output and skill reader | high |\n| Script writes errors to stdout instead of stderr — corrupts JSON | high |\n| `$ARGUMENTS` not passed to script — flags silently ignored | high |\n| GC sweep omits a prefix class introduced by the same task | high |\n| Version skew workaround missing null-vs-absent check | medium |\n| Temp variable name shadows another variable in the same flow | medium |\n| `--project-root` default assumption wrong for the usage context | medium |\n| `diff_dir` cleanup responsibility ambiguous between command and skill | medium |\n| `allowed-tools` missing `Skill` or `Bash` | medium |\n| Command performs non-trivial JSON processing before delegation | low |\n| SKILL.md decision site re-derives a script-resolved flag (e.g., reads `config.X` instead of `flags.X` after Step 1) | high |\n| Carve-out site (legitimately reads `config.X`) lacks inline rationale comment | medium |\n"

const corpusScriptResolution = "---\nname: script-resolution\ndescription: \"Reviews injected-path script resolution and Glob-based reference lookup patterns in commands and skills for runtime correctness across installed and development contexts\"\ntriggers:\n  - \"**/commands/*.md\"\n  - \"**/skills/**/SKILL.md\"\nskip-when:\n  - \"**/node_modules/**\"\n  - \"docs/**\"\nseverity: high\nmodel: sonnet\n---\n\n# Script Resolution Review\n\nReview the runtime script resolution and file reference lookup patterns embedded in command and skill markdown files. This project resolves Node.js helper scripts at runtime from the plugin root injected into session context by the `SessionStart` hook — a stable `sdlc plugin root: <abs>` line emitted on `startup|clear|compact`. Skill bodies invoke scripts with the literal form `node \"<PLUGIN_ROOT>/scripts/<subdir>/<script>.js\"`, substituting `<PLUGIN_ROOT>` from that line. Because the hook that fired IS the active install, its root is the one correct target — no version ranking or `find` traversal is needed (the #258 multi-version-ambiguity concern is dissolved). Subagent prompt templates and orchestrator agent templates run in subagent context, where the injected line is ABSENT — no plugin-script resolver of any form belongs there; pass a pre-computed path in instead. Auto-approving this invocation form is a user-deployed recommendation, not something this checklist enforces: see `permissions.allow` in `settings.json` (documented in `plugins/sdlc-utilities/scripts/README.md` and `docs/plugin-installation.md`), never `SKILL.md` frontmatter.\n\n## Checklist\n\n- [ ] Every plugin-script invocation uses the injected-path form: `node \"<PLUGIN_ROOT>/scripts/<subdir>/<script>.js\"` — no `find ~/.claude/plugins` resolver, no `$SCRIPT` variable, no cached-version ranking, no `trap`\n- [ ] The block instructs the model to substitute `<PLUGIN_ROOT>` from the `sdlc plugin root:` line injected into session context by the `SessionStart` hook\n- [ ] The script path in `<PLUGIN_ROOT>/scripts/<subdir>/<script>.js` exactly matches the file as it exists in `plugins/*/scripts/` (case-sensitive, no typos, correct extension, correct subdirectory)\n- [ ] No resolver of any form (old `find`-based or the new injected-path form) appears in a subagent prompt template (`**/*-prompt.md`) or orchestrator agent template (`agents/*.md`) — the injected line is absent in subagent context, so any resolver there fails silently; pre-computed paths must be passed in instead\n- [ ] Glob-based reference file lookups (REFERENCE.md, EXAMPLES.md, agent definitions) use `path: ~/.claude` first and explicitly document a cwd fallback if not found\n- [ ] Glob patterns for reference file lookups are specific enough to match exactly one file — e.g., `**/review/REFERENCE.md` not `**/REFERENCE.md`\n- [ ] No resolution pattern uses hardcoded absolute paths other than the `<PLUGIN_ROOT>` substitution described above\n- [ ] When a skill re-resolves the same script in a later step (e.g., first in Step 2 for validation, then in Step 7 for execution), both resolution blocks use the identical injected-path form — no divergent logic for the same script\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| `find ~/.claude/plugins` resolver used instead of the injected-path form — can silently select a fixture or marketplace-clone copy of the script | high |\n| Plugin-script resolver (old or new form) present in a subagent prompt template or orchestrator agent template — the injected line is absent there, so resolution fails silently | high |\n| Script path mismatch between resolution pattern and actual file | high |\n| Missing/incorrect instruction to substitute `<PLUGIN_ROOT>` from the `sdlc plugin root:` context line | medium |\n| Glob reference lookup pattern too broad — could match wrong file | medium |\n| Missing cwd fallback for Glob-based reference lookup | medium |\n| Divergent resolution patterns for the same script across steps | medium |\n| Hardcoded absolute path other than the `<PLUGIN_ROOT>` substitution | medium |\n"

const corpusSecurity = "---\nname: security-review\ndescription: \"OWASP Top 10 review — tags every finding with the matching A01–A10 category\"\ntriggers:\n  - \"plugins/**/scripts/**/*.js\"\n  - \"plugins/**/skills/**/*.md\"\n  - \"plugins/**/hooks/**/*.js\"\n  - \"plugins/**/hooks/hooks.json\"\n  - \"tests/promptfoo/fixtures-fs/**\"\n  - \".github/workflows/**\"\n  - \"schemas/**/*.json\"\nskip-when:\n  - \"**/*.test.*\"\n  - \"**/*.spec.*\"\n  - \"**/__fixtures__/**\"\nseverity: high\nmax-files: 50\nmodel: sonnet\n---\n\n# Security Review (OWASP Top 10)\n\nReview changes against the OWASP Top 10 (2025) for this marketplace plugin.\n\n## Tagging Instruction (REQUIRED)\n\nFor every finding, set `**OWASP:**` to the matching category code (`A01`–`A10`). When a finding spans multiple categories, pick the most specific. Omit the field only when no OWASP category applies (rare).\n\n## Checklist\n\n- [ ] A01 — Broken access control: scripts that touch git/gh/jira respect the configured scope; no path-traversal or unscoped writes outside the project root\n- [ ] A02 — Cryptographic failures: no MD5/SHA1/DES, no plaintext credential persistence, hashes used only for non-secret keys (caches)\n- [ ] A03 — Injection: no `exec()` / `child_process.exec` with unsanitised user input, no shell interpolation of untrusted strings, regex inputs anchored where appropriate\n- [ ] A04 — Insecure design: trust boundaries between user input → script → LLM → tool calls are explicit; no implicit elevation\n- [ ] A05 — Security misconfiguration: hooks, settings, and CI workflows do not disable security checks or grant overly broad permissions\n- [ ] A06 — Vulnerable & outdated components: no deprecated/unmaintained npm packages, no known-CVE versions added in lockfile updates\n- [ ] A07 — Identification & authentication failures: tokens (gh/jira) sourced from approved env/keychain only — never echoed, persisted, or logged\n- [ ] A08 — Software & data integrity failures: no unsigned plugin downloads, no untrusted deserialisation, hooks load only from approved paths\n- [ ] A09 — Security logging & monitoring failures: failure paths surface actionable error text without leaking secrets; audit-relevant events are logged\n- [ ] A10 — SSRF: outbound HTTP (links.js, gh/jira) validates URLs against an allowlist; no fetches to user-controlled internal hosts or metadata endpoints\n\n## Severity Guide\n\n| Category | Default Severity |\n|----------|------------------|\n| A01 — Broken access control | critical |\n| A02 — Cryptographic failures | critical |\n| A03 — Injection | critical |\n| A04 — Insecure design | high |\n| A05 — Security misconfiguration | high |\n| A06 — Vulnerable & outdated components | high |\n| A07 — Identification & authentication failures | critical |\n| A08 — Software & data integrity failures | high |\n| A09 — Security logging & monitoring failures | medium |\n| A10 — SSRF | high |\n"

const corpusSkillArchitecture = "---\nname: skill-architecture\ndescription: \"Reviews skill, command, and agent definitions for structural consistency, cross-references, and adherence to architecture principles\"\ntriggers:\n  - \"**/skills/**/SKILL.md\"\n  - \"**/commands/*.md\"\n  - \"**/agents/*.md\"\n  - \"**/skills/**/REFERENCE.md\"\n  - \"**/skills/**/EXAMPLES.md\"\nskip-when:\n  - \"**/node_modules/**\"\n  - \"docs/**\"\nseverity: high\nmodel: opus\n---\n\n# Skill Architecture Review\n\nReview skill definitions (SKILL.md), command files, and agent definitions for structural consistency and adherence to this project's architecture principles. These files are the core product — they define AI agent behavior and workflows.\n\n## Architecture Principles to Verify\n\nThis project mandates (from AGENTS.md):\n1. **Spec-driven development** — design before implementation\n2. **Plan - Critique - Improve - Do - Critique - Improve** — mandatory dual critique gates in every pipeline\n3. **Cache-first incremental scanning** — snapshot hashing where applicable\n4. **Parallel execution** — independent steps must run concurrently\n5. **Self-learning directives** — learnings flow into `.claude/learnings/log.md`\n6. **Specificity over generics** — every skill targets a concrete task\n\n## Checklist\n\n- [ ] Skill has clear, descriptive name and description in frontmatter/header\n- [ ] Workflow steps are numbered and follow a logical sequence\n- [ ] Multi-step skills include Plan-Critique-Improve gates (not just Plan-Do)\n- [ ] Cross-references to other skills/commands use correct paths and names (e.g., `See Also` sections)\n- [ ] Skills that produce output define explicit output format\n- [ ] Commands properly delegate to their corresponding skill\n- [ ] Agent definitions specify clear role, constraints, and output format\n- [ ] Learning capture sections reference `.claude/learnings/log.md` correctly\n- [ ] Glob patterns used for file discovery (e.g., `**/scripts/*.js`) are valid and specific\n- [ ] Independent steps within a workflow are marked for parallel execution\n- [ ] Error handling steps specify what to do on failure (exit codes, user messages)\n- [ ] REFERENCE.md and EXAMPLES.md files are consistent with their parent SKILL.md\n- [ ] Within a single SKILL.md, gate conditions for the same behavioral concept (e.g., 'draft artifact' at one step vs 'write artifact' at a later step) use identical condition phrasing and reference the same field. Divergent phrasings for the same concept are flagged.\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| Missing critique gate in a multi-step pipeline | high |\n| Broken cross-reference (wrong skill name or path) | high |\n| Command does not delegate to its skill | high |\n| Missing error handling for script invocations | high |\n| Inconsistent output format between skill and its reference | medium |\n| Missing See Also / Learning Capture section | medium |\n| Steps that could run in parallel are sequential | medium |\n| Vague or generic instructions (not project-specific) | medium |\n| Minor formatting inconsistency | low |\n| Missing description in frontmatter | low |\n| Two or more gates for the same concept use different conditions (e.g., one says `flags.X === true`, another says `config.X === true`, another says \"if X is enabled\") | high |\n"

const corpusSpecCompliance = "---\nname: spec-compliance\ndescription: \"Reviews that SKILL.md changes are consistent with the corresponding spec in docs/specs/, and that spec was updated before implementation\"\ntriggers:\n  - \"**/skills/**/SKILL.md\"\n  - \"docs/specs/*.md\"\n  - \"docs/spec-template.md\"\nskip-when:\n  - \"**/node_modules/**\"\n  - \"tests/**\"\nseverity: high\nmodel: opus\nrequires-full-diff: true\n---\n\n# Spec Compliance Review\n\nReview that every skill implementation change is traceable to a specification in `docs/specs/<skill-name>.md`. Specs are the source of truth for skill behavior — SKILL.md implements the spec, not the other way around.\n\n## Checklist\n\n### Spec-first ordering\n- [ ] If a SKILL.md was modified, the corresponding `docs/specs/<skill-name>.md` was also modified in the same changeset (or was already up to date)\n- [ ] New behavioral requirements in SKILL.md have matching R-prefixed entries in the spec\n- [ ] No SKILL.md changes introduce behavior that contradicts existing spec requirements\n\n### Requirement coverage\n- [ ] Every R (Core Requirement) entry in the spec has corresponding implementation in SKILL.md\n- [ ] Every A (Argument) entry in the spec is handled in SKILL.md's flag/argument parsing\n- [ ] Every G (Quality Gate) entry in the spec appears in SKILL.md's critique/validation steps\n- [ ] Every E (Error Handling) entry in the spec has a matching error recovery path in SKILL.md\n- [ ] Every C (Constraint) entry in the spec is enforced in SKILL.md (often in DO NOT sections)\n- [ ] Every I (Integration) entry in the spec reflects actual skill interactions in SKILL.md\n\n### Prepare script contract\n- [ ] If the spec lists P (Prepare Script Contract) entries, SKILL.md consumes those exact fields from the prepare script output\n- [ ] No SKILL.md code depends on prepare script fields not listed in the spec's P entries\n\n### Spec structure\n- [ ] New or modified specs follow the template at `docs/spec-template.md`\n- [ ] Requirement numbering is sequential within each prefix (R1, R2, R3 — no gaps)\n- [ ] Specs contain only WHAT (behavioral contract), not HOW (implementation details)\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| SKILL.md changed but spec not updated — new behavior has no spec backing | critical |\n| SKILL.md contradicts a spec requirement | critical |\n| Spec requirement exists but SKILL.md does not implement it | high |\n| Spec quality gate missing from SKILL.md critique step | high |\n| SKILL.md depends on prepare script field not in spec P entries | medium |\n| Spec argument entry missing for a SKILL.md flag | medium |\n| Minor numbering gap in spec requirements | low |\n| Spec wording could be more precise but is not wrong | info |\n"

const corpusTestQuality = "---\nname: test-quality\ndescription: \"Reviews promptfoo behavioral test datasets, fixtures, and test scripts for assertion quality, fixture accuracy, skill coverage, and test staleness (outdated assertions that no longer match current intended behavior)\"\ntriggers:\n  - \"tests/promptfoo/datasets/*.yaml\"\n  - \"tests/promptfoo/fixtures/*.md\"\n  - \"tests/promptfoo/fixtures-fs/**\"\n  - \"tests/promptfoo/scripts/*.js\"\n  - \"tests/promptfoo/promptfooconfig*.yaml\"\nskip-when:\n  - \"tests/promptfoo/.promptfoo-data/**\"\n  - \"tests/promptfoo/.env\"\nseverity: medium\nmodel: sonnet\n---\n\n# Test Quality Review\n\nReview promptfoo behavioral test datasets, fixtures, and supporting scripts for correctness and meaningful coverage. This project uses promptfoo to validate that SDLC skills produce correct AI agent behavior. Each dataset YAML file defines test cases with `vars` (skill_path, project_context, user_request) and `assert` blocks (icontains, regex, not-icontains, llm-rubric).\n\n## Checklist\n\n- [ ] Each test case `description` clearly identifies the skill and the specific behavior being tested — e.g., `\"commit: --auto flag skips interactive approval prompt\"`, not just `\"test commit\"`\n- [ ] `vars.skill_path` references a SKILL.md that actually exists at that path in the repository\n- [ ] `vars.project_context` references a fixture file (`file://fixtures/...`) that exists in `tests/promptfoo/fixtures/` — no broken references\n- [ ] Fixture appropriateness comments at the top of each dataset are accurate — a fixture marked CORRECT provides signals relevant to the skill, a fixture marked INVALID explains why it is unsuitable\n- [ ] `assert` blocks include at least one structural assertion (icontains/regex) AND one behavioral assertion (llm-rubric) — structural alone misses intent, behavioral alone is flaky\n- [ ] `icontains` and `regex` assertions match strings actually produced by the skill workflow — not hallucinated output patterns\n- [ ] `not-icontains` / `not-regex` assertions verify meaningful exclusions (e.g., skill must NOT propose a dimension when evidence is absent) — not trivial negations\n- [ ] `llm-rubric` assertions describe expected behavior specifically enough that a grading LLM can distinguish pass from fail — no vague criteria like \"response is good\"\n- [ ] When a skill adds or changes behavior (new flags, new workflow steps), corresponding test cases are added or updated in the dataset — no untested behavior changes\n- [ ] `fixtures-fs/` directory-based fixtures have the expected file tree structure matching what the test scenario requires (e.g., `plugins/sdlc-utilities/skills/` hierarchy for discovery tests)\n- [ ] Test helper scripts in `tests/promptfoo/scripts/` handle edge cases (missing files, malformed input) without crashing — they should exit cleanly with a descriptive error\n- [ ] `promptfooconfig*.yaml` references valid dataset paths and provider configuration — no stale entries pointing to renamed or deleted datasets\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| Broken fixture reference — test case references nonexistent file | high |\n| skill_path references a SKILL.md that doesn't exist | high |\n| Test case has no behavioral assertion (llm-rubric) — only structural checks | medium |\n| Fixture appropriateness comment is wrong — fixture doesn't match scenario | medium |\n| Skill behavior changed but no test case updated | high |\n| llm-rubric criteria too vague to distinguish pass/fail | medium |\n| promptfooconfig references deleted dataset | medium |\n| Test description doesn't identify which skill is tested | low |\n| fixtures-fs structure has unnecessary extra files | low |\n"

const corpusTypeSafetyReview = "---\nname: type-safety-review\ndescription: \"Reviews TypeScript files in the Astro site for strict-mode compliance, any-type usage, null safety, and type annotation quality\"\ntriggers:\n  - \"site/src/**/*.ts\"\nskip-when:\n  - \"site/src/**/*.d.ts\"\n  - \"**/node_modules/**\"\n  - \"site/dist/**\"\n  - \"site/.astro/**\"\nseverity: medium\nmodel: haiku\nmax-files: 30\n---\n\n# Type Safety Review\n\nReview TypeScript files in the `site/` Astro project for compliance with `strict: true` tsconfig and sound type annotation practices. This project uses Astro 5 with pnpm — TypeScript utilities live in `site/src/data/`, `site/src/utils/`, and content config files.\n\n## Checklist\n\n- [ ] No `any` type used without a justified comment — prefer `unknown` with type guards or explicit types\n- [ ] No non-null assertion (`!`) without a surrounding comment explaining why null is impossible at that point\n- [ ] All function parameters and return types are explicitly annotated in non-trivial functions\n- [ ] `undefined` and `null` cases are handled explicitly — no implicit assumptions that optional fields are always present\n- [ ] Type assertions (`as X`) are used only when TypeScript cannot infer the type from context; never as a workaround for type errors\n- [ ] Object shapes are defined via `interface` or `type` alias — no inline `{ foo: string; bar: number }` repeated across files\n- [ ] Generic types are constrained where possible (`T extends Record<string, unknown>` not just `T`)\n- [ ] No `@ts-ignore` or `@ts-expect-error` without an accompanying comment explaining the known issue\n- [ ] Imported types use `import type` syntax to avoid runtime imports being emitted\n- [ ] Array and object destructuring preserves types — no destructuring into `any`-typed intermediates\n- [ ] Astro content collection types (from `content.config.ts`) are re-exported or typed correctly — no implicit `any` from untyped collection entries\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| `any` type silently widening a collection or API boundary | high |\n| Non-null assertion on a value that can be null in practice | high |\n| `@ts-ignore` hiding a real type error | high |\n| Missing return type on exported function with complex return shape | medium |\n| Implicit `undefined` not handled in conditional | medium |\n| Type assertion used as a workaround instead of proper typing | medium |\n| Inline object type duplicated across files | low |\n| Missing `import type` for type-only imports | low |\n| Unconstrained generic that could be narrowed | low |\n"

const corpusUIReview = "---\nname: ui-review\ndescription: \"Reviews Astro components and pages for semantic HTML, Tailwind CSS usage, responsive design, and component composition patterns\"\ntriggers:\n  - \"site/src/**/*.astro\"\n  - \"site/src/styles/**/*.css\"\nskip-when:\n  - \"**/node_modules/**\"\n  - \"site/dist/**\"\n  - \"site/.astro/**\"\nseverity: medium\nmodel: haiku\nmax-files: 40\n---\n\n# UI Review\n\nReview Astro 5 components and Tailwind CSS v4 styling for quality, consistency, and accessibility basics.\n\n## Checklist\n\n- [ ] Astro components use semantic HTML elements (`<nav>`, `<main>`, `<article>`, `<section>`) over generic `<div>` wrappers\n- [ ] Interactive elements have accessible names (text content, `aria-label`, or `aria-labelledby`)\n- [ ] Images include meaningful `alt` attributes; decorative images use `alt=\"\"`\n- [ ] Tailwind CSS classes follow project conventions — no conflicting or redundant utility classes\n- [ ] Responsive design: layouts adapt to mobile/tablet/desktop (check `sm:`, `md:`, `lg:` breakpoints)\n- [ ] Component props are typed and documented via Astro `Props` interface\n- [ ] No inline styles when a Tailwind utility or CSS class exists for the same purpose\n- [ ] Links use descriptive text (avoid \"click here\" or bare URLs as link text)\n- [ ] Color contrast is sufficient — avoid light-on-light or dark-on-dark text patterns\n- [ ] Component composition is appropriate — shared elements (Nav, Footer, SEOHead) are reused, not duplicated\n- [ ] Astro `client:*` directives are used only when client-side interactivity is actually needed\n- [ ] Page metadata (`<title>`, `<meta description>`) is present via SEOHead component\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| Missing accessible name on interactive element | high |\n| Broken layout on mobile (missing responsive classes) | high |\n| Duplicated component logic that should be shared | medium |\n| Inline styles replacing available Tailwind utilities | medium |\n| Missing `alt` on informative image | medium |\n| Unnecessary `client:load` on static content | medium |\n| Redundant Tailwind classes | low |\n| Missing page metadata | low |\n"

// ---------------------------------------------------------------------------
// Invalid-fixture set — taken verbatim from tests/promptfoo/fixtures-fs/ in
// the source repo, one per D-check exercised.
// ---------------------------------------------------------------------------

const invalidBadName = "---\ndescription: \"A test dimension missing its name\"\ntriggers:\n  - \"**/*.js\"\nseverity: medium\n---\n\n# Test Dimension\n\nThis dimension is used for testing validation of missing required fields.\nThe name field is intentionally omitted to trigger a D2 validation error.\n"

const invalidTypo = "---\nname: typo-dimension\ndescription: \"A test dimension with a typo in severity field name\"\ntriggers:\n  - \"**/*.js\"\nsevrity: high\n---\n\n# Typo Test Dimension\n\nThis dimension has a misspelled field name (sevrity instead of severity)\nto test D11 unknown field detection with typo suggestions.\n"

const invalidDupFirst = "---\nname: code-quality-review\ndescription: \"First dimension with this name\"\ntriggers:\n  - \"**/*.js\"\nseverity: medium\n---\n\n# Code Quality Review\n\nThis is the first dimension file using the name code-quality-review.\nUsed to test D10 duplicate name detection.\n"

const invalidDupSecond = "---\nname: code-quality-review\ndescription: \"Second dimension with duplicate name\"\ntriggers:\n  - \"**/*.ts\"\nseverity: medium\n---\n\n# Code Quality Review Duplicate\n\nThis is the second dimension file using the same name code-quality-review.\nUsed to test D10 duplicate name detection.\n"

const invalidBadGlob = "---\nname: bad-glob-dimension\ndescription: \"A test dimension with invalid glob syntax\"\ntriggers:\n  - \"***/*.js\"\nseverity: medium\n---\n\n# Bad Glob Test\n\nThis dimension has an invalid glob pattern with triple stars to trigger D5 validation error.\n"

const invalidShortBody = "---\nname: short-body\ndescription: \"A test dimension with a body that is too short\"\ntriggers:\n  - \"**/*.js\"\nseverity: medium\n---\n\nReview.\n"

const invalidBadModel = "---\nname: bad-model-dimension\ndescription: \"A test dimension with non-string model frontmatter to trigger D13\"\nseverity: medium\nmodel: 123\ntriggers:\n  - \"**/*.ts\"\n---\n\n# Bad Model Test\n\nThis dimension declares `model: 123` (a number) in its frontmatter. The\nvalidator must emit a D13 warning naming the field and the expected type.\n"

// ---------------------------------------------------------------------------
// ToInstructions golden fixtures — the security-review.md +_common.md pair
// from tests/promptfoo/fixtures-fs/project-with-dimensions/ (with a common
// prompt) and the real dependency-management.md (without one). Golden
// outputs computed by calling the actual dimensionToInstructions() JS
// reference implementation on this exact input.
// ---------------------------------------------------------------------------

const toiSecurityReviewSrc = "---\nname: security-review\ndescription: OWASP Top 10 review — tags every finding with the matching A01–A10 category.\nseverity: high\ntriggers:\n  - \"src/**/*.ts\"\n  - \"src/**/*.js\"\n  - \"**/*.env*\"\n---\n\n# Security Review (OWASP Top 10)\n\nReview changes against the OWASP Top 10 (2025).\n\n## Tagging Instruction (REQUIRED)\n\nFor every finding, set `**OWASP:**` to the matching category code (`A01`–`A10`). When a finding spans multiple categories, pick the most specific. Omit only when no OWASP category applies.\n\n## Checklist\n\n- [ ] A01 — Broken access control\n- [ ] A02 — Cryptographic failures\n- [ ] A03 — Injection\n- [ ] A04 — Insecure design\n- [ ] A05 — Security misconfiguration\n- [ ] A06 — Vulnerable & outdated components\n- [ ] A07 — Identification & authentication failures\n- [ ] A08 — Software & data integrity failures\n- [ ] A09 — Security logging & monitoring failures\n- [ ] A10 — SSRF\n\n## Severity Guide\n\n| Category | Default Severity |\n|----------|------------------|\n| A01, A02, A03, A07 | critical |\n| A04, A05, A06, A08, A10 | high |\n| A09 | medium |\n"

const toiCommonSrc = "This is common prompt content shared across all review dimensions.\nIt provides baseline guidance for all reviewers.\n"

const toiDepMgmtWant = "---\napplyTo: \"site/package.json,site/pnpm-lock.yaml\"\n---\n# dependency-management — Review Instructions\n\nReviews dependency changes in site/package.json and pnpm lockfile for version pinning, lockfile consistency, and unintended bumps\n\nDefault severity: medium\n\n## Checklist\n\n- Lockfile (`pnpm-lock.yaml`) is updated consistently with `package.json` changes — no divergence\n- New dependencies use a narrow version range (`^` or `~`), not `*` or `latest`\n- Major version bumps are intentional — check if Astro or Tailwind CSS migration notes apply\n- Dev dependencies are not in the production `dependencies` list\n- No duplicate packages solving the same problem added alongside existing deps\n- `pnpm.onlyBuiltDependencies` allowlist is updated if a new native dependency is added (currently: esbuild, sharp)\n- Transitive dependency changes in lockfile are reviewed for unexpected major bumps\n- New dependencies are compatible with the project's license (MIT)\n- Astro plugin dependencies (`@astrojs/*`) are version-compatible with the installed Astro version\n\n## Severity Guide\n\n| Finding | Severity |\n|---------|----------|\n| Lockfile diverges from package.json | high |\n| Package with known critical CVE added | critical |\n| Unintended major version bump in lockfile | medium |\n| `*` or `latest` version specifier | medium |\n| Dev dependency in production list | medium |\n| Duplicate dependency solving same problem | low |\n| Missing `onlyBuiltDependencies` entry for native dep | low |\n\n## Note\n\nIn Claude Code reviews, files matching these patterns are excluded: **/node_modules/**.\nCopilot path-specific instructions do not support exclusion patterns — use judgment when findings apply to these files.\n"

const toiSecReviewWant = "---\napplyTo: \"src/**/*.ts,src/**/*.js,**/*.env*\"\n---\n# security-review — Review Instructions\n\nOWASP Top 10 review — tags every finding with the matching A01–A10 category.\n\nDefault severity: high\n\n## Common Review Instructions\n\nThis is common prompt content shared across all review dimensions.\nIt provides baseline guidance for all reviewers.\n\n## Checklist\n\n- A01 — Broken access control\n- A02 — Cryptographic failures\n- A03 — Injection\n- A04 — Insecure design\n- A05 — Security misconfiguration\n- A06 — Vulnerable & outdated components\n- A07 — Identification & authentication failures\n- A08 — Software & data integrity failures\n- A09 — Security logging & monitoring failures\n- A10 — SSRF\n\n## Severity Guide\n\n| Category | Default Severity |\n|----------|------------------|\n| A01, A02, A03, A07 | critical |\n| A04, A05, A06, A08, A10 | high |\n| A09 | medium |\n"

// ---------------------------------------------------------------------------
// assertMessages compares two message slices, treating nil and an empty
// slice as equal (Validate returns nil, not []string{}, when it finds
// nothing to report).
// ---------------------------------------------------------------------------

func assertMessages(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("message count mismatch: got %d %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("message[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// mustParse parses src via frontmatter.Parse and fails the test on error.
func mustParse(t *testing.T, src string) (map[string]any, string) {
	t.Helper()
	meta, body, err := frontmatter.Parse([]byte(src))
	if err != nil {
		t.Fatalf("frontmatter.Parse: %v", err)
	}
	return meta, string(body)
}

// TestCorpusFrontmatterParity proves that every real review-dimension
// fixture in the source repo decodes, via gopkg.in/yaml.v3, to the exact
// frontmatter structure the source's hand-rolled parseSimpleYaml produces
// for the same input (Acceptance Criterion 1). None of these 14 fixtures
// exercise a construct where yaml.v3 and parseSimpleYaml diverge (e.g.
// negative integers, which parseSimpleYaml's /^\d+$/ regex cannot match and
// therefore leaves as a string) — that is the point: this is the corpus of
// currently-shipping dimension files, and yaml.v3 is a strict superset of
// the subset they actually use.
func TestCorpusFrontmatterParity(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]any
	}{
		{"ci-cd-pipeline", corpusCICDPipeline, map[string]any{
			"name":        "ci-cd-pipeline",
			"description": "Reviews GitHub Actions workflows and CI scripts for permissions, secret handling, job ordering, and script-to-workflow contract alignment",
			"triggers":    []any{".github/workflows/*.yml", ".github/scripts/*.js"},
			"skip-when":   []any{"**/node_modules/**"},
			"severity":    "high",
			"model":       "sonnet",
		}},
		{"code-quality", corpusCodeQuality, map[string]any{
			"name":        "code-quality",
			"description": "Reviews Node.js scripts for error handling, async patterns, consistent CLI conventions, and common code smells",
			"triggers":    []any{"**/*.js"},
			"skip-when":   []any{"**/node_modules/**", "**/dist/**", "**/build/**", "**/vendor/**"},
			"severity":    "medium",
			"model":       "sonnet",
		}},
		{"dependency-management", corpusDependencyManagement, map[string]any{
			"name":        "dependency-management",
			"description": "Reviews dependency changes in site/package.json and pnpm lockfile for version pinning, lockfile consistency, and unintended bumps",
			"triggers":    []any{"site/package.json", "site/pnpm-lock.yaml"},
			"skip-when":   []any{"**/node_modules/**"},
			"severity":    "medium",
			"model":       "haiku",
			"max-files":   10,
		}},
		{"docs-skill-sync", corpusDocsSkillSync, map[string]any{
			"name":               "docs-skill-sync",
			"description":        "Reviews that skill changes are reflected in docs/skills/ markdown, site/src/data/skills-meta.ts, and the README skills table",
			"triggers":           []any{"**/skills/**/SKILL.md", "docs/skills/*.md", "site/src/data/skills-meta.ts", "site/src/content.config.ts", "README.md"},
			"skip-when":          []any{"**/node_modules/**", "tests/**", ".claude/skills/**"},
			"severity":           "high",
			"model":              "haiku",
			"requires-full-diff": true,
		}},
		{"hook-readiness", corpusHookReadiness, map[string]any{
			"name":        "hook-readiness",
			"description": "Reviews skills and scripts for reactive patterns better served as Claude Code harness hooks, and validates hooks.json structural correctness",
			"triggers":    []any{"**/skills/**/SKILL.md", "**/hooks/hooks.json", "**/scripts/*.js"},
			"skip-when":   []any{"**/node_modules/**", "docs/**", "tests/**"},
			"severity":    "medium",
			"model":       "sonnet",
		}},
		{"release-consistency", corpusReleaseConsistency, map[string]any{
			"name":               "release-consistency",
			"description":        "Reviews version and changelog changes for consistency across plugin.json, CHANGELOG.md, version.json, and git tag references",
			"triggers":           []any{"plugins/sdlc-utilities/.claude-plugin/plugin.json", "CHANGELOG.md", ".claude/version.json", ".claude-plugin/marketplace.json"},
			"skip-when":          []any{"**/node_modules/**", "tests/**"},
			"severity":           "high",
			"model":              "haiku",
			"requires-full-diff": true,
		}},
		{"runtime-contract", corpusRuntimeContract, map[string]any{
			"name":        "runtime-contract",
			"description": "Reviews the command-to-script-to-skill execution pipeline for temp file lifecycle, exit code semantics, argument passing, JSON schema agreement, and version skew resilience",
			"triggers":    []any{"**/commands/*.md", "**/skills/**/SKILL.md", "**/scripts/*.js"},
			"skip-when":   []any{"**/node_modules/**", "docs/**"},
			"severity":    "high",
			"model":       "opus",
		}},
		{"script-resolution", corpusScriptResolution, map[string]any{
			"name":        "script-resolution",
			"description": "Reviews injected-path script resolution and Glob-based reference lookup patterns in commands and skills for runtime correctness across installed and development contexts",
			"triggers":    []any{"**/commands/*.md", "**/skills/**/SKILL.md"},
			"skip-when":   []any{"**/node_modules/**", "docs/**"},
			"severity":    "high",
			"model":       "sonnet",
		}},
		{"security-review", corpusSecurity, map[string]any{
			"name":        "security-review",
			"description": "OWASP Top 10 review — tags every finding with the matching A01–A10 category",
			"triggers":    []any{"plugins/**/scripts/**/*.js", "plugins/**/skills/**/*.md", "plugins/**/hooks/**/*.js", "plugins/**/hooks/hooks.json", "tests/promptfoo/fixtures-fs/**", ".github/workflows/**", "schemas/**/*.json"},
			"skip-when":   []any{"**/*.test.*", "**/*.spec.*", "**/__fixtures__/**"},
			"severity":    "high",
			"max-files":   50,
			"model":       "sonnet",
		}},
		{"skill-architecture", corpusSkillArchitecture, map[string]any{
			"name":        "skill-architecture",
			"description": "Reviews skill, command, and agent definitions for structural consistency, cross-references, and adherence to architecture principles",
			"triggers":    []any{"**/skills/**/SKILL.md", "**/commands/*.md", "**/agents/*.md", "**/skills/**/REFERENCE.md", "**/skills/**/EXAMPLES.md"},
			"skip-when":   []any{"**/node_modules/**", "docs/**"},
			"severity":    "high",
			"model":       "opus",
		}},
		{"spec-compliance", corpusSpecCompliance, map[string]any{
			"name":               "spec-compliance",
			"description":        "Reviews that SKILL.md changes are consistent with the corresponding spec in docs/specs/, and that spec was updated before implementation",
			"triggers":           []any{"**/skills/**/SKILL.md", "docs/specs/*.md", "docs/spec-template.md"},
			"skip-when":          []any{"**/node_modules/**", "tests/**"},
			"severity":           "high",
			"model":              "opus",
			"requires-full-diff": true,
		}},
		{"test-quality", corpusTestQuality, map[string]any{
			"name":        "test-quality",
			"description": "Reviews promptfoo behavioral test datasets, fixtures, and test scripts for assertion quality, fixture accuracy, skill coverage, and test staleness (outdated assertions that no longer match current intended behavior)",
			"triggers":    []any{"tests/promptfoo/datasets/*.yaml", "tests/promptfoo/fixtures/*.md", "tests/promptfoo/fixtures-fs/**", "tests/promptfoo/scripts/*.js", "tests/promptfoo/promptfooconfig*.yaml"},
			"skip-when":   []any{"tests/promptfoo/.promptfoo-data/**", "tests/promptfoo/.env"},
			"severity":    "medium",
			"model":       "sonnet",
		}},
		{"type-safety-review", corpusTypeSafetyReview, map[string]any{
			"name":        "type-safety-review",
			"description": "Reviews TypeScript files in the Astro site for strict-mode compliance, any-type usage, null safety, and type annotation quality",
			"triggers":    []any{"site/src/**/*.ts"},
			"skip-when":   []any{"site/src/**/*.d.ts", "**/node_modules/**", "site/dist/**", "site/.astro/**"},
			"severity":    "medium",
			"model":       "haiku",
			"max-files":   30,
		}},
		{"ui-review", corpusUIReview, map[string]any{
			"name":        "ui-review",
			"description": "Reviews Astro components and pages for semantic HTML, Tailwind CSS usage, responsive design, and component composition patterns",
			"triggers":    []any{"site/src/**/*.astro", "site/src/styles/**/*.css"},
			"skip-when":   []any{"**/node_modules/**", "site/dist/**", "site/.astro/**"},
			"severity":    "medium",
			"model":       "haiku",
			"max-files":   40,
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta, body := mustParse(t, tc.src)

			if len(meta) != len(tc.want) {
				t.Fatalf("field count mismatch: got %d fields %v, want %d fields %v", len(meta), keysOf(meta), len(tc.want), keysOf(tc.want))
			}
			for k, wantV := range tc.want {
				gotV, ok := meta[k]
				if !ok {
					t.Errorf("field %q: missing from parsed frontmatter", k)
					continue
				}
				if !valuesEqual(gotV, wantV) {
					t.Errorf("field %q: got %#v, want %#v", k, gotV, wantV)
				}
			}

			// Acceptance Criterion 2 (clean side): the real corpus is
			// confirmed (via the JS reference implementation) to produce
			// zero errors and zero warnings today — the port must agree.
			msgs := Validate(Dimension{Meta: meta, Body: body})
			if len(msgs) != 0 {
				t.Errorf("Validate: got %d messages for a known-clean fixture: %q", len(msgs), msgs)
			}
		})
	}
}

// keysOf returns the sorted keys of a map[string]any, used only for
// t.Fatalf diagnostics above.
func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// valuesEqual compares two frontmatter values for equality, treating
// []any element-wise (its elements are always strings in these fixtures).
func valuesEqual(a, b any) bool {
	aArr, aOK := a.([]any)
	bArr, bOK := b.([]any)
	if aOK != bOK {
		return false
	}
	if aOK {
		if len(aArr) != len(bArr) {
			return false
		}
		for i := range aArr {
			if aArr[i] != bArr[i] {
				return false
			}
		}
		return true
	}
	return a == b
}

// TestValidate_InvalidFixtures reproduces validateDimensionFile's exact
// error/warning strings (Acceptance Criterion 2) for the invalid-fixture set
// under tests/promptfoo/fixtures-fs/ in the source repo, one fixture per
// D-check. Golden strings were captured by calling the JS reference
// implementation directly on this exact fixture content.
func TestValidate_InvalidFixtures(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "D2 missing name",
			src:  invalidBadName,
			want: []string{"Missing required field: name"},
		},
		{
			name: "D11 unknown field with typo suggestion",
			src:  invalidTypo,
			want: []string{`Unknown frontmatter field: "sevrity" (did you mean: severity?)`},
		},
		{
			name: "D10 duplicate name, first file individually clean",
			src:  invalidDupFirst,
			want: nil,
		},
		{
			name: "D10 duplicate name, second file individually clean",
			src:  invalidDupSecond,
			want: nil,
		},
		{
			name: "D5 invalid glob pattern in triggers",
			src:  invalidBadGlob,
			want: []string{`Invalid glob pattern in triggers: "***/*.js"`},
		},
		{
			name: "D9 body too short",
			src:  invalidShortBody,
			want: []string{"Body must contain at least 10 characters of review instructions (got: 7)"},
		},
		{
			name: "D13 model must be a non-empty string",
			src:  invalidBadModel,
			want: []string{`Field "model" must be a non-empty string (got: 123)`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta, body := mustParse(t, tc.src)
			got := Validate(Dimension{Meta: meta, Body: body})
			assertMessages(t, got, tc.want)
		})
	}
}

// TestValidate_D0ReadFailure covers the D0 (unreadable file) shape: Validate
// must report it distinctly from D1 (missing frontmatter), even though byte-
// for-byte parity with the JS fs error text is not achievable (Go's os
// errors are worded differently from Node's) — see dimensions.go's Validate
// doc comment.
func TestValidate_D0ReadFailure(t *testing.T) {
	d := Dimension{Err: os.ErrNotExist}
	got := Validate(d)
	assertMessages(t, got, []string{"Cannot read file: file does not exist"})
}

// TestValidate_D1MissingFrontmatter covers the D1 check via a Dimension
// whose Err is frontmatter.ErrNoFrontmatter, as Load would produce for a
// Markdown file with no "---" delimited block.
func TestValidate_D1MissingFrontmatter(t *testing.T) {
	_, _, err := frontmatter.Parse([]byte("# No frontmatter here\n\nJust a body.\n"))
	if err == nil {
		t.Fatalf("frontmatter.Parse: expected ErrNoFrontmatter, got nil")
	}
	got := Validate(Dimension{Err: err})
	assertMessages(t, got, []string{"Missing YAML frontmatter block (--- delimiters)"})
}

// TestToInstructions_NoCommon renders dependency-management.md (a real
// corpus fixture with no _common.md in its directory) and compares against
// the JS reference implementation's output for the same input.
func TestToInstructions_NoCommon(t *testing.T) {
	meta, body := mustParse(t, corpusDependencyManagement)
	got := ToInstructions(Dimension{Meta: meta, Body: body})
	if got != toiDepMgmtWant {
		t.Errorf("ToInstructions mismatch\n--- got ---\n%s\n--- want ---\n%s", got, toiDepMgmtWant)
	}
}

// TestToInstructions_WithCommon renders a security-review.md +_common.md
// pair (mirroring tests/promptfoo/fixtures-fs/project-with-dimensions/) and
// compares against the JS reference implementation's output, proving the
// "## Common Review Instructions" injection matches.
func TestToInstructions_WithCommon(t *testing.T) {
	meta, body := mustParse(t, toiSecurityReviewSrc)
	got := ToInstructions(Dimension{Meta: meta, Body: body, Common: toiCommonSrc})
	if got != toiSecReviewWant {
		t.Errorf("ToInstructions mismatch\n--- got ---\n%s\n--- want ---\n%s", got, toiSecReviewWant)
	}
}

// TestLoad exercises Load's directory-listing behavior directly: alphabetic
// ordering, _common.md exclusion from the dimension list but inclusion as
// shared Common content, and non-.md files being skipped.
func TestLoad(t *testing.T) {
	dir := t.TempDir()

	files := map[string]string{
		"b-second.md": invalidShortBody,
		"a-first.md":  invalidBadGlob,
		"_common.md":  "Shared instructions.\n",
		"notes.txt":   "not a dimension file",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%s): %v", name, err)
		}
	}

	dims, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(dims) != 2 {
		t.Fatalf("Load: got %d dimensions, want 2 (got files: %v)", len(dims), fileNames(dims))
	}
	if dims[0].File != "a-first.md" || dims[1].File != "b-second.md" {
		t.Errorf("Load: got file order %v, want [a-first.md b-second.md]", fileNames(dims))
	}
	for _, d := range dims {
		if d.Common != "Shared instructions." {
			t.Errorf("Load: dimension %s Common = %q, want %q", d.File, d.Common, "Shared instructions.")
		}
	}
}

// TestLoad_MissingDir confirms Load treats a nonexistent directory as "zero
// dimensions", not an error — matching resolveDimensionsDir/validateAll's
// behavior when no dimensions directory exists yet.
func TestLoad_MissingDir(t *testing.T) {
	dims, err := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Load: got error %v, want nil", err)
	}
	if len(dims) != 0 {
		t.Fatalf("Load: got %d dimensions, want 0", len(dims))
	}
}

func fileNames(dims []Dimension) []string {
	names := make([]string, len(dims))
	for i, d := range dims {
		names[i] = d.File
	}
	return names
}
