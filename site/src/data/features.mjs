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
      { text: 'Open it with one command', doc: 'docs/skills/dashboard.md' },
    ],
  },
  {
    slug: 'stats',
    title: 'Know what each run cost',
    summary: 'Each run records how long every step took.',
    description: 'Near the end of a run, ship writes a run report from the ship state, the execute state and the learnings of the run.',
    media: [],
    claims: [],
  },
  {
    slug: 'harden',
    title: 'Learn from each failure',
    summary: 'After a failed run, harden proposes a new guardrail, so the same failure does not return.',
    description: 'Harden analyzes a pipeline failure and proposes approved edits to the guardrails, so the same class of failure is caught earlier next time.',
    media: [],
    claims: [],
  },
  {
    slug: 'long-pipelines',
    title: 'Run long pipelines safely',
    summary: 'A pipeline can run for hours or days. A stopped run resumes where it stopped.',
    description: 'Ship keeps the state of each step on disk, so a stopped run resumes at the step where it stopped.',
    media: [],
    claims: [],
  },
];
