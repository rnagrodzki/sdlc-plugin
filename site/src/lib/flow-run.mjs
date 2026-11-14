// The run of the workflow animation on Home (WorkflowSteps.astro).
// The parts of the run are the workflow steps, then the learn loop.
// `at` is the index of the part in progress: -1 before a run, and the number
// of parts when the run is done. workflow-run.ts applies the values that
// these functions return and holds no state branch of its own.

/**
 * @param {number} index the index of a part
 * @param {number} at the index of the part in progress
 * @returns {'idle'|'run'|'done'}
 */
export function partState(index, at) {
  if (at < 0) return 'idle';
  if (index < at) return 'done';
  return index === at ? 'run' : 'idle';
}

/** @returns {number} the next value of `at`. It stops at `parts`, the done state. */
export function nextAt(at, parts) {
  return Math.min(at + 1, parts);
}

/** @returns {boolean} true when each part of the run is done */
export function isRunDone(at, parts) {
  return at >= parts;
}
