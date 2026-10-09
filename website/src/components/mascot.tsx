import type { CSSProperties, SVGProps } from 'react';

// The Starport mark: the five-point star from the favicon, with a face. The
// body takes `--mark` and the face takes `--mark-ink`. Both hold the gold on
// every ground, so this is the same sticker as the favicon and the app icon
// (`public/favicon.svg` is the fitted idle face of this drawing).
//
// It is a server component in a static export, so motion is CSS: `.mascot-*`
// in `app/global.css` carries the blink and the wink, and the reduced-motion
// block there stops them. The face weight is a prop, because the caller sizes
// the mark with CSS and knows whether it is under about 27px, where the face
// lines fall below a third of a pixel per viewBox unit and need thickening.
export type MascotState = 'idle' | 'working' | 'error' | 'empty' | 'celebrate' | 'wink';

// The states that draw nothing outside the body. Their box can close in on
// the drawing; every other state keeps the accessory room. A new state that
// forgets to list itself here gets the reserved box, which is only wasteful,
// never a clipped accessory.
const FACE_ONLY: ReadonlySet<MascotState> = new Set(['idle', 'wink']);

// The drawing lives in a 120x104 box. The star is centred at (60, 56) with
// an outer radius of 46 and an inner radius of 25, one point up, and a round
// 12-unit stroke in the body colour that fattens it and rounds its points.
// With the stroke the body fills x 10..110 and y 4..99. The room left over is
// the two notches beside the top point, and the accessories go in the right
// one: the thought dots, the drop, the zz, the sparks. Nothing else draws
// there.
const BOX = { x: 0, y: 0, w: 120, h: 104 };
const INK = { x: 10, y: 4, w: 100, h: 96 };

// Fitted, the box closes in on the body with a 2-unit margin all round. A
// fitted mark draws about a sixth wider than a reserved one at the same
// height, which is what a brand slot wants.
const FIT_PAD = 2;
const FIT = {
  x: INK.x - FIT_PAD,
  y: INK.y - FIT_PAD,
  w: INK.w + FIT_PAD * 2,
  h: INK.h + FIT_PAD * 2,
};

// The star's ten vertices, top point first, clockwise.
const STAR = 'M60 10 L74.7 35.8 L103.7 41.8 L83.8 63.7 L87 93.2 L60 81 L33 93.2 L36.2 63.7 L16.3 41.8 L45.3 35.8 Z';

const EYE_L = 49;
const EYE_R = 71;
const EYE_Y = 53;

function Body() {
  return (
    <path
      data-part="body"
      d={STAR}
      fill="var(--mark)"
      stroke="var(--mark)"
      strokeWidth="12"
      strokeLinejoin="round"
    />
  );
}

// Eyes are the only part that can move. `blink` attaches the keyframes; the
// group scales about the eye line so a blink closes the dots in place.
//
// `wink` is the second, slower flourish: the right dot and the closed arc run
// one cycle in counterphase, so the eye swaps to the arc for three quarters of
// a second and back. The arc is only in the DOM while the wink is, so a face
// that cannot wink carries no hidden shape.
function DotEyes({
  ink,
  dx,
  blink,
  wink,
  r,
  w,
}: {
  ink: string;
  dx: number;
  blink: boolean;
  wink: boolean;
  r: number;
  w: number;
}) {
  const style: CSSProperties = { transformOrigin: `60px ${EYE_Y}px` };
  return (
    <g
      data-part="eyes"
      data-blink={blink ? 'true' : undefined}
      data-wink={wink ? 'true' : undefined}
      className={blink ? 'mascot-blink' : undefined}
      style={style}
      fill={ink}
    >
      <circle cx={EYE_L + dx} cy={EYE_Y} r={r} />
      <circle cx={EYE_R + dx} cy={EYE_Y} r={r} className={wink ? 'mascot-wink-open' : undefined} />
      {wink ? (
        <path className="mascot-wink-shut" d={WINK} fill="none" stroke={ink} strokeWidth={w} strokeLinecap="round" />
      ) : null}
    </g>
  );
}

// The held wink: one eye open, the other the same arc the flourish swaps in.
function WinkEyes({ ink, r, w }: { ink: string; r: number; w: number }) {
  return (
    <g data-part="eyes" fill={ink}>
      <circle cx={EYE_L} cy={EYE_Y} r={r} />
      <path d={WINK} fill="none" stroke={ink} strokeWidth={w} strokeLinecap="round" />
    </g>
  );
}

function ClosedEyes({ ink, up, w }: { ink: string; up: boolean; w: number }) {
  const d = up ? 'M43 55 q6 -7 12 0 M65 55 q6 -7 12 0' : 'M43 52 q6 6 12 0 M65 52 q6 6 12 0';
  return <path data-part="eyes" d={d} fill="none" stroke={ink} strokeWidth={w} strokeLinecap="round" />;
}

function CrossEyes({ ink, w }: { ink: string; w: number }) {
  return (
    <path
      data-part="eyes"
      d="M45 49 l8 8 M53 49 l-8 8 M67 49 l8 8 M75 49 l-8 8"
      fill="none"
      stroke={ink}
      strokeWidth={w}
      strokeLinecap="round"
    />
  );
}

