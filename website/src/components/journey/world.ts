// The world owns one continuous scene: a request leaves an app through its
// OpenAI or OpenRouter client, enters the Starport listener, passes the
// gateway key check, resolves its model in the catalog generation, takes a
// route to a provider, and streams back to the app as server-sent events.
// Beside the route sit the catalog source, the durable state, the console,
// the enterprise controls, and the host that runs the one binary. Every part
// is drawn from `progress` (0..1), so the scrolling stage, the static
// storyboard, and the hero still share one source of truth.

import { CHAPTERS, WORLD_LABELS, WORLD_PARTS, WORLD_PROVIDERS, type WorldPartId } from '@/lib/splash-facts';
import { type Viewport, chapterIndexFor, chapters, clamp, interpolateCamera, mix, monotoneCubic, range, requestStateFor, smoothstep, visibilityWindow } from './timeline';
import { type FrameOptions, type Rgb, Scene, drawBackdrop, mixRgb, paletteFor, rgba } from './scene';

export { type FrameOptions, type Palette, type Rgb, paletteFor, resolveFonts, rgb, setFonts } from './scene';

function fact(id: WorldPartId) {
  const part = WORLD_PARTS.find((entry) => entry.id === id);
  if (!part) throw new Error(`no world part ${id}`);
  return part;
}

function chapterFacts(id: string) {
  const chapter = CHAPTERS.find((entry) => entry.id === id);
  if (!chapter) throw new Error(`no chapter ${id}`);
  return chapter;
}

function chapterIndex(id: string) {
  const index = chapters.findIndex((chapter) => chapter.id === id);
  if (index < 0) throw new Error(`no chapter ${id}`);
  return index;
}

// The two doors of the listener come from the README table of client
// contracts: the family and the path of each base URL.
const surfaces = chapterFacts('surfaces').visuals[0];
const doors = (surfaces.kind === 'table' ? surfaces.rows : []).map(([family, url]) => ({ family, path: new URL(url).pathname }));
if (doors.length !== 2) throw new Error('the surfaces chapter must name two client contracts');

// The provider column, top to bottom. The request goes to OpenAI, so its card
// is the lowest one and the stream leaves it straight down.
const providers = [...WORLD_PROVIDERS.filter((name) => name !== 'OpenAI'), 'OpenAI'];
const REQUEST_PROVIDER = providers.length - 1;

const credentialChips = chapterFacts('credentials').chips;
const lifetimeChips = chapterFacts('lifetime').chips;
const storageChips = chapterFacts('storage').chips;
const storageStatus = chapterFacts('storage').status;

// World geometry, in world units. The route runs along y = 0 from the app on
// the left to the providers on the right; y grows downwards.
const APP = { x: 0, y: 0, w: 280, h: 236 };
const DOOR = { x: 520, w: 200, h: 76, gap: 62 };
const KEYS = { x: 880, y: 0, w: 260, h: 236 };
const CATALOG = { x: 1220, y: 0, w: 280, h: 236 };
const PLANNING = { x: 1570, y: 0, w: 280, h: 236 };
// The cards are wide enough that the longest name keeps its title at the
// hero still's zoom, where the title font sits on its screen floor.
const PROVIDER = { x: 2070, w: 360, h: 68, ys: [-123, -41, 41, 123] };
// The one binary frames the listener, the key check, the catalog, the route
// planning, and the enterprise controls.
const BINARY = { x0: 380, x1: 1760, y0: -420, y1: 330 };
const CONTROLS = { x: 880, y: -275, w: 340, h: 190 };
// Beside the route: the catalog source and the console above the binary,
// the durable state below it, and the host around the binary and its state.
const SOURCE = { x: 1220, y: -640, w: 340, h: 120 };
const CONSOLE = { x: 590, y: -650, w: 340, h: 200 };
const STATE = { x: 1000, y: 640, w: 920, h: 340 };
const HOST = { x0: 330, x1: 1810, y0: -470, y1: 870 };
// The stream back: down from the provider, along the floor of the binary,
// and up into the app.
const STREAM_Y = 270;
const streamPath = [
  { x: PROVIDER.x, y: PROVIDER.ys[REQUEST_PROVIDER] + PROVIDER.h / 2 },
  { x: PROVIDER.x, y: STREAM_Y },
  { x: APP.x, y: STREAM_Y },
  { x: APP.x, y: APP.y + APP.h / 2 },
];
const CHUNK_XS = [1700, 1380, 1060];
const LAST_CHUNK_X = 700;

type Box = { x: number; y: number; w: number; h: number };
const left = (box: Box) => box.x - box.w / 2;
const right = (box: Box) => box.x + box.w / 2;
const top = (box: Box) => box.y - box.h / 2;
const bottom = (box: Box) => box.y + box.h / 2;
const doorY = (index: number) => (index === 0 ? -DOOR.gap : DOOR.gap);

// Where the request rests in each chapter.
const REST = {
  app: { x: right(APP) + 40, y: 0 },
  door: { x: DOOR.x + DOOR.w / 2, y: -DOOR.gap },
  keys: { x: left(KEYS), y: 0 },
  catalog: { x: left(CATALOG), y: 0 },
  done: { x: APP.x, y: 165 },
  state: { x: STATE.x, y: top(STATE) },
  console: { x: CONSOLE.x, y: -480 },
  controls: { x: CONTROLS.x, y: -150 },
};

// ---------------------------------------------------------------------------
// Chapter weights: how much of each chapter's scene is on stage. A chapter
// holds full weight through its window and crossfades with its neighbour in
// the gap between them, so the weights always sum to one.
// ---------------------------------------------------------------------------

const GAP = chapters[1].start - chapters[0].end;

function weights(progress: number) {
  return chapters.map((chapter, index) =>
    visibilityWindow(progress, index === 0 ? -1 : chapter.start - GAP, index === chapters.length - 1 ? 2 : chapter.end + GAP, GAP),
  );
}

function share(id: string, offset: number) {
  const chapter = chapters[chapterIndex(id)];
  return chapter.start + offset * (chapter.end - chapter.start);
}

