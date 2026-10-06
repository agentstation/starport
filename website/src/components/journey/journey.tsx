'use client';

import Image from 'next/image';
import Link from 'next/link';
import { type CSSProperties, Fragment, useEffect, useRef, useState } from 'react';

import { Mascot } from '@/components/mascot';
import { Code, Table } from '@/components/splash/code-block';
import { Inline } from '@/components/splash/inline';
import { InstallTabs } from '@/components/splash/install-tabs';
import { GITHUB_URL } from '@/lib/layout.shared';
import { COMPOSE_METHOD, type CodeBlock, INSTALL_METHODS, type InstallMethod } from '@/lib/splash-facts';

import {
  type Chapter,
  type Viewport,
  COPY_EDGE,
  actFor,
  actSpans,
  chapterIndexFor,
  chapterRest,
  chapterStop,
  chapters,
  clamp,
  interpolateCamera,
  mix,
  range,
  requestStateFor,
  requestStates,
  smoothstep,
  viewportFor,
  visibilityWindow,
} from './timeline';
import { drawFrame, resolveFonts, stillAspect } from './world';

// Reduced motion, or a viewport too short to stage the scene, gets the static
// storyboard: the same world, drawn once per chapter. A phone stacks copy above
// the world, so it needs more height than a desktop stage. Keep in step with
// the same query in `src/app/(home)/journey.css`.
const STATIC_QUERY = '(prefers-reduced-motion: reduce), (max-height: 620px), (max-width: 759px) and (max-height: 779px)';
// Each still shows its chapter where the chapter's scene has played: the
// chapter's own stop, and the end of the journey for the last one.
const STILL_PROGRESS = chapters.map((chapter, index) => (index === chapters.length - 1 ? 0.99 : chapter.end - COPY_EDGE));

// The final beat closes the journey with the install tabs. Its first tab is
// the Compose block of the deploy chapter, so the tabs hold every way to run
// Starport that the README gives.
const FINAL_METHODS: InstallMethod[] = chapters[chapters.length - 1].facts.visuals
  .filter((visual): visual is CodeBlock => visual.kind === 'code')
  .map((block) => ({ ...COMPOSE_METHOD, command: block.text }))
  .concat(INSTALL_METHODS);

