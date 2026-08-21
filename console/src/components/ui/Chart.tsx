import type { ReactNode } from "react";
import { ResponsiveContainer } from "recharts";

import { Card } from "@/components/ui/Card";

// Chart surfaces (DESIGN.md): Recharts with the neutral series palette —
// neutral bars/lines from the text scale, semantic color only for
// semantic series, accent never by default. Faint gridlines, tabular
// axis labels. These CSS-variable colors keep every chart on the role
// tokens across both themes.
export const CHART = {
  neutral: "var(--text-3)",
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

// ChartCard is one chart panel: label row, headline value for the
// window, and a fixed-height plot area.
export function ChartCard({
  title,
  value,
  children,
}: {
  title: string;
  value: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card className="min-w-0">
      <div className="mb-3 flex items-baseline justify-between gap-2">
        <h2 className="text-xs font-medium text-text-3">{title}</h2>
        <span className="text-sm font-medium tabular-nums text-text-1">{value}</span>
      </div>
      <div className="h-36">
        <ResponsiveContainer width="100%" height="100%">
          {children as React.ReactElement}
        </ResponsiveContainer>
      </div>
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
