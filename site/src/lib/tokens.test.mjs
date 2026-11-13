import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { checkPairs } from './contrast.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const tokensCss = fs.readFileSync(path.join(here, '../styles/tokens.css'), 'utf8');
const baseCss = fs.readFileSync(path.join(here, '../styles/base.css'), 'utf8');
const reference = fs.readFileSync(
  path.join(here, '../../../docs/internal/site-design/site-preview.src.html'),
  'utf8',
);

const stripComments = (css) => css.replace(/\/\*[\s\S]*?\*\//g, '');

/** Read each `--name: value` declaration of a CSS text into a Map. */
function readCustomProperties(css) {
  const props = new Map();
  for (const decl of stripComments(css).split(/[;{}]/)) {
    const m = decl.match(/^\s*(--[\w-]+)\s*:\s*([\s\S]+?)\s*$/);
    if (m) props.set(m[1], m[2]);
  }
  return props;
}

const TOKENS = readCustomProperties(tokensCss);

/** Resolve var(--x) references to the final value of a token. */
function token(name) {
  const value = TOKENS.get(name);
  if (value === undefined) throw new Error(`tokens.css has no ${name}`);
  return value.replace(/var\((--[\w-]+)\)/g, (_, ref) => token(ref));
}

/** Parse a hex or rgba() color into [r, g, b, a]. */
function rgba(color) {
  const v = color.trim().toLowerCase();
  const hex = v.match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/);
  if (hex) {
    const d = hex[1].length === 3 ? [...hex[1]].map((c) => c + c).join('') : hex[1];
    return [0, 2, 4].map((i) => parseInt(d.slice(i, i + 2), 16)).concat(1);
  }
  const fn = v.match(/^rgba?\(([^)]+)\)$/);
  if (fn) {
    const parts = fn[1].split(',').map(Number);
    return [parts[0], parts[1], parts[2], parts.length === 4 ? parts[3] : 1];
  }
  throw new Error(`Unsupported color: ${color}`);
}

/** One key for each color value, so #fff, #FFFFFF and rgba(255,255,255,1) match. */
const colorKey = (color) => {
  const [r, g, b, a] = rgba(color);
  return `${r},${g},${b},${Math.round(a * 1000) / 1000}`;
};

/** Paint a color with alpha over an opaque surface; return the opaque hex. */
function over(fg, bg) {
  const [fr, fgG, fb, fa] = rgba(fg);
  const [br, bgG, bb] = rgba(bg);
  const mix = (f, b) => Math.round(f * fa + b * (1 - fa)).toString(16).padStart(2, '0');
  return `#${mix(fr, br)}${mix(fgG, bgG)}${mix(fb, bb)}`;
}

// Surfaces the pages paint text and controls on.
const bg = token('--bg');
const panel = over(token('--glass'), bg); // a glass panel over the page, #F4F2EF
const field = over(token('--f1'), bg); // the amber color field, #E5CDAE
const fieldGreen = over(token('--f2'), bg);
const paper = token('--paper');
const ink = token('--ink');
const part = over(token('--accent-24'), panel); // a time-bar part
const partHover = over(token('--accent-42'), panel);

// Each text and control pair the pages use. Text needs 4.5 (WCAG 1.4.3);
// control and part edges need 3 (WCAG 1.4.11).
const PAIRS = [
  { fg: ink, bg, min: 4.5, label: 'ink on the page' },
  { fg: ink, bg: panel, min: 4.5, label: 'ink on a glass panel' },
  { fg: ink, bg: paper, min: 4.5, label: 'ink on paper' },
  { fg: ink, bg: field, min: 4.5, label: 'ink on the amber field' },
  { fg: ink, bg: token('--accent-tint'), min: 4.5, label: 'ink on a hovered map chip' },
  { fg: ink, bg: part, min: 4.5, label: 'ink on a time-bar part' },
  { fg: ink, bg: partHover, min: 4.5, label: 'ink on a hovered time-bar part' },
  { fg: token('--muted'), bg, min: 4.5, label: 'muted on the page (inactive steps)' },
  { fg: token('--muted'), bg: panel, min: 4.5, label: 'muted on a glass panel (inactive steps)' },
  { fg: token('--muted'), bg: paper, min: 4.5, label: 'muted on paper' },
  { fg: token('--muted'), bg: field, min: 4.5, label: 'muted on the amber field' },
  { fg: token('--muted'), bg: fieldGreen, min: 4.5, label: 'muted on the green field' },
  { fg: token('--muted'), bg: over(token('--white-50'), bg), min: 4.5, label: 'muted on a who chip' },
  { fg: token('--accent'), bg, min: 4.5, label: 'accent on the page' },
  { fg: token('--accent'), bg: panel, min: 4.5, label: 'accent on a glass panel' },
  { fg: token('--accent'), bg: paper, min: 4.5, label: 'accent on paper' },
  { fg: token('--accent'), bg: token('--accent-tint'), min: 4.5, label: 'accent on a hovered map chip' },
  { fg: token('--accent-on-field'), bg: field, min: 4.5, label: 'change title on the amber field' },
  { fg: token('--on-accent'), bg: token('--accent'), min: 4.5, label: 'on-accent on accent' },
  { fg: token('--go-text'), bg: token('--go-tint'), min: 4.5, label: 'harden command on the hovered node' },
  { fg: token('--go-text'), bg: paper, min: 4.5, label: 'harden command on the node' },
  { fg: token('--white'), bg: token('--go'), min: 4.5, label: 'white on the selected harden node' },
  { fg: token('--white-78'), bg: ink, min: 4.5, label: 'text of the dashboard node' },
  { fg: token('--cream'), bg: ink, min: 4.5, label: 'command of the dashboard node' },
  { fg: token('--ink-50'), bg: panel, min: 3, label: 'switch track when off' },
  { fg: token('--accent'), bg: panel, min: 3, label: 'switch track when on' },
  { fg: token('--accent-70'), bg: panel, min: 3, label: 'time-bar part edge' },
  { fg: token('--accent'), bg, min: 3, label: 'pill edge and focus ring' },
  { fg: ink, bg: panel, min: 3, label: 'ink focus ring on a panel' },
  { fg: token('--go'), bg: panel, min: 3, label: 'harden loop on the map' },
  { fg: token('--lamp-off'), bg: ink, min: 3, label: 'dashboard lamp when off' },
  { fg: token('--go-lit'), bg: ink, min: 3, label: 'dashboard lamp when lit' },
];

