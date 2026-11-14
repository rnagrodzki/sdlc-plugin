// The other benefits of the Home page: the benefits that the workflow panel,
// the customization section (src/data/customize.mjs) and the self-healing
// section do not show. Each one names the skills that
// give it, and Home links each name to its skill page. A text may hold `code`
// spans.
// src/lib/home-data.test.mjs checks each skill name against the skill map of
// the docs.
export const benefits = [
  {
    title: 'Problems found before any code exists',
    text: 'The idea becomes written requirements, one question at a time. The plan passes its own review and two hard gates before a task starts.',
    skills: ['preplan', 'verify-requirements', 'plan'],
  },
  {
    title: 'A review that fits your project',
    text: 'Each quality dimension of your project has its own reviewer. You read one comment, with no duplicates and with corrected severities.',
    skills: ['review'],
  },
  {
    title: 'One command for every review finding',
    text: 'Local review, human reviewer or Copilot: each finding is checked against the code, then fixed or answered. A ledger accounts for every finding.',
    skills: ['received-review', 'deferred'],
  },
  {
    title: 'Supervised or unattended, by your choice',
    text: 'Each commit and each pull request waits for your `yes`. `--auto` removes the questions, and `--dry-run` shows the steps before a run.',
    skills: ['ship', 'commit', 'pr'],
  },
  {
    title: 'Output in the style of your project',
    text: 'The commit message follows the style of your history. The pull request text covers every commit, and a section with no evidence gets `N/A`.',
    skills: ['commit', 'pr'],
  },
  {
    title: 'A fit with your tools',
    text: 'The skills work with GitHub, Jira and OpenSpec. Each skill also runs alone, so you can use one step and not the full pipeline.',
    skills: ['jira', 'openspec-save', 'setup'],
  },
];
