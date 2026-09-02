// Sparkline renders a neutral 24-point trend as an inline SVG polyline over
// a zero baseline. Neutral by design (DESIGN.md): trends inform, they do not
// alarm. The baseline says where zero sits, and a caller that passes `max`
// puts two sparklines on one scale, so one error never rises as high as a
// thousand requests beside it.
export function Sparkline({ points, max }: { points: number[]; max?: number }) {
  if (points.length < 2) return null;
  const width = 72;
  const height = 20;
  const scale = max ?? Math.max(...points);
  const step = width / (points.length - 1);
  const path = points
    .map((point, index) => {
      const x = (index * step).toFixed(1);
      const share = scale > 0 ? point / scale : 0;
      const y = (height - 2 - share * (height - 4)).toFixed(1);
      return `${x},${y}`;
    })
    .join(" ");
  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      width={width}
      height={height}
      aria-hidden="true"
      className="text-text-4"
    >
      <line
        data-testid="sparkline-baseline"
        x1="0"
        x2={width}
        y1={height - 1}
        y2={height - 1}
        stroke="var(--border-2)"
        strokeWidth="1"
      />
      <polyline
        points={path}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.25"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}
