'use client';

import { type KeyboardEvent, useEffect, useId, useRef, useState } from 'react';

import type { InstallMethod } from '@/lib/splash';

import { codeLines } from './shell';

// InstallTabs shows one install method at a time. The tab list follows the
// ARIA tabs pattern: the arrow keys, Home, and End move the selection, and
// only the selected tab is in the tab order. The page renders the block in
// the hero and in the closing section, so each instance owns its ids.
export function InstallTabs({ methods }: { methods: InstallMethod[] }) {
  const [active, setActive] = useState(0);
  const [copied, setCopied] = useState(false);
  const timer = useRef(0);
  const tabs = useRef<(HTMLButtonElement | null)[]>([]);
  const id = useId();
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const method = methods[active];
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(method.command);
    } catch {
      return;
    }
    setCopied(true);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopied(false), 1600);
  };
  const select = (index: number) => {
    const next = (index + methods.length) % methods.length;
    setActive(next);
    setCopied(false);
    tabs.current[next]?.focus();
  };
  const onKey = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    if (event.key === 'ArrowRight') select(index + 1);
    else if (event.key === 'ArrowLeft') select(index - 1);
    else if (event.key === 'Home') select(0);
    else if (event.key === 'End') select(methods.length - 1);
    else return;
    event.preventDefault();
  };

  return (
    <div className="install">
      <div className="install-head">
        <div className="install-tabs" role="tablist" aria-label="Install method">
          {methods.map((option, index) => (
            <button
              key={option.id}
              ref={(node) => {
                tabs.current[index] = node;
              }}
              type="button"
              role="tab"
              id={`${id}-tab-${option.id}`}
              aria-selected={index === active}
              aria-controls={`${id}-panel`}
              tabIndex={index === active ? 0 : -1}
              onClick={() => select(index)}
              onKeyDown={(event) => onKey(event, index)}
            >
              {option.title}
            </button>
          ))}
        </div>
        <button type="button" className="install-copy" onClick={copy}>
          <span aria-live="polite">{copied ? 'Copied' : 'Copy'}</span>
        </button>
      </div>
      <p className="install-note">shell · {method.note}</p>
      <pre
        role="tabpanel"
        id={`${id}-panel`}
        aria-labelledby={`${id}-tab-${method.id}`}
        // The panel scrolls sideways on a narrow screen, so it takes focus.
        tabIndex={0}
      >
        <code>
          {codeLines(method.command, true).map((line, index) => (
            <span className="line" key={index}>
              <i aria-hidden="true">{line.prompt ? '$ ' : '  '}</i>
              {line.text}
            </span>
          ))}
        </code>
      </pre>
    </div>
  );
}
