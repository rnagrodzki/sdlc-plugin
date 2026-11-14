// Reads the title, the one-line lead and the "On this page" entries of a Markdown docs page.
// The docs index, the docs pages and DocLayout use it.

/** Links become their text. The marks of inline code, bold and italic text are removed. */
function plain(text) {
  return text
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[`*]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

/**
 * @param {string} markdown the body of a docs page
 * @param {string} fallbackTitle the title when the page has no H1
 * @returns {{ title: string, lead: string, leadIsIntro: boolean }}
 *   `title`: the text of the first H1.
 *   `lead`: the first sentence of the first paragraph or block quote under the H1, as plain text.
 *     It is '' when that block is a list, a table, a code block or a heading.
 *     A sentence ends at the first `.`, `!` or `?` that a space or the end of the block follows.
 *   `leadIsIntro`: true when the lead is the complete block and the block has no link, so a page
 *     that shows the lead in its title block can hide the block and lose no text and no link.
 */
export function docMeta(markdown, fallbackTitle) {
  const lines = markdown.split(/\r?\n/);
  const h1 = lines.findIndex((l) => /^#\s/.test(l));
  const title = h1 === -1 ? fallbackTitle : plain(lines[h1].replace(/^#\s+/, '').replace(/\s+#+\s*$/, ''));

  let i = h1 + 1; // with no H1, the lead comes from the first block of the page
  while (i < lines.length && lines[i].trim() === '') i++;
  const block = [];
  for (; i < lines.length; i++) {
    const line = lines[i].trim();
    if (line === '' || /^(#|\||```|~~~|[-*+] |\d+\. )/.test(line)) break;
    block.push(line.replace(/^>\s?/, ''));
  }
  const raw = block.join(' ');
  const intro = plain(raw);
  const lead = /^.*?[.!?](?=\s|$)/.exec(intro)?.[0] ?? intro;
  const hasLink = /\[[^\]]*\]\([^)]*\)/.test(raw);
  return { title, lead, leadIsIntro: lead !== '' && lead === intro && !hasLink };
}

/**
 * @param {ReadonlyArray<{ depth: number, slug: string, text: string }>} headings the headings of a rendered page
 * @param {{ maxDepth?: number }} [options] the deepest heading level to keep. The default is 3
 * @returns {Array<{ depth: number, slug: string, text: string }>} the h2 to h`maxDepth` headings, in page order
 */
export function tocEntries(headings, { maxDepth = 3 } = {}) {
  return headings.filter((h) => h.depth >= 2 && h.depth <= maxDepth);
}
