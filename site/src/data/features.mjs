// The 5 workflow changes of the Home page and their product pages.
// The slugs are final: routes and links use them. The titles are draft copy.
// Tasks 15 and 24 fill `claims` and `media`. An entry gets a product page only
// when it has 1 or more claims (isRoutable in src/lib/assets.mjs).
export const features = [
  {
    slug: 'customize',
    title: 'Run it by your rules',
    summary: 'You write your rules and your follow-up actions in plain text. Each run follows them.',
    description: 'A follow-up instruction is an action in plain text that the agent does after a step, after a ship run or after a skill. Guardrails, review dimensions, templates and style settings shape the other parts of a run.',
    media: [],
    claims: [
      { text: 'Write a follow-up action as one sentence in config, and each ship run does it after the step that you name', doc: 'docs/instructions.md#what-it-does' },
      { text: '13 lists: one for each of the 10 pipeline steps, 2 for the review fixes and 1 for the end of the run', doc: 'docs/instructions.md#keys' },
      { text: 'A team list in the committed config, and your own list in a local file, for one project or for all your projects', doc: 'docs/instructions.md#where-to-write-them' },
      { text: 'Each instruction sets which questions the agent can ask, and what the agent does when it cannot ask', doc: 'docs/instructions.md#questions-while-an-instruction-runs' },
      { text: 'A failed instruction never fails the step or the run, and the run summary names it', doc: 'docs/instructions.md#what-you-see' },
      { text: 'The lists also run after 7 skills that you run alone', doc: 'docs/instructions.md#keys' },
      { text: 'Plan instructions reach each step and each sub-agent of a plan run', doc: 'docs/skills/plan/writing-style.md#custom-plan-instructions' },
      { text: 'Harden instructions shape the proposals for each of the 4 hardening surfaces', doc: 'docs/skills/harden/custom-instructions.md' },
      { text: 'Guardrails, review dimensions, templates and the pipeline steps are settings of your project', doc: 'docs/skills/setup/flags-and-sections.md#section-ids' },
    ],
  },
  {
    slug: 'dashboard',
    title: 'See every pipeline at once',
    summary: 'One page shows every pipeline in every repository: progress, session activity and issues.',
    description: 'The dashboard is one local page that lists the pipelines of every registered repository, not only the repository of the current session.',
    media: ['dashboard-main'],
    claims: [
      { text: 'Live progress of each run in every registered repository', doc: 'docs/dashboard.md#what-it-shows' },
      { text: 'Each step lists its follow-up instructions, one row for each, with the status of each instruction', doc: 'docs/instructions.md#what-you-see' },
      { text: 'Open it with one command', doc: 'docs/skills/dashboard.md' },
    ],
  },
  {
    slug: 'stats',
    title: 'Know what each run cost',
    summary: 'Each run records how long every step took.',
    description: 'Near the end of a run, ship writes a run report from the ship state, the execute state and the learnings of the run.',
    media: ['dashboard-stats'],
    claims: [
      { text: 'The dashboard lists the duration of each finished run, with the plan and ship time', doc: 'docs/dashboard.md#what-it-shows' },
      { text: 'A run report records the step timings, the review waves and the self-healing changes of a run', doc: 'docs/skills/ship/state-and-trace.md#run-report' },
      { text: 'Learnings stats count the recorded lessons by category and by skill', doc: 'docs/skills/harden/learnings.md#learnings-stats' },
    ],
  },
  {
    slug: 'harden',
    title: 'Learn from each failure',
    summary: 'After a failed run, harden proposes a new guardrail, so the same failure does not return.',
    description: 'Harden analyzes a pipeline failure and proposes approved edits to the guardrails, so the same class of failure is caught earlier next time.',
    media: ['dashboard-main'],
    claims: [
      { text: 'Harden triages the learnings of earlier runs and proposes guardrail edits', doc: 'docs/skills/harden/learnings.md#learnings-triage' },
      { text: 'A lesson that recurs 3 times becomes a skill recommendation, with a priority', doc: 'docs/skills/harden/learnings.md#skill-recommendations' },
      { text: 'Each run keeps one record of its hardening in the ship state, and the dashboard shows it', doc: 'docs/skills/harden/ship-state-record.md' },
    ],
  },
  {
    slug: 'long-pipelines',
    title: 'Run long pipelines safely',
    summary: 'A pipeline can run for hours or days. A stopped run resumes where it stopped.',
    description: 'Ship keeps the state of each step on disk, so a stopped run resumes at the step where it stopped.',
    media: ['dashboard-long-run', 'dashboard-ship-detail'],
    claims: [
      { text: 'Ship saves the state of each step, so a stopped run resumes where it stopped', doc: 'docs/skills/ship/state-and-trace.md#state-and-resolution-trace' },
      { text: 'A plan run recovers after the compaction of the session context', doc: 'docs/skills/plan/conclude-and-recovery.md#recovery-after-compaction' },
      { text: 'The dashboard keeps the plan times of a run after the cleanup of the plan state', doc: 'docs/dashboard.md#lifetime-of-the-source-data' },
    ],
  },
];
