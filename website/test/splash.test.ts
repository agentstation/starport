import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { Journey } from '../src/components/journey/journey';
import { Mascot, type MascotProps, type MascotState } from '../src/components/mascot';
import {
  ACT_GROUNDS,
  COPY_EDGE,
  type Viewport,
  acts,
  cameras,
  chapterIndexFor,
  chapterRest,
  chapterStop,
  chapters,
  groundCrossings,
  interpolateCamera,
  monotoneCubic,
  requestStates,
  visibilityWindow,
} from '../src/components/journey/timeline';
import { night } from '../src/components/journey/scene';
import { paletteFor, requestRoute, stillAspect } from '../src/components/journey/world';
import {
  CHAPTERS,
  type CodeBlock,
  COMPOSE_METHOD,
  HEADLINE,
  HOSTING,
  INSTALL_METHODS,
  lede,
  PERSISTENT_METHOD,
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
  ...HOSTING.sources,
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
  PERSISTENT_METHOD.note,
  ...WORLD_PARTS.flatMap((part) => [part.title, part.label, part.card, ...part.details]),
  ...WORLD_PROVIDERS,
  ...Object.values(REQUEST_STATES),
  ...Object.values(WORLD_LABELS).flat(),
  HOSTING.caption,
  ...HOSTING.hosts.flatMap((host) => [host.name, ...(host.note ? [host.note] : [])]),
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

  it('tells the gateway story, then the server, the fleet, and the laptop', () => {
    expect(CHAPTERS.map((chapter) => chapter.id)).toEqual([
      'sdk',
      'surfaces',
      'credentials',
      'catalog',
      'first-request',
      'console',
      'enterprise',
      'storage',
      'server',
      'scale-out',
      'laptop',
    ]);
  });

  it('cites only repository files that exist', () => {
    for (const file of new Set(sources)) expect(() => readRepoFile(file), file).not.toThrow();
  });

  it('has eleven chapters with an eyebrow, a claim, a body, and three chips', () => {
    expect(CHAPTERS).toHaveLength(11);
    expect(new Set(CHAPTERS.map((chapter) => chapter.id)).size).toBe(11);
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

  it('draws the production server from the README Compose block and targets.md T3', () => {
    const targets = readRepoFile('docs/site/architecture/targets.md');
    const text = readme.replace(/\s+/g, ' ');
    expect(text).toContain('one Starport process with persistent Badger, SQLite, and file storage');
    expect(text).toContain('The three named volumes retain configuration, application data, and catalog state');
    expect(targets).toContain('T3 uses the T2 storage recipe on durable volumes with explicit service paths. It runs one active gateway.');
    expect(targets).toContain('Badger for records, SQLite for relational data, and a local directory for file bytes');
    expect(WORLD_LABELS.volumes).toEqual(['Badger', 'SQLite', 'Files']);
  });

  it('names the hosts as plain text, claims no tested cloud, and sources the restricted case to T6', () => {
    const targets = readRepoFile('docs/site/architecture/targets.md');
    expect(HOSTING.sources).toEqual([README, 'docs/site/architecture/targets.md']);
    expect(HOSTING.hosts.map((host) => host.name)).toEqual(['AWS', 'Google Cloud', 'Azure', 'On-premises']);
    expect(HOSTING.hosts.map((host) => host.glyph)).toEqual(['cloud', 'cloud', 'cloud', 'rack']);
    // The README names each cloud, as a secret manager, never as a host.
    for (const host of HOSTING.hosts.slice(0, 3)) expect(readme, host.name).toContain(host.name);
    const restricted = HOSTING.hosts.filter((host) => host.note);
    expect(restricted.map((host) => host.name)).toEqual(['On-premises']);
    expect(targets).toContain('| T6 | Restricted or air-gapped installation |');
    expect(restricted[0].note).toBe('Restricted or air-gapped');
    for (const text of [HOSTING.caption, ...HOSTING.hosts.flatMap((host) => [host.name, host.note ?? ''])]) {
      expect(text, text).not.toMatch(/\b(tested|supported|certified|qualified|official)\b/i);
    }
    expect(CHAPTERS.find((chapter) => chapter.id === 'server')?.status).toMatchObject({ badge: 'Qualification open', target: 'T3 one production server' });
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

  it('has eleven chapters in five acts, with alternating sides', () => {
    expect(chapters).toHaveLength(11);
    expect([...new Set(chapters.map((chapter) => chapter.act))]).toEqual([
      'client',
      'gateway',
      'request',
      'operate',
      'deploy',
    ]);
    expect(acts.map((act) => act.id)).toEqual(['client', 'gateway', 'request', 'operate', 'deploy']);
    expect(chapters.filter((chapter) => chapter.act === 'deploy').map((chapter) => chapter.id)).toEqual([
      'server',
      'scale-out',
      'laptop',
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
    // card. The last chapter's copy and install tabs cover the world on a
    // phone.
    const legible = 0.35;
    chapters.forEach((chapter, index) => {
      if (chapter.id === 'laptop') return;
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

  it('alternates paper and night by act and ends on the gold wash', () => {
    const at = (id: string) => chapterStop(chapters.findIndex((chapter) => chapter.id === id));
    expect(acts.map((act) => ACT_GROUNDS[act.id])).toEqual(['paper', 'night', 'paper', 'night', 'wash']);
    const paper = [250, 250, 250];
    const nightGround = [10, 11, 12];
    const expected: Record<string, number[]> = {
      sdk: paper,
      surfaces: paper,
      credentials: nightGround,
      catalog: nightGround,
      'first-request': paper,
      console: nightGround,
      enterprise: nightGround,
      storage: nightGround,
      server: [240, 178, 62],
      'scale-out': [240, 178, 62],
      laptop: [240, 178, 62],
    };
    for (const chapter of chapters) {
      const palette = paletteFor(at(chapter.id));
      expect(palette.background, chapter.id).toEqual(expected[chapter.id]);
      // The ink steps with the ground, so no frame reads ink on its own tone.
      expect(palette.ink, chapter.id).toEqual(
        palette.washMix === 1 ? [26, 18, 4] : palette.paperMix === 1 ? [24, 24, 27] : [246, 247, 248],
      );
    }
    // The stage is paper from its first frame; the hero keeps the night.
    expect(paletteFor(0).background).toEqual(paper);
    expect(paletteFor(1).background).toEqual([240, 178, 62]);
    expect(night().background).toEqual(nightGround);
    expect(night().ink).toEqual([246, 247, 248]);
  });

  it('turns the ground once per act gap, mid-gap, with no copy on stage', () => {
    expect(groundCrossings.map((crossing) => [crossing.after, crossing.from, crossing.to])).toEqual([
      ['surfaces', 'paper', 'night'],
      ['catalog', 'night', 'paper'],
      ['first-request', 'paper', 'night'],
      ['storage', 'night', 'wash'],
    ]);
    const groundOf = (progress: number) => {
      const palette = paletteFor(progress);
      return palette.washMix === 1 ? 'wash' : palette.paperMix === 1 ? 'paper' : palette.paperMix === 0 && palette.washMix === 0 ? 'night' : 'turning';
    };
    for (const crossing of groundCrossings) {
      const index = chapters.findIndex((chapter) => chapter.id === crossing.after);
      const before = chapters[index];
      const after = chapters[index + 1];
      const gap = after.start - before.end;
      expect(groundOf(before.end), crossing.after).toBe(crossing.from);
      expect(groundOf(after.start), crossing.after).toBe(crossing.to);
      for (let step = 0; step <= 200; step += 1) {
        const progress = before.end + (gap * step) / 200;
        if (groundOf(progress) !== 'turning') continue;
        // The middle third of the gap.
        expect(progress, crossing.after).toBeGreaterThanOrEqual(before.end + gap / 3);
        expect(progress, crossing.after).toBeLessThanOrEqual(after.start - gap / 3);
        for (const chapter of chapters) {
          expect(visibilityWindow(progress, chapter.start, chapter.end, COPY_EDGE), `${chapter.id} at ${progress}`).toBe(0);
        }
      }
    }
    // Inside a chapter window the ground holds.
    for (const chapter of chapters) {
      const grounds = new Set<string>();
      for (let step = 0; step <= 50; step += 1) grounds.add(groundOf(chapter.start + ((chapter.end - chapter.start) * step) / 50));
      expect([...grounds], chapter.id).toEqual([ACT_GROUNDS[chapter.act]]);
    }
  });

  it('gives every chapter a storyboard still, in chapter order', () => {
    for (const chapter of chapters) expect(stillAspect(chapter.id), chapter.id).not.toBe(stillAspect('none'));
    const markup = renderToStaticMarkup(
      createElement(Journey, { poster: { width: 1280, height: 800 }, build: { release: 'v0.0.0', starmap: 'v0.0.0' } }),
    );
    const order = [...markup.matchAll(/<article id="chapter-([a-z-]+)"/g)].map((match) => match[1]);
    expect(order).toEqual(chapters.map((chapter) => chapter.id));
  });

  it('moves the request through its states in order', () => {
    expect(requestStates.map((state) => state.label)).toEqual(Object.values(REQUEST_STATES));
    for (let index = 1; index < requestStates.length; index += 1) {
      expect(requestStates[index].at).toBeGreaterThan(requestStates[index - 1].at);
    }
  });
});

describe('mascot', () => {
  const render = (props: MascotProps = {}) => renderToStaticMarkup(createElement(Mascot, props));
  const parts = (markup: string) => [...markup.matchAll(/data-part="([a-z]+)"/g)].map((m) => m[1]);
  const viewBox = (markup: string) => /viewBox="([^"]+)"/.exec(markup)?.[1];

  // The fitted box closes in on the body; the reserved one keeps the notch
  // the accessories draw in. Both numbers are the contract the favicon and
  // the brand slot size against.
  const FITTED = '8 2 104 100';
  const RESERVED = '0 0 120 104';

  it('draws the body and a face in every state, with the accessory its state owns', () => {
    const expected: Record<MascotState, string[]> = {
      idle: ['body', 'eyes', 'mouth'],
      wink: ['body', 'eyes', 'mouth'],
      working: ['body', 'eyes', 'mouth', 'thinking'],
      error: ['body', 'eyes', 'mouth', 'drop'],
      empty: ['body', 'eyes', 'mouth', 'sleep'],
      celebrate: ['body', 'eyes', 'mouth', 'sparks'],
    };
    for (const [state, want] of Object.entries(expected) as [MascotState, string[]][]) {
      const markup = render({ state });
      expect(markup).toContain(`data-state="${state}"`);
      expect(parts(markup)).toEqual(want);
    }
  });

  it('colours the body with the mark and the face with its ink', () => {
    const markup = render();
    expect(markup).toContain('fill="var(--mark)"');
    expect(markup).toContain('fill="var(--mark-ink)"');
  });

  it('blinks and winks at rest, and only blinks while working', () => {
    const idle = render();
    expect(idle).toContain('class="mascot-blink"');
    expect(idle).toContain('class="mascot-wink-open"');
    expect(idle).toContain('class="mascot-wink-shut"');

    const working = render({ state: 'working' });
    expect(working).toContain('class="mascot-blink"');
    expect(working).not.toContain('mascot-wink');

    for (const state of ['wink', 'error', 'empty', 'celebrate'] as const) {
      expect(render({ state })).not.toContain('mascot-');
    }
  });

  it('fits the box to the body for a face-only state', () => {
    expect(viewBox(render())).toBe(FITTED);
    expect(viewBox(render({ state: 'wink' }))).toBe(FITTED);
  });

  it('reserves the accessory room for a state that draws one, or on request', () => {
    for (const state of ['working', 'error', 'empty', 'celebrate'] as const) {
      expect(viewBox(render({ state }))).toBe(RESERVED);
    }
    expect(viewBox(render({ reserveAccessories: true }))).toBe(RESERVED);
    expect(viewBox(render({ state: 'wink', reserveAccessories: true }))).toBe(RESERVED);
  });

  it('is an image when titled and hidden from the tree when not', () => {
    const titled = render({ title: 'Starport' });
    expect(titled).toContain('role="img"');
    expect(titled).toContain('aria-label="Starport"');
    expect(titled).not.toContain('aria-hidden');

    const plain = render();
    expect(plain).toContain('aria-hidden="true"');
    expect(plain).not.toContain('role=');
    expect(plain).not.toContain('aria-label');
  });

  it('passes the slot its class and thickens the face when small', () => {
    const markup = render({ className: 'rail-mark', small: true });
    expect(markup).toContain('class="rail-mark"');
    expect(markup).toContain('stroke-width="5.5"');
    expect(render()).toContain('stroke-width="4"');
  });
});
