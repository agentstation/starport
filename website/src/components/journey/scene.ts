// The draw context shared by every part of the world: the palette, the
// camera-aware canvas helpers, and the backdrop. World geometry and the story
// itself live in world.ts. Ported from the Nimbus odyssey scene, with its
// three grounds: night, daylight paper, and the gold wash of the finale.

import { type Camera, type Viewport, clamp, mix, paperMixFor, range, smoothstep, washMixFor } from './timeline';

export type Rgb = [number, number, number];

export type Palette = {
  background: Rgb;
  // The chrome ground: the nav, the run link and the playhead sit on it.
  chromeBackground: Rgb;
  ink: Rgb;
  muted: Rgb;
  // `accent` is the display tone that fills chips, lanes and the token, and
  // `accentText` is the reading tone for accent-coloured labels.
  // `accentCarrier` is the ground under an accent label.
  accent: Rgb;
  accentText: Rgb;
  accentCarrier: Rgb;
  // How far the ground has turned to paper and to the wash (0..1).
  paperMix: number;
  washMix: number;
};

export type FrameOptions = {
  width: number;
  height: number;
  dpr: number;
  progress: number;
  time: number;
  viewport: Viewport;
  ambient: boolean;
  // Storyboard stills centre the camera target instead of leaving room for copy.
  centered?: boolean;
  // The zoom the chapter was composed at, before the stage pulled the camera
  // back to fit; small labels fade by it.
  lodZoom?: number;
  // How far the frame pulled the camera back to fit the stage (1 = composed
  // size). The minimum label size follows it, so labels stay in proportion
  // to their cards instead of growing against them on a shorter stage.
  fit?: number;
  // A frame composed by its caller: the splash names its own camera, and the
  // stage fitting that follows the chapter keyframes is skipped.
  camera?: Partial<Camera>;
  // The progress the ground is read at, when it is not the frame's own: the
  // hero still frames the server chapter on the night ground of the hero.
  ground?: number;
};

// The palette stops: the Starport role tokens, read off the ground scopes in
// tokens.css, and the Nimbus stops exactly. Canvas cannot read a CSS
// variable, so the values are literal here. A change to the token sheet
// belongs in this table too.
type PaletteStops = {
  night: Rgb;
  paper: Rgb;
  wash: Rgb;
  washInk: Rgb;
  inkNight: Rgb;
  inkPaper: Rgb;
  accentNight: Rgb;
  accentPaper: Rgb;
  accentTextNight: Rgb;
  accentTextPaper: Rgb;
};

const STOPS: PaletteStops = {
  night: [10, 11, 12], // --bg-canvas night, #0a0b0c
  paper: [250, 250, 250], // --bg-canvas paper, #fafafa
  // --accent, #f0b23e, under --accent-ink, #1a1204: 10.9:1.
  wash: [240, 178, 62],
  washInk: [26, 18, 4],
  inkNight: [246, 247, 248], // --text-1 night, #f6f7f8
  inkPaper: [24, 24, 27], // --text-1 paper, #18181b
  // The gold is the accent on every ground but the wash: the lanes, the
  // chip borders and the token stay #f0b23e as the page whitens. A darkened
  // gold turns olive.
  accentNight: [240, 178, 62], // --accent, #f0b23e
  accentPaper: [240, 178, 62], // --accent, #f0b23e
  // A caption in the accent has no area behind it, and the gold is 1.8:1 on
  // paper, so there the caption takes the accent's own ink, as
  // `--accent-text` does. Two colours, never a darkened third.
  accentTextNight: [240, 178, 62], // --accent, #f0b23e
  accentTextPaper: [26, 18, 4], // --accent-ink, #1a1204
};

// The night stop, for a surface that stays night on every ground: the
// laptop screen in the last chapter.
export function night(): Palette {
  return paletteFor(0);
}

// Canvas fonts cannot read CSS variables; the page resolves the loaded
// families once and hands them over.
let monoFamily = 'ui-monospace, monospace';
let sansFamily = 'system-ui, sans-serif';

export function setFonts(mono: string, sans: string) {
  if (mono.trim()) monoFamily = mono;
  if (sans.trim()) sansFamily = sans;
}

// The page declares its families on the `.splash` scope (tokens.css).
export function resolveFonts(scope: Element) {
  const styles = getComputedStyle(scope);
  setFonts(styles.getPropertyValue('--font-mono'), styles.getPropertyValue('--font-sans'));
}

