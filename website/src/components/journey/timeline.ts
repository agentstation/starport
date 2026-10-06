// The timeline owns the scroll → scene-time contract: acts, chapter windows,
// the request's narrative state, and the camera path for each viewport class.
// Everything here is pure data and pure math so the world renderer, the
// static storyboard, and the tests can share it.

import { CHAPTERS, type Chapter as ChapterFacts, REQUEST_STATES } from '@/lib/splash-facts';

export type Align = 'left' | 'right';
export type Viewport = 'wide' | 'medium' | 'compact';
export type ActId = 'client' | 'gateway' | 'request' | 'operate' | 'deploy';

export type Chapter = {
  id: string;
  act: ActId;
  // The facts of the chapter: the eyebrow, the claim, the body, the chips,
  // and the code. src/lib/splash-facts.ts owns them.
  facts: ChapterFacts;
  start: number;
  end: number;
  align: Align;
};

// `rail` is the label on the progress rail, where an act's span is too short
// for its full name.
export type Act = { id: ActId; label: string; rail?: string };

export const acts: Act[] = [
  { id: 'client', label: 'Client' },
  { id: 'gateway', label: 'Gateway' },
  { id: 'request', label: 'Request', rail: 'Stream' },
  { id: 'operate', label: 'Operate' },
  { id: 'deploy', label: 'Deploy' },
];

const ACT_OF: Record<string, ActId> = {
  sdk: 'client',
  surfaces: 'client',
  credentials: 'gateway',
  catalog: 'gateway',
  'first-request': 'request',
  console: 'operate',
  enterprise: 'operate',
  storage: 'operate',
  server: 'deploy',
  'scale-out': 'deploy',
  laptop: 'deploy',
};

export type CameraKeyframe = {
  at: number;
  x: number;
  y: number;
  zoom: number;
  // Screen-space anchor (0..1) where the camera target lands. Alternating the
  // anchor keeps the world in the negative space beside the copy.
  anchorX: number;
  anchorY: number;
};

export type Camera = Omit<CameraKeyframe, 'at'>;

// Eleven chapters. Each window is 0.0731 of the scroll, with a 0.0196 gap
// where the camera travels and the copy crossfades. Nimbus spaces nineteen
// chapters at 0.0505 with a 0.0399 window; the eleven windows here keep its
// window-to-period ratio, and the section height (journey.css) keeps its
// scroll distance per chapter. The last window runs from 0.927 to the end.
const PERIOD = 0.0927;
const WINDOW = 0.0731;

export const chapters: Chapter[] = CHAPTERS.map((facts, index) => {
  const act = ACT_OF[facts.id];
  if (!act) throw new Error(`splash chapter ${facts.id} has no act`);
  const start = Number((index * PERIOD).toFixed(4));
  const end = index === CHAPTERS.length - 1 ? 1 : Number((start + WINDOW).toFixed(4));
  return { id: facts.id, act, facts, start, end, align: index % 2 === 0 ? 'left' : 'right' };
});

export function actFor(chapter: Chapter) {
  return acts.find((act) => act.id === chapter.act) ?? null;
}

// Progress span of each act, from its first chapter's start to its last
// chapter's end. The first act owns the start of the rail.
export function actSpans() {
  return acts.map((act) => {
    const own = chapters.filter((chapter) => chapter.act === act.id);
    return { act, start: own[0].start, end: own[own.length - 1].end };
  });
}

// The request's narrative state, read out in the status bar and drawn beside
// the travelling token. Each entry starts at `at` and holds until the next.
// The first request chapter plans, streams, and finishes inside its window,
// so it holds three entries. The laptop chapter last restates the temporary
// gateway that the quick start runs.
function at(id: string, offset = 0) {
  const chapter = chapters.find((entry) => entry.id === id);
  if (!chapter) throw new Error(`no chapter ${id}`);
  return Number((chapter.start + offset * (chapter.end - chapter.start)).toFixed(4));
}

