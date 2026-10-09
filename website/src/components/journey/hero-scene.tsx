'use client';

import { type CSSProperties, type PointerEvent as ReactPointerEvent, useCallback, useEffect, useRef, useState } from 'react';

import { Inline } from '@/components/splash/inline';
import { WORLD_PARTS, type WorldPartId } from '@/lib/splash-facts';

import { travelTo } from './journey';
import { night } from './scene';
import { type Viewport, chapterStop, chapters } from './timeline';
import { type Part, drawFrame, parts, resolveFonts } from './world';

// The hero frames the whole world at the server stop: every part of the
// request path is on the host, so the diagram the reader lands on is the
// same world they scroll into. The hero sits on the night ground of the page
// top, so it draws the server stop on the night palette.
const HERO_PROGRESS = chapterStop(chapters.findIndex((chapter) => chapter.id === 'server'));
// The frame: the host and every part on it, centred and zoomed to fit the
// still. The world span is the drawing's extent plus air.
const HERO_FRAME = { x: 1025, y: 60, w: 2480, h: 1780 };
// A phone frame crops the air around the host: text keeps its screen-pixel
// floor, so the box needs the width.
const HERO_FRAME_COMPACT = { x: 1025, y: 40, w: 2380, h: 1500 };
const COMPACT_WIDTH = 520;

// The camera for a still of the given size: the frame it shows and the zoom
// that fits it. The scene draws with it, and the pointer maps through it.
function heroCamera(width: number, height: number) {
  const compact = width < COMPACT_WIDTH;
  const frame = compact ? HERO_FRAME_COMPACT : HERO_FRAME;
  const viewport: Viewport = compact ? 'compact' : 'wide';
  return { frame, viewport, zoom: Math.min(width / frame.w, height / frame.h) };
}

const PART_COPY = new Map(WORLD_PARTS.map((part) => [part.id, part]));

// A part under the pointer, with its box in the still's own pixels, and
// where its card goes: under the box, or over it when the still ends first.
// A Starport card holds up to four lines of body copy, so it is taller than
// the Nimbus card (120).
type Hover = { id: WorldPartId; left: number; top: number; width: number; height: number; card: CSSProperties };
const CARD_W = 264;
const CARD_H = 172;
const CARD_GAP = 10;
const EDGE = 8;

function partAt(width: number, height: number, sx: number, sy: number): Hover | null {
  const { frame, zoom } = heroCamera(width, height);
  const wx = frame.x + (sx - width / 2) / zoom;
  const wy = frame.y + (sy - height / 2) / zoom;
  let hit: Part | undefined;
  for (const part of parts(zoom)) {
    if (wx >= part.x0 && wx <= part.x1 && wy >= part.y0 && wy <= part.y1) hit = part;
  }
  if (!hit) return null;
  const left = width / 2 + (hit.x0 - frame.x) * zoom;
  const top = height / 2 + (hit.y0 - frame.y) * zoom;
  const w = (hit.x1 - hit.x0) * zoom;
  const h = (hit.y1 - hit.y0) * zoom;
  const cardLeft = Math.min(Math.max(EDGE, left + w / 2 - CARD_W / 2), Math.max(EDGE, width - CARD_W - EDGE));
  const below = top + h + CARD_GAP + CARD_H <= height;
  // A part as tall as the still (the host) has room on neither side, so its
  // card sits inside the still, under the top edge.
  const above = top - CARD_GAP - CARD_H >= 0;
  const card: CSSProperties = below
    ? { left: cardLeft, top: top + h + CARD_GAP }
    : above
      ? { left: cardLeft, bottom: height - top + CARD_GAP }
      : { left: cardLeft, top: EDGE };
  return { id: hit.id, left, top, width: w, height: h, card };
}

