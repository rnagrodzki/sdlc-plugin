/**
 * Copy a command to the clipboard, with a fallback for a browser that has no
 * clipboard API. The browser glue is site/src/scripts/copy.ts: it passes
 * navigator.clipboard and a function that selects the text on the page.
 *
 * 'copied'   the clipboard holds the text
 * 'selected' no clipboard API, the text is selected for a manual copy
 * 'failed'   the clipboard refused the text, or nothing could select it
 *
 * @param {string} text
 * @param {{ clipboard?: { writeText?: (text: string) => Promise<void> } | null, selectText?: (() => void) | null }} deps
 * @returns {Promise<'copied'|'selected'|'failed'>}
 */
export async function copyCommand(text, { clipboard, selectText } = {}) {
  if (clipboard && typeof clipboard.writeText === 'function') {
    try {
      await clipboard.writeText(text);
      return 'copied';
    } catch {
      return 'failed';
    }
  }
  if (typeof selectText === 'function') {
    try {
      selectText();
      return 'selected';
    } catch {
      return 'failed';
    }
  }
  return 'failed';
}
