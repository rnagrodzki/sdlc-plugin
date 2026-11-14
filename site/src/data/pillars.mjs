// The self-healing and self-learning section of Home. Each pillar names the
// skills that give it, and Home links each name to its skill page. `feature`
// is the slug of an entry of features.mjs: Home links the pillar to that
// product page when the entry has claims.
// src/lib/home-data.test.mjs checks each skill name against the skill map of
// the docs, and each feature slug against features.mjs.
export const pillars = [
  {
    id: 'heal',
    kicker: 'Self-healing',
    title: 'A failure gets a repair, not a stop',
    lead: 'The run corrects the problem and continues.',
    points: [
      'A failed task gets 2 retries, on a stronger model when one exists.',
      'Review findings get their fix in a separate commit before the pull request opens.',
      'A failed CI check gets a diagnosis and a minimal fix.',
      'A stopped run continues where it stopped, also in a new session.',
    ],
    skills: ['execute', 'received-review', 'verify-pipeline', 'ship'],
    feature: 'long-pipelines',
  },
  {
    id: 'learn',
    kicker: 'Self-learning',
    title: 'Each failure makes the next run stricter',
    lead: 'A lesson from one run becomes a rule for the later runs.',
    points: [
      'Each run logs its lessons.',
      'A failure becomes a guardrail for a class of failures, not for one file.',
      'The next plan loads the new guardrails.',
      'A lesson that recurs 3 times becomes a skill recommendation.',
    ],
    skills: ['harden', 'plan'],
    feature: 'harden',
  },
  {
    id: 'maintain',
    kicker: 'Self-maintaining',
    title: 'The rules follow your project',
    lead: 'The pipeline cleans its own rule files while you use it.',
    points: [
      'A rule with a dead reference is removed.',
      'A rule that another rule covers is removed, and an oversize rule is split.',
      'Rules only get stricter. No rule is relaxed.',
      'The run report lists each self-healing change of the run.',
    ],
    skills: ['harden', 'ship'],
  },
];

/** The limit of the automation, stated below the pillars. It may hold `code` spans. */
export const pillarNote = 'You approve each rule edit. With `--auto`, harden applies only the safe edits and stores the others for you.';