export const requestStates: { at: number; label: string }[] = [
  { at: 0, label: REQUEST_STATES.queued },
  { at: at('surfaces'), label: REQUEST_STATES.received },
  { at: at('credentials'), label: REQUEST_STATES.authorized },
  { at: at('catalog'), label: REQUEST_STATES.resolved },
  { at: at('first-request'), label: REQUEST_STATES.planned },
  { at: at('first-request', 0.25), label: REQUEST_STATES.streaming },
  { at: at('first-request', 0.62), label: REQUEST_STATES.done },
  { at: at('console'), label: REQUEST_STATES.console },
  { at: at('enterprise'), label: REQUEST_STATES.logged },
  { at: at('storage'), label: REQUEST_STATES.stored },
  { at: at('server'), label: REQUEST_STATES.served },
  { at: at('scale-out'), label: REQUEST_STATES.balanced },
  { at: at('laptop'), label: REQUEST_STATES.temporary },
];

export function requestStateFor(progress: number) {
  let state = requestStates[0].label;
  for (const entry of requestStates) {
    if (progress >= entry.at) state = entry.label;
  }
  return state;
}

export function clamp(value: number, min = 0, max = 1) {
  return Math.min(max, Math.max(min, value));
}

export function mix(from: number, to: number, amount: number) {
  return from + (to - from) * amount;
}

export function smoothstep(value: number) {
  const t = clamp(value);
  return t * t * (3 - 2 * t);
}

export function range(value: number, start: number, end: number) {
  return clamp((value - start) / Math.max(0.0001, end - start));
}

// 0 outside [start, end], 1 inside, eased over `edge` at both ends.
export function visibilityWindow(value: number, start: number, end: number, edge = 0.03) {
  const fadeIn = smoothstep(range(value, start, start + edge));
  const fadeOut = 1 - smoothstep(range(value, end - edge, end));
  return clamp(fadeIn * fadeOut);
}

// A chapter's copy fades over this much of the journey at each end. Nimbus
// uses 0.0114 for a 0.0505 period; this is the same share of a 0.0927
// period.
export const COPY_EDGE = 0.0209;

// The ground under the stage. As in Nimbus, the acts alternate, starting
// light against the night hero: paper for the client, night for the
// gateway, paper for the request, night for operating it, and the gold wash
// for deploying it. The stage is paper from its first frame, so the edge
// between the hero and the stage is the first turn, as Nimbus has it.
export type Ground = 'night' | 'paper' | 'wash';

export const ACT_GROUNDS: Record<ActId, Ground> = {
  client: 'paper',
  gateway: 'night',
  request: 'paper',
  operate: 'night',
  deploy: 'wash',
};

// Each turn is eased over a short span at the middle of the gap between the
// last chapter of one act and the first of the next, inside the middle
// third of the gap, where no copy is on stage.
const CROSSING = 0.0035;

export type GroundCrossing = { after: string; from: Ground; to: Ground; middle: number };

export const groundCrossings: GroundCrossing[] = chapters.slice(0, -1).flatMap((chapter, index) => {
  const next = chapters[index + 1];
  const from = ACT_GROUNDS[chapter.act];
  const to = ACT_GROUNDS[next.act];
  if (from === to) return [];
  return [{ after: chapter.id, from, to, middle: Number(((chapter.end + next.start) / 2).toFixed(4)) }];
});

function turn(crossing: GroundCrossing, progress: number) {
  return smoothstep(range(progress, crossing.middle - CROSSING / 2, crossing.middle + CROSSING / 2));
}

// How far the ground has turned to `ground`, from the first act's ground
// through every turn to or from it.
function mixFor(ground: Ground, progress: number) {
  let amount = ACT_GROUNDS[chapters[0].act] === ground ? 1 : 0;
  for (const crossing of groundCrossings) {
    if (crossing.to === ground) amount += turn(crossing, progress);
    else if (crossing.from === ground) amount -= turn(crossing, progress);
  }
  return clamp(amount);
}

export function paperMixFor(progress: number) {
  return mixFor('paper', progress);
}

export function washMixFor(progress: number) {
  return mixFor('wash', progress);
}

// Where chapter travel lands: the point where the chapter's copy is fully on
// stage and its scene has played. The first stop is the top of the page,
// where the first chapter is already on stage.
export function chapterStop(index: number) {
  return index === 0 ? 0 : chapters[index].end - COPY_EDGE;
}

