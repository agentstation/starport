import { describe, expect, it } from 'vitest';

import {
  type Viewport,
  cameras,
  chapterIndexFor,
  chapterRest,
  chapterStop,
  chapters,
  interpolateCamera,
  monotoneCubic,
  requestStates,
} from '../src/components/journey/timeline';
import { requestRoute } from '../src/components/journey/world';
import {
  CHAPTERS,
  type CodeBlock,
  COMPOSE_METHOD,
  HEADLINE,
  INSTALL_METHODS,
  lede,
  posterSize,
  README,
  readRepoFile,
  REQUEST_STATES,
  type TableBlock,
  WORLD_LABELS,
  WORLD_PARTS,
  WORLD_PROVIDERS,
  WORLD_SOURCES,
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
  ...WORLD_SOURCES,
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
  COMPOSE_METHOD.note,
  ...WORLD_PARTS.flatMap((part) => [part.title, part.label, part.card, ...part.details]),
  ...WORLD_PROVIDERS,
  ...Object.values(REQUEST_STATES),
  ...Object.values(WORLD_LABELS),
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

  it('draws each part of the request path once, with a title and a card', () => {
    expect(WORLD_PARTS.map((part) => part.id).sort()).toEqual([
      'app',
      'catalog',
      'console',
      'controls',
      'host',
      'keys',
      'listener',
      'planning',
      'providers',
      'state',
      'stream',
    ]);
    for (const part of WORLD_PARTS) {
      expect(part.title, part.id).not.toBe('');
      expect(part.card, part.id).toMatch(/\.$/);
    }
  });

  it('restates the Compose note from README.md', () => {
    expect(readme).toContain('The default Compose file builds one Starport process');
  });

  it('reads the poster size from the PNG header', () => {
    expect(posterSize()).toEqual({ width: 1280, height: 800 });
  });
});

const VIEWPORTS: Viewport[] = ['wide', 'medium', 'compact'];

describe('journey timeline', () => {
  it('keeps the chapters sourced from the splash facts, in order', () => {
    expect(chapters.map((chapter) => chapter.id)).toEqual(CHAPTERS.map((chapter) => chapter.id));
    chapters.forEach((chapter, index) => expect(chapter.facts, chapter.id).toBe(CHAPTERS[index]));
  });

  it('has ten chapters in seven acts, with alternating sides', () => {
    expect(chapters).toHaveLength(10);
    expect([...new Set(chapters.map((chapter) => chapter.act))]).toEqual([
      'client',
      'gateway',
      'catalog',
      'request',
      'state',
      'operate',
      'deploy',
    ]);
    chapters.forEach((chapter, index) => expect(chapter.align, chapter.id).toBe(index % 2 === 0 ? 'left' : 'right'));
  });

  it('orders the chapter windows, keeps them disjoint, and keeps them inside 0..1', () => {
    expect(chapters[0].start).toBe(0);
    expect(chapters[chapters.length - 1].end).toBe(1);
    chapters.forEach((chapter, index) => {
      expect(chapter.start, chapter.id).toBeGreaterThanOrEqual(0);
      expect(chapter.end, chapter.id).toBeLessThanOrEqual(1);
      expect(chapter.end, chapter.id).toBeGreaterThan(chapter.start);
      if (index > 0) expect(chapter.start, chapter.id).toBeGreaterThan(chapters[index - 1].end);
      const rest = chapterRest(index);
      expect(rest.to, chapter.id).toBeGreaterThan(rest.from);
      const stop = chapterStop(index);
      expect(stop, chapter.id).toBeGreaterThanOrEqual(rest.from);
      expect(stop, chapter.id).toBeLessThanOrEqual(rest.to);
    });
  });

  it.each(VIEWPORTS)('has camera keyframes in order, with one at each end of each chapter, on a %s stage', (viewport) => {
    const frames = cameras[viewport];
    for (let index = 1; index < frames.length; index += 1) {
      expect(frames[index].at).toBeGreaterThan(frames[index - 1].at);
    }
    chapters.forEach((chapter) => {
      expect(frames.some((frame) => frame.at === chapter.start), chapter.id).toBe(true);
      expect(frames.some((frame) => frame.at === chapter.end), chapter.id).toBe(true);
    });
    for (const frame of frames) {
      expect(frame.zoom).toBeGreaterThan(0);
      expect(frame.anchorX).toBeGreaterThanOrEqual(0);
      expect(frame.anchorX).toBeLessThanOrEqual(1);
    }
  });

  // The composed stage width of each viewport class (timeline.ts).
  const STAGE_WIDTH: Record<Viewport, number> = { wide: 1440, medium: 1024, compact: 390 };

  it.each(VIEWPORTS)('keeps the request in frame through each chapter rest on a %s stage', (viewport) => {
    const route = requestRoute();
    const times = route.map((point) => point.at);
    const xs = route.map((point) => point.x);
    const width = STAGE_WIDTH[viewport];
    chapters.forEach((chapter, index) => {
      const rest = chapterRest(index);
      for (let step = 0; step <= 40; step += 1) {
        const progress = rest.from + ((rest.to - rest.from) * step) / 40;
        const camera = interpolateCamera(progress, viewport);
        const x = monotoneCubic(times, xs, progress);
        const left = camera.x - (camera.anchorX * width) / camera.zoom;
        const right = camera.x + ((1 - camera.anchorX) * width) / camera.zoom;
        expect(x, `${chapter.id} at ${progress.toFixed(4)}`).toBeGreaterThanOrEqual(left);
        expect(x, `${chapter.id} at ${progress.toFixed(4)}`).toBeLessThanOrEqual(right);
      }
    });
  });

  it('frames every phone chapter at a zoom where its card titles stay inside their cards', () => {
    // Below this zoom, a title on the compact screen floor is wider than its
    // card. The deploy chapter's copy covers the world on a phone.
    const legible = 0.35;
    chapters.forEach((chapter, index) => {
      if (chapter.id === 'deploy') return;
      const rest = chapterRest(index);
      for (let step = 0; step <= 20; step += 1) {
        const progress = rest.from + ((rest.to - rest.from) * step) / 20;
        expect(interpolateCamera(progress, 'compact').zoom, chapter.id).toBeGreaterThanOrEqual(legible);
      }
    });
  });

  it('names a chapter for each position, monotone in the scroll', () => {
    let previous = 0;
    for (let step = 0; step <= 1000; step += 1) {
      const index = chapterIndexFor(step / 1000);
      expect(index).toBeGreaterThanOrEqual(previous);
      expect(index - previous).toBeLessThanOrEqual(1);
      previous = index;
    }
    expect(previous).toBe(chapters.length - 1);
    chapters.forEach((chapter, index) => expect(chapterIndexFor(chapterStop(index)), chapter.id).toBe(index));
  });

  it('moves the request through its states in order', () => {
    expect(requestStates.map((state) => state.label)).toEqual(Object.values(REQUEST_STATES));
    for (let index = 1; index < requestStates.length; index += 1) {
      expect(requestStates[index].at).toBeGreaterThan(requestStates[index - 1].at);
    }
  });
});
