// Browser glue of the install command box. The logic is in lib/copy-command.mjs.
import { copyCommand } from '../lib/copy-command.mjs';

const LABELS = {
  copied: 'Copied',
  selected: 'Select the text',
  failed: 'Copy failed',
} as const;
const RESET_MS = { copied: 1600, selected: 2200, failed: 2200 } as const;

function selectContents(node: Element | null): void {
  if (!node) throw new Error('no text to select');
  const selection = window.getSelection();
  if (!selection) throw new Error('no selection API');
  const range = document.createRange();
  range.selectNodeContents(node);
  selection.removeAllRanges();
  selection.addRange(range);
}

document.querySelectorAll<HTMLButtonElement>('[data-copy-button]').forEach((button) => {
  const box = button.closest('[data-install]');
  const status = box?.querySelector<HTMLElement>('[data-copy-status]') ?? null;
  const idle = button.textContent ?? 'Copy';
  let timer: number | undefined;

  button.addEventListener('click', async () => {
    const result = await copyCommand(button.dataset.copy ?? '', {
      clipboard: navigator.clipboard,
      selectText: () => selectContents(box?.querySelector('[data-install-lines]') ?? null),
    });
    button.textContent = LABELS[result];
    if (status) status.textContent = LABELS[result];
    window.clearTimeout(timer);
    timer = window.setTimeout(() => {
      button.textContent = idle;
      if (status) status.textContent = '';
    }, RESET_MS[result]);
  });
});