// A chapter's rest: from the point where its copy is fully on stage to the
// point where it starts to leave. The reader can stop anywhere inside it, and
// the scene's beats play under the scroll. The first chapter rests from the
// top of the page; the last rests to the end of the journey.
export function chapterRest(index: number) {
  const chapter = chapters[index];
  return {
    from: index === 0 ? 0 : chapter.start + COPY_EDGE,
    to: index === chapters.length - 1 ? 1 : chapter.end - COPY_EDGE,
  };
}

export function chapterIndexFor(progress: number) {
  let active = 0;
  for (let index = 1; index < chapters.length; index += 1) {
    const threshold = (chapters[index - 1].end + chapters[index].start) / 2;
    if (progress >= threshold) active = index;
  }
  return active;
}

export function viewportFor(width: number): Viewport {
  if (width < 760) return 'compact';
  if (width < 1100) return 'medium';
  return 'wide';
}

// Monotone cubic (Fritsch–Carlson) interpolation through keyframes. Velocity
// is continuous across keyframes, repeated values hold with zero velocity,
// and nothing overshoots. Camera moves and the request's route both use it,
// so neither stops dead at every waypoint.
export function monotoneCubic(times: number[], values: number[], t: number) {
  const count = times.length;
  if (count === 0) return 0;
  if (count === 1 || t <= times[0]) return values[0];
  if (t >= times[count - 1]) return values[count - 1];
  let index = 0;
  while (index < count - 2 && t > times[index + 1]) index += 1;
  const h = times[index + 1] - times[index];
  if (h <= 0) return values[index + 1];
  const slopes = (k: number) => (values[k + 1] - values[k]) / (times[k + 1] - times[k]);
  const secant = slopes(index);
  const tangentAt = (k: number) => {
    if (k === 0 || k === count - 1) return 0;
    const left = slopes(k - 1);
    const right = slopes(k);
    if (left * right <= 0) return 0;
    const m = (left + right) / 2;
    const bound = 3 * Math.min(Math.abs(left), Math.abs(right));
    return Math.sign(m) * Math.min(Math.abs(m), bound);
  };
  let m0 = tangentAt(index);
  let m1 = tangentAt(index + 1);
  if (secant === 0) {
    m0 = 0;
    m1 = 0;
  }
  const s = (t - times[index]) / h;
  const s2 = s * s;
  const s3 = s2 * s;
  const h00 = 2 * s3 - 3 * s2 + 1;
  const h10 = s3 - 2 * s2 + s;
  const h01 = -2 * s3 + 3 * s2;
  const h11 = s3 - s2;
  return h00 * values[index] + h10 * h * m0 + h01 * values[index + 1] + h11 * h * m1;
}

// The three places below the host that the last chapters travel to: the
// production server over its row of hosts, the fleet, and the laptop.
// world.ts draws them; the camera frames them.
export const SERVER = { x: 1025, y: 1450, hostsY: 2000 };
export const FLEET = { x: 1025, y: 2750, replicaY: 2700, storeY: 2980 };
export const LAPTOP = { x: 1025, y: 3580 };

// One composition per chapter and viewport class: the world point the camera
// frames, its zoom, and the screen anchor. Each chapter holds a keyframe at
// the start and at the end of its window, so the camera drifts a little
// while the copy is on stage and travels in the gap between two chapters.
// A `pan` holds the frame until `from` (a share of the window), moves it by
// `dx` and `dy` until `to`, and holds it again: a phone frame follows the
// request where the route is wider or taller than the screen.
type Pan = { from: number; to: number; dx?: number; dy?: number };
type Shot = { x: number; y: number; zoom: number; anchorX: number; anchorY: number; dx?: number; dz?: number; pan?: Pan };

