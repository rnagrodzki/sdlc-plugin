// The skills of the `## Skill map` table of docs/skills/README.md, in the
// order of the table. The Skills menu of the nav, the skill list of Home and
// the all-skills page read this list, so they cannot differ from the docs.
import fs from 'node:fs';
import path from 'node:path';
import { parseTableUnder } from './steps-table.mjs';

/** The roles of the `Role` column, in the order the site shows them. */
export const ROLES = ['pipeline', 'around'];

/**
 * @param {string} readme the text of docs/skills/README.md
 * @returns {Array<{name: string, role: string, summary: string}>}
 */
export function skillMap(readme) {
  const skills = parseTableUnder(readme, '## Skill map').map((r) => ({ name: r.Skill, role: r.Role, summary: r.Summary }));
  if (skills.length === 0) throw new Error('docs/skills/README.md: no table under "## Skill map"');
  for (const skill of skills) {
    if (!ROLES.includes(skill.role)) {
      throw new Error(`docs/skills/README.md: skill "${skill.name}" has the role "${skill.role}", not one of ${ROLES.join(', ')}`);
    }
  }
  return skills;
}

/**
 * Reads the skill map from the docs. The site builds from `site/`, so the
 * default folder is `../docs/skills`.
 * @param {string} [docsDir]
 */
export function readSkillMap(docsDir = path.resolve('../docs/skills')) {
  return skillMap(fs.readFileSync(path.join(docsDir, 'README.md'), 'utf8'));
}

/** @returns {string} the route of a skill page, with the base and a trailing slash */
export function skillHref(name, base) {
  return `${base.replace(/\/+$/, '')}/skills/${name}/`;
}
