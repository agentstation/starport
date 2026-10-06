'use client';

import { type KeyboardEvent, useEffect, useId, useRef, useState } from 'react';

import type { ScenePart, ScenePartId } from '@/lib/splash';

import { Inline } from './inline';

// Each part except Starport has one wire to Starport. A request wire is on
// the request path, and it is the only amber mark in the scene. The
// stylesheet sets the direction of each wire for the wide and the compact
// layout.
const REQUEST_PATH: Record<ScenePartId, boolean> = {
  catalog: false,
  app: true,
  starport: false,
  providers: true,
  state: false,
};

// RequestScene draws the request path as a diagram in the DOM. A pointer over
// a part, a tap, or keyboard focus shows the card of that part in the readout
// under the diagram. Every card stays in the readout cell, so the cell keeps
// the height of the longest card and nothing moves when a card opens. Each
// part is a button, and its card is its accessible description.
export function RequestScene({ parts }: { parts: ScenePart[] }) {
  const [active, setActive] = useState<ScenePartId | null>(null);
  const figure = useRef<HTMLElement>(null);
  const id = useId();

  // A tap keeps a card open until the next tap lands outside the scene.
  useEffect(() => {
    if (!active) return;
    const onOutside = (event: PointerEvent) => {
      if (event.target instanceof Node && figure.current?.contains(event.target)) return;
      setActive(null);
    };
    document.addEventListener('pointerdown', onOutside);
    return () => document.removeEventListener('pointerdown', onOutside);
  }, [active]);

  const onKey = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape' && active) {
      setActive(null);
      event.stopPropagation();
    }
  };
  const leave = (part: ScenePartId) => setActive((current) => (current === part ? null : current));

  return (
    <figure
      ref={figure}
      className="scene"
      aria-label="The request path. Your app sends a request with a gateway API key to Starport. Starport plans the route from the accepted Starmap catalog generation, keeps durable state, and calls a provider with a provider inference credential."
      onKeyDown={onKey}
    >
      <div className="scene-grid">
        {parts.map((part) => (
          <div
            key={part.id}
            className={`scene-part scene-${part.id}`}
            data-open={active === part.id ? '' : undefined}
          >
            {part.id === 'starport' ? null : (
              <span className={REQUEST_PATH[part.id] ? 'wire wire-request' : 'wire'} aria-hidden="true">
                {REQUEST_PATH[part.id] ? <i /> : null}
              </span>
            )}
            <button
              type="button"
              aria-describedby={`${id}-${part.id}`}
              onPointerEnter={(event) => {
                if (event.pointerType === 'mouse') setActive(part.id);
              }}
              onPointerLeave={(event) => {
                if (event.pointerType === 'mouse') leave(part.id);
              }}
              onFocus={() => setActive(part.id)}
              onBlur={() => leave(part.id)}
              onClick={() => setActive(part.id)}
            >
              <span className="part-role">{part.role}</span>
              <span className="part-label">{part.label}</span>
              <span className="part-details">
                {part.details.map((detail) => (
                  <span key={detail}>
                    <Inline text={detail} />
                  </span>
                ))}
              </span>
            </button>
          </div>
        ))}
      </div>
      <figcaption className="scene-readout">
        <span className="scene-card scene-hint" data-open={active === null ? '' : undefined} aria-hidden="true">
          <span className="legend-request">Request path</span>
          <span className="legend-state">Catalog and state</span>
          <span>Select a part to read its card.</span>
        </span>
        {parts.map((part) => (
          <span
            key={part.id}
            id={`${id}-${part.id}`}
            className="scene-card"
            data-open={active === part.id ? '' : undefined}
          >
            <strong>{part.label}</strong>
            <span>
              <Inline text={part.card} />
            </span>
          </span>
        ))}
      </figcaption>
    </figure>
  );
}