// Wide (1440×900 composition): the copy sits in a left or right column and
// the world takes the other side. The credentials and first-request
// chapters pull back to show the whole route; the others frame one part and
// its neighbours. The server chapter goes down under the host to the machine
// and its row of hosts, the scale-out chapter goes down again to the fleet,
// and the laptop chapter goes down to the laptop.
const wideShots: Record<string, Shot> = {
  sdk: { x: 240, y: 0, zoom: 1, anchorX: 0.68, anchorY: 0.5, dx: 20 },
  surfaces: { x: 330, y: 0, zoom: 0.9, anchorX: 0.32, anchorY: 0.5, dx: 20 },
  credentials: { x: 1025, y: -250, zoom: 0.37, anchorX: 0.68, anchorY: 0.5 },
  catalog: { x: 1220, y: -300, zoom: 0.74, anchorX: 0.32, anchorY: 0.5, dx: 20 },
  'first-request': { x: 1025, y: 40, zoom: 0.37, anchorX: 0.68, anchorY: 0.5 },
  console: { x: 590, y: -380, zoom: 0.76, anchorX: 0.32, anchorY: 0.5, dx: 20 },
  enterprise: { x: 880, y: -150, zoom: 0.9, anchorX: 0.68, anchorY: 0.5, dx: 20 },
  storage: { x: 1000, y: 620, zoom: 0.8, anchorX: 0.32, anchorY: 0.5, dx: 20 },
  server: { x: SERVER.x, y: SERVER.y + 185, zoom: 0.62, anchorX: 0.68, anchorY: 0.5, dz: 0.02 },
  'scale-out': { x: FLEET.x, y: FLEET.y, zoom: 0.6, anchorX: 0.32, anchorY: 0.5, dx: 20 },
  laptop: { x: LAPTOP.x, y: LAPTOP.y, zoom: 0.78, anchorX: 0.68, anchorY: 0.5, dz: 0.02 },
};

// Medium (1024×768 composition): the copy column is wider, so the world is
// framed tighter and sits lower to clear the headline.
const mediumShots: Record<string, Shot> = {
  sdk: { x: 240, y: 0, zoom: 0.6, anchorX: 0.73, anchorY: 0.56 },
  surfaces: { x: 330, y: 0, zoom: 0.52, anchorX: 0.27, anchorY: 0.56 },
  credentials: { x: 1025, y: -250, zoom: 0.23, anchorX: 0.69, anchorY: 0.56 },
  catalog: { x: 1250, y: -300, zoom: 0.48, anchorX: 0.27, anchorY: 0.56 },
  'first-request': { x: 1025, y: 40, zoom: 0.23, anchorX: 0.69, anchorY: 0.56 },
  console: { x: 590, y: -380, zoom: 0.56, anchorX: 0.27, anchorY: 0.56 },
  enterprise: { x: 880, y: -150, zoom: 0.6, anchorX: 0.73, anchorY: 0.56 },
  storage: { x: 1000, y: 620, zoom: 0.5, anchorX: 0.27, anchorY: 0.56 },
  server: { x: SERVER.x, y: SERVER.y + 185, zoom: 0.5, anchorX: 0.73, anchorY: 0.5 },
  'scale-out': { x: FLEET.x, y: FLEET.y, zoom: 0.4, anchorX: 0.27, anchorY: 0.56 },
  laptop: { x: LAPTOP.x, y: LAPTOP.y, zoom: 0.52, anchorX: 0.73, anchorY: 0.56 },
};

