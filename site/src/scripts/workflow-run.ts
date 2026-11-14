// Browser glue of the workflow animation on Home (WorkflowSteps.astro).
// The page draws the rail complete. This file arms the rail (the class
// `is-armed` puts each part in its start state), plays the run one time when
// the workflow comes on screen, then a button replays it. The CSS animation
// of each part is the timer: its end starts the next part. `partState` and
// `nextAt` decide the state and `loopMotion` decides the motion; this file
// only applies what they return.
import { isRunDone, nextAt, partState } from '../lib/flow-run.mjs';
import { loopMotion } from '../lib/loop-motion.mjs';

function initFlow(root: HTMLElement): void {
  // The parts of the run: each workflow step, then the learn loop.
  const parts = [...root.querySelectorAll<HTMLElement>('[data-flow-part]')];
  const replay = root.querySelector<HTMLButtonElement>('[data-flow-replay]');
  if (parts.length === 0) return;

  const media = window.matchMedia('(prefers-reduced-motion: reduce)');
  const env = { onScreen: false, tabHidden: document.hidden, reducedMotion: media.matches };
  let at = -1;
  let played = false; // the run starts by itself one time only

  function render(): void {
    parts.forEach((part, i) => {
      const state = partState(i, at);
      part.classList.toggle('is-run', state === 'run');
      part.classList.toggle('is-done', state === 'done');
    });
    if (replay) replay.hidden = env.reducedMotion || !isRunDone(at, parts.length);
  }

  function apply(): void {
    const motion = loopMotion(env);
    root.classList.toggle('is-paused', motion === 'pause');
    root.classList.toggle('is-armed', motion !== 'stop');
    if (motion === 'stop') {
      // Reduced motion: the rail stays as the page drew it.
      at = -1;
      played = false;
    } else if (motion === 'run' && !played) {
      played = true;
      at = 0;
    }
    render();
  }

  // Only the timer of a part moves the run. The stations, the chips of the
  // ship step and the first half of the loop animate too, and their end
  // changes nothing.
  root.addEventListener('animationend', (event) => {
    if (!(event.target instanceof HTMLElement) || !event.target.hasAttribute('data-flow-timer')) return;
    at = nextAt(at, parts.length);
    render();
  });
  replay?.addEventListener('click', () => {
    at = 0;
    render();
  });

  // The run starts when the top of the workflow is well inside the window.
  new IntersectionObserver(
    (entries) => {
      env.onScreen = entries.some((e) => e.isIntersecting);
      apply();
    },
    { rootMargin: '0px 0px -25% 0px' },
  ).observe(root);
  document.addEventListener('visibilitychange', () => {
    env.tabHidden = document.hidden;
    apply();
  });
  media.addEventListener('change', (e) => {
    env.reducedMotion = e.matches;
    apply();
  });
  apply();
}

document.querySelectorAll<HTMLElement>('[data-flow]').forEach(initFlow);