test('each declared pair meets its target ratio', () => {
  assert.deepEqual(checkPairs(PAIRS), []);
});

test('the check fails each pair as the reference had it before the 5 fixes', () => {
  const before = [
    { fg: token('--accent'), bg: field, min: 4.5, label: 'change title' },
    { fg: token('--go'), bg: token('--go-tint'), min: 4.5, label: 'harden command' },
    { fg: over('rgba(90,82,71,.5)', panel), bg: panel, min: 4.5, label: 'inactive step' },
    { fg: 'rgba(31,26,20,.22)', bg: panel, min: 3, label: 'switch track' },
    { fg: token('--accent-24'), bg: panel, min: 3, label: 'time-bar part edge' },
  ];
  assert.deepEqual(
    checkPairs(before).map((f) => f.label),
    before.map((p) => p.label),
  );
});

test('each token of the reference .site block has the same value in tokens.css', () => {
  const block = reference.match(/\n\.site\{([\s\S]*?)\n\}/);
  assert.ok(block, 'the reference has a .site block');
  const site = readCustomProperties(block[1]);
  // The site loads Geist through the Astro fonts API, so the font tokens
  // name the variables of that API instead of the family names.
  const fonts = new Map([
    ['--display', ['--font-display', 'var(--font-geist)']],
    ['--body', ['--font-body', 'var(--font-geist)']],
    ['--mono', ['--font-mono', 'var(--font-geist-mono)']],
  ]);
  assert.ok(site.size > 0);
  for (const [name, value] of site) {
    if (fonts.has(name)) {
      const [ours, family] = fonts.get(name);
      assert.ok(TOKENS.get(ours)?.startsWith(family), `${ours} starts with ${family}`);
      continue;
    }
    assert.equal(TOKENS.get(name), value, `${name} in tokens.css`);
  }
});

test('each raw color of the reference site CSS is a named token with the same value', () => {
  const start = reference.indexOf('/* ---------- the site, locked look');
  const end = reference.indexOf('</style>');
  assert.ok(start > 0 && end > start, 'the reference has the site CSS');
  const siteCss = stripComments(reference.slice(start, end));
  const raw = siteCss.match(/#[0-9A-Fa-f]{6}\b|#[0-9A-Fa-f]{3}\b|rgba?\([^)]*\)/g) ?? [];
  assert.ok(raw.length > 0);
  const known = new Set();
  for (const value of TOKENS.values()) {
    try {
      known.add(colorKey(value));
    } catch {
      // not a color token
    }
  }
  const missing = [...new Set(raw)].filter((c) => !known.has(colorKey(c)));
  assert.deepEqual(missing, []);
});

test('base.css stops all animations and transitions under reduced motion', () => {
  const css = stripComments(baseCss).replace(/\s+/g, ' ');
  const block = css.match(/@media \(prefers-reduced-motion: reduce\) \{(.*?\})\s*\}/);
  assert.ok(block, 'base.css has a reduced-motion block');
  assert.match(block[1], /^\s*\*, \*::before, \*::after \{/);
  assert.match(block[1], /animation: none !important/);
  assert.match(block[1], /transition: none !important/);
});

test('base.css uses only tokens that tokens.css declares', () => {
  const used = [...baseCss.matchAll(/var\((--[\w-]+)\)/g)].map((m) => m[1]);
  assert.ok(used.length > 0);
  assert.deepEqual(used.filter((name) => !TOKENS.has(name)), []);
});