// The parts each chapter is about. The other parts on stage step back so
// the chapter's diagram reads on its own.
type Key = WorldPartId | 'source' | 'binary';
const FOCUS: Record<string, Key[]> = {
  sdk: ['app', 'listener'],
  surfaces: ['app', 'listener', 'binary'],
  credentials: ['app', 'listener', 'keys', 'catalog', 'planning', 'providers', 'source', 'binary'],
  catalog: ['catalog', 'source', 'planning'],
  'first-request': ['app', 'listener', 'keys', 'catalog', 'planning', 'providers', 'stream', 'binary'],
  lifetime: ['state', 'binary'],
  console: ['console', 'listener'],
  storage: ['state', 'binary'],
  enterprise: ['controls', 'keys', 'binary'],
  deploy: ['app', 'listener', 'keys', 'catalog', 'planning', 'providers', 'stream', 'state', 'console', 'controls', 'source', 'host', 'binary'],
};
const DIM = 0.3;

// The chapter that first draws each part beside the route. The route itself
// is on stage from the start.
const INTRO: Partial<Record<Key, string>> = {
  source: 'credentials',
  stream: 'first-request',
  state: 'lifetime',
  console: 'console',
  controls: 'enterprise',
  host: 'deploy',
};

type World = { scene: Scene; progress: number; weights: number[] };

function weightOf(world: World, ...ids: string[]) {
  return ids.reduce((sum, id) => sum + world.weights[chapterIndex(id)], 0);
}

function alphaOf(world: World, key: Key) {
  const intro = INTRO[key];
  const shown = intro ? smoothstep(range(world.progress, chapters[chapterIndex(intro)].start - GAP, chapters[chapterIndex(intro)].start)) : 1;
  if (shown <= 0.01) return 0;
  const focus = chapters.reduce((sum, chapter, index) => sum + world.weights[index] * (FOCUS[chapter.id].includes(key) ? 1 : DIM), 0);
  return shown * focus;
}

// ---------------------------------------------------------------------------
// Card grammar: a panel, a title that leaves when the camera is far out or
// when it would not fit, a label and a rule that fade with the detail level,
// and the chips under them.
// ---------------------------------------------------------------------------

// The share of its size that a title may give up to fit its card. At a far
// zoom a title sits on its screen floor, so this lets it go a little under
// the floor before the card drops it.
const TITLE_SHRINK = 0.82;

// Sets the title font, shrunk to fit when it must, and says whether the
// title fits at all.
function titleFits(scene: Scene, value: string, room: number) {
  scene.sans(13, 600);
  const width = scene.measure(value);
  if (width <= room) return true;
  if (room / width < TITLE_SHRINK) return false;
  scene.sans(13, 600, room / width);
  return true;
}

// Draws the card head and returns the y where its body starts.
function cardHead(scene: Scene, box: Box, alpha: number, title: string, label: string) {
  const { palette } = scene;
  const x = left(box) + 20;
  const titleY = top(box) + Math.max(26, scene.fontSize(13) * 0.6 + 10);
  if (titleFits(scene, title, box.w - 40)) scene.text(title, x, titleY, palette.ink, alpha * scene.far);
  const labelY = titleY + Math.max(20, scene.fontSize(13) * 0.55 + scene.fontSize(11) * 0.55 + 4);
  scene.sans(11.5);
  scene.text(label, x, labelY, palette.muted, alpha * scene.detail);
  const ruleY = labelY + Math.max(16, scene.fontSize(11) * 0.6 + 8);
  scene.line(left(box), ruleY, right(box), ruleY, palette.ink, alpha * scene.detail * 0.12);
  return ruleY;
}

// A column of chips under a card head.
function chipColumn(scene: Scene, labels: string[], x: number, startY: number, alpha: number, lit: (index: number) => number, color: (index: number) => Rgb, step = 34) {
  const spacing = Math.max(step, scene.px(30));
  labels.forEach((label, index) => scene.chip(label, x, startY + 22 + index * spacing, alpha, lit(index), color(index)));
}

// ---------------------------------------------------------------------------
// The parts
// ---------------------------------------------------------------------------

// The host and the binary are frames, drawn first so every card sits on
// them. The host names the targets under its floor; the binary names itself
// in its top-right corner, clear of the console and catalog lanes.
function drawFrames(world: World) {
  const { scene } = world;
  const { ctx, palette } = scene;

  const host = alphaOf(world, 'host');
  if (host > 0.01) {
    scene.roundRect(HOST.x0, HOST.y0, HOST.x1 - HOST.x0, HOST.y1 - HOST.y0, 24);
    ctx.lineWidth = scene.px(1);
    ctx.setLineDash([scene.px(6), scene.px(6)]);
    ctx.strokeStyle = rgba(palette.ink, host * 0.3);
    ctx.stroke();
    ctx.setLineDash([]);
    const part = fact('host');
    const rowY = HOST.y1 + scene.px(30);
    scene.sans(13, 600);
    scene.text(part.title, HOST.x0, rowY, palette.ink, host * scene.far);
    let x = HOST.x0 + scene.measure(part.title) + scene.px(16);
    for (const detail of part.details) x += scene.chip(detail, x, rowY, host * scene.far, 0.4, palette.ink) + scene.px(10);
  }

  const binary = alphaOf(world, 'binary');
  if (binary > 0.01) {
    scene.roundRect(BINARY.x0, BINARY.y0, BINARY.x1 - BINARY.x0, BINARY.y1 - BINARY.y0, 18);
    ctx.fillStyle = rgba(mixRgb(palette.background, palette.ink, 0.025), binary);
    ctx.fill();
    ctx.lineWidth = scene.px(1);
    ctx.strokeStyle = rgba(palette.ink, binary * 0.2);
    ctx.stroke();
    scene.sans(12, 600);
    scene.text(WORLD_LABELS.binary, BINARY.x1 - scene.px(16), BINARY.y0 + scene.px(20), palette.muted, binary * scene.far, 'right');
  }
}

