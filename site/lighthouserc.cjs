// Lighthouse CI for the Home page. Each category must reach 0.95 or more as
// the median of 3 runs: the default best run hides slow runs.
// BASE comes from site.config.mjs. require() of an ES module needs Node 22.12
// or later (engines in package.json); the module has no top-level await.
const { BASE } = require('./site.config.mjs');
const median = { minScore: 0.95, aggregationMethod: 'median' };
module.exports = { ci: {
  collect: { startServerCommand: 'bun run preview --port 4321', startServerReadyPattern: 'localhost:4321',
             url: [`http://localhost:4321${BASE}/`], numberOfRuns: 3 },
  assert: { assertions: {
    'categories:performance': ['error', median], 'categories:accessibility': ['error', median],
    'categories:best-practices': ['error', median], 'categories:seo': ['error', median] } },
  upload: { target: 'filesystem', outputDir: '.lighthouseci' },
} };
