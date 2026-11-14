// Selection logic of the all-skills page (LoopMap.astro, HandoverTable.astro,
// SkillTiles.astro). loop-map.ts marks the map nodes and the handover table
// rows that `related` returns.
//
// Map node ids carry a kind prefix, because one name can be a ship step and a
// skill (`review`, `commit`, `pr`, `harden`):
//   step:<name>   a step of the ship Steps table
//   skill:<name>  a skill node: preplan, plan, or a skill tile
//   lane:harden   the harden lane on the way back to plan
//   ship          the ship label on the track

export const SHIP_ID = 'ship';
export const HARDEN_LANE_ID = 'lane:harden';

/** Skills that the map always draws as nodes, before the ship band. */
export const FIXED_SKILLS = ['preplan', 'plan'];

/** @typedef {{ From: string, To: string }} Handover a row of the Handovers table */
/** @typedef {{ name: string, role: string }} SkillInfo a row of the Skill map table */

/** @param {Array<string|{name: string}>} shipSteps */
function stepNames(shipSteps) {
  return (shipSteps ?? []).map((s) => (typeof s === 'string' ? s : s.name));
}

/**
 * The map node id of a skill name in a handover row.
 * @param {string} name
 * @param {Array<string|{name: string}>} [shipSteps]
 */
export function nodeFor(name, shipSteps = []) {
  if (name === 'harden') return HARDEN_LANE_ID;
  if (name === 'ship') return SHIP_ID;
  if (stepNames(shipSteps).includes(name)) return `step:${name}`;
  return `skill:${name}`;
}

/**
 * The skill names that have a `skill:` node: the fixed map skills and each
 * Skill map entry that is not a ship step, ship or harden.
 * @param {{ shipSteps?: Array<string|{name: string}>, skills?: SkillInfo[] }} data
 * @returns {string[]}
 */
export function skillNodeNames({ shipSteps = [], skills = [] }) {
  const names = [...FIXED_SKILLS];
  for (const { name } of skills) {
    if (!names.includes(name) && nodeFor(name, shipSteps) === `skill:${name}`) names.push(name);
  }
  return names;
}

/**
 * @param {string} id a map node id
 * @param {{ shipSteps?: Array<string|{name: string}>, handovers?: Handover[], skills?: SkillInfo[] }} data
 *   `skills` gives the role of each skill (`pipeline` or `around`).
 * @returns {{ nodes: string[], rows: number[] }} the linked map nodes and the
 *   handover table rows (0-based) of one id. An unknown id returns no nodes and no rows.
 */
export function related(id, { shipSteps = [], handovers = [], skills = [] } = {}) {
  if (typeof id !== 'string') return { nodes: [], rows: [] };
  const steps = stepNames(shipSteps);
  const skillNodes = skillNodeNames({ shipSteps: steps, skills });

  const rowsOf = (name) =>
    handovers.flatMap((row, i) => (row.From === name || row.To === name ? [i] : []));
  const exists = (nodeId) =>
    nodeId === SHIP_ID ||
    nodeId === HARDEN_LANE_ID ||
    (nodeId.startsWith('step:') && steps.includes(nodeId.slice(5))) ||
    (nodeId.startsWith('skill:') && skillNodes.includes(nodeId.slice(6)));

  if (id === SHIP_ID) {
    return { nodes: steps.map((s) => `step:${s}`), rows: rowsOf('ship') };
  }

  if (id === HARDEN_LANE_ID) {
    const nodes = [HARDEN_LANE_ID];
    if (steps.length > 0) nodes.push(`step:${steps[steps.length - 1]}`);
    nodes.push('skill:plan');
    return { nodes, rows: rowsOf('harden') };
  }

  if (id.startsWith('step:')) {
    const i = steps.indexOf(id.slice(5));
    if (i === -1) return { nodes: [], rows: [] };
    const nodes = [];
    if (i > 0) nodes.push(`step:${steps[i - 1]}`);
    nodes.push(id);
    if (i < steps.length - 1) nodes.push(`step:${steps[i + 1]}`);
    // Handover rows name skills, not ship steps.
    return { nodes, rows: [] };
  }

  if (id.startsWith('skill:')) {
    const name = id.slice(6);
    if (!skillNodes.includes(name)) return { nodes: [], rows: [] };
    const rows = rowsOf(name);
    const role = skills.find((s) => s.name === name)?.role ?? 'pipeline';
    if (role === 'around') return { nodes: [id], rows };
    const nodes = [id];
    for (const i of rows) {
      const row = handovers[i];
      const other = nodeFor(row.From === name ? row.To : row.From, steps);
      if (exists(other) && !nodes.includes(other)) nodes.push(other);
    }
    return { nodes, rows };
  }

  return { nodes: [], rows: [] };
}