// The app the developer already has: one card with the client call the
// request leaves through.
function drawApp(world: World) {
  const { scene } = world;
  const alpha = alphaOf(world, 'app');
  if (alpha <= 0.01 || !scene.inView(left(APP), right(APP))) return;
  const { palette } = scene;
  const part = fact('app');
  const here = weightOf(world, 'sdk');
  scene.panel(APP.x, APP.y, APP.w, APP.h, alpha, 0.2 + here * 0.4, palette.accent);
  const body = cardHead(scene, APP, alpha, part.title, part.label);
  const last = part.details.length - 1;
  chipColumn(
    scene,
    part.details,
    left(APP) + 20,
    body,
    alpha * scene.detail,
    (index) => (index === last ? 1 : 0.2),
    (index) => (index === last ? palette.accent : palette.ink),
    36,
  );
}

// The listener: two doors into one binary, and the lanes that reach them
// from the app and leave them for the key check. The request takes the
// OpenAI door; the OpenRouter door carries its own traffic.
function drawListener(world: World) {
  const { scene } = world;
  const alpha = alphaOf(world, 'listener');
  if (alpha <= 0.01 || !scene.inView(right(APP), left(KEYS))) return;
  const { palette } = scene;
  const appAlpha = Math.min(alpha, alphaOf(world, 'app'));
  const keysAlpha = Math.min(alpha, alphaOf(world, 'keys'));
  const here = weightOf(world, 'surfaces', 'console', 'deploy');

  doors.forEach((door, index) => {
    const y = doorY(index);
    const request = index === 0;
    const color = request ? palette.accent : palette.muted;
    scene.lane(right(APP), 0, DOOR.x - DOOR.w / 2, y, color, appAlpha * (request ? 0.7 : 0.4), request ? 1.4 : 1);
    scene.laneTraffic(right(APP), 0, DOOR.x - DOOR.w / 2, y, color, appAlpha * 0.9, { speed: 1.6 + index * 0.3, phase: index * 0.37 });
    scene.lane(DOOR.x + DOOR.w / 2, y, left(KEYS), 0, color, keysAlpha * (request ? 0.7 : 0.4), request ? 1.4 : 1);
    scene.laneTraffic(DOOR.x + DOOR.w / 2, y, left(KEYS), 0, color, keysAlpha * 0.9, { speed: 1.8 + index * 0.3, phase: index * 0.41 });

    scene.panel(DOOR.x, y, DOOR.w, DOOR.h, alpha, request ? 0.25 + here * 0.5 : 0.15, request ? palette.accent : undefined);
    const x = DOOR.x - DOOR.w / 2 + 18;
    scene.mono(12, 600);
    scene.text(door.path, x, y - (scene.detail > 0.01 ? 12 : 0), palette.ink, alpha * scene.far);
    scene.sans(11.5);
    scene.text(door.family, x, y + 13, palette.muted, alpha * scene.detail);
  });

  const part = fact('listener');
  const titleY = DOOR.gap + DOOR.h / 2 + scene.px(22);
  if (titleFits(scene, part.title, 400)) scene.text(part.title, DOOR.x - DOOR.w / 2, titleY, palette.ink, alpha * scene.far);
  scene.sans(11.5);
  scene.text(part.label, DOOR.x - DOOR.w / 2, titleY + scene.px(20), palette.muted, alpha * scene.detail);
}

// The gateway key check: the scopes and the limits that a gateway API key
// carries. The enterprise controls above it feed its budgets and limits.
function drawKeys(world: World) {
  const { scene } = world;
  const alpha = alphaOf(world, 'keys');
  if (alpha <= 0.01 || !scene.inView(left(KEYS), right(KEYS), 120, -400, 200)) return;
  const { palette } = scene;
  const part = fact('keys');
  const here = weightOf(world, 'credentials');
  scene.panel(KEYS.x, KEYS.y, KEYS.w, KEYS.h, alpha, 0.2 + here * 0.4, palette.accent);
  const body = cardHead(scene, KEYS, alpha, part.title, part.label);
  const scope = world.progress >= chapters[chapterIndex('credentials')].start - GAP ? 1 : 0.2;
  chipColumn(
    scene,
    part.details,
    left(KEYS) + 20,
    body - 4,
    alpha * scene.detail,
    (index) => (index === 0 ? scope : 0.2),
    (index) => (index === 0 ? palette.accent : palette.ink),
  );
  // The request rides the accent lane on to the catalog.
  const next = Math.min(alpha, alphaOf(world, 'catalog'));
  scene.line(right(KEYS), 0, left(CATALOG), 0, palette.accent, next * 0.7, 1.4);
  scene.lineTraffic(right(KEYS), 0, left(CATALOG), 0, palette.accent, next * 0.9, { speed: 2.4 });
}

// The catalog generation: a source supplies a candidate, Starport validates
// it and accepts it as the head. The catalog chapter steps through the three
// states under the scroll; elsewhere the head is the live generation.
function drawCatalog(world: World) {
  const { scene, progress } = world;
  const alpha = alphaOf(world, 'catalog');
  if (alpha <= 0.01 || !scene.inView(left(CATALOG), right(CATALOG), 120, -800, 200)) return;
  const { palette } = scene;
  const part = fact('catalog');
  const here = weightOf(world, 'catalog');
  scene.panel(CATALOG.x, CATALOG.y, CATALOG.w, CATALOG.h, alpha, 0.2 + here * 0.4, palette.accent);
  const body = cardHead(scene, CATALOG, alpha, part.title, part.label);

  // The pipeline lights one state at a time while the catalog chapter plays,
  // and rests on the head everywhere else.
  const chapter = chapters[chapterIndex('catalog')];
  const step = progress < chapter.start || progress > chapter.end ? 2 : clamp(Math.floor(range(progress, chapter.start + 0.012, chapter.end - 0.03) * 2.999), 0, 2);
  const x = left(CATALOG) + 20;
  const spacing = Math.max(36, scene.px(32));
  const detail = alpha * scene.detail;
  part.details.forEach((label, index) => {
    const y = body + 22 + index * spacing;
    scene.chip(label, x, y, detail, index === step ? 1 : 0.15, index === step ? palette.ink : palette.muted);
    if (index > 0) {
      const from = body + 22 + (index - 1) * spacing + scene.px(11);
      scene.line(x + scene.px(18), from, x + scene.px(18), y - scene.px(11), palette.ink, detail * 0.3);
    }
  });

  // The route goes on to planning.
  const next = Math.min(alpha, alphaOf(world, 'planning'));
  scene.line(right(CATALOG), 0, left(PLANNING), 0, palette.accent, next * 0.7, 1.4);
  scene.lineTraffic(right(CATALOG), 0, left(PLANNING), 0, palette.accent, next * 0.9, { speed: 2.4, phase: 0.3 });
}

