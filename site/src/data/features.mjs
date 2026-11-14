// The 4 workflow changes of the Home page and their product pages.
// The slugs are final: routes and links use them. The titles are draft copy.
// Tasks 15 and 24 fill `claims` and `media`. An entry gets a product page only
// when it has 1 or more claims (isRoutable in src/lib/assets.mjs).
export const features = [
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
