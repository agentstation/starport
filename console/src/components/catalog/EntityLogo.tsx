import { useEffect, useState } from "react";

// EntityLogo renders catalog identity offline through a fallback chain:
// bundled gateway SVG → tinted monochrome (currentColor marks inherit the
// theme text color) → two-letter initials. No external URL is ever
// fetched; the only request goes to this gateway's public logo route.

export type EntityKind = "providers" | "authors";

// One fetch per mark for the whole session: a grid of cards shares the
// same promise, and a 404 is remembered instead of retried per card.
const marks = new Map<string, Promise<string | null>>();

function fetchMark(kind: EntityKind, id: string): Promise<string | null> {
  const key = `${kind}/${id}`;
  let pending = marks.get(key);
  if (!pending) {
    pending = fetch(`/api/v1/logos/${kind}/${encodeURIComponent(id)}.svg`)
      .then((response) => (response.ok ? response.text() : null))
      .catch(() => null);
    marks.set(key, pending);
  }
  return pending;
}

// Initials fall back to the first letters of the first two words, so
// "Black Forest Labs" reads BF and single-word names read their first
// two letters.
export function entityInitials(name: string): string {
  const [first, second] = name.trim().split(/[\s-_/]+/).filter(Boolean);
  if (!first) return "?";
  const letters = second ? `${first[0]}${second[0]}` : first.slice(0, 2);
  return letters.toUpperCase();
}

export function EntityLogo({
  kind,
  id,
  name,
  size = 24,
  className = "",
}: {
  kind: EntityKind;
  id: string;
  name: string;
  size?: number;
  className?: string;
}) {
  // undefined = loading, null = no bundled mark → initials.
  const [svg, setSvg] = useState<string | null | undefined>(undefined);

  useEffect(() => {
    let alive = true;
    setSvg(undefined);
    void fetchMark(kind, id).then((text) => {
      if (alive) setSvg(text);
    });
    return () => {
      alive = false;
    };
  }, [kind, id]);

  const frame = `inline-flex shrink-0 items-center justify-center overflow-hidden rounded-sm ${className}`;

  if (svg === null) {
    return (
      <span
        aria-hidden="true"
        data-testid="entity-initials"
        className={`${frame} border border-border-1 bg-bg-raised font-medium text-text-2`}
        style={{ width: size, height: size, fontSize: size * 0.38 }}
      >
        {entityInitials(name)}
      </span>
    );
  }

  return (
    <span
      aria-hidden="true"
      data-testid="entity-mark"
      // The SVG comes only from this gateway's embedded, license-audited
      // bundle, so inlining is safe — and inlining is what lets
      // fill="currentColor" marks tint with the theme.
      dangerouslySetInnerHTML={svg ? { __html: svg } : undefined}
      className={`${frame} text-text-1 [&_svg]:h-[1em] [&_svg]:w-[1em]`}
      style={{ width: size, height: size, fontSize: size }}
    />
  );
}
