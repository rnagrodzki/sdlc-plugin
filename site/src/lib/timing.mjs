/**
 * Splits the measured time of a run between its steps.
 * @param {Array<{ name: string, optional: boolean }>} steps
 * @param {Record<string, number>} times  milliseconds for each step name
 * @param {{ includeOptional?: boolean }} [options]
 * @returns {{ totalMs: number, parts: Array<{ name: string, ms: number, share: number, measured: boolean }> }}
 */
export function stepShares(steps, times, { includeOptional = true } = {}) {
  const used = steps.filter((s) => includeOptional || !s.optional);
  const parts = used.map((s) => {
    const t = Object.hasOwn(times ?? {}, s.name) ? times[s.name] : undefined;
    const measured = typeof t === 'number' && Number.isFinite(t) && t >= 0;
    return { name: s.name, ms: measured ? t : 0, share: 0, measured };
  });
  const totalMs = parts.reduce((sum, p) => sum + p.ms, 0);
  if (totalMs > 0) for (const p of parts) p.share = p.ms / totalMs;
  return { totalMs, parts };
}

/**
 * @param {Record<string, { version: string, runDate: string, times: Record<string, number> }>} stepTimes
 * @param {string} skill
 * @returns {{ version: string, runDate: string, times: Record<string, number> } | null}
 */
export function timesFor(stepTimes, skill) {
  return stepTimes && Object.hasOwn(stepTimes, skill) ? stepTimes[skill] : null;
}
