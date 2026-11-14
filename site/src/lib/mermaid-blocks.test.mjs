import test from 'node:test';
import assert from 'node:assert/strict';
import { mermaidBlocks, renderMermaid } from './mermaid-blocks.mjs';

// A fake page: each block is a <pre> that holds one <code>. Replacing the <pre> is recorded.
function fakePage(texts) {
  const replaced = [];
  const doc = {
    createElement: (tag) => ({ tag, className: '', innerHTML: '' }),
  };
  const codes = texts.map((text, i) => {
    const pre = { replaceWith: (node) => replaced.push({ index: i, node }) };
    return { textContent: text, parentElement: pre, ownerDocument: doc };
  });
  return {
    root: {
      querySelectorAll(sel) {
        assert.equal(sel, 'pre > code.language-mermaid');
        return codes;
      },
    },
    codes,
    replaced,
  };
}

function fakeMermaid(render) {
  const calls = { initialize: [] };
  return {
    calls,
    mermaid: {
      default: {
        initialize: (cfg) => calls.initialize.push(cfg),
        render,
      },
    },
  };
}

test('mermaidBlocks skips a block with empty text', () => {
  const { root, codes } = fakePage(['flowchart LR\n A-->B', '', '   \n']);
  assert.deepEqual(mermaidBlocks(root), [codes[0]]);
});

test('no block: returns 0 and does not call importMermaid', async () => {
  const { root } = fakePage([]);
  let imports = 0;
  const count = await renderMermaid(root, { importMermaid: async () => { imports += 1; } });
  assert.equal(count, 0);
  assert.equal(imports, 0);
});

test('2 blocks with one empty: returns 1 and calls importMermaid one time', async () => {
  const { root } = fakePage(['flowchart LR\n A-->B', '']);
  const { mermaid } = fakeMermaid(async () => ({ svg: '<svg></svg>' }));
  let imports = 0;
  const count = await renderMermaid(root, { importMermaid: async () => { imports += 1; return mermaid; } });
  assert.equal(count, 1);
  assert.equal(imports, 1);
});

test('themeVariables reach initialize with startOnLoad false', async () => {
  const { root } = fakePage(['flowchart LR\n A-->B']);
  const { mermaid, calls } = fakeMermaid(async () => ({ svg: '<svg></svg>' }));
  const themeVariables = { primaryColor: '#FFFDF9' };
  await renderMermaid(root, { importMermaid: async () => mermaid, themeVariables });
  assert.deepEqual(calls.initialize, [{ startOnLoad: false, theme: 'base', themeVariables }]);
});

test('1 block: the pre element is replaced by a div that holds the svg', async () => {
  const { root, replaced } = fakePage(['flowchart LR\n A-->B']);
  const { mermaid } = fakeMermaid(async (id, text) => {
    assert.equal(id, 'mermaid-0');
    assert.equal(text, 'flowchart LR\n A-->B');
    return { svg: '<svg id="d"></svg>' };
  });
  const count = await renderMermaid(root, { importMermaid: async () => mermaid, themeVariables: {} });
  assert.equal(count, 1);
  assert.equal(replaced.length, 1);
  assert.equal(replaced[0].node.tag, 'div');
  assert.equal(replaced[0].node.className, 'mermaid-diagram');
  assert.equal(replaced[0].node.innerHTML, '<svg id="d"></svg>');
});

test('importMermaid rejects: returns 0 and keeps each code block', async () => {
  const { root, replaced } = fakePage(['flowchart LR\n A-->B']);
  const count = await renderMermaid(root, {
    importMermaid: async () => { throw new Error('chunk failed'); },
    themeVariables: {},
  });
  assert.equal(count, 0);
  assert.equal(replaced.length, 0);
});

test('the first render throws: the first code block stays and the second is replaced', async () => {
  const { root, replaced } = fakePage(['bad diagram', 'flowchart LR\n A-->B']);
  const { mermaid } = fakeMermaid(async (id) => {
    if (id === 'mermaid-0') throw new Error('parse error');
    return { svg: '<svg></svg>' };
  });
  const count = await renderMermaid(root, { importMermaid: async () => mermaid, themeVariables: {} });
  assert.equal(count, 1);
  assert.deepEqual(replaced.map((r) => r.index), [1]);
});