// The catalog source above the binary, and the lane that brings a candidate
// generation down into the catalog. The catalog-acquisition credential reads
// it; that credential never pays a provider.
function drawSource(world: World) {
  const { scene } = world;
  const alpha = alphaOf(world, 'source');
  if (alpha <= 0.01 || !scene.inView(left(SOURCE), right(SOURCE), 120, -800, 0)) return;
  const { palette } = scene;
  scene.line(SOURCE.x, bottom(SOURCE), CATALOG.x, top(CATALOG), palette.muted, alpha * 0.5);
  scene.lineTraffic(SOURCE.x, bottom(SOURCE), CATALOG.x, top(CATALOG), palette.ink, alpha * 0.5, { speed: 1.2 });
  scene.panel(SOURCE.x, SOURCE.y, SOURCE.w, SOURCE.h, alpha, 0.12);
  const titleY = top(SOURCE) + Math.max(28, scene.fontSize(13) * 0.6 + 12);
  if (titleFits(scene, WORLD_LABELS.source, SOURCE.w - 40)) scene.text(WORLD_LABELS.source, left(SOURCE) + 20, titleY, palette.ink, alpha * scene.far);
  scene.chip('`STARPORT_CATALOG_SOURCE_API_KEY`', left(SOURCE) + 20, titleY + Math.max(42, scene.px(30)), alpha * scene.detail, 0.2, palette.ink);
}

// Route planning: the offerings the generation names, each with one verdict.
// The request's offering is routable; the rows under it stand for the rest.
function drawPlanning(world: World) {
  const { scene } = world;
  const alpha = alphaOf(world, 'planning');
  if (alpha <= 0.01 || !scene.inView(left(PLANNING), PROVIDER.x + PROVIDER.w / 2)) return;
  const { ctx, palette } = scene;
  const part = fact('planning');
  const here = weightOf(world, 'first-request');
  scene.panel(PLANNING.x, PLANNING.y, PLANNING.w, PLANNING.h, alpha, 0.2 + here * 0.4, palette.accent);
  const body = cardHead(scene, PLANNING, alpha, part.title, part.label);
  const [model, routable, unroutable] = part.details;
  const x = left(PLANNING) + 20;
  const detail = alpha * scene.detail;
  const rowY = body + Math.max(22, scene.px(18));
  scene.face(model, 11, 500);
  scene.text(model.replace(/`/g, ''), x, rowY, palette.ink, detail);
  scene.chip(routable, x, rowY + Math.max(28, scene.px(26)), detail, 1, palette.accent);
  // Placeholder rows: a bar for an offering the drawing does not name.
  [unroutable, routable].forEach((verdict, index) => {
    const y = rowY + Math.max(72, scene.px(64)) + index * Math.max(34, scene.px(30));
    ctx.fillStyle = rgba(palette.ink, detail * 0.16);
    scene.roundRect(x, y - 4, 80, 8, 4);
    ctx.fill();
    scene.chip(verdict, x + 92, y, detail, 0.1, palette.muted);
  });

  // Lanes to every provider; the request's lane is the bright one.
  const lanes = Math.min(alpha, alphaOf(world, 'providers'));
  providers.forEach((_, index) => {
    const request = index === REQUEST_PROVIDER;
    const color = request ? palette.accent : palette.muted;
    const x1 = PROVIDER.x - PROVIDER.w / 2;
    scene.lane(right(PLANNING), 0, x1, PROVIDER.ys[index], color, lanes * (request ? 0.7 : 0.35), request ? 1.4 : 1);
    scene.laneTraffic(right(PLANNING), 0, x1, PROVIDER.ys[index], color, lanes * (request ? 0.9 : 0.6), { count: 1, speed: 2 + index * 0.3, phase: index * 0.23 });
  });
}

// The providers: one card each, each paid by its own provider inference
// credential. OpenAI is the request's provider.
function drawProviders(world: World) {
  const { scene } = world;
  const alpha = alphaOf(world, 'providers');
  if (alpha <= 0.01 || !scene.inView(PROVIDER.x - PROVIDER.w / 2, PROVIDER.x + PROVIDER.w / 2)) return;
  const { palette } = scene;
  const part = fact('providers');
  const x = PROVIDER.x - PROVIDER.w / 2;
  const columnTop = PROVIDER.ys[0] - PROVIDER.h / 2;
  const titleY = columnTop - scene.px(22);
  scene.sans(13, 600);
  scene.text(part.title, x, titleY, palette.ink, alpha * scene.far);
  scene.sans(11.5);
  scene.text(part.label, x, titleY - scene.px(20), palette.muted, alpha * scene.detail);
  const reached = smoothstep(range(world.progress, share('first-request', 0.12), share('first-request', 0.2)));
  providers.forEach((name, index) => {
    const y = PROVIDER.ys[index];
    const request = index === REQUEST_PROVIDER;
    scene.panel(PROVIDER.x, y, PROVIDER.w, PROVIDER.h, alpha, request ? 0.2 + reached * 0.5 : 0.1, request ? palette.accent : undefined);
    const nameY = request && scene.detail > 0.01 ? y - 11 : y;
    if (titleFits(scene, name, PROVIDER.w - 32)) scene.text(name, x + 16, nameY, palette.ink, alpha * scene.far);
    if (request) {
      scene.face(part.details[0], 10.5, 500);
      scene.text(part.details[0].replace(/`/g, ''), x + 16, y + 13, palette.muted, alpha * scene.detail);
    }
  });
}