// A label never renders below the viewport's minimum screen size, whatever
// the zoom, so a fitted still stays legible.
function minFontPx(viewport: Viewport) {
  return viewport === 'compact' ? 10 : 11;
}

// The width, in world units, of mono text as a frame at this zoom draws it.
// Hit-testing shares this with the drawing, so a row laid out from measured
// text and its hover targets agree. Without a document (a script), the mono
// face's advance stands in.
let gauge: CanvasRenderingContext2D | null | undefined;
export function measureMono(text: string, size: number, weight: number, zoom: number, viewport: Viewport, fit = 1) {
  const fontSize = Math.max(size, (minFontPx(viewport) * fit) / zoom);
  if (gauge === undefined) gauge = typeof document === 'undefined' ? null : document.createElement('canvas').getContext('2d');
  if (!gauge) return fontSize * 0.6 * text.length;
  gauge.font = `${weight} ${fontSize.toFixed(2)}px ${monoFamily}`;
  return gauge.measureText(text).width;
}

// Per-viewport layout: the zoom range over which small labels fade (level of
// detail), and the zoom range under which the card titles leave too. A label
// never renders below the minimum screen size, so at a low zoom it grows in
// world units. The detail range starts where that growth would spill a chip
// out of its card; the far range starts where a title would cover its card.
// Between the two, the world checks each title against its card.
export type Layout = { lod: [number, number]; far: [number, number] };

export const layouts: Record<Viewport, Layout> = {
  wide: { lod: [0.55, 0.7], far: [0.18, 0.22] },
  medium: { lod: [0.55, 0.7], far: [0.18, 0.22] },
  compact: { lod: [0.5, 0.63], far: [0.14, 0.17] },
};

export function mixRgb(from: Rgb, to: Rgb, amount: number): Rgb {
  return [mix(from[0], to[0], amount), mix(from[1], to[1], amount), mix(from[2], to[2], amount)];
}

export function rgb(color: Rgb) {
  return `rgb(${color[0].toFixed(0)} ${color[1].toFixed(0)} ${color[2].toFixed(0)})`;
}

export function rgba(color: Rgb, alpha: number) {
  return `rgb(${color[0].toFixed(0)} ${color[1].toFixed(0)} ${color[2].toFixed(0)} / ${clamp(alpha).toFixed(3)})`;
}

export function paletteFor(progress: number): Palette {
  const paperMix = paperMixFor(progress);
  const washMix = washMixFor(progress);
  const background = mixRgb(mixRgb(STOPS.night, STOPS.paper, paperMix), STOPS.wash, washMix);
  // The ink swaps in one step as the background passes its midpoint, so the
  // two never meet at the same grey and the frame keeps its contrast through
  // a crossfade. Any continuous mix passes through that grey; a step does not.
  const inkMix = paperMix < 0.5 ? 0 : 1;
  const ink = mixRgb(mixRgb(STOPS.inkNight, STOPS.inkPaper, inkMix), STOPS.washInk, washMix);
  const chromeBackground = mixRgb(mixRgb(STOPS.night, STOPS.paper, inkMix), STOPS.wash, washMix);
  // On the wash the ground is already the accent, so both accent jobs step
  // aside for its ink. Gold on gold is not a tone.
  const accent = mixRgb(mixRgb(STOPS.accentNight, STOPS.accentPaper, paperMix), STOPS.washInk, washMix);
  // The caption steps with the ink, for the same reason: a continuous fade
  // from the gold to its ink passes through the darkened golds.
  const accentText = mixRgb(mixRgb(STOPS.accentTextNight, STOPS.accentTextPaper, inkMix), STOPS.washInk, washMix);
  // The ground an accent label sits on, so the gold can stay gold on paper:
  // the page's own ground on night and on the wash, and the accent ink on
  // paper, where the ground is too light to show the gold.
  const accentCarrier = mixRgb(mixRgb(STOPS.night, STOPS.washInk, paperMix), STOPS.wash, washMix);
  return {
    background,
    chromeBackground,
    ink,
    muted: mixRgb(chromeBackground, ink, 0.56),
    accent,
    accentText,
    accentCarrier,
    paperMix,
    washMix,
  };
}

export type TrafficOptions = {
  count?: number;
  speed?: number;
  phase?: number;
  size?: number;
  reverse?: boolean;
};

