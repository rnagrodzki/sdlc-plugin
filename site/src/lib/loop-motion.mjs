// The motion of the signal on the all-skills map (LoopMap.astro).
// loop-map.ts applies the value that loopMotion returns and holds no motion
// branch of its own.

/**
 * @param {{ onScreen: boolean, tabHidden: boolean, reducedMotion: boolean }} env
 * @returns {'run'|'pause'|'stop'} `stop` ends all motion, `pause` keeps the
 *   position for later, `run` moves the signal.
 */
export function loopMotion({ onScreen, tabHidden, reducedMotion }) {
  if (reducedMotion) return 'stop';
  if (tabHidden) return 'pause';
  if (!onScreen) return 'pause';
  return 'run';
}
