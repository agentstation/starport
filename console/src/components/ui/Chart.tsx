import type { ComponentProps, CSSProperties, ReactElement, ReactNode } from "react";
import { cloneElement, useEffect, useState } from "react";
import type { ReferenceDot } from "recharts";

import { Card } from "@/components/ui/Card";

// Chart surfaces (DESIGN.md): Recharts with the neutral series palette —
// neutral bars/lines from the text scale, semantic color only for
// semantic series, accent never by default. Faint gridlines, tabular
// axis labels, endpoint emphasized. These CSS-variable colors keep every
// chart on the role tokens across both themes. A stack needs steps a
// reader can tell apart inside a five-pixel bar, so the strong and soft
// neutrals sit two steps apart.
export const CHART = {
  neutral: "var(--text-3)",
  neutralStrong: "var(--text-2)",
  neutralSoft: "var(--text-4)",
  grid: "var(--border-1)",
  axis: "var(--text-4)",
  error: "var(--error)",
} as const;

export const AXIS_TICK = {
  fontSize: 11,
  fontFamily: "var(--font-mono)",
  fill: CHART.axis,
} as const;

// USAGE_SYNC_ID joins the charts that share one bucket array, so a hover
// on one draws the cursor and tooltip on all of them at the same slice.
export const USAGE_SYNC_ID = "usage";

// CURSOR is the tooltip crosshair on a line or area chart. It sits one
// step above the gridlines and dashes, so it reads as state, not chrome.
export const CURSOR = { stroke: "var(--border-3)", strokeDasharray: "3 3" } as const;

// A Series names one encoding on a chart for the chip legend under it.
export type Series = { name: string; color: string; opacity?: number };

// Endpoint is the ReferenceDot that marks where a series ends. DESIGN.md
// asks for the emphasized endpoint on every chart, and one dot says where
// the data stops inside a window that may run on past it.
export type Endpoint = Pick<
  ComponentProps<typeof ReferenceDot>,
  "x" | "y" | "r" | "fill" | "stroke"
>;

// endpointOf finds the last point that carries a value. A trailing slice
// with nothing measured has nothing to mark, so the dot sits on the last
// measured one.
export function endpointOf<T extends { label: string }>(
  points: readonly T[],
  value: (point: T) => number | null,
  fill: string,
): Endpoint | null {
  for (let index = points.length - 1; index >= 0; index--) {
    const point = points[index]!;
    const y = value(point);
    if (y !== null && Number.isFinite(y)) {
      return { x: point.label, y, r: 3, fill, stroke: "none" };
    }
  }
  return null;
}

const REDUCED_MOTION = "(prefers-reduced-motion: reduce)";

function prefersReducedMotion(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia(REDUCED_MOTION).matches;
}

// useChartMotion answers whether a series may animate. Recharts interpolates
// in JavaScript, so the stylesheet's reduced-motion block never reaches it.
export function useChartMotion(): boolean {
  const [reduced, setReduced] = useState(prefersReducedMotion);
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const query = window.matchMedia(REDUCED_MOTION);
    const onChange = () => setReduced(query.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);
  return !reduced;
}

const FILL: CSSProperties = { width: "100%", height: "100%" };

// ChartCard is one chart panel: label row, headline value for the window,
// a fixed-height plot area, and a footer that carries the chip legend and
// the caption. The plot fills the box through the Recharts 3 responsive
// prop, which sizes by CSS, so no wrapper measures it.
export function ChartCard({
  title,
  value,
  series,
  caption,
  children,
}: {
  title: string;
  value: ReactNode;
  series?: readonly Series[];
  caption?: ReactNode;
  children: ReactElement<{ responsive?: boolean; style?: CSSProperties }>;
}) {
  return (
    <Card className="min-w-0">
      <div className="mb-3 flex items-baseline justify-between gap-2">
        <h2 className="text-xs font-medium text-text-3">{title}</h2>
        <span className="text-sm font-medium tabular-nums text-text-1">{value}</span>
      </div>
      <div className="h-36">{cloneElement(children, { responsive: true, style: FILL })}</div>
      {(series || caption) && (
        <div className="mt-2 flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs text-text-3">
          {series && (
            <ul className="flex items-center gap-3" aria-label={`${title} series`}>
              {series.map((item) => (
                <li key={item.name} className="flex items-center gap-1.5">
                  <span
                    aria-hidden="true"
                    className="size-1.5 rounded-full"
                    style={{ background: item.color, opacity: item.opacity ?? 1 }}
                  />
                  {item.name}
                </li>
              ))}
            </ul>
          )}
          {caption && <span className="tabular-nums">{caption}</span>}
        </div>
      )}
    </Card>
  );
}

// ChartTip renders the recharts tooltip on the popover contract: raised
// ground, hairline border, mono numbers.
export function ChartTip({
  active,
  label,
  rows,
}: {
  active?: boolean;
  label?: ReactNode;
  rows: { name: string; value: string; color?: string }[];
}) {
  if (!active || rows.length === 0) return null;
  return (
    <div className="rounded-sm border border-border-2 bg-bg-raised px-2.5 py-1.5 text-xs shadow-[0_4px_12px_rgba(0,0,0,0.3)]">
      {label !== undefined && <div className="mb-1 text-text-3">{label}</div>}
      {rows.map((row) => (
        <div key={row.name} className="flex items-center justify-between gap-4">
          <span className="flex items-center gap-1.5 text-text-2">
            {row.color && (
              <span
                aria-hidden="true"
                className="size-1.5 rounded-full"
                style={{ background: row.color }}
              />
            )}
            {row.name}
          </span>
          <span className="font-mono tabular-nums text-text-1">{row.value}</span>
        </div>
      ))}
    </div>
  );
}
