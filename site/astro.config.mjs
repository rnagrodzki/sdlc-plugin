import { defineConfig, fontProviders } from 'astro/config';
import sitemap from '@astrojs/sitemap';
import fs from 'node:fs';
import path from 'node:path';
import { SITE, BASE, REPO_BLOB_URL } from './site.config.mjs';
import { remarkDocLinks, assertUniqueRoutes } from './src/lib/doc-links.mjs';
import { listPublished } from './src/lib/published-paths.mjs';
import { remarkStripSteps } from './src/lib/steps-table.mjs';

const repoRoot = path.resolve('..');
const publishedPaths = listPublished(repoRoot, fs);
assertUniqueRoutes(publishedPaths, { base: BASE });

export default defineConfig({
  site: SITE,
  base: BASE,
  trailingSlash: 'always',
  output: 'static',
  integrations: [sitemap()],
  fonts: [
    { provider: fontProviders.fontsource(), name: 'Geist', cssVariable: '--font-geist', weights: [400, 500, 600, 700] },
    { provider: fontProviders.fontsource(), name: 'Geist Mono', cssVariable: '--font-geist-mono', weights: [400, 500] },
  ],
  markdown: {
    syntaxHighlight: { type: 'shiki', excludeLangs: ['mermaid'] },
    remarkPlugins: [[remarkDocLinks, { publishedPaths, base: BASE, repoBlobUrl: REPO_BLOB_URL, repoRoot }], remarkStripSteps],
  },
});