// The machine values in a request state are code spans in the facts. The rail
// caption is mono already, so it shows the plain text.
const plain = (text: string) => text.replace(/`/g, '');

function easeInOutCubic(t: number) {
  return t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
}

// The scroll driver. On a fine pointer the wheel is taken over. Inside a
// chapter's rest each notch moves a target by this much of its native
// distance, and the page follows the target as a critically damped spring
// with this angular frequency (per second). The spring keeps the velocity
// continuous across notches, so the beats flow under the scroll instead of
// pulsing with each notch, and a small input makes a small, smooth move.
const WHEEL_GAIN = 0.65;
const GLIDE_OMEGA = 11;
// A chapter holds at the edge of its rest. The gesture that brought the reader
// to the edge does not push through it. A later gesture that pushes this far
// (in native wheel pixels, within this window) carries the reader on to the
// next chapter; a gentle nudge does not.
const PUSH_THRESHOLD = 30;
const PUSH_WINDOW = 800;
// And a push counts only once the edge has held this long.
const EDGE_HOLD = 80;
// A new wheel gesture starts after this long with no wheel input, when the
// direction flips, or when the deltas rise again by this ratio: a trackpad's
// momentum tail only decays, so a rise is a new swipe, even inside the tail.
const GESTURE_GAP = 160;
const GESTURE_RISE = 1.5;
// The carry between two chapters: an eased travel that takes about a second,
// and up to half a second more when the camera has far to go. The camera
// moves only in the gap between the chapters, while the copy is off stage,
// so the carry spends most of its time there: the gap weighs this much more
// than the copy's fade at either end. The wheel stays quiet until the gesture
// that started the carry has ended.
const CARRY_DURATION = 1000;
const CARRY_EXTRA = 500;
const GAP_WEIGHT = 8;
// A scroll the driver did not make (scrollbar, keyboard, touch) has ended
// when this long passes with no further movement.
const REST_DEBOUNCE = 140;
// How long a reader rests on a chapter before the prompt offers the next one.
const NUDGE_DELAY = 1800;
// Progress per millisecond that reads as a full gust on the rail mark: a
// chapter's span in a little over half a second, the pace of a chapter drive.
// A Starport chapter spans 0.1 of the journey (Nimbus: 0.05).
const FULL_GUST = 0.1 / 550;
// Tooling that sets the scroll position itself puts this attribute on <html>,
// and the driver leaves the scroll alone.
const HOLD_ATTRIBUTE = 'data-journey-hold';
// A beat never shrinks below this share of its size to fit the stage.
const MIN_FIT = 0.62;

function scrollsWithin(target: EventTarget | null, deltaY: number, stopAt: HTMLElement) {
  let node = target instanceof Element ? target : null;
  while (node && node !== stopAt && node !== document.body) {
    const { overflowY } = getComputedStyle(node);
    if ((overflowY === 'auto' || overflowY === 'scroll') && node.scrollHeight > node.clientHeight) {
      if (deltaY > 0 ? node.scrollTop + node.clientHeight < node.scrollHeight - 1 : node.scrollTop > 0) return true;
    }
    node = node.parentElement;
  }
  return false;
}

// The band of the stage that a beat may fill, in stage pixels: below the
// nav, and above the rail and the scroll prompt. A phone stacks the copy
// above the world, so its band is the upper half of the stage. The final
// beat also clears the afterword above the rail.
function beatBand(viewport: Viewport, height: number, final: boolean, afterword: number) {
  if (viewport === 'compact') {
    const bottom = final ? afterword + 92 : height * 0.44;
    return { top: 72, bottom, centred: false };
  }
  const top = viewport === 'wide' ? 104 : 88;
  const bottom = final ? afterword + 116 : 132;
  return { top, bottom, centred: true };
}

type JourneyHandles = {
  section: HTMLElement;
  stage: HTMLElement;
  canvas: HTMLCanvasElement;
  percent: HTMLElement | null;
  track: HTMLElement;
  playhead: HTMLElement;
  onChapter: (index: number) => void;
  onState: (label: string) => void;
  // The chapter to nudge the reader on to once they have rested on one, or
  // -1 while they are moving.
  onNudge: (index: number) => void;
};

// The scrolling journey. Scroll position is the playhead; the scene follows it
// with framerate-independent smoothing. The scroll driver below gives the
// scroll its weight and rests it on a chapter. Every DOM write is guarded so
// an idle frame writes nothing.
function mountJourney(handles: JourneyHandles) {
  const { section, stage, canvas, track, playhead } = handles;
  const ctx = canvas.getContext('2d', { alpha: false });
  if (!ctx) return () => {};

  const beats = Array.from(section.querySelectorAll<HTMLElement>('[data-beat]'));
  // No beat has a state yet, so the first pass sets every beat. A beat that
  // is off stage is inert: its links and code blocks must not take focus
  // while they are invisible.
  const beatState = beats.map(() => ({ opacity: -1, shift: 0, live: null as boolean | null }));
  const finalIndex = beats.length - 1;
  const finalBeat = beats[finalIndex];
  const colophon = stage.querySelector<HTMLElement>('.afterword');
  const vars = new Map<string, string>();
  const setVar = (name: string, value: string) => {
    if (vars.get(name) === value) return;
    vars.set(name, value);
    stage.style.setProperty(name, value);
  };

  let width = 0;
  let height = 0;
  let dpr = 1;
  let viewport: Viewport = 'wide';
  let sectionTop = 0;
  let distance = 1;

  let target = 0;
  let progress = 0;
  let lastTime = 0;
  let frameId = 0;
  let running = false;
  let inView = true;
  let lastDraw = 0;
  let driving = false;
  let travelId = 0;
  let gliding = false;
  let glideTo = 0;
  let glideAt = 0;
  let glideVelocity = 0;
  let expectedY = -1;
  let lastY = window.scrollY;
  let direction = 1;
  let touching = false;
  let restTimer = 0;
  let wheelAt = -Infinity;
  let lastDelta = 0;
  let gesture = 0;
  let edgeGesture = -1;
  let edgeAt = -Infinity;
  let push = 0;
  let pushAt = -Infinity;
  let pushDirection = 0;
  let locked = false;
  let scrubbing = false;
  let scrubPointer = -1;
  let scrubShift = 0;
  let chapter = -1;
  let nudge = -1;
  let idleAt = 0;
  let stateLabel = '';
  let percentText = '';
  let lastProgress = -1;
  let lastProgressAt = 0;
  let gust = 0;

  const readScroll = () => {
    target = clamp((window.scrollY - sectionTop) / distance);
  };

  // The stage has one night ground, so the frame writes no palette (Nimbus
  // writes its moving palette here). It writes the progress that the rail,
  // the act fill, and the scroll hint read, and the chapter and the request
  // state for the nav and the rail caption.
  const applyChrome = () => {
    setVar('--journey-progress', progress.toFixed(4));
    setVar('--scroll-hint-opacity', (1 - smoothstep(range(progress, 0, 0.06))).toFixed(3));

    // The wind: the mark on the rail is blown along by the journey's own
    // speed, signed forward. A chapter's drive is a full gust; the lean
    // eases in over a few frames and dies with the scroll.
    const now = performance.now();
    if (lastProgress >= 0) {
      const speed = (progress - lastProgress) / Math.max(1, now - lastProgressAt);
      gust += (clamp(speed / FULL_GUST, -1, 1) - gust) * 0.12;
      if (Math.abs(gust) < 0.002) gust = 0;
    }
    lastProgress = progress;
    lastProgressAt = now;
    setVar('--rail-gust', gust.toFixed(3));

    const percent = `${String(Math.round(progress * 100)).padStart(3, '0')}%`;
    if (percent !== percentText) {
      percentText = percent;
      if (handles.percent) handles.percent.textContent = percent;
      playhead.setAttribute('aria-valuenow', String(Math.round(progress * 100)));
    }
    const nextChapter = chapterIndexFor(progress);
    if (nextChapter !== chapter) {
      chapter = nextChapter;
      handles.onChapter(chapter);
    }
    const nextState = requestStateFor(progress);
    if (nextState !== stateLabel) {
      stateLabel = nextState;
      handles.onState(stateLabel);
    }
  };

  const applyBeats = () => {
    beats.forEach((node, index) => {
      const beat = chapters[index];
      // The first beat is already on stage at the top; the last stays on stage
      // at the bottom of the scroll.
      const windowStart = index === 0 ? -1 : beat.start;
      const windowEnd = index === finalIndex ? 2 : beat.end;
      const alpha = visibilityWindow(progress, windowStart, windowEnd, COPY_EDGE);
      const phase = clamp((progress - beat.start) / (beat.end - beat.start));
      const shift = mix(26, -26, phase) * (1 - alpha);
      const state = beatState[index];
      if (Math.abs(alpha - state.opacity) > 0.004 || Math.abs(shift - state.shift) > 0.4) {
        state.opacity = alpha;
        state.shift = shift;
        node.style.opacity = alpha.toFixed(3);
        node.style.transform = `translate3d(0, ${shift.toFixed(1)}px, 0)`;
        if (node === finalBeat && colophon) colophon.style.opacity = alpha.toFixed(3);
      }
      const live = alpha > 0.5;
      if (live !== state.live) {
        state.live = live;
        node.classList.toggle('is-live', live);
        node.inert = !live;
        if (node === finalBeat && colophon) {
          colophon.inert = !live;
          colophon.classList.toggle('is-live', live);
        }
      }
    });
  };

  // Each beat is placed in its band and, when its copy and code are taller
  // than the band, scaled down to fit. The layout height ignores the scale
  // and the driver's shift, so the measure is stable.
  const fitBeats = () => {
    const afterword = colophon?.offsetHeight ?? 0;
    beats.forEach((node, index) => {
      const band = beatBand(viewport, height, index === finalIndex, afterword);
      const room = Math.max(1, height - band.top - band.bottom);
      const fit = clamp(room / Math.max(1, node.offsetHeight), MIN_FIT, 1);
      node.style.setProperty('--fit', fit.toFixed(3));
      node.style.top = `${Math.round(band.centred ? band.top + room / 2 : band.top)}px`;
      node.style.translate = band.centred ? '0 -50%' : '0 0';
    });
  };

  const draw = (now: number) => {
    drawFrame(ctx, {
      width,
      height,
      dpr,
      progress,
      time: now,
      viewport,
      ambient: true,
    });
    applyChrome();
    applyBeats();
  };

  const step = (dt: number) => {
    if (gliding) {
      const seconds = dt / 1000;
      const gap = glideTo - glideAt;
      glideVelocity += (GLIDE_OMEGA * GLIDE_OMEGA * gap - 2 * GLIDE_OMEGA * glideVelocity) * seconds;
      glideAt += glideVelocity * seconds;
      if (Math.abs(glideTo - glideAt) < 0.5 && Math.abs(glideVelocity) < 20) {
        glideAt = glideTo;
        glideVelocity = 0;
        gliding = false;
      }
      setScroll(glideAt);
    }
    // The scroll is already smooth while the driver moves it; the scene
    // follows it closely. A native scroll gets the fuller smoothing.
    const tau = scrubbing ? 30 : gliding || driving ? 40 : viewport === 'compact' ? 70 : 105;
    progress += (target - progress) * (1 - Math.exp(-dt / tau));
    if (Math.abs(target - progress) < 0.0003) progress = target;
  };

  const stop = () => {
    running = false;
    cancelAnimationFrame(frameId);
  };

  const tick = (now: number) => {
    frameId = requestAnimationFrame(tick);
    const dt = clamp(now - lastTime, 1, 64);
    lastTime = now;
    step(dt);
    // Settled: the scene still runs (traffic rides the lanes), at half
    // rate to spare the battery. The loop rests only when the stage leaves
    // the view or the tab hides.
    const settled = progress === target && !driving && !gliding && !scrubbing;
    // A reader who has rested on a chapter for a moment is offered the next
    // one, so the end of a scene never reads as the end of the page. Any
    // movement takes the offer away again; the last chapter has its own
    // calls to action.
    const rest = chapter >= 0 && chapter < finalIndex ? chapterRest(chapter) : null;
    const resting = settled && !touching && !held() && rest !== null && progress >= rest.from - 0.0001 && progress <= rest.to + 0.0001;
    if (!resting) idleAt = now;
    const nextNudge = resting && now - idleAt > NUDGE_DELAY ? chapter + 1 : -1;
    if (nextNudge !== nudge) {
      nudge = nextNudge;
      handles.onNudge(nudge);
    }
    if (settled && now - lastDraw < 28) return;
    lastDraw = now;
    draw(now);
  };

  const start = () => {
    if (running || !inView || document.hidden) return;
    running = true;
    lastTime = performance.now();
    frameId = requestAnimationFrame(tick);
  };

  const measure = () => {
    width = stage.clientWidth;
    height = stage.clientHeight;
    viewport = viewportFor(width);
    // Bound the backing store: at most ~4.2M device pixels, never above 2x.
    dpr = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(4_200_000 / Math.max(1, width * height)));
    canvas.width = Math.round(width * dpr);
    canvas.height = Math.round(height * dpr);
    sectionTop = section.getBoundingClientRect().top + window.scrollY;
    distance = Math.max(1, section.offsetHeight - height);
    fitBeats();
    readScroll();
    if (!running) {
      progress = target;
      draw(performance.now());
    }
  };

  const root = document.documentElement;
  const finePointer = window.matchMedia('(pointer: fine)').matches;
  const held = () => root.hasAttribute(HOLD_ATTRIBUTE);
  const limit = () => Math.max(0, root.scrollHeight - window.innerHeight);
  const scrollFor = (progress: number) => Math.round(sectionTop + progress * distance);

  // A chapter's rest in scroll pixels. The first chapter's rest starts where
  // the stage does, below the hero; the last chapter's reaches to the end
  // of the page, which is the journey's last stop: the afterword is on the
  // stage, so nothing scrolls below it. Whole pixels, so that a position the
  // driver has set lands inside the rest and not a fraction outside it.
  const restBounds = (index: number) => {
    const rest = chapterRest(index);
    return {
      index,
      from: scrollFor(rest.from),
      to: index === chapters.length - 1 ? limit() : scrollFor(rest.to),
    };
  };

  // The hero above the stage is an ordinary page: the wheel is the
  // browser's there, and a scroll that ends there is left where it ended.
  const aboveStage = (y: number) => y < sectionTop - 1;

  // The rest that holds a scroll position, or null in a gap.
  const restAround = (y: number) => {
    for (let index = 0; index < chapters.length; index += 1) {
      const rest = restBounds(index);
      if (y >= rest.from - 1 && y <= rest.to + 1) return rest;
    }
    return null;
  };

  // From a gap, the rest the journey carries the reader to: the next
  // chapter's arrival going forward, the previous chapter's finished scene
  // going back. Null inside a rest.
  const carryFrom = (y: number, heading: number) => {
    if (aboveStage(y)) return null;
    for (let index = 0; index < chapters.length; index += 1) {
      const rest = restBounds(index);
      if (y >= rest.from - 1 && y <= rest.to + 1) return null;
      if (y < rest.from) return heading < 0 ? restBounds(index - 1) : rest;
    }
    return null;
  };

  // From a chapter's rest, the rest the journey carries the reader to when
  // they push past its edge. Null at either end of the journey.
  const restBeyond = (index: number, heading: number) => {
    const next = index + (heading < 0 ? -1 : 1);
    if (next < 0 || next >= chapters.length) return null;
    return restBounds(next);
  };

  // Every scroll this code makes is expected, so the scroll listener can tell
  // it from a scroll the reader made with the scrollbar, the keyboard, or a
  // touch.
  const setScroll = (y: number) => {
    expectedY = y;
    window.scrollTo(0, y);
    readScroll();
  };

  // Start or retarget the glide toward a scroll position inside a rest.
  const glide = (to: number) => {
    if (!gliding) {
      glideAt = window.scrollY;
      glideVelocity = 0;
    }
    glideTo = to;
    gliding = true;
    start();
  };

  // An eased scroll that the scene follows, so the browser's smooth scrolling
  // never fights the scene's smoothing. Chapter travel and the carry between
  // chapters both use it.
  const drive = (to: number, duration: number, shape: (t: number) => number = easeInOutCubic) => {
    const from = window.scrollY;
    const delta = to - from;
    interrupt();
    if (Math.abs(delta) < 1) return;
    const startedAt = performance.now();
    driving = true;
    direction = delta > 0 ? 1 : -1;
    const stepDrive = (now: number) => {
      const t = clamp((now - startedAt) / duration);
      setScroll(from + delta * shape(t));
      if (t < 1) {
        travelId = requestAnimationFrame(stepDrive);
      } else {
        driving = false;
      }
    };
    travelId = requestAnimationFrame(stepDrive);
    start();
  };

  // Carry the reader to a rest: the eased travel, with the wheel locked until
  // the gesture that started it has ended, so its momentum does not run on
  // into the chapter that arrives.
  const carryTo = (rest: { index: number; from: number; to: number }) => {
    locked = true;
    push = 0;
    const startY = window.scrollY;
    const heading = rest.from > startY ? 1 : -1;
    const endY = heading > 0 ? rest.from : rest.to;
    const startP = clamp((startY - sectionTop) / distance);
    const endP = clamp((endY - sectionTop) / distance);
    const span = endP - startP;
    if (Math.abs(span) < 1e-6) {
      drive(endY, CARRY_DURATION);
      return;
    }
    // The gap between the two chapters, as fractions of the carry.
    const before = chapters[heading > 0 ? rest.index - 1 : rest.index];
    const after = chapters[heading > 0 ? rest.index : rest.index + 1];
    const gapIn = clamp(((heading > 0 ? before.end : after.start) - startP) / span);
    const gapOut = clamp(((heading > 0 ? after.start : before.end) - startP) / span);
    // Time per unit of travel, and its running sum, so that the carry can be
    // read back from time to position.
    const weight = (u: number) => 1 + (GAP_WEIGHT - 1) * smoothstep(range(u, gapIn - 0.06, gapIn + 0.06)) * (1 - smoothstep(range(u, gapOut - 0.06, gapOut + 0.06)));
    const steps = 96;
    const sums = [0];
    for (let i = 1; i <= steps; i += 1) sums.push(sums[i - 1] + weight((i - 0.5) / steps));
    const total = sums[steps];
    const position = (eased: number) => {
      const wanted = eased * total;
      let i = 1;
      while (i < steps && sums[i] < wanted) i += 1;
      return (i - 1 + (wanted - sums[i - 1]) / (sums[i] - sums[i - 1])) / steps;
    };
    // The camera's travel between the two rests, on screen: a long shift or
    // a big change of zoom earns the carry more time.
    const cameraA = interpolateCamera(startP, viewport);
    const cameraB = interpolateCamera(endP, viewport);
    const shift = ((Math.abs(cameraB.x - cameraA.x) + Math.abs(cameraB.y - cameraA.y)) * Math.min(cameraA.zoom, cameraB.zoom)) / 600;
    const zoomed = Math.abs(Math.log(cameraB.zoom / cameraA.zoom)) / 1.5;
    const duration = CARRY_DURATION + CARRY_EXTRA * clamp(shift + zoomed);
    drive(endY, duration, (t) => position(easeInOutCubic(t)));
  };

  // At rest in a gap between two chapters: carry the reader to the chapter.
  const carry = () => {
    if (held() || driving || gliding) return;
    const rest = carryFrom(window.scrollY, direction);
    if (rest === null) return;
    carryTo(rest);
  };

  const onWheel = (event: WheelEvent) => {
    if (!finePointer || held() || event.ctrlKey || event.metaKey || event.deltaY === 0) return;
    if (scrollsWithin(event.target, event.deltaY, section)) return;
    // Above the stage, and at the top of the stage heading up, the browser
    // keeps the wheel, so the reader scrolls out to the hero the way they
    // scrolled in.
    if (aboveStage(window.scrollY) || (event.deltaY < 0 && window.scrollY <= sectionTop + 1)) return;
    event.preventDefault();
    // A drag in progress owns the page.
    if (scrubbing) return;
    const now = performance.now();
    const unit = event.deltaMode === 1 ? 32 : event.deltaMode === 2 ? height : 1;
    const delta = event.deltaY * unit;
    const fresh =
      now - wheelAt > GESTURE_GAP ||
      Math.sign(delta) !== Math.sign(lastDelta) ||
      Math.abs(delta) > Math.abs(lastDelta) * GESTURE_RISE + 4;
    wheelAt = now;
    lastDelta = delta;
    if (fresh) gesture += 1;
    // A carry in flight, and the gesture that started it, take the wheel's
    // input without moving. A chapter travel yields to the wheel.
    if (locked) {
      if (driving || !fresh) return;
      locked = false;
    }
    stopTravel();
    direction = delta > 0 ? 1 : -1;
    // A glide in flight carries on from its target; a reversal starts from
    // where the page is now, so it answers at once.
    const from = gliding && Math.sign(glideTo - window.scrollY) === direction ? glideTo : window.scrollY;
    const rest = restAround(from);
    if (rest === null) {
      // In a gap, where only the scrollbar or the keyboard can leave the
      // reader: carry them on now.
      carry();
      return;
    }
    const edge = direction > 0 ? rest.to : rest.from;
    const atEdge = direction > 0 ? from >= edge - 1 : from <= edge + 1;
    if (!atEdge) {
      const to = clamp(from + delta * WHEEL_GAIN, rest.from, rest.to);
      // This gesture reached the edge: it holds there, and does not push on.
      if (to === edge) {
        edgeGesture = gesture;
        edgeAt = now;
      }
      push = 0;
      glide(to);
      return;
    }
    if (gesture === edgeGesture || now - edgeAt < EDGE_HOLD) return;
    if (direction !== pushDirection || now - pushAt > PUSH_WINDOW) push = 0;
    pushDirection = direction;
    pushAt = now;
    push += Math.abs(delta);
    if (push < PUSH_THRESHOLD) return;
    const beyond = restBeyond(rest.index, direction);
    if (beyond === null) return;
    carryTo(beyond);
  };

  const onScroll = () => {
    const y = window.scrollY;
    if (y !== lastY) direction = y > lastY ? 1 : -1;
    lastY = y;
    readScroll();
    start();
    if (Math.abs(y - expectedY) <= 1) {
      expectedY = -1;
      return;
    }
    // The reader moved the page: a glide or a travel in flight yields to
    // them, and when their scroll ends in a gap, the journey carries them to
    // a chapter.
    interrupt();
    window.clearTimeout(restTimer);
    if (!touching) restTimer = window.setTimeout(carry, REST_DEBOUNCE);
  };

  const onTouchStart = () => {
    touching = true;
    interrupt();
  };

  const onTouchEnd = () => {
    touching = false;
    window.clearTimeout(restTimer);
    restTimer = window.setTimeout(carry, REST_DEBOUNCE);
  };

  // Chapter travel from the nav: it lands on the chapter's own stop, and takes
  // longer the further it goes.
  const travel = (index: number) => {
    const to = scrollFor(chapterStop(index));
    const duration = clamp(480 + (Math.abs(to - window.scrollY) / distance) * 1500, 520, 1400);
    locked = false;
    drive(to, duration);
  };

  // The brand leaves the journey for the hero above it, with the same glide,
  // paced by the distance to the top of the page.
  const travelHome = () => {
    const duration = clamp(480 + (window.scrollY / distance) * 1500, 520, 1400);
    locked = false;
    drive(0, duration);
  };

  // Direct manipulation: the reader drags the rail's playhead, and the page
  // follows the pointer. The scene follows the page closely, and the drop
  // lands in a chapter the way a scroll does.
  const railProgress = (event: PointerEvent) => {
    const rect = track.getBoundingClientRect();
    return clamp((event.clientX - rect.left) / Math.max(1, rect.width));
  };

  const scrubTo = (value: number) => {
    const y = scrollFor(value);
    if (y !== window.scrollY) direction = y > window.scrollY ? 1 : -1;
    setScroll(y);
    start();
  };

  const bindScrub = (handle: HTMLElement, read: (event: PointerEvent) => number) => {
    const onDown = (event: PointerEvent) => {
      if (event.button !== 0 || scrubbing || held()) return;
      event.preventDefault();
      handle.setPointerCapture(event.pointerId);
      interrupt();
      window.clearTimeout(restTimer);
      locked = false;
      push = 0;
      scrubbing = true;
      scrubPointer = event.pointerId;
      // The handle stays under the pointer where it was taken, instead of
      // jumping to the pointer.
      scrubShift = target - read(event);
      stage.classList.add('is-scrubbing');
    };
    const onMove = (event: PointerEvent) => {
      if (!scrubbing || event.pointerId !== scrubPointer) return;
      scrubTo(clamp(read(event) + scrubShift));
    };
    const onUp = (event: PointerEvent) => {
      if (!scrubbing || event.pointerId !== scrubPointer) return;
      scrubbing = false;
      scrubPointer = -1;
      stage.classList.remove('is-scrubbing');
      // Dropped in a gap: the journey carries the reader to a chapter. A touch
      // schedules its own carry when it ends.
      if (!touching) carry();
    };
    handle.addEventListener('pointerdown', onDown);
    handle.addEventListener('pointermove', onMove);
    handle.addEventListener('pointerup', onUp);
    handle.addEventListener('pointercancel', onUp);
    handle.addEventListener('lostpointercapture', onUp);
    return () => {
      handle.removeEventListener('pointerdown', onDown);
      handle.removeEventListener('pointermove', onMove);
      handle.removeEventListener('pointerup', onUp);
      handle.removeEventListener('pointercancel', onUp);
      handle.removeEventListener('lostpointercapture', onUp);
    };
  };

  // The playhead is a slider to the keyboard: it steps by chapter.
  const SLIDER_STEPS = new Map([
    ['ArrowRight', 1],
    ['ArrowUp', 1],
    ['ArrowLeft', -1],
    ['ArrowDown', -1],
  ]);
  const onSliderKey = (event: KeyboardEvent) => {
    const last = chapters.length - 1;
    const stepBy = SLIDER_STEPS.get(event.key);
    let index: number;
    if (event.key === 'Home') index = 0;
    else if (event.key === 'End') index = last;
    else if (stepBy !== undefined) index = clamp(chapter + stepBy, 0, last);
    else return;
    event.preventDefault();
    // The window's keydown ends a travel; this one starts it.
    event.stopPropagation();
    travel(index);
  };

  const stopTravel = () => {
    if (!driving) return;
    cancelAnimationFrame(travelId);
    driving = false;
  };

  // Any other input from the reader ends a glide or a travel in flight.
  const interrupt = () => {
    gliding = false;
    stopTravel();
  };

  const onVisibility = () => {
    if (document.hidden) stop();
    else start();
  };

  // The beats change height when the web fonts arrive.
  let mounted = true;
  void document.fonts?.ready.then(() => {
    if (mounted) measure();
  });

  const observer = new IntersectionObserver(
    ([entry]) => {
      inView = entry.isIntersecting;
      if (inView) start();
      else stop();
    },
    { threshold: 0 },
  );
  observer.observe(section);

  const resizeObserver = new ResizeObserver(measure);
  resizeObserver.observe(stage);
  window.addEventListener('scroll', onScroll, { passive: true });
  window.addEventListener('wheel', onWheel, { passive: false });
  window.addEventListener('touchstart', onTouchStart, { passive: true });
  window.addEventListener('touchend', onTouchEnd, { passive: true });
  window.addEventListener('touchcancel', onTouchEnd, { passive: true });
  window.addEventListener('keydown', interrupt);
  document.addEventListener('visibilitychange', onVisibility);
  const unbindPlayhead = bindScrub(playhead, railProgress);
  playhead.addEventListener('keydown', onSliderKey);

  measure();
  start();
  travelRegistry.current = travel;
  homeRegistry.current = travelHome;

  return () => {
    mounted = false;
    stop();
    cancelAnimationFrame(travelId);
    window.clearTimeout(restTimer);
    observer.disconnect();
    resizeObserver.disconnect();
    window.removeEventListener('scroll', onScroll);
    window.removeEventListener('wheel', onWheel);
    window.removeEventListener('touchstart', onTouchStart);
    window.removeEventListener('touchend', onTouchEnd);
    window.removeEventListener('touchcancel', onTouchEnd);
    window.removeEventListener('keydown', interrupt);
    document.removeEventListener('visibilitychange', onVisibility);
    unbindPlayhead();
    playhead.removeEventListener('keydown', onSliderKey);
    stage.classList.remove('is-scrubbing');
    beats.forEach((node) => {
      node.style.opacity = '';
      node.style.transform = '';
      node.style.top = '';
      node.style.translate = '';
      node.style.removeProperty('--fit');
      node.classList.remove('is-live');
      node.inert = false;
    });
    if (colophon) {
      colophon.style.opacity = '';
      colophon.inert = false;
      colophon.classList.remove('is-live');
    }
    travelRegistry.current = travelStatic;
    homeRegistry.current = travelHomeStatic;
  };
}

// Static storyboard: one still from the same world per chapter.
function mountStoryboard(section: HTMLElement) {
  const stills = Array.from(section.querySelectorAll<HTMLCanvasElement>('canvas.still'));
  const draw = () => {
    stills.forEach((canvas, index) => {
      const width = canvas.clientWidth;
      const height = canvas.clientHeight;
      const ctx = canvas.getContext('2d', { alpha: false });
      if (!width || !height || !ctx) return;
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      canvas.width = Math.round(width * dpr);
      canvas.height = Math.round(height * dpr);
      drawFrame(ctx, {
        width,
        height,
        dpr,
        progress: STILL_PROGRESS[index] ?? 0.5,
        time: 0,
        viewport: 'compact',
        ambient: false,
        centered: true,
      });
    });
  };
  draw();
  // The canvas labels take the web fonts once they arrive.
  let mounted = true;
  void document.fonts?.ready.then(() => {
    if (mounted) draw();
  });
  const resizeObserver = new ResizeObserver(draw);
  resizeObserver.observe(section);
  travelRegistry.current = travelStatic;
  return () => {
    mounted = false;
    resizeObserver.disconnect();
  };
}

function travelStatic(index: number) {
  const node = document.getElementById(`chapter-${chapters[index].id}`);
  if (!node) return;
  // The chapter nav is sticky above the storyboard.
  const top = node.getBoundingClientRect().top + window.scrollY - 88;
  window.scrollTo({ top, behavior: 'auto' });
}

const travelRegistry: { current: (index: number) => void } = { current: travelStatic };

// The way back to the hero at the top of the page, where the journey began.
function travelHomeStatic() {
  window.scrollTo({ top: 0, behavior: 'auto' });
}

const homeRegistry: { current: () => void } = { current: travelHomeStatic };

// Chapter travel for the page around the journey: the hero's scroll prompt
// lands on the first chapter through the same drive the nav uses.
export function travelTo(index: number) {
  travelRegistry.current(index);
}

// The second skip link: the afterword sits on the sticky stage, so an anchor
// jump would land at the top of the stage. The link travels to the end card
// instead.
export function SkipToEnd() {
  return (
    <a
      className="skip skip-end"
      href="#afterword"
      onClick={(event) => {
        event.preventDefault();
        travelRegistry.current(chapters.length - 1);
      }}
    >
      Skip to the end
    </a>
  );
}

type Poster = { width: number; height: number };

// Demo is the README recording. The preview is static, as in the README. The
// link opens the 38-second recording, so the page plays no animation on its
// own. On the animated stage the world beside the copy is the picture, so
// the stage shows the caption and its links only.
function Demo({ poster }: { poster: Poster }) {
  return (
    <figure className="demo">
      <a href="/demo/first-use.gif">
        <Image
          src="/demo/poster.png"
          width={poster.width}
          height={poster.height}
          alt="A terminal installs Starport, reads the catalog, starts a temporary gateway, and streams an OpenAI answer. Select the preview to play the recording."
        />
      </a>
      <figcaption>
        <a href="/demo/first-use.gif">Watch the 38-second first request</a> or read the{' '}
        <a href={`${GITHUB_URL}/blob/main/docs/assets/first-use-v1.2.0/TRANSCRIPT.md`}>transcript</a>.
      </figcaption>
    </figure>
  );
}

function Beat({ beat, index, final, poster }: { beat: Chapter; index: number; final: boolean; poster: Poster }) {
  const { facts } = beat;
  return (
    <article
      id={`chapter-${beat.id}`}
      data-beat
      aria-labelledby={`chapter-${beat.id}-title`}
      className={`beat beat-${beat.align}${final ? ' beat-final' : ''}`}
      style={{ opacity: index === 0 ? 1 : 0, '--still-aspect': stillAspect(beat.id).toFixed(3) } as CSSProperties}
    >
      <p className="beat-eyebrow">
        <span>
          {String(index + 1).padStart(2, '0')}
          <i aria-hidden="true">·</i>
          {facts.eyebrow}
        </span>
        {facts.status ? <em>{facts.status.badge}</em> : null}
      </p>
      <h2 id={`chapter-${beat.id}-title`}>{facts.claim}</h2>
      <p className="beat-body">
        <Inline text={facts.body} />
      </p>
      {final ? (
        <InstallTabs methods={FINAL_METHODS} />
      ) : (
        <div className="beat-visuals">
          {facts.visuals.map((visual, key) =>
            visual.kind === 'code' ? <Code key={key} block={visual} /> : <Table key={key} block={visual} />,
          )}
          {facts.demo ? <Demo poster={poster} /> : null}
        </div>
      )}
      {facts.status ? (
        <p className="beat-status">
          <span>{facts.status.target}</span> {facts.status.text}
        </p>
      ) : null}
      <p className="proof">
        {facts.chips.map((chip, key) => (
          <Fragment key={chip}>
            {key > 0 ? ' · ' : null}
            <Inline text={chip} />
          </Fragment>
        ))}
      </p>
      {final ? (
        <div className="final-actions">
          <Link href="/docs/start" className="pill primary">
            Get started
          </Link>
          <Link href="/docs" className="pill">
            Read the docs
          </Link>
        </div>
      ) : null}
      <canvas className="still" aria-hidden="true" />
    </article>
  );
}

export function Journey({ poster, build }: { poster: Poster; build: { release: string; starmap: string } }) {
  const journeyRef = useRef<HTMLElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const percentRef = useRef<HTMLElement>(null);
  const trackRef = useRef<HTMLElement>(null);
  const playheadRef = useRef<HTMLSpanElement>(null);
  const [activeChapter, setActiveChapter] = useState(0);
  const [requestState, setRequestState] = useState(requestStates[0].label);
  const [nudge, setNudge] = useState(-1);

  useEffect(() => {
    const section = journeyRef.current;
    const stage = stageRef.current;
    const canvas = canvasRef.current;
    const track = trackRef.current;
    const playhead = playheadRef.current;
    if (!section || !stage || !canvas || !track || !playhead) return;
    const media = window.matchMedia(STATIC_QUERY);
    let cleanup: (() => void) | undefined;
    const boot = () => {
      cleanup?.();
      resolveFonts(section.closest('.splash') ?? document.body);
      cleanup = media.matches
        ? mountStoryboard(section)
        : mountJourney({
            section,
            stage,
            canvas,
            percent: percentRef.current,
            track,
            playhead,
            onChapter: setActiveChapter,
            onState: setRequestState,
            onNudge: setNudge,
          });
    };
    boot();
    media.addEventListener('change', boot);
    return () => {
      media.removeEventListener('change', boot);
      cleanup?.();
    };
  }, []);

  const lastIndex = chapters.length - 1;
  const activeAct = actFor(chapters[activeChapter]);
  const spans = actSpans();

  return (
    <section className="journey" id="journey" ref={journeyRef} aria-label="Request path through Starport">
      <div className="journey-stage" ref={stageRef}>
        <canvas className="world" ref={canvasRef} aria-hidden="true" />

        <header className="nav-shell">
          <button type="button" className="brand" onClick={() => homeRegistry.current()} aria-label="Starport. Back to the top of the page.">
            {/* The mark is decoration beside the name, so it carries no title. */}
            <Mascot className="brand-mark" />
            <span>Starport</span>
          </button>
          <nav className="chapter-nav" aria-label="Chapters">
            {spans.map(({ act, start, end }) => {
              const members = chapters.map((beat, index) => ({ beat, index })).filter(({ beat }) => beat.act === act.id);
              const current = activeAct?.id === act.id;
              return (
                <div key={act.id} className="act-group" data-current={current ? 'true' : undefined} style={{ '--act-start': start, '--act-end': end } as CSSProperties}>
                  <button type="button" className="act-button" onClick={() => travelRegistry.current(members[0].index)} aria-current={current ? 'step' : undefined}>
                    {act.label}
                  </button>
                  {members.length > 1 ? (
                    <div className="act-chapters">
                      <div className="act-chapters-row">
                        {members.map(({ beat, index }) => (
                          <button key={beat.id} type="button" onClick={() => travelRegistry.current(index)} aria-current={index === activeChapter ? 'step' : undefined}>
                            {beat.facts.eyebrow}
                          </button>
                        ))}
                      </div>
                    </div>
                  ) : null}
                  <span className="act-fill" aria-hidden="true" />
                </div>
              );
            })}
          </nav>
          <Link className="run-link" href="/docs/start">
            Run locally
          </Link>
        </header>

        <div className="beats">
          {chapters.map((beat, index) => (
            <Beat key={beat.id} beat={beat} index={index} final={index === lastIndex} poster={poster} />
          ))}
        </div>

        <div className="journey-status">
          <nav className="progress-track" aria-label="Progress" ref={trackRef}>
            <span className="played" aria-hidden="true" />
            {spans.map(({ act, start, end }) => (
              <span
                key={act.id}
                className="rail-act"
                aria-hidden="true"
                style={{ left: `${(start * 100).toFixed(2)}%`, width: `${((end - start) * 100).toFixed(2)}%` }}
              >
                {act.rail ?? act.label}
              </span>
            ))}
            {chapters.map((beat, index) => (
              <button
                key={beat.id}
                type="button"
                style={{ left: `${(beat.start * 100).toFixed(2)}%` }}
                onClick={() => travelRegistry.current(index)}
                aria-current={index === activeChapter ? 'step' : undefined}
                aria-label={`Chapter ${index + 1}: ${beat.facts.eyebrow}`}
              />
            ))}
            <span
              className="playhead"
              ref={playheadRef}
              role="slider"
              tabIndex={0}
              aria-label="Position"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={0}
              aria-valuetext={chapters[activeChapter].facts.eyebrow}
            >
              <span className="rail-gust" aria-hidden="true">
                {/* The playhead is the mascot, small: at this size the face
                    needs the heavy line. It leans with the gust and bobs on
                    the drift; the eyes keep their own blink underneath. */}
                <Mascot className="rail-mark" small />
              </span>
            </span>
            {/* Where the journey is, as a caption that rides under the
                playhead. It lives inside the rail, so its length never
                changes the rail's. */}
            <div className="rail-caption">
              <span aria-live="polite" aria-atomic="true">
                {chapters[activeChapter].facts.eyebrow}
              </span>
              <span className="state">{plain(requestState)}</span>
            </div>
          </nav>
          {/* The percent closes the row, inline with the rail line. Its
              digits are fixed width, so the rail's length holds. */}
          <span className="telemetry">
            request <b ref={percentRef}>000%</b>
          </span>
        </div>

        {nudge >= 0 ? (
          <button type="button" className="scroll-prompt is-next" onClick={() => travelRegistry.current(nudge)}>
            <span>Next · {chapters[nudge].facts.eyebrow}</span>
            <i aria-hidden="true">
              <svg viewBox="0 0 16 16" width="14" height="14">
                <path d="M3.5 6.5 8 11l4.5-4.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </i>
          </button>
        ) : (
          <div className="scroll-prompt" aria-hidden="true">
            <i />
            <span>Scroll to follow the request</span>
          </div>
        )}
        {/* The afterword is the last chapter's own footer: the page ends where
            the journey does, so the end card is never pushed off the stage
            to make room for a footer below it. The driver fades it with the
            final beat; in the storyboard it is an ordinary footer after the
            last card. */}
        <footer className="afterword" id="afterword">
          <nav aria-label="Footer">
            <Link href="/docs">Docs</Link>
            <a href={GITHUB_URL}>GitHub</a>
            <a href={`${GITHUB_URL}/releases`}>Releases</a>
          </nav>
          <p className="build">
            This build: release <code>{build.release}</code> · Starmap <code>{build.starmap}</code>
          </p>
          <p className="fine">AGPLv3 · Starport · one binary inference gateway</p>
        </footer>
      </div>
    </section>
  );
}
