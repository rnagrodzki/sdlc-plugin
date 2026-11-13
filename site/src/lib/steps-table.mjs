// Reads the `## Steps` and `### Run facts` tables of a skill page in docs/skills/.
// A bad table throws a StepsTableError, so the build stops and a docs page
// and its site page cannot differ.

/** @typedef {{ name: string, actor: 'user'|'tool'|'agent'|'skill', does: string, handsOver: string, optional: boolean, when: string }} Step */
/** @typedef {{ starts: string, ends: string, asks: string, resume: string }} RunFacts */

const STEP_HEADER = ['Step', 'Actor', 'What it does', 'Hands over', 'Optional', 'When'];
const FACT_HEADER = ['Fact', 'Value'];
const ACTORS = ['user', 'tool', 'agent', 'skill'];
const FACT_KEYS = { Starts: 'starts', Ends: 'ends', 'Asks the user': 'asks', Resume: 'resume' };

export class StepsTableError extends Error {
  /** @type {'no-section'|'bad-header'|'bad-actor'|'bad-optional'|'no-facts'|'duplicate-key'} */
  code;

  constructor(code, message) {
    super(message);
    this.name = 'StepsTableError';
    this.code = code;
  }
}

const HEADING_RE = /^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$/;
const FENCE_RE = /^ {0,3}(`{3,}|~{3,})/;

/** Yields the lines of the markdown with the heading info of each, skipping fenced code. */
function scanLines(markdown) {
  const lines = markdown.split(/\r?\n/);
  const out = [];
  let fence = null;
  for (const text of lines) {
    const f = FENCE_RE.exec(text);
    if (fence) {
      if (f && f[1][0] === fence[0] && f[1].length >= fence.length) fence = null;
      out.push({ text, heading: null });
      continue;
    }
    if (f) {
      fence = f[1];
      out.push({ text, heading: null });
      continue;
    }
    const h = HEADING_RE.exec(text);
    out.push({ text, heading: h ? { level: h[1].length, title: h[2].trim() } : null });
  }
  return out;
}

function parseHeadingArg(heading) {
  const m = HEADING_RE.exec(heading.trim());
  if (!m) throw new TypeError(`heading must look like "## Title", got "${heading}"`);
  return { level: m[1].length, title: m[2].trim() };
}

function splitRow(line) {
  let s = line.trim();
  if (s.startsWith('|')) s = s.slice(1);
  if (s.endsWith('|') && !s.endsWith('\\|')) s = s.slice(0, -1);
  const cells = [];
  let cur = '';
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '\\' && s[i + 1] === '|') {
      cur += '|';
      i++;
    } else if (s[i] === '|') {
      cells.push(cur.trim());
      cur = '';
    } else {
      cur += s[i];
    }
  }
  cells.push(cur.trim());
  return cells;
}

const isSeparator = (cells) => cells.length > 0 && cells.every((c) => /^:?-+:?$/.test(c));

/**
 * Finds the first table under the first matching heading.
 * Returns null when the table is absent or has no separator row.
 * Throws no-section when the heading is absent.
 */
function readTable(markdown, heading) {
  const want = parseHeadingArg(heading);
  const lines = scanLines(markdown);
  const start = lines.findIndex(
    (l) => l.heading && l.heading.level === want.level && l.heading.title === want.title,
  );
  if (start === -1) throw new StepsTableError('no-section', `heading "${heading}" not found`);

  let i = start + 1;
  for (; i < lines.length; i++) {
    if (lines[i].heading) return null;
    if (lines[i].text.trim().startsWith('|')) break;
  }
  if (i >= lines.length) return null;

  const header = splitRow(lines[i].text);
  const sep = i + 1 < lines.length ? splitRow(lines[i + 1].text) : [];
  if (!lines[i + 1]?.text.trim().startsWith('|') || !isSeparator(sep)) return null;

  const rows = [];
  for (let j = i + 2; j < lines.length; j++) {
    if (lines[j].heading || !lines[j].text.trim().startsWith('|')) break;
    rows.push(splitRow(lines[j].text));
  }
  return { header, rows };
}

/**
 * Reads the first table under the first heading that matches.
 * `heading` is a full heading line, for example "## Steps".
 * @returns {Array<Record<string,string>>}
 */
export function parseTableUnder(markdown, heading) {
  const table = readTable(markdown, heading);
  if (!table) return [];
  return table.rows.map((cells) =>
    Object.fromEntries(table.header.map((name, idx) => [name, cells[idx] ?? ''])),
  );
}

function requireTable(markdown, heading, expectedHeader) {
  const table = readTable(markdown, heading);
  if (!table) throw new StepsTableError('bad-header', `no table with a separator row under "${heading}"`);
  if (table.header.join('\u0000') !== expectedHeader.join('\u0000')) {
    throw new StepsTableError(
      'bad-header',
      `table under "${heading}" must have the columns: ${expectedHeader.join(' | ')}`,
    );
  }
  for (const row of table.rows) {
    if (row.length !== expectedHeader.length) {
      throw new StepsTableError(
        'bad-header',
        `row "${row[0]}" under "${heading}" has ${row.length} cells, expected ${expectedHeader.length}`,
      );
    }
  }
  return table.rows;
}

/** Returns the lines of the `## Steps` section, up to the next heading of level 1 or 2. */
function stepsSection(markdown) {
  const lines = scanLines(markdown);
  const start = lines.findIndex((l) => l.heading && l.heading.level === 2 && l.heading.title === 'Steps');
  if (start === -1) throw new StepsTableError('no-section', 'heading "## Steps" not found');
  let end = lines.length;
  for (let i = start + 1; i < lines.length; i++) {
    if (lines[i].heading && lines[i].heading.level <= 2) {
      end = i;
      break;
    }
  }
  return lines.slice(start, end).map((l) => l.text).join('\n');
}

/**
 * @param {string} markdown
 * @returns {{ steps: Step[], facts: RunFacts }}
 */
export function parseSteps(markdown) {
  const section = stepsSection(markdown);

  const steps = [];
  const seenSteps = new Set();
  for (const [name, actor, does, handsOver, optional, when] of requireTable(section, '## Steps', STEP_HEADER)) {
    if (seenSteps.has(name)) throw new StepsTableError('duplicate-key', `step "${name}" appears two times`);
    seenSteps.add(name);
    if (!ACTORS.includes(actor)) {
      throw new StepsTableError('bad-actor', `step "${name}": actor "${actor}" must be one of ${ACTORS.join(', ')}`);
    }
    if (optional !== 'yes' && optional !== 'no') {
      throw new StepsTableError('bad-optional', `step "${name}": Optional "${optional}" must be yes or no`);
    }
    steps.push({ name, actor, does, handsOver, optional: optional === 'yes', when });
  }

  let factRows;
  try {
    factRows = requireTable(section, '### Run facts', FACT_HEADER);
  } catch (err) {
    if (err instanceof StepsTableError && err.code === 'no-section') {
      throw new StepsTableError('no-facts', '"### Run facts" not found under "## Steps"');
    }
    throw err;
  }
  const facts = {};
  for (const [label, value] of factRows) {
    const key = FACT_KEYS[label];
    if (!key) continue;
    if (key in facts) throw new StepsTableError('duplicate-key', `Run facts label "${label}" appears two times`);
    facts[key] = value;
  }
  for (const [label, key] of Object.entries(FACT_KEYS)) {
    if (!(key in facts)) throw new StepsTableError('no-facts', `Run facts label "${label}" is absent`);
  }
  return { steps, facts: /** @type {RunFacts} */ (facts) };
}

/**
 * Remark plugin. Removes the `## Steps` section, with its `### Run facts` table,
 * from a page in docs/skills/. The site shows the steps in its own component.
 * Pages outside docs/skills/ stay as they are.
 */
export function remarkStripSteps() {
  return (tree, file) => {
    const path = String(file?.path ?? '').replace(/\\/g, '/');
    if (!/(^|\/)docs\/skills\/[^/]+$/.test(path)) return;
    const children = tree.children;
    const isH2Steps = (n) =>
      n.type === 'heading' && n.depth === 2 && n.children?.map((c) => c.value ?? '').join('').trim() === 'Steps';
    const start = children.findIndex(isH2Steps);
    if (start === -1) return;
    let end = children.length;
    for (let i = start + 1; i < children.length; i++) {
      if (children[i].type === 'heading' && children[i].depth <= 2) {
        end = i;
        break;
      }
    }
    children.splice(start, end - start);
  };
}
