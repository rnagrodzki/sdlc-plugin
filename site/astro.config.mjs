import { defineConfig, fontProviders } from 'astro/config';
import sitemap from '@astrojs/sitemap';
import { SITE, BASE } from './site.config.mjs';

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
  markdown: { syntaxHighlight: { type: 'shiki', excludeLangs: ['mermaid'] } },
});