// The stream back: server-sent events from the provider, along the floor of
// the binary, into the app. The chunks appear as the request streams and
// the last line closes the stream.
function drawStream(world: World) {
  const { scene, progress } = world;
  const alpha = alphaOf(world, 'stream');
  if (alpha <= 0.01) return;
  const { palette } = scene;
  const flowing = smoothstep(range(progress, share('first-request', 0.25), share('first-request', 0.32)));
  scene.path(streamPath, palette.accent, alpha * mix(0.25, 0.7, flowing), 1.4);
  scene.pathTraffic(streamPath, palette.accent, alpha * flowing, { count: 4, speed: 1.4 });
  scene.sans(11.5, 500);
  scene.text(WORLD_LABELS.stream, 1220, STREAM_Y - scene.px(26), palette.muted, alpha * scene.far, 'center');
  CHUNK_XS.forEach((x, index) => {
    const seen = smoothstep(range(progress, share('first-request', 0.3 + index * 0.05), share('first-request', 0.36 + index * 0.05)));
    scene.chip(WORLD_LABELS.chunk, x, STREAM_Y, alpha * seen, 0.5, palette.accent, 'center');
  });
  const closed = smoothstep(range(progress, share('first-request', 0.46), share('first-request', 0.52)));
  scene.chip(WORLD_LABELS.last, LAST_CHUNK_X, STREAM_Y, alpha * closed, 1, palette.accent, 'center');
}

// The enterprise controls inside the binary, above the key check they feed.
function drawControls(world: World) {
  const { scene } = world;
  const alpha = alphaOf(world, 'controls');
  if (alpha <= 0.01 || !scene.inView(left(CONTROLS), right(CONTROLS), 120, -600, 0)) return;
  const { palette } = scene;
  const part = fact('controls');
  const here = weightOf(world, 'enterprise');
  scene.line(CONTROLS.x, bottom(CONTROLS), KEYS.x, top(KEYS), palette.muted, alpha * 0.5);
  scene.panel(CONTROLS.x, CONTROLS.y, CONTROLS.w, CONTROLS.h, alpha, 0.15 + here * 0.3);
  const body = cardHead(scene, CONTROLS, alpha, part.title, part.label);
  const columnX = [left(CONTROLS) + 20, left(CONTROLS) + 180];
  const spacing = Math.max(34, scene.px(30));
  part.details.forEach((label, index) => {
    const x = columnX[index % 2];
    const y = body + 22 + Math.floor(index / 2) * spacing;
    scene.chip(label, x, y, alpha * scene.detail, 0.2 + here * 0.6, palette.ink);
  });
}

// The console above the listener: the one-time launch link opens it, and
// its lane lands on the OpenAI door, the same listener the clients use.
function drawConsole(world: World) {
  const { scene } = world;
  const alpha = alphaOf(world, 'console');
  if (alpha <= 0.01 || !scene.inView(left(CONSOLE), right(CONSOLE), 120, -800, 0)) return;
  const { palette } = scene;
  const part = fact('console');
  const here = weightOf(world, 'console');
  scene.line(CONSOLE.x, bottom(CONSOLE), CONSOLE.x, -DOOR.gap - DOOR.h / 2, palette.muted, alpha * 0.5);
  scene.lineTraffic(CONSOLE.x, -DOOR.gap - DOOR.h / 2, CONSOLE.x, bottom(CONSOLE), palette.ink, alpha * 0.45, { speed: 1.2 });
  scene.panel(CONSOLE.x, CONSOLE.y, CONSOLE.w, CONSOLE.h, alpha, 0.15 + here * 0.3);
  const body = cardHead(scene, CONSOLE, alpha, part.title, part.label);
  chipColumn(
    scene,
    part.details,
    left(CONSOLE) + 20,
    body - 4,
    alpha * scene.detail,
    (index) => (index === 0 ? 0.3 + here * 0.7 : 0.2),
    () => palette.ink,
    32,
  );
}

// Durable state under the binary: the three roles as stores, and beside
// them either the lifetime of a temporary gateway (in-memory stores, dashed)
// or the two storage recipes. A cache sits apart: it never holds durable
// state.
function drawState(world: World) {
  const { scene, progress } = world;
  const alpha = alphaOf(world, 'state');
  if (alpha <= 0.01 || !scene.inView(left(STATE), right(STATE), 120, 300, 900)) return;
  const { ctx, palette } = scene;
  const part = fact('state');
  // The lifetime chapter shows the temporary gateway; from the storage
  // chapter on, the stores are durable.
  const durable = smoothstep(range(progress, chapters[chapterIndex('storage')].start - GAP, chapters[chapterIndex('storage')].start));

  scene.line(STATE.x, BINARY.y1, STATE.x, top(STATE), palette.muted, alpha * 0.5);
  scene.lineTraffic(STATE.x, BINARY.y1, STATE.x, top(STATE), palette.ink, alpha * 0.45, { speed: 1.4 });
  scene.panel(STATE.x, STATE.y, STATE.w, STATE.h, alpha, 0.15);
  const body = cardHead(scene, STATE, alpha, part.title, part.label);

  // The three stores. In memory they are outlined in dashes.
  const storeY = body + 90;
  part.details.forEach((name, index) => {
    const x = left(STATE) + 80 + index * 140;
    ctx.setLineDash(durable < 0.5 ? [scene.px(5), scene.px(4)] : []);
    scene.cylinder(x, storeY, 110, 120, alpha * mix(0.75, 1, durable), 0.1 + durable * 0.2);
    ctx.setLineDash([]);
    scene.sans(12.5, 600);
    scene.text(name, x, scene.cylinderBodyTop(storeY, 110, 120) + 22, palette.ink, alpha * scene.far, 'center');
  });

  // Beside the stores: the lifetime of a temporary gateway, or the recipes.
  const columnX = left(STATE) + 470;
  const detail = alpha * scene.detail;
  const spacing = Math.max(36, scene.px(30));
  lifetimeChips.forEach((label, index) => {
    scene.chip(label, columnX, body + 30 + index * spacing, detail * (1 - durable), index === 2 ? 0.8 : 0.3, palette.ink);
  });
  const recipes = [
    { name: WORLD_LABELS.local, stores: storageChips[0], badge: '' },
    { name: WORLD_LABELS.shared, stores: storageChips[1], badge: storageStatus?.badge ?? '' },
  ];
  recipes.forEach((recipe, index) => {
    const y = body + 26 + index * Math.max(70, scene.px(58));
    scene.sans(11.5, 500);
    scene.text(recipe.name, columnX, y, palette.muted, detail * durable);
    if (recipe.badge) scene.chip(recipe.badge, right(STATE) - 24, y, detail * durable, 0.2, palette.muted, 'right');
    scene.chip(recipe.stores, columnX, y + Math.max(28, scene.px(24)), detail * durable, 0.4, palette.ink);
  });

  // The cache strip along the floor of the state.
  const stripTop = bottom(STATE) - 86;
  scene.roundRect(left(STATE) + 24, stripTop, STATE.w - 48, 62, 10);
  ctx.setLineDash([scene.px(4), scene.px(4)]);
  ctx.lineWidth = scene.px(1);
  ctx.strokeStyle = rgba(palette.ink, alpha * 0.25);
  ctx.stroke();
  ctx.setLineDash([]);
  const cacheY = stripTop + 31 - (scene.detail > 0.01 ? 10 : 0);
  scene.sans(12, 600);
  scene.text(WORLD_LABELS.cache, left(STATE) + 44, cacheY, palette.muted, alpha * scene.far);
  scene.sans(11.5);
  scene.text(WORLD_LABELS.cacheNote, left(STATE) + 44, cacheY + 21, palette.muted, detail);
}

