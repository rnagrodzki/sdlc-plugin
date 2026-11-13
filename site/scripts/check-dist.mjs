// Runs the scan of dist/ after `astro build`. All logic is in src/lib/dist-check.mjs.
import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { BASE } from '../site.config.mjs';
import { runCheck } from '../src/lib/dist-check.mjs';

const listFiles = (dir) =>
  readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => path.relative(dir, path.join(entry.parentPath, entry.name)).split(path.sep).join('/'));

const r = runCheck({
  distDir: 'dist',
  repoRoot: '..',
  base: BASE,
  listFiles,
  readFile: (file) => readFileSync(file, 'utf8'),
  readdirSync,
});
r.lines.forEach((line) => console.log(line));
process.exit(r.exitCode);