// One coordinate of a cubic bezier at t.
function bezier(t: number, p0: number, p1: number, p2: number, p3: number) {
  const u = 1 - t;
  return u * u * u * p0 + 3 * u * u * t * p1 + 3 * u * t * t * p2 + t * t * t * p3;
}

// The point at share `t` of the length of a polyline.
export function pointAlong(points: { x: number; y: number }[], t: number) {
  const lengths = points.slice(1).map((point, index) => Math.hypot(point.x - points[index].x, point.y - points[index].y));
  const total = lengths.reduce((sum, length) => sum + length, 0);
  let left = clamp(t) * total;
  for (let index = 0; index < lengths.length; index += 1) {
    if (left <= lengths[index] || index === lengths.length - 1) {
      const share = lengths[index] === 0 ? 0 : clamp(left / lengths[index]);
      return { x: mix(points[index].x, points[index + 1].x, share), y: mix(points[index].y, points[index + 1].y, share) };
    }
    left -= lengths[index];
  }
  return points[points.length - 1];
}

// Draw context: one per frame. Font sizes are world units but never fall below
// a legible screen size, so the compact camera can zoom out without producing
// unreadable labels.
export class Scene {
  ctx: CanvasRenderingContext2D;
  camera: Camera;
  palette: Palette;
  zoom: number;
  minPx: number;
  viewport: Viewport;
  width: number;
  viewLeft: number;
  viewRight: number;
  viewTop: number;
  viewBottom: number;
  // Level of detail: small labels fade out as the camera pulls back, so the
  // minimum screen font size never makes neighbouring labels collide.
  detail: number;
  // Card titles stay until the camera is far out, where the whole host is in
  // frame and a title would be wider than its card.
  far: number;
  textAlpha = 1;
  // The frame time in milliseconds, and whether the frame is one of a
  // running sequence. Ambient motion (traffic on the lanes, a blinking
  // caret) follows the time; a static frame draws none of it.
  time: number;
  ambient: boolean;

  constructor(ctx: CanvasRenderingContext2D, options: FrameOptions, camera: Camera, palette: Palette) {
    this.ctx = ctx;
    this.camera = camera;
    this.palette = palette;
    this.zoom = camera.zoom;
    this.time = options.time;
    this.ambient = options.ambient;
    this.minPx = minFontPx(options.viewport) * (options.fit ?? 1);
    this.viewport = options.viewport;
    const layout = layouts[options.viewport];
    this.detail = smoothstep(range(options.lodZoom ?? camera.zoom, layout.lod[0], layout.lod[1]));
    this.far = smoothstep(range(options.lodZoom ?? camera.zoom, layout.far[0], layout.far[1]));
    const { width, height } = options;
    this.width = width;
    this.viewLeft = camera.x - (camera.anchorX * width) / camera.zoom;
    this.viewRight = camera.x + ((1 - camera.anchorX) * width) / camera.zoom;
    this.viewTop = camera.y - (camera.anchorY * height) / camera.zoom;
    this.viewBottom = camera.y + ((1 - camera.anchorY) * height) / camera.zoom;
  }

  inView(x0: number, x1: number, margin = 120, y0 = -600, y1 = 600) {
    return x1 + margin >= this.viewLeft && x0 - margin <= this.viewRight && y1 + margin >= this.viewTop && y0 - margin <= this.viewBottom;
  }

  // Line widths are constant on screen.
  px(value: number) {
    return value / this.zoom;
  }

  fontSize(size: number) {
    return Math.max(size, this.minPx / this.zoom);
  }

  mono(size: number, weight = 500) {
    this.ctx.font = `${weight} ${this.fontSize(size).toFixed(2)}px ${monoFamily}`;
  }

  sans(size: number, weight = 400, scale = 1) {
    this.ctx.font = `${weight} ${(this.fontSize(size) * scale).toFixed(2)}px ${sansFamily}`;
  }

  screenX(x: number) {
    return this.camera.anchorX * this.width + (x - this.camera.x) * this.zoom;
  }

  text(value: string, x: number, y: number, color: Rgb, alpha: number, align: CanvasTextAlign = 'left') {
    alpha *= this.textAlpha;
    if (alpha <= 0.01) return;
    this.ctx.fillStyle = rgba(color, alpha);
    this.ctx.textAlign = align;
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText(value, x, y);
  }

