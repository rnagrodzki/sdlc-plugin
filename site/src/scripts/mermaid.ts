// Browser glue of the Mermaid diagrams. The logic is in lib/mermaid-blocks.mjs.
// Mermaid is about 1 MB. The dynamic import runs only when the page holds a diagram.
import { renderMermaid } from '../lib/mermaid-blocks.mjs';

const style = getComputedStyle(document.documentElement);
const token = (name: string): string => style.getPropertyValue(name).trim();

renderMermaid(document, {
  importMermaid: () => import('mermaid'),
  themeVariables: {
    background: token('--paper'),
    primaryColor: token('--paper'),
    primaryTextColor: token('--ink'),
    primaryBorderColor: token('--accent'),
    lineColor: token('--muted'),
    textColor: token('--ink'),
    fontFamily: token('--font-body'),
  },
});
