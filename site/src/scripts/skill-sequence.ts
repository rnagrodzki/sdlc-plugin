// Browser glue of the skill page sequence (SkillSequence.astro). The state
// logic is in lib/sequence-state.mjs; this file reads the DOM, sends actions
// to nextState and renders the state it returns.
import { nextState, visibleSteps, formatDuration, shareText } from '../lib/sequence-state.mjs';

type SeqState = { selected: string; showOptional: boolean; replaying: boolean; reducedMotion: boolean };
type SeqAction =
  | { type: 'select'; name: string }
  | { type: 'toggle-optional' }
  | { type: 'replay' }
  | { type: 'replay-done' }
  | { type: 'motion'; reduced: boolean };

/** Time between two steps of a replay, as in the reference script. */
const REPLAY_STEP_MS = 1100;
/** A time-bar part shows its name when it holds more than this share of the run. */
const LABEL_SHARE = 0.09;

function initSequence(root: HTMLElement): void {
  const items = [...root.querySelectorAll<HTMLLIElement>('li[data-step]')];
  if (items.length === 0) return;
  const steps = items.map((li) => ({ name: li.dataset.step ?? '', optional: li.dataset.optional === 'true' }));
  const itemOf = new Map(items.map((li) => [li.dataset.step ?? '', li]));
  const segs = [...root.querySelectorAll<HTMLButtonElement>('[data-seg]')];
  const hasTimes = root.dataset.hasTimes === 'true';

  const q = <T extends Element>(sel: string) => root.querySelector<T>(sel);
  const track = q<HTMLElement>('[data-seq-track]');
  const fill = q<HTMLElement>('[data-seq-fill]');
  const dot = q<HTMLElement>('[data-seq-dot]');
  const now = q<HTMLElement>('[data-now]');
  const toggle = q<HTMLInputElement>('[data-opt-toggle]');
  const replayButton = q<HTMLButtonElement>('[data-replay]');
  const total = q<HTMLElement>('[data-time-total]');

  const media = window.matchMedia('(prefers-reduced-motion: reduce)');
  let state: SeqState = {
    selected: steps[0].name,
    showOptional: true,
    replaying: false,
    reducedMotion: media.matches,
  };
  let timer: number | undefined;

  const msOf = (li: HTMLElement | undefined) => (li?.dataset.ms === undefined ? undefined : Number(li.dataset.ms));
  const visibleTotal = () =>
    items.reduce((sum, li) => sum + (li.hidden ? 0 : (msOf(li) ?? 0)), 0);

  function setText(sel: string, text: string): void {
    const el = q<HTMLElement>(sel);
    if (el) el.textContent = text;
  }

  function layoutBar(): void {
    const sum = visibleTotal();
    for (const seg of segs) {
      const li = itemOf.get(seg.dataset.seg ?? '');
      const off = !li || li.hidden;
      const ms = msOf(li) ?? 0;
      seg.classList.toggle('is-off', off);
      seg.classList.toggle('is-in', !off);
      seg.style.flexGrow = off ? '0' : String(ms);
      seg.textContent = !off && sum > 0 && ms / sum > LABEL_SHARE ? (seg.dataset.label ?? '') : '';
      seg.tabIndex = off ? -1 : 0;
      if (off) seg.setAttribute('aria-hidden', 'true');
      else seg.removeAttribute('aria-hidden');
    }
    if (total) total.textContent = formatDuration(sum);
  }

  function place(): void {
    const li = itemOf.get(state.selected);
    if (!fill || !dot) return;
    if (!li || li.hidden || !track) {
      dot.style.opacity = '0';
      fill.style.height = '0px';
      return;
    }
    const y = li.offsetTop + 25;
    dot.style.opacity = '';
    dot.style.transform = `translateY(${y - 27}px)`;
    fill.style.height = `${Math.max(0, y - 26)}px`;
  }

  function renderPanel(): void {
    if (!now) return;
    const li = itemOf.get(state.selected);
    const rest = [...now.children].filter((el) => !el.hasAttribute('data-now-count'));
    rest.forEach((el) => ((el as HTMLElement).hidden = !li));
    if (!li) {
      setText('[data-now-count]', 'No step is selected');
      return;
    }
    const visible = visibleSteps(steps, state.showOptional);
    const index = visible.findIndex((s) => s.name === state.selected);
    setText('[data-now-count]', `Step ${index + 1} of ${visible.length}`);
    setText('[data-now-who]', li.dataset.who ?? '');
    if (hasTimes) {
      const ms = msOf(li);
      setText('[data-now-time]', ms === undefined ? 'not in this run' : formatDuration(ms));
      setText('[data-now-share]', ms === undefined ? 'The measured run did not use this step' : shareText(ms, visibleTotal()));
    }
    // The copied markup is the page's own build output (Astro escaped it), so copying it is safe.
    const copy = (to: string, from: string) => {
      const target = q<HTMLElement>(to);
      if (target) target.innerHTML = li.querySelector(from)?.innerHTML ?? '';
    };
    copy('[data-now-name]', '[data-step-name]');
    copy('[data-now-when]', '[data-io-when]');
    copy('[data-now-out]', '[data-io-out]');
    if (!state.reducedMotion) {
      now.classList.remove('swap');
      void now.offsetWidth; // restart the swap animation
      now.classList.add('swap');
    }
  }

  function stopTimer(): void {
    window.clearInterval(timer);
    timer = undefined;
  }

  function tick(): void {
    const visible = visibleSteps(steps, state.showOptional);
    const index = visible.findIndex((s) => s.name === state.selected);
    if (index === -1 || index + 1 >= visible.length) dispatch({ type: 'replay-done' });
    else dispatch({ type: 'select', name: visible[index + 1].name });
  }

  function render(prev: SeqState | null): void {
    if (!prev || prev.showOptional !== state.showOptional) {
      for (const li of items) li.hidden = li.dataset.optional === 'true' && !state.showOptional;
      layoutBar();
      if (toggle) toggle.checked = state.showOptional;
    }
    const selectedIndex = items.findIndex((li) => li.dataset.step === state.selected);
    items.forEach((li, k) => {
      li.classList.toggle('is-now', k === selectedIndex);
      li.classList.toggle('is-done', selectedIndex !== -1 && k < selectedIndex);
      const button = li.querySelector('.seq-btn');
      if (k === selectedIndex) button?.setAttribute('aria-current', 'step');
      else button?.removeAttribute('aria-current');
    });
    for (const seg of segs) seg.classList.toggle('is-now', seg.dataset.seg === state.selected);
    place();
    if (!prev || prev.selected !== state.selected || prev.showOptional !== state.showOptional) renderPanel();

    if (replayButton) replayButton.disabled = state.reducedMotion;
    // A replay moves every 1.1 s; a live region would read each step aloud.
    now?.setAttribute('aria-live', state.replaying ? 'off' : 'polite');
    if (state.replaying && timer === undefined) timer = window.setInterval(tick, REPLAY_STEP_MS);
    if (!state.replaying) stopTimer();
  }

  function dispatch(action: SeqAction): void {
    const prev = state;
    state = nextState(state, action, steps) as SeqState;
    if (state !== prev) render(prev);
  }

  /** A user choice ends a replay first, as in the reference script. */
  function userAction(action: SeqAction): void {
    if (state.replaying) dispatch({ type: 'replay-done' });
    dispatch(action);
  }

  items.forEach((li) => {
    li.querySelector('.seq-btn')?.addEventListener('click', () => userAction({ type: 'select', name: li.dataset.step ?? '' }));
  });
  segs.forEach((seg) => {
    seg.addEventListener('click', () => userAction({ type: 'select', name: seg.dataset.seg ?? '' }));
  });
  toggle?.addEventListener('change', () => userAction({ type: 'toggle-optional' }));
  replayButton?.addEventListener('click', () => userAction({ type: 'replay' }));
  media.addEventListener('change', (event) => dispatch({ type: 'motion', reduced: event.matches }));
  window.addEventListener('resize', place);

  root.querySelectorAll<HTMLElement>('[data-seq-controls]').forEach((el) => (el.hidden = false));
  render(null);

  // The load sequence of the reference: the run replays one time. nextState
  // ignores the replay under reduced motion.
  dispatch({ type: 'replay' });
}

document.querySelectorAll<HTMLElement>('[data-seq-root]').forEach(initSequence);