  roundRect(x: number, y: number, w: number, h: number, radius: number) {
    const r = Math.max(0, Math.min(radius, w / 2, h / 2));
    const { ctx } = this;
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  panel(cx: number, cy: number, w: number, h: number, alpha: number, emphasis = 0, tint?: Rgb) {
    if (alpha <= 0.01) return;
    const { ctx, palette } = this;
    const x = cx - w / 2;
    const y = cy - h / 2;
    this.roundRect(x, y, w, h, 10);
    ctx.fillStyle = rgba(mixRgb(palette.background, palette.ink, 0.05 + emphasis * 0.05), alpha);
    ctx.fill();
    ctx.lineWidth = this.px(1);
    ctx.strokeStyle = rgba(tint ?? palette.ink, alpha * (0.22 + emphasis * 0.6));
    ctx.stroke();
    if (emphasis > 0.01 && tint) {
      ctx.lineWidth = this.px(2);
      ctx.strokeStyle = rgba(tint, alpha * emphasis * 0.9);
      ctx.stroke();
    }
  }

  // Database symbol: a cylinder with an elliptical lid. It carries the panel
  // fill and line, so a store reads as one family with the cards.
  cylinder(cx: number, cy: number, w: number, h: number, alpha: number, emphasis = 0, tint?: Rgb) {
    if (alpha <= 0.01) return;
    const { ctx, palette } = this;
    const rx = w / 2;
    const ry = Math.min(h * 0.12, w * 0.12);
    const top = cy - h / 2 + ry;
    const bottom = cy + h / 2 - ry;
    const line = tint ?? palette.ink;
    ctx.beginPath();
    ctx.moveTo(cx - rx, top);
    ctx.lineTo(cx - rx, bottom);
    ctx.ellipse(cx, bottom, rx, ry, 0, Math.PI, 0, true);
    ctx.lineTo(cx + rx, top);
    ctx.ellipse(cx, top, rx, ry, 0, 0, Math.PI, true);
    ctx.closePath();
    ctx.fillStyle = rgba(mixRgb(palette.background, palette.ink, 0.05 + emphasis * 0.05), alpha);
    ctx.fill();
    ctx.lineWidth = this.px(1);
    ctx.strokeStyle = rgba(line, alpha * (0.22 + emphasis * 0.6));
    ctx.stroke();
    if (emphasis > 0.01 && tint) {
      ctx.lineWidth = this.px(2);
      ctx.strokeStyle = rgba(tint, alpha * emphasis * 0.9);
      ctx.stroke();
    }
    // The lid: a full ellipse, a shade lighter, with its front edge drawn.
    ctx.beginPath();
    ctx.ellipse(cx, top, rx, ry, 0, 0, Math.PI * 2);
    ctx.fillStyle = rgba(mixRgb(palette.background, palette.ink, 0.1 + emphasis * 0.08), alpha);
    ctx.fill();
    ctx.lineWidth = this.px(1);
    ctx.strokeStyle = rgba(line, alpha * (0.22 + emphasis * 0.6));
    ctx.stroke();
  }

  // Top of the body text area inside a cylinder: below the lid's front edge.
  cylinderBodyTop(cy: number, w: number, h: number) {
    const ry = Math.min(h * 0.12, w * 0.12);
    return cy - h / 2 + ry * 3;
  }

  // Bezier lane between two points with a horizontal departure and arrival.
  lane(x0: number, y0: number, x1: number, y1: number, color: Rgb, alpha: number, width = 1, dash?: number[]) {
    if (alpha <= 0.01) return;
    const dx = (x1 - x0) * 0.5;
    this.curve(x0, y0, x0 + dx, y0, x1 - dx, y1, x1, y1, color, alpha, width, dash);
  }

  // Bezier lane with a vertical departure and arrival: down from a box and up
  // into the next one.
  laneVertical(x0: number, y0: number, x1: number, y1: number, color: Rgb, alpha: number, width = 1, dash?: number[]) {
    if (alpha <= 0.01) return;
    const dy = (y1 - y0) * 0.5;
    this.curve(x0, y0, x0, y0 + dy, x1, y1 - dy, x1, y1, color, alpha, width, dash);
  }

  private curve(x0: number, y0: number, c0x: number, c0y: number, c1x: number, c1y: number, x1: number, y1: number, color: Rgb, alpha: number, width: number, dash?: number[]) {
    const { ctx } = this;
    ctx.beginPath();
    ctx.moveTo(x0, y0);
    ctx.bezierCurveTo(c0x, c0y, c1x, c1y, x1, y1);
    ctx.lineWidth = this.px(width);
    ctx.strokeStyle = rgba(color, alpha);
    ctx.setLineDash(dash ? dash.map((value) => this.px(value)) : []);
    ctx.stroke();
    ctx.setLineDash([]);
  }

  // Traffic: dots that ride a path while the frame is ambient, as if the
  // system were always busy. Each dot's place is a function of the frame
  // time, so the motion is steady from frame to frame, and a static frame
  // draws none of it. `speed` is cycles per eight seconds; `reverse` sends
  // the dots from the path's end to its start. The dots fade in and out at
  // the ends, so none pops onto or off the lane.
  traffic(path: (t: number) => { x: number; y: number }, color: Rgb, alpha: number, options: TrafficOptions = {}) {
    if (!this.ambient || alpha <= 0.01) return;
    const { count = 2, speed = 2, phase = 0, size = 2.4, reverse = false } = options;
    const { ctx } = this;
    const cycle = this.time * 0.000125 * speed * (reverse ? -1 : 1);
    for (let index = 0; index < count; index += 1) {
      const t = (((phase + index / count + cycle) % 1) + 1) % 1;
      const fade = smoothstep(range(t, 0, 0.12)) * (1 - smoothstep(range(t, 0.88, 1)));
      if (fade <= 0.01) continue;
      const p = path(t);
      ctx.beginPath();
      ctx.arc(p.x, p.y, this.px(size), 0, Math.PI * 2);
      ctx.fillStyle = rgba(color, alpha * fade);
      ctx.fill();
    }
  }

  // Traffic on a horizontal-departure lane, the curve `lane` draws.
  laneTraffic(x0: number, y0: number, x1: number, y1: number, color: Rgb, alpha: number, options: TrafficOptions = {}) {
    const dx = (x1 - x0) * 0.5;
    this.traffic((t) => ({ x: bezier(t, x0, x0 + dx, x1 - dx, x1), y: bezier(t, y0, y0, y1, y1) }), color, alpha, options);
  }

  // Traffic on a vertical-departure lane, the curve `laneVertical` draws.
  laneVerticalTraffic(x0: number, y0: number, x1: number, y1: number, color: Rgb, alpha: number, options: TrafficOptions = {}) {
    const dy = (y1 - y0) * 0.5;
    this.traffic((t) => ({ x: bezier(t, x0, x0, x1, x1), y: bezier(t, y0, y0 + dy, y1 - dy, y1) }), color, alpha, options);
  }

  // Traffic on a straight lane.
  lineTraffic(x0: number, y0: number, x1: number, y1: number, color: Rgb, alpha: number, options: TrafficOptions = {}) {
    this.traffic((t) => ({ x: mix(x0, x1, t), y: mix(y0, y1, t) }), color, alpha, options);
  }

  // A slow breath in [0, 1] for a part that is always running. Zero in a
  // static frame, so the still keeps its composed tone.
  breath(rate = 1, phase = 0) {
    return this.ambient ? 0.5 + 0.5 * Math.sin(this.time * 0.0025 * rate + phase) : 0;
  }

  // An orthogonal lane through `points` with rounded corners: the stream
  // that runs back along the floor of the gateway to the app.
  path(points: { x: number; y: number }[], color: Rgb, alpha: number, width = 1, radius = 40, dash?: number[]) {
    if (alpha <= 0.01 || points.length < 2) return;
    const { ctx } = this;
    ctx.beginPath();
    ctx.moveTo(points[0].x, points[0].y);
    for (let index = 1; index < points.length - 1; index += 1) {
      ctx.arcTo(points[index].x, points[index].y, points[index + 1].x, points[index + 1].y, radius);
    }
    const last = points[points.length - 1];
    ctx.lineTo(last.x, last.y);
    ctx.lineWidth = this.px(width);
    ctx.strokeStyle = rgba(color, alpha);
    ctx.setLineDash(dash ? dash.map((value) => this.px(value)) : []);
    ctx.stroke();
    ctx.setLineDash([]);
  }

  // Traffic on the lane that `path` draws, by distance along its legs.
  pathTraffic(points: { x: number; y: number }[], color: Rgb, alpha: number, options: TrafficOptions = {}) {
    this.traffic((t) => pointAlong(points, t), color, alpha, options);
  }

  // A straight lane, dashed or solid, in world units.
  line(x0: number, y0: number, x1: number, y1: number, color: Rgb, alpha: number, width = 1, dash?: number[]) {
    if (alpha <= 0.01) return;
    const { ctx } = this;
    ctx.beginPath();
    ctx.moveTo(x0, y0);
    ctx.lineTo(x1, y1);
    ctx.lineWidth = this.px(width);
    ctx.strokeStyle = rgba(color, alpha);
    ctx.setLineDash(dash ? dash.map((value) => this.px(value)) : []);
    ctx.stroke();
    ctx.setLineDash([]);
  }

  tag(value: string, x: number, y: number, alpha: number, color?: Rgb, align: CanvasTextAlign = 'left') {
    this.mono(11);
    this.text(value, x, y, color ?? this.palette.muted, alpha, align);
  }

  // A small pill-shaped chip, sized around its label so the minimum screen
  // font size never spills out of it. A label between backticks is a machine
  // value and draws in mono; any other label is prose and draws in sans. The
  // width cap is a screen measure, so a label the chip can hold at one zoom
  // fits at every zoom.
  chip(label: string, x: number, y: number, alpha: number, lit: number, color: Rgb, align: 'left' | 'center' | 'right' = 'left', maxW = this.px(290)) {
    if (alpha <= 0.01) return 0;
    const { ctx, palette } = this;
    const value = this.face(label, 10.5, 500);
    const w = Math.max(this.px(24), Math.min(ctx.measureText(value).width + this.px(24), maxW));
    const h = this.px(22);
    const left = align === 'center' ? x - w / 2 : align === 'right' ? x - w : x;
    this.roundRect(left, y - h / 2, w, h, h / 2);
    ctx.fillStyle = rgba(mixRgb(palette.background, color, 0.12 + lit * 0.18), alpha);
    ctx.fill();
    ctx.lineWidth = this.px(1);
    ctx.strokeStyle = rgba(color, alpha * (0.3 + lit * 0.6));
    ctx.stroke();
    this.text(value, left + this.px(12), y, mixRgb(palette.muted, palette.ink, 0.4 + lit * 0.6), alpha);
    return w;
  }

  // Sets the font for a label and returns the label without its backticks:
  // mono for a machine value, sans one step up for prose (mono renders one
  // size down from adjacent sans).
  face(label: string, size: number, weight: number) {
    if (label.includes('`')) {
      this.mono(size, weight);
      return label.replace(/`/g, '');
    }
    this.sans(size + 0.5, weight);
    return label;
  }

  // Width of a label in the current font, for laying tags out beside titles.
  measure(value: string) {
    return this.ctx.measureText(value).width;
  }
}

export function drawBackdrop(ctx: CanvasRenderingContext2D, options: FrameOptions, camera: Camera, palette: Palette) {
  const { width, height, dpr } = options;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.fillStyle = rgb(palette.background);
  ctx.fillRect(0, 0, width, height);

  // Two dot fields at different parallax depths give the travel a floor.
  const layers = [
    { spacing: 56, parallax: 0.22, radius: 0.9, alpha: 0.16 },
    { spacing: 168, parallax: 0.5, radius: 1.6, alpha: 0.2 },
  ];
  ctx.fillStyle = rgba(palette.ink, 1);
  for (const layer of layers) {
    const spacing = layer.spacing;
    const offsetX = (((-camera.x * camera.zoom * layer.parallax) % spacing) + spacing) % spacing;
    const offsetY = (((-camera.y * camera.zoom * layer.parallax) % spacing) + spacing) % spacing;
    // The wash is a light, saturated ground, so its dots step back by half.
    ctx.globalAlpha = layer.alpha * (1 - palette.washMix * 0.5);
    ctx.beginPath();
    for (let x = offsetX - spacing; x < width + spacing; x += spacing) {
      for (let y = offsetY - spacing; y < height + spacing; y += spacing) {
        ctx.moveTo(x + layer.radius, y);
        ctx.arc(x, y, layer.radius, 0, Math.PI * 2);
      }
    }
    ctx.fill();
  }
  ctx.globalAlpha = 1;

  // Soft vignette keeps the copy columns calm.
  const vignette = ctx.createRadialGradient(
    width * camera.anchorX,
    height * camera.anchorY,
    Math.min(width, height) * 0.25,
    width * 0.5,
    height * 0.5,
    Math.max(width, height) * 0.85,
  );
  vignette.addColorStop(0, rgba(palette.background, 0));
  vignette.addColorStop(1, rgba(palette.background, 0.7));
  ctx.fillStyle = vignette;
  ctx.fillRect(0, 0, width, height);
}