// The credential roles: three tags, each beside the part its credential
// opens. They show while the credentials chapter is on stage.
function drawCredentialTags(world: World) {
  const { scene } = world;
  const alpha = weightOf(world, 'credentials') * scene.far;
  if (alpha <= 0.01) return;
  const { palette } = scene;
  const [gateway, provider, catalog] = credentialChips;
  scene.chip(gateway, APP.x, top(APP) - scene.px(24), alpha, 0.7, palette.accent, 'center');
  scene.chip(provider, PROVIDER.x + PROVIDER.w / 2, PROVIDER.ys[0] - PROVIDER.h / 2 - scene.px(56), alpha, 0.7, palette.accent, 'right');
  scene.chip(catalog, SOURCE.x + scene.px(12), (bottom(SOURCE) + BINARY.y0) / 2, alpha, 0.5, palette.ink);
}

// ---------------------------------------------------------------------------
// The request
// ---------------------------------------------------------------------------

type RoutePoint = { x: number; y: number; at: number };

function routePoints(): RoutePoint[] {
  const end = (id: string) => chapters[chapterIndex(id)].end;
  const start = (id: string) => chapters[chapterIndex(id)].start;
  const hold = (point: { x: number; y: number }, id: string): RoutePoint[] => [
    { ...point, at: id === 'sdk' ? 0 : start(id) },
    { ...point, at: id === 'deploy' ? 1 : end(id) },
  ];
  return [
    ...hold(REST.app, 'sdk'),
    // Out along the accent lane to the OpenAI door.
    { x: DOOR.x - DOOR.w / 2, y: -DOOR.gap, at: end('sdk') + GAP * 0.7 },
    ...hold(REST.door, 'surfaces'),
    ...hold(REST.keys, 'credentials'),
    ...hold(REST.catalog, 'catalog'),
    // The first request: planned, sent to OpenAI, streamed back along the
    // floor of the binary, and done at the app.
    { x: left(PLANNING), y: 0, at: start('first-request') },
    { x: right(PLANNING), y: 0, at: share('first-request', 0.1) },
    { x: PROVIDER.x - PROVIDER.w / 2, y: PROVIDER.ys[REQUEST_PROVIDER], at: share('first-request', 0.18) },
    { ...streamPath[0], at: share('first-request', 0.25) },
    { ...streamPath[1], at: share('first-request', 0.3) },
    { ...streamPath[2], at: share('first-request', 0.52) },
    { ...REST.done, at: share('first-request', 0.6) },
    { ...REST.done, at: end('first-request') },
    ...hold(REST.state, 'lifetime'),
    ...hold(REST.console, 'console'),
    ...hold(REST.state, 'storage'),
    ...hold(REST.controls, 'enterprise'),
    ...hold(REST.door, 'deploy'),
  ];
}

type Route = { times: number[]; xs: number[]; ys: number[] };
const ROUTE_POINTS = routePoints();
const ROUTE: Route = { times: ROUTE_POINTS.map((p) => p.at), xs: ROUTE_POINTS.map((p) => p.x), ys: ROUTE_POINTS.map((p) => p.y) };

export function requestRoute() {
  return ROUTE_POINTS;
}

function pointOnRoute(progress: number) {
  return { x: monotoneCubic(ROUTE.times, ROUTE.xs, progress), y: monotoneCubic(ROUTE.times, ROUTE.ys, progress) };
}

// Where the state pill sits at each rest, so it covers no card label: a world
// offset from the request, a screen offset in pixels, and the pill's
// alignment on that point. Between rests the pill rides under the request.
type Spot = { id: string; from?: number; dx?: number; dy?: number; sx?: number; sy?: number; align: 'left' | 'center' | 'right'; hide?: boolean };
const SPOTS: Spot[] = [
  { id: 'sdk', dx: APP.x - REST.app.x, dy: top(APP), sy: -22, align: 'center' },
  { id: 'surfaces', dx: -DOOR.w / 2, dy: -DOOR.h / 2, sy: -22, align: 'center' },
  { id: 'credentials', dx: KEYS.x - REST.keys.x, dy: bottom(KEYS), sy: 24, align: 'center' },
  { id: 'catalog', dx: CATALOG.x - REST.catalog.x, dy: bottom(CATALOG), sy: 24, align: 'center' },
  { id: 'first-request', from: 0.6, sx: 16, align: 'left' },
  { id: 'lifetime', sx: 16, sy: -28, align: 'left' },
  { id: 'console', sx: 16, align: 'left' },
  { id: 'storage', sx: 16, sy: -28, align: 'left' },
  { id: 'enterprise', sx: 16, align: 'left' },
  // The last chapter frames the whole world; the status rail carries the
  // state there, and the hero still shows the request alone.
  { id: 'deploy', align: 'center', hide: true },
];

