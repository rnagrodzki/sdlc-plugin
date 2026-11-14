// The state of the step sequence on a skill page. skill-sequence.ts renders
// the state that nextState returns; this file holds no DOM code, so the tests
// run in Node.

/** @typedef {{ selected: string, showOptional: boolean, replaying: boolean, reducedMotion: boolean }} SeqState */
/** @typedef {{ name: string, optional: boolean }} SeqStep */
/**
 * @typedef {{ type: 'select', name: string } | { type: 'toggle-optional' } | { type: 'replay' }
 *   | { type: 'replay-done' } | { type: 'motion', reduced: boolean }} SeqAction
 */

/**
 * @param {SeqStep[]} steps
 * @param {boolean} showOptional
 * @returns {SeqStep[]} the steps that the page shows, in order
 */
export function visibleSteps(steps, showOptional) {
  return steps.filter((s) => showOptional || !s.optional);
}

/** @returns {string} the name of the first visible step, or '' when no step is visible */
function firstVisible(steps, showOptional) {
  return visibleSteps(steps, showOptional)[0]?.name ?? '';
}

/**
 * @param {SeqState} state
 * @param {SeqAction} action
 * @param {SeqStep[]} steps
 * @returns {SeqState} a new state; the input state is not changed
 */
export function nextState(state, action, steps) {
  switch (action.type) {
    case 'select': {
      const step = steps.find((s) => s.name === action.name);
      if (!step || (step.optional && !state.showOptional)) return state;
      return { ...state, selected: action.name };
    }
    case 'toggle-optional': {
      const current = steps.find((s) => s.name === state.selected);
      if (state.showOptional && current?.optional) {
        return { ...state, showOptional: false, selected: firstVisible(steps, false) };
      }
      return { ...state, showOptional: !state.showOptional };
    }
    case 'replay':
      if (state.reducedMotion) return state;
      return { ...state, replaying: true, selected: firstVisible(steps, state.showOptional) };
    case 'replay-done':
      return { ...state, replaying: false };
    case 'motion':
      return action.reduced ? { ...state, reducedMotion: true, replaying: false } : { ...state, reducedMotion: false };
    default:
      return state;
  }
}

/**
 * @param {number} ms
 * @returns {string} for example "2h 19m 2s", "9m 16s", "2s"; "<1s" for a time under half a second
 */
export function formatDuration(ms) {
  const total = Math.round(ms / 1000);
  if (total === 0) return ms > 0 ? '<1s' : '0s';
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  return (h ? `${h}h ` : '') + (m || h ? `${m}m ` : '') + `${s}s`;
}

/**
 * @param {number} ms the time of one step
 * @param {number} totalMs the time of the visible steps
 * @returns {string} the share line of the step panel
 */
export function shareText(ms, totalMs) {
  if (!(ms > 0) || !(totalMs > 0)) return 'This step has no measured time';
  const pct = (ms / totalMs) * 100;
  return pct < 1 ? 'Under 1% of the run' : `${Math.round(pct)}% of the run`;
}

const ACTOR_LABELS = { user: 'You', tool: 'Tool', agent: 'Agent', skill: 'Skill' };

/** @param {string} actor @returns {string} the badge text of an actor */
export function actorLabel(actor) {
  return ACTOR_LABELS[actor] ?? actor;
}

/**
 * Splits the text of a Steps table cell into plain, code and strong parts.
 * The components render each part as text, so no HTML from the docs reaches the page.
 * @param {string} text
 * @returns {Array<{ kind: 'text'|'code'|'strong', value: string }>}
 */
export function inlineParts(text) {
  const parts = [];
  for (const piece of String(text).split(/(`[^`]+`|\*\*[^*]+\*\*)/)) {
    if (piece === '') continue;
    if (piece.length > 2 && piece.startsWith('`') && piece.endsWith('`')) {
      parts.push({ kind: 'code', value: piece.slice(1, -1) });
    } else if (piece.length > 4 && piece.startsWith('**') && piece.endsWith('**')) {
      parts.push({ kind: 'strong', value: piece.slice(2, -2) });
    } else {
      parts.push({ kind: 'text', value: piece });
    }
  }
  return parts;
}
