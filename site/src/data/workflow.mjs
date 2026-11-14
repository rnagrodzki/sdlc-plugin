// The recommended workflow of Home: 4 skills take an idea to a pull request,
// and each skill offers the next one. `result` is what the step hands to the
// user. `runs` names the skills that a step runs for the user.
// src/lib/home-data.test.mjs checks each skill name against the skill map of
// the docs.
export const workflow = [
  {
    skill: 'preplan',
    command: '/sdlc:preplan <idea>',
    title: 'Shape the idea',
    does: 'Answer one question at a time.',
    result: 'topic file',
  },
  {
    skill: 'plan',
    command: '/sdlc:plan',
    title: 'Plan the work',
    does: 'The plan gets a review before any code exists.',
    result: 'plan file',
  },
  {
    skill: 'ship',
    command: '/sdlc:ship',
    title: 'Build and ship',
    does: 'Confirm each step, or run with no questions.',
    result: 'reviewed pull request',
    runs: ['execute', 'commit', 'review', 'received-review', 'pr'],
  },
  {
    skill: 'deferred',
    command: '/sdlc:deferred',
    title: 'Triage the rest',
    does: 'Each finding that the run did not fix becomes an issue, or you close it.',
    result: 'GitHub issues',
  },
];

/**
 * The loop that closes the workflow: the skill that turns a failure into a
 * guardrail, and the workflow skill that loads the guardrail on its next run.
 */
export const learnLoop = {
  skill: 'harden',
  to: 'plan',
  text: 'Each run teaches the next plan. A failure becomes a guardrail.',
};
