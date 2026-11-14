// Finds the Mermaid code blocks of a page and renders them with Mermaid.
// The browser glue is in scripts/mermaid.ts. Mermaid loads only when the page holds a block.

/**
 * @param {{ querySelectorAll(sel: string): ArrayLike<{ textContent: string }> }} root
 * @returns {Array<{ textContent: string }>} the code elements under 'pre > code.language-mermaid' that have text.
 *   A block with empty text is skipped.
 */
export function mermaidBlocks(root) {
  return Array.from(root.querySelectorAll('pre > code.language-mermaid')).filter(
    (code) => (code.textContent ?? '').trim() !== '',
  );
}

/**
 * Imports Mermaid and renders each block. With no block, it calls importMermaid zero times.
 * @param {{ querySelectorAll(sel: string): ArrayLike<any> }} root
 * @param {{ importMermaid: () => Promise<{ default: any }>, themeVariables?: Record<string, string> }} deps
 * @returns {Promise<number>} the count of rendered blocks
 */
export async function renderMermaid(root, { importMermaid, themeVariables }) {
  const blocks = mermaidBlocks(root);
  if (blocks.length === 0) return 0;
  let m;
  try {
    m = (await importMermaid()).default;
  } catch {
    // The import fails: keep each code block.
    return 0;
  }
  m.initialize({ startOnLoad: false, theme: 'base', themeVariables });
  let count = 0;
  for (const [i, code] of blocks.entries()) {
    try {
      const { svg } = await m.render(`mermaid-${i}`, code.textContent);
      const div = code.ownerDocument.createElement('div');
      div.className = 'mermaid-diagram';
      div.innerHTML = svg;
      code.parentElement.replaceWith(div); // replace the <pre>
      count += 1;
    } catch {
      // One bad diagram: keep its code block and go to the next one.
    }
  }
  return count;
}
