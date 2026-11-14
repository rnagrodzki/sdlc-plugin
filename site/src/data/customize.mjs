// The customization section of the Home page: what a team writes in config so
// that the pipeline follows its project. `followUps` is the main block: the
// follow-up instructions of docs/instructions.md. `customizations` lists the
// other settings. Each one names the skills that read it, and Home links each
// name to its skill page. A text may hold `code` spans.
// A `doc` is the repository path of a published docs page, with an optional
// #anchor. Home links it. `feature` is the slug of an entry of features.mjs:
// Home links the block to that product page when the entry has claims.
// src/lib/home-data.test.mjs checks each skill name against the skill map of
// the docs, each doc path against the published files, and the feature slug
// against features.mjs. The scan of the build (scripts/check-dist.mjs) checks
// each anchor.

/** The follow-up instructions. The sample entries come from docs/instructions.md. */
export const followUps = {
  kicker: 'Follow-up instructions',
  title: 'Write it one time. Each run does it.',
  lead: 'Move the ticket. Post the link. Add a note. Write each action as one sentence, and the agent does it after the step that you name.',
  points: [
    'One list for each of the 10 pipeline steps, 2 lists for the review fixes, and 1 list for the end of the run.',
    'The team list is in the committed config. Your own list is in a local file, for one project or for all your projects.',
    'You set which questions the agent can ask. An action such as a merge, a release or a post has its own switch.',
    'A failed instruction never fails the step or the run. The summary names it, and the dashboard shows the status of each instruction.',
    'The lists also run after a skill that you run alone.',
  ],
  samples: [
    {
      file: '.sdlc-v2/config.toml',
      who: 'The list of the team, in git',
      code: [
        '[[instructions.commit]]',
        'text = "Post the branch name and the commit subject to Slack channel #ship-log."',
        '',
        '[[instructions.pr]]',
        'text = "Move the Jira ticket named in the branch name to \'In Review\'."',
        'criticalQuestions = true',
      ].join('\n'),
    },
    {
      file: '.sdlc-v2/local.toml',
      who: 'Your own list, not in git',
      code: [
        '[[instructions.pr]]',
        'text = "Open the PR URL in my browser."',
        '',
        '[[instructions.ship]]',
        'text = "Add one line with the PR link and today\'s date to ~/notes/shipped.md."',
      ].join('\n'),
    },
  ],
  skills: ['ship', 'execute', 'commit', 'review', 'received-review', 'harden', 'pr', 'verify-pipeline'],
  doc: 'docs/instructions.md',
  docLabel: 'Read the reference of the follow-up instructions',
  feature: 'customize',
};

/** The other settings that make a run follow the project. */
export const customizations = [
  {
    title: 'Rules for every plan',
    text: 'Write a rule of your project as a plan instruction. Plan shows it at its start and gives it to each step and each sub-agent.',
    skills: ['plan'],
    docs: [{ label: 'Custom plan instructions', doc: 'docs/skills/plan/writing-style.md#custom-plan-instructions' }],
  },
  {
    title: 'Guardrails with your severity',
    text: 'A guardrail is one rule in plain text, with the severity `error` or `warning`. Plan guardrails check your idea, your requirements and the plan. Execute guardrails check each wave of the run.',
    skills: ['preplan', 'verify-requirements', 'plan', 'execute'],
    docs: [
      { label: 'Plan guardrails', doc: 'docs/skills/preplan/guardrail-check.md#what-the-check-covers' },
      { label: 'Execute guardrails', doc: 'docs/skills/execute/tips-and-gotchas.md#guardrails' },
    ],
  },
  {
    title: 'A reviewer for each concern',
    text: 'A review dimension is one concern, such as security or documentation, with its own review instructions. `/sdlc:setup --dimensions` scans your project and proposes the dimensions.',
    skills: ['review', 'setup'],
    docs: [{ label: 'Review dimensions', doc: 'docs/skills/review/settings.md#dimensions' }],
  },
  {
    title: 'Guidance for the self-learning',
    text: 'Tell harden how to write its proposals, for example `Prefer error severity for CI rules.` Each of the 4 proposal surfaces has its own list.',
    skills: ['harden'],
    docs: [{ label: 'Custom harden instructions', doc: 'docs/skills/harden/custom-instructions.md' }],
  },
  {
    title: 'Your templates',
    text: 'The pull request text uses the headings of your template, in the same order. A plan and the description of a Jira issue follow your template too.',
    skills: ['pr', 'plan', 'jira'],
    docs: [
      { label: 'Pull request template', doc: 'docs/skills/pr/description.md#your-own-template' },
      { label: 'Jira templates', doc: 'docs/skills/jira/templates.md' },
      { label: 'Plan template', doc: 'docs/skills/setup/flags-and-sections.md#section-ids' },
    ],
  },
  {
    title: 'Commit rules of your project',
    text: 'Set the allowed types and scopes, a pattern for the subject, and the required trailers. With no rules, the message follows the style of your recent commits.',
    skills: ['commit'],
    docs: [{ label: 'Commit rules', doc: 'docs/skills/commit/message-rules.md#commit-rules-in-the-project-config' }],
  },
  {
    title: 'The pipeline steps that you want',
    text: 'Turn each ship step on or off in config, or for one run with `--steps`. Set a short set of steps for `--quick`. Select the steps that wait for your confirmation.',
    skills: ['ship'],
    docs: [
      { label: 'Pipeline steps', doc: 'docs/skills/ship/pipeline-steps.md#default-pipeline-steps' },
      { label: 'Supervised or unattended', doc: 'docs/getting-started.md#4-supervised-vs-unattended' },
    ],
  },
  {
    title: 'The writing style of your team',
    text: 'Set the reader level, the writing standard, the tone and the language. Every sdlc skill writes to you in that style, and a plan has its own layout density.',
    skills: ['setup', 'plan'],
    docs: [{ label: 'Writing style', doc: 'docs/skills/plan/writing-style.md#settings' }],
  },
  {
    title: 'The models of each run',
    text: 'Select a model tier for a run: the fastest, the balanced or the most correct. With `custom`, you set the model of each task.',
    skills: ['execute', 'ship'],
    docs: [{ label: 'Quality tiers', doc: 'docs/skills/execute/flags-and-configuration.md#quality-tiers' }],
  },
];

/** The settings that have no card, stated below the cards. The text may hold `code` spans. */
export const customizeNote = {
  text: 'There is more: the release versioning, the integration branch, the review scope. Two commented config files list every option, and `/sdlc:setup` explains each one.',
  docs: [
    { label: 'Config files', doc: 'docs/skills/setup/config-files.md' },
    { label: 'Setup sections', doc: 'docs/skills/setup/flags-and-sections.md#section-ids' },
  ],
};
