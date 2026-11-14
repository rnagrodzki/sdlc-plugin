// Lighthouse CI for the Home page. Each category must reach 0.95 or more as
// the median of 3 runs: the default best run hides slow runs.
// The base path in the URL must match BASE in site.config.mjs.
const median = { minScore: 0.95, aggregationMethod: 'median' };
module.exports = { ci: {
  collect: { startServerCommand: 'bun run preview --port 4321', startServerReadyPattern: 'localhost:4321',
             url: ['http://localhost:4321/sdlc-plugin/'], numberOfRuns: 3 },
  assert: { assertions: {
    'categories:performance': ['error', median], 'categories:accessibility': ['error', median],
    'categories:best-practices': ['error', median], 'categories:seo': ['error', median] } },
  upload: { target: 'filesystem', outputDir: '.lighthouseci' },
} };