// Compact (390×844 composition): the copy owns the upper band, the world
// lives in the lower band, centred, and the camera never pans with the copy.
// A phone is too narrow for the whole route at a legible size, so each frame
// holds only the parts its chapter names: the key check, the catalog and the
// planning for the credentials; the catalog pipeline; the providers and then
// the stream home for the first request; one card for the console and the
// controls; the durable state; the machine, then its row of hosts, for the
// server; the balancer and the replicas, then the shared stores, for the
// scale-out. The laptop chapter's copy owns the phone stage (world.ts).
const compactShots: Record<string, Shot> = {
  sdk: { x: 240, y: 0, zoom: 0.45, anchorX: 0.5, anchorY: 0.78 },
  surfaces: { x: 400, y: 0, zoom: 0.45, anchorX: 0.5, anchorY: 0.78 },
  credentials: { x: 1215, y: -250, zoom: 0.37, anchorX: 0.5, anchorY: 0.69 },
  catalog: { x: 1220, y: 0, zoom: 0.7, anchorX: 0.5, anchorY: 0.74 },
  // The stream runs from 0.3 to 0.52 of the window (world.ts); the frame
  // moves with it from the providers to the app.
  'first-request': { x: 1780, y: 60, zoom: 0.4, anchorX: 0.5, anchorY: 0.76, pan: { from: 0.3, to: 0.52, dx: -1430 } },
  console: { x: 590, y: -560, zoom: 0.62, anchorX: 0.5, anchorY: 0.76 },
  enterprise: { x: 880, y: -230, zoom: 0.85, anchorX: 0.5, anchorY: 0.76 },
  storage: { x: 1000, y: 640, zoom: 0.4, anchorX: 0.5, anchorY: 0.8 },
  // The frame holds the machine while the request comes in, then moves down
  // until the hosts it can run on are in frame under it.
  server: {
    x: SERVER.x,
    y: SERVER.y,
    zoom: 0.38,
    anchorX: 0.5,
    anchorY: 0.78,
    pan: { from: 0.35, to: 0.6, dy: SERVER.hostsY - 140 - SERVER.y },
  },
  // The frame holds the balancer and the replicas while the request goes in,
  // then moves down to the stores they share.
  'scale-out': {
    x: FLEET.x,
    y: FLEET.replicaY - 90,
    zoom: 0.36,
    anchorX: 0.5,
    anchorY: 0.78,
    pan: { from: 0.35, to: 0.6, dy: FLEET.storeY - FLEET.replicaY },
  },
  laptop: { x: LAPTOP.x, y: LAPTOP.y, zoom: 0.4, anchorX: 0.5, anchorY: 0.78 },
};

function keyframes(shots: Record<string, Shot>): CameraKeyframe[] {
  return chapters.flatMap((chapter) => {
    const shot = shots[chapter.id];
    if (!shot) throw new Error(`no camera for chapter ${chapter.id}`);
    const { dx = 0, dz = 0, pan, ...camera } = shot;
    if (pan) {
      const at = (share: number) => Number((chapter.start + share * (chapter.end - chapter.start)).toFixed(4));
      return [
        { at: chapter.start, ...camera },
        { at: at(pan.from), ...camera },
        { at: at(pan.to), ...camera, x: camera.x + (pan.dx ?? 0), y: camera.y + (pan.dy ?? 0) },
        { at: chapter.end, ...camera, x: camera.x + (pan.dx ?? 0), y: camera.y + (pan.dy ?? 0) },
      ];
    }
    return [
      { at: chapter.start, ...camera },
      { at: chapter.end, ...camera, x: camera.x + dx, zoom: camera.zoom + dz },
    ];
  });
}

export const cameras: Record<Viewport, CameraKeyframe[]> = {
  wide: keyframes(wideShots),
  medium: keyframes(mediumShots),
  compact: keyframes(compactShots),
};

type Track = { times: number[]; values: Record<keyof Camera, number[]> };
const tracks = new Map<Viewport, Track>();

function trackFor(viewport: Viewport): Track {
  let track = tracks.get(viewport);
  if (!track) {
    const frames = cameras[viewport];
    track = {
      times: frames.map((key) => key.at),
      values: {
        x: frames.map((key) => key.x),
        y: frames.map((key) => key.y),
        zoom: frames.map((key) => key.zoom),
        anchorX: frames.map((key) => key.anchorX),
        anchorY: frames.map((key) => key.anchorY),
      },
    };
    tracks.set(viewport, track);
  }
  return track;
}

export function interpolateCamera(progress: number, viewport: Viewport): Camera {
  const track = trackFor(viewport);
  return {
    x: monotoneCubic(track.times, track.values.x, progress),
    y: monotoneCubic(track.times, track.values.y, progress),
    zoom: monotoneCubic(track.times, track.values.zoom, progress),
    anchorX: monotoneCubic(track.times, track.values.anchorX, progress),
    anchorY: monotoneCubic(track.times, track.values.anchorY, progress),
  };
}