function Mouth({ ink, d, w }: { ink: string; d: string; w: number }) {
  return <path data-part="mouth" d={d} fill="none" stroke={ink} strokeWidth={w} strokeLinecap="round" />;
}

// The right eye closed: the same arc `ClosedEyes` draws, on its own.
const WINK = 'M65 55 q6 -7 12 0';
const SMILE = 'M52 62 q8 8 16 0';
const FLAT = 'M55 65 h10';
const WOBBLE = 'M52 66 q4 -4 8 0 t8 0';

// weight is the face line width in viewBox units; the dot radius follows it.
type FaceWeight = { w: number; r: number };

function Face({
  state,
  ink,
  spark,
  blink,
  wink,
  weight,
}: {
  state: MascotState;
  ink: string;
  spark: string;
  blink: boolean;
  wink: boolean;
  weight: FaceWeight;
}) {
  const { w, r } = weight;
  switch (state) {
    case 'idle':
      return (
        <>
          <DotEyes ink={ink} dx={0} blink={blink} wink={wink} r={r} w={w} />
          <Mouth ink={ink} d={SMILE} w={w} />
        </>
      );
    case 'wink':
      return (
        <>
          <WinkEyes ink={ink} r={r} w={w} />
          <Mouth ink={ink} d={SMILE} w={w} />
        </>
      );
    case 'working':
      return (
        <>
          <DotEyes ink={ink} dx={3} blink={blink} wink={false} r={r} w={w} />
          <Mouth ink={ink} d={FLAT} w={w} />
          <g data-part="thinking" fill={spark}>
            <circle cx="84" cy="24" r="2" />
            <circle cx="93" cy="17" r="2.6" />
            <circle cx="103" cy="9" r="3.2" />
          </g>
        </>
      );
    case 'error':
      return (
        <>
          <CrossEyes ink={ink} w={w} />
          <Mouth ink={ink} d={WOBBLE} w={w} />
          <path data-part="drop" d="M96 22 c0 -4 5 -10 5 -10 s5 6 5 10 a5 5 0 0 1 -10 0z" fill={spark} />
        </>
      );
    case 'empty':
      return (
        <>
          <ClosedEyes ink={ink} up={false} w={w} />
          <Mouth ink={ink} d={FLAT} w={w} />
          <g data-part="sleep" fill="none" stroke={spark} strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
            <path d="M90 18 h8 l-8 8 h8" />
            <path d="M102 6 h6 l-6 6 h6" />
          </g>
        </>
      );
    case 'celebrate':
      return (
        <>
          <ClosedEyes ink={ink} up w={w} />
          <path data-part="mouth" d="M50 62 q10 12 20 0 z" fill={ink} />
          <g data-part="sparks" fill={spark}>
            <path d="M100 7 l2 5 5 2 -5 2 -2 5 -2 -5 -5 -2 5 -2z" />
            <path d="M18 16 l1.5 4 4 1.5 -4 1.5 -1.5 4 -1.5 -4 -4 -1.5 4 -1.5z" />
            <path d="M112 58 l1 3 3 1 -3 1 -1 3 -1 -3 -3 -1 3 -1z" />
          </g>
        </>
      );
  }
}

export type MascotProps = SVGProps<SVGSVGElement> & {
  state?: MascotState;
  /** Accessible name. Leave it off when the text beside the mark already says it. */
  title?: string;
  /** Thickens the face for a mark under about 27px tall. */
  small?: boolean;
  /**
   * Keep the accessory room in the box even where the state does not use it.
   * A slot whose state changes needs one box for every state, or the mark
   * jumps when the reading changes. A slot that only ever shows a face leaves
   * this off and gets the larger mark.
   */
  reserveAccessories?: boolean;
};

export function Mascot({ state = 'idle', title, small = false, reserveAccessories = false, ...props }: MascotProps) {
  // Only open dot eyes can blink, and the wink is idle's alone. A mascot with
  // work in flight, an error on screen or nothing to show does not wink at
  // you; a resting one does, rarely enough that it reads as a greeting rather
  // than a tic.
  const blink = state === 'idle' || state === 'working';
  const wink = state === 'idle';
  const ink = 'var(--mark-ink)';
  // The accessories (thought dots, drop, zz, sparks) sit outside the body, so
  // they take the text colour of the surface rather than the mark.
  const spark = 'currentColor';
  const weight: FaceWeight = small ? { w: 5.5, r: 4.6 } : { w: 4, r: 3.7 };
  // The box: fitted to the body by default, reserved (the whole 120x104)
  // whenever the state draws an accessory or the caller asked for one box
  // across state changes. Reserving on the state as well as on the prop is
  // what makes a clipped accessory impossible rather than merely unlikely.
  const view = FACE_ONLY.has(state) && !reserveAccessories ? FIT : BOX;
  return (
    <svg
      viewBox={`${view.x} ${view.y} ${view.w} ${view.h}`}
      xmlns="http://www.w3.org/2000/svg"
      data-state={state}
      role={title ? 'img' : undefined}
      aria-label={title}
      aria-hidden={title ? undefined : true}
      {...props}
    >
      <Body />
      <Face state={state} ink={ink} spark={spark} blink={blink} wink={wink} weight={weight} />
    </svg>
  );
}
