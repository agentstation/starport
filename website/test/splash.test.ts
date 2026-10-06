import { describe, expect, it } from 'vitest';

import {
  CHAPTERS,
  type CodeBlock,
  HEADLINE,
  INSTALL_METHODS,
  lede,
  posterSize,
  README,
  readRepoFile,
  SCENE_PARTS,
  type TableBlock,
} from '../src/lib/splash';

const readme = readRepoFile(README);

// The copy rules ban these words in splash prose. A machine value between
// backticks is not prose, so the check removes it first.
const BANNED = [
  'amazing',
  'best-in-class',
  'blazing',
  'cutting-edge',
  'easily',
  'easy',
  'effortless',
  'effortlessly',
  'enterprise-grade',
  'game-changing',
  'incredible',
  'just',
  'leverage',
  'lightning',
  'magic',
  'magical',
  'next-generation',
  'powerful',
  'revolutionary',
  'robust',
  'seamless',
  'seamlessly',
  'simply',
  'stunning',
  'supercharge',
  'unparalleled',
  'world-class',
];

// A word, an apostrophe, and a contraction ending: "don't", "it's", "we'll".
const CONTRACTION = /\b[a-z]+['’](?:s|t|re|ve|ll|d|m)\b/i;

const codeBlocks = CHAPTERS.flatMap((chapter) =>
  chapter.visuals.filter((visual): visual is CodeBlock => visual.kind === 'code'),
);
const tables = CHAPTERS.flatMap((chapter) =>
  chapter.visuals.filter((visual): visual is TableBlock => visual.kind === 'table'),
);

// Every repository file that a block or a status names, and the README.
const sources = [
  README,
  ...CHAPTERS.flatMap((chapter) => [
    ...chapter.visuals.map((visual) => visual.source),
    ...(chapter.status ? [chapter.status.source] : []),
  ]),
];
const corpus = [...new Set(sources)].map(readRepoFile).join('\n');

// prose is every sentence and fragment that the page shows outside a block.
const prose = [
  ...HEADLINE,
  ...INSTALL_METHODS.map((method) => method.note),
  ...SCENE_PARTS.flatMap((part) => [part.role, part.label, part.card, ...part.details]),
  ...CHAPTERS.flatMap((chapter) => [
    chapter.eyebrow,
    chapter.claim,
    chapter.body,
    ...chapter.chips,
    ...(chapter.status ? [chapter.status.badge] : []),
  ]),
];

const machineValues = (text: string) => text.split('`').filter((_, index) => index % 2 === 1);
const words = (text: string) => text.split('`').filter((_, index) => index % 2 === 0).join(' ');

describe('splash facts', () => {
  it('takes the lede verbatim from the first paragraph of README.md', () => {
    const text = lede();
    expect(text).toMatch(/^Starport is a self-hosted LLM inference gateway in one binary\. /);
    expect(readme.replace(/\n/g, ' ')).toContain(text);
    expect(machineValues(text)).toEqual(['/v1', '/api/v1']);
  });

  it.each(INSTALL_METHODS.map((method) => [method.title, method.command]))(
    'copies the %s commands verbatim from README.md',
    (_title, command) => {
      expect(readme).toContain(command);
    },
  );

  it.each(codeBlocks.map((block) => [block.text.split('\n')[0], block]))(
    'copies the block that starts with %s verbatim from its source',
    (_first, block) => {
      const source = readRepoFile(block.source);
      if (block.verbatim === 'block') {
        expect(source).toContain(block.text);
        return;
      }
      if (block.verbatim === 'spans') {
        for (const line of block.text.split('\n')) expect(source).toContain(`\`${line}\``);
        return;
      }
      // Each line of a collected block is a whole line of its source.
      const lines = new Set(source.split('\n').map((line) => line.trim()));
      for (const line of block.text.split('\n')) expect(lines, line).toContain(line);
    },
  );

  it.each(tables.map((table) => [table.head.join(', '), table]))(
    'copies the table with the columns %s verbatim from its source',
    (_head, table) => {
      const source = readRepoFile(table.source);
      const row = (cells: string[], mono: number[]) =>
        `| ${cells.map((cell, index) => (mono.includes(index) ? `\`${cell}\`` : cell)).join(' | ')} |`;
      expect(source).toContain(row(table.head, []));
      for (const cells of table.rows) expect(source).toContain(row(cells, table.mono));
    },
  );

  it('copies each target status verbatim from its source', () => {
    const statuses = CHAPTERS.flatMap((chapter) => (chapter.status ? [chapter.status] : []));
    expect(statuses.length).toBeGreaterThan(0);
    for (const status of statuses) {
      const source = readRepoFile(status.source);
      expect(source).toContain(status.target);
      expect(source).toContain(status.text);
    }
  });

  it('names only machine values and versions that the sources state', () => {
    const values = prose.flatMap(machineValues);
    expect(values.length).toBeGreaterThan(0);
    for (const value of values) expect(corpus, value).toContain(value);
    const versions = prose.flatMap((text) => text.match(/\d+(?:\.\d+)+/g) ?? []);
    for (const version of versions) expect(corpus, version).toContain(version);
  });

  it('has ten chapters with an eyebrow, a claim, a body, and three chips', () => {
    expect(CHAPTERS).toHaveLength(10);
    expect(new Set(CHAPTERS.map((chapter) => chapter.id)).size).toBe(10);
    for (const chapter of CHAPTERS) {
      expect(chapter.eyebrow, chapter.id).not.toBe('');
      expect(chapter.claim, chapter.id).toMatch(/\.$/);
      const sentences = chapter.body.split(/(?<=\.) /);
      expect(sentences.length, chapter.id).toBeGreaterThanOrEqual(1);
      expect(sentences.length, chapter.id).toBeLessThanOrEqual(3);
      expect(chapter.chips, chapter.id).toHaveLength(3);
      for (const chip of chapter.chips) expect(chip, chapter.id).not.toBe('');
      expect(chapter.visuals.length, chapter.id).toBeGreaterThan(0);
    }
  });

  it('writes no contraction and no banned word', () => {
    for (const text of prose) {
      expect(text, text).not.toMatch(CONTRACTION);
      const found = words(text)
        .toLowerCase()
        .split(/[^a-z-]+/)
        .filter((word) => BANNED.includes(word));
      expect(found, text).toEqual([]);
    }
  });

  it('draws the five parts of the request path', () => {
    expect(SCENE_PARTS.map((part) => part.id).sort()).toEqual(['app', 'catalog', 'providers', 'starport', 'state']);
  });

  it('reads the poster size from the PNG header', () => {
    expect(posterSize()).toEqual({ width: 1280, height: 800 });
  });
});