function drawRequest(world: World, time: number, ambient: boolean) {
  const { scene, progress } = world;
  const point = pointOnRoute(progress);
  if (!scene.inView(point.x, point.x, 200, point.y, point.y)) return;
  const { ctx, palette } = scene;
  const pulse = ambient ? 0.5 + 0.5 * Math.sin(time * 0.004) : 0.5;
  const size = scene.px(7) + scene.px(1.2) * pulse;

  // Trail: where the request was a moment ago. Faster travel leaves a longer
  // trail, so a hold reads as rest and a move reads as motion.
  for (let index = 1; index <= 14; index += 1) {
    const back = pointOnRoute(progress - index * 0.006);
    const fade = 1 - index / 15;
    ctx.beginPath();
    ctx.arc(back.x, back.y, scene.px(2.4) * fade, 0, Math.PI * 2);
    ctx.fillStyle = rgba(palette.accent, fade * 0.45);
    ctx.fill();
  }

  const glow = ctx.createRadialGradient(point.x, point.y, 0, point.x, point.y, scene.px(44));
  glow.addColorStop(0, rgba(palette.accent, 0.45));
  glow.addColorStop(1, rgba(palette.accent, 0));
  ctx.fillStyle = glow;
  ctx.fillRect(point.x - scene.px(44), point.y - scene.px(44), scene.px(88), scene.px(88));

  ctx.save();
  ctx.translate(point.x, point.y);
  ctx.rotate(Math.PI / 4);
  ctx.fillStyle = rgba(palette.accent, 1);
  ctx.fillRect(-size, -size, size * 2, size * 2);
  ctx.strokeStyle = rgba(palette.background, 1);
  ctx.lineWidth = scene.px(1.5);
  ctx.strokeRect(-size, -size, size * 2, size * 2);
  ctx.restore();

  // The state pill. Each rest names its spot; between rests the pill rides
  // under the request. It fades where the request leaves the frame.
  const label = requestStateFor(progress).replace(/`/g, '');
  scene.mono(10.5, 600);
  const padX = scene.px(8);
  const pillW = scene.measure(label) + padX * 2;
  const pillH = scene.px(20);
  let spotWeight = 0;
  let hidden = 0;
  let pillLeft = 0;
  let pillY = 0;
  for (const spot of SPOTS) {
    const chapter = chapters[chapterIndex(spot.id)];
    const from = spot.from === undefined ? (spot.id === 'sdk' ? -1 : chapter.start - GAP / 2) : share(spot.id, spot.from);
    const to = spot.id === 'deploy' ? 2 : chapter.end + GAP / 2;
    const weight = visibilityWindow(progress, from, to, GAP / 2);
    if (weight <= 0.001) continue;
    const ax = point.x + (spot.dx ?? 0) + scene.px(spot.sx ?? 0);
    const ay = point.y + (spot.dy ?? 0) + scene.px(spot.sy ?? 0);
    const spotLeft = spot.align === 'center' ? ax - pillW / 2 : spot.align === 'left' ? ax : ax - pillW;
    pillLeft += spotLeft * weight;
    pillY += ay * weight;
    spotWeight += weight;
    if (spot.hide) hidden += weight;
  }
  const rest = clamp(1 - spotWeight);
  pillLeft += (point.x - pillW / 2) * rest;
  pillY += (point.y + scene.px(26)) * rest;
  pillLeft = clamp(pillLeft, scene.viewLeft + scene.px(8), Math.max(scene.viewLeft + scene.px(8), scene.viewRight - pillW - scene.px(8)));
  const screenX = scene.screenX(point.x);
  const edgeFade = (1 - smoothstep(range(screenX, scene.width - 8, scene.width + 40))) * smoothstep(range(screenX, -40, 8));
  const labelAlpha = edgeFade * (1 - clamp(hidden));
  if (labelAlpha <= 0.01) return;
  scene.roundRect(pillLeft, pillY - pillH / 2, pillW, pillH, pillH / 2);
  ctx.fillStyle = rgba(palette.background, labelAlpha * 0.86);
  ctx.fill();
  ctx.lineWidth = scene.px(1);
  ctx.strokeStyle = rgba(palette.accent, labelAlpha * 0.5);
  ctx.stroke();
  scene.text(label, pillLeft + padX, pillY, palette.accentText, labelAlpha);
}

// ---------------------------------------------------------------------------
// The frame
// ---------------------------------------------------------------------------

// The world box each storyboard still frames, centred, per chapter. The
// still fits the box, so a narrow still keeps the whole composition.
const STILLS: Record<string, Box> = {
  sdk: { x: 250, y: 0, w: 880, h: 330 },
  surfaces: { x: 400, y: 10, w: 1000, h: 360 },
  credentials: { x: 1025, y: -230, w: 2420, h: 1000 },
  catalog: { x: 1290, y: -300, w: 920, h: 880 },
  'first-request': { x: 1025, y: 40, w: 2420, h: 620 },
  lifetime: { x: 1000, y: 600, w: 1000, h: 540 },
  console: { x: 600, y: -390, w: 760, h: 800 },
  storage: { x: 1000, y: 600, w: 1000, h: 540 },
  enterprise: { x: 900, y: -130, w: 800, h: 580 },
  deploy: { x: 1025, y: 60, w: 2460, h: 1780 },
};

// The aspect ratio of a chapter's storyboard still: its box, held between a
// square and a wide strip so that each still keeps a readable height.
export function stillAspect(id: string) {
  const box = STILLS[id];
  return box ? clamp(box.w / box.h, 1, 2.2) : 1.6;
}

// The stage chrome in screen pixels: the nav band above and the status rail
// below, per viewport (journey.css). The world is framed between them.
const CHROME: Record<Viewport, { top: number; bottom: number }> = {
  wide: { top: 100, bottom: 76 },
  medium: { top: 84, bottom: 84 },
  compact: { top: 66, bottom: 68 },
};

export function drawFrame(ctx: CanvasRenderingContext2D, options: FrameOptions) {
  const { width, height, dpr, progress, time, viewport, ambient, centered } = options;
  const camera = interpolateCamera(progress, viewport);
  if (centered) {
    // Storyboard stills: the chapter's box, centred and fitted to the still.
    const box = STILLS[chapters[chapterIndexFor(progress)].id];
    camera.x = box.x;
    camera.y = box.y;
    camera.zoom = Math.min(width / box.w, height / box.h) * 0.92;
    camera.anchorX = 0.5;
    camera.anchorY = 0.5;
  }
  // The chrome band: the nav at the top and the status rail at the bottom.
  // The world is framed in the band between them, so no composition sits
  // under either. Storyboard stills have no chrome.
  const chrome = centered ? { top: 0, bottom: 0 } : CHROME[viewport];
  const safeTop = chrome.top;
  const safeH = height - chrome.top - chrome.bottom;
  // Desktop keyframes are composed for the 1440×900 stage's band and tablet
  // keyframes for 1024×768's. A shorter or narrower stage pulls the camera
  // back a little so the compositions stay inside the band and clear of the
  // copy. Small labels fade by the composed zoom, so a stage that pulls the
  // camera back to fit keeps the detail its chapter was composed with.
  const composed = options.camera !== undefined || centered;
  if (options.camera) Object.assign(camera, options.camera);
  const lodZoom = camera.zoom;
  let fit = 1;
  if (composed) {
    // The caller's frame, or the still's box, is the frame.
  } else if (viewport !== 'compact') {
    const composedH = 900 - CHROME.wide.top - CHROME.wide.bottom;
    const composedTabletH = 768 - CHROME.medium.top - CHROME.medium.bottom;
    fit = clamp(safeH / (viewport === 'wide' ? composedH : composedTabletH), 0.8, 1) * clamp(width / (viewport === 'wide' ? 1440 : 1024), 0.8, 1);
    camera.zoom *= fit;
  } else {
    // Phone keyframes are composed for 390×844. The copy block above the
    // world has a fixed height, so the world scales with the room left below
    // it and its anchor drops towards the centre of that room.
    const room = clamp((safeH - 366) / 344, 0.7, 1);
    camera.zoom *= room;
    camera.anchorY += (1 - room) * 0.08;
  }
  const palette = paletteFor();

  drawBackdrop(ctx, options, camera, palette);

  ctx.setTransform(
    dpr * camera.zoom,
    0,
    0,
    dpr * camera.zoom,
    dpr * (width * camera.anchorX - camera.x * camera.zoom),
    dpr * (safeTop + safeH * camera.anchorY - camera.y * camera.zoom),
  );

  const scene = new Scene(ctx, { ...options, lodZoom, fit }, camera, palette);
  const world: World = { scene, progress, weights: weights(progress) };
  drawFrames(world);
  drawSource(world);
  drawConsole(world);
  drawState(world);
  drawStream(world);
  drawListener(world);
  drawApp(world);
  drawKeys(world);
  drawControls(world);
  drawCatalog(world);
  drawPlanning(world);
  drawProviders(world);
  drawCredentialTags(world);
  drawRequest(world, time, ambient);

  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  // The chrome mask: the nav band and the status rail lie over the stage, so
  // whatever a tall composition leaves under them fades into the background
  // before it reaches them. Storyboard stills have no chrome.
  if (!centered && !options.camera) {
    const topMask = ctx.createLinearGradient(0, chrome.top - 30, 0, chrome.top + 34);
    topMask.addColorStop(0, rgba(palette.background, 1));
    topMask.addColorStop(1, rgba(palette.background, 0));
    ctx.fillStyle = topMask;
    ctx.fillRect(0, 0, width, chrome.top + 34);
    const bottomMask = ctx.createLinearGradient(0, height - chrome.bottom - 34, 0, height - chrome.bottom + 30);
    bottomMask.addColorStop(0, rgba(palette.background, 0));
    bottomMask.addColorStop(1, rgba(palette.background, 1));
    ctx.fillStyle = bottomMask;
    ctx.fillRect(0, height - chrome.bottom - 34, width, chrome.bottom + 34);
  }
  return { camera, palette };
}

// ---------------------------------------------------------------------------
// Hit-testing
// ---------------------------------------------------------------------------

// The parts of the hero still that a pointer can name, as world rectangles.
// The hero hit-tests these under the cursor and shows the part's card. The
// list is in nesting order, and a later part wins a contested point, so a
// card beats the host frame around it. Labels drawn outside a card take the
// zoom, since they keep their screen size.
export type Part = { id: WorldPartId; x0: number; y0: number; x1: number; y1: number };

export function parts(zoom: number): Part[] {
  const px = (value: number) => value / zoom;
  const box = (id: WorldPartId, b: Box): Part => ({ id, x0: left(b), y0: top(b), x1: right(b), y1: bottom(b) });
  const list: Part[] = [];
  list.push({ id: 'host', x0: HOST.x0, y0: HOST.y0, x1: HOST.x1, y1: HOST.y1 + px(48) });
  list.push({ id: 'stream', x0: APP.x - px(13), y0: STREAM_Y - px(13), x1: PROVIDER.x + px(13), y1: STREAM_Y + px(13) });
  list.push(box('state', STATE));
  list.push(box('app', APP));
  list.push({ id: 'listener', x0: DOOR.x - DOOR.w / 2, y0: -DOOR.gap - DOOR.h / 2, x1: DOOR.x + DOOR.w / 2, y1: DOOR.gap + DOOR.h / 2 + px(34) });
  list.push(box('keys', KEYS));
  list.push(box('catalog', CATALOG));
  list.push(box('catalog', SOURCE));
  list.push(box('planning', PLANNING));
  list.push({
    id: 'providers',
    x0: PROVIDER.x - PROVIDER.w / 2,
    y0: PROVIDER.ys[0] - PROVIDER.h / 2 - px(34),
    x1: PROVIDER.x + PROVIDER.w / 2,
    y1: PROVIDER.ys[PROVIDER.ys.length - 1] + PROVIDER.h / 2,
  });
  list.push(box('controls', CONTROLS));
  list.push(box('console', CONSOLE));
  // A card is a few screen pixels tall in a small still, so every part is at
  // least a fingertip wide and tall.
  const least = px(26);
  return list.map((part) => {
    const padX = Math.max(0, (least - (part.x1 - part.x0)) / 2);
    const padY = Math.max(0, (least - (part.y1 - part.y0)) / 2);
    return { ...part, x0: part.x0 - padX, x1: part.x1 + padX, y0: part.y0 - padY, y1: part.y1 + padY };
  });
}
