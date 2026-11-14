// Change gate of the Site workflow. Reads changed paths from stdin, one on each
// line, and prints 'site=true' or 'site=false'. All logic is in src/lib/site-paths.mjs.
// Uses only built-in modules: the workflow runs it before setup-node.
import { readFileSync } from 'node:fs';
import { siteChanged } from '../src/lib/site-paths.mjs';

const lines = readFileSync(0, 'utf8')
  .split('\n')
  .map((line) => line.replace(/\r$/, ''))
  .filter((line) => line !== '');
console.log(siteChanged(lines));