// The scene in the hero: the server stop, drawn as a centred still and kept
// alive at half rate while it is on screen, so the request rides the lanes.
// Reduced motion gets the still alone.
function mountScene(canvas: HTMLCanvasElement) {
  const ctx = canvas.getContext('2d', { alpha: false });
  if (!ctx) return () => {};
  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)');

  let width = 0;
  let height = 0;
  let dpr = 1;
  let frameId = 0;
  let running = false;
  let inView = true;
  let lastDraw = 0;

  const draw = (time: number) => {
    if (!width || !height) return;
    const { frame, viewport, zoom } = heroCamera(width, height);
    drawFrame(ctx, {
      width,
      height,
      dpr,
      progress: HERO_PROGRESS,
      time,
      viewport,
      ambient: !reduced.matches,
      centered: true,
      ground: night(),
      camera: { x: frame.x, y: frame.y, zoom, anchorX: 0.5, anchorY: 0.5 },
    });
  };

  const tick = (now: number) => {
    frameId = requestAnimationFrame(tick);
    if (now - lastDraw < 28) return;
    lastDraw = now;
    draw(now);
  };

  const stop = () => {
    running = false;
    cancelAnimationFrame(frameId);
  };

  const start = () => {
    if (running || !inView || document.hidden || reduced.matches) return;
    running = true;
    frameId = requestAnimationFrame(tick);
  };

  const measure = () => {
    width = canvas.clientWidth;
    height = canvas.clientHeight;
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    canvas.width = Math.round(width * dpr);
    canvas.height = Math.round(height * dpr);
    draw(performance.now());
  };

  const onVisibility = () => {
    if (document.hidden) stop();
    else start();
  };
  const onMotion = () => {
    stop();
    draw(performance.now());
    start();
  };

  // The canvas labels take the web fonts once they arrive.
  let mounted = true;
  void document.fonts?.ready.then(() => {
    if (!mounted) return;
    resolveFonts(canvas.closest('.splash') ?? document.body);
    draw(performance.now());
  });

  const observer = new IntersectionObserver(
    ([entry]) => {
      inView = entry.isIntersecting;
      if (inView) start();
      else stop();
    },
    { threshold: 0 },
  );
  observer.observe(canvas);
  const resizeObserver = new ResizeObserver(measure);
  resizeObserver.observe(canvas);
  document.addEventListener('visibilitychange', onVisibility);
  reduced.addEventListener('change', onMotion);

  measure();
  start();

  return () => {
    mounted = false;
    stop();
    observer.disconnect();
    resizeObserver.disconnect();
    document.removeEventListener('visibilitychange', onVisibility);
    reduced.removeEventListener('change', onMotion);
  };
}

// The still with the pointer over it: the part under the cursor gets an
// outline and a card that names it. Touch gets the same from a tap. The
// journey below restates every card as a chapter, so the still is one image
// to assistive technology.
export function HeroScene() {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [hover, setHover] = useState<Hover | null>(null);
  const current = useRef<WorldPartId | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    resolveFonts(canvas.closest('.splash') ?? document.body);
    return mountScene(canvas);
  }, []);

  const onPointer = useCallback((event: ReactPointerEvent<HTMLElement>) => {
    const box = event.currentTarget.getBoundingClientRect();
    const next = partAt(box.width, box.height, event.clientX - box.left, event.clientY - box.top);
    if ((next?.id ?? null) === current.current) return;
    current.current = next?.id ?? null;
    setHover(next);
  }, []);
  // A finger leaves the moment it lifts, so a tap keeps its card until the
  // next tap lands somewhere else.
  const onLeave = useCallback((event: ReactPointerEvent<HTMLElement>) => {
    if (event.pointerType === 'touch') return;
    current.current = null;
    setHover(null);
  }, []);
  useEffect(() => {
    if (!hover) return;
    const figure = canvasRef.current?.parentElement;
    const onOutside = (event: PointerEvent) => {
      if (figure && event.target instanceof Node && figure.contains(event.target)) return;
      current.current = null;
      setHover(null);
    };
    document.addEventListener('pointerdown', onOutside);
    return () => document.removeEventListener('pointerdown', onOutside);
  }, [hover]);

  const copy = hover ? PART_COPY.get(hover.id) : undefined;
  return (
    <figure
      className="hero-scene"
      role="img"
      aria-label="The request path on your host. Your app calls the Starport listener with a gateway API key. Starport checks the key, plans the route from the accepted catalog generation, calls a provider with a provider inference credential, streams the answer back, and keeps durable state."
      onPointerMove={onPointer}
      onPointerDown={onPointer}
      onPointerLeave={onLeave}
      onPointerCancel={onLeave}
    >
      <canvas ref={canvasRef} aria-hidden="true" />
      {hover && copy ? (
        <>
          <span className="hero-part" aria-hidden="true" style={{ left: hover.left, top: hover.top, width: hover.width, height: hover.height }} />
          <div className="hero-tip" aria-hidden="true" style={hover.card}>
            <strong>{copy.title}</strong>
            <p>
              <Inline text={copy.card} />
            </p>
          </div>
        </>
      ) : null}
    </figure>
  );
}

// The prompt under the hero: it carries the reader to the first chapter
// through the same drive that the chapter nav uses.
export function HeroScroll() {
  return (
    <button type="button" className="hero-scroll" onClick={() => travelTo(0)}>
      <span>Scroll to follow the request</span>
      <i className="hero-scroll-ring" aria-hidden="true">
        <svg viewBox="0 0 16 16" width="18" height="18">
          <path d="M3.5 6.5 8 11l4.5-4.5" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </i>
    </button>
  );
}
