// Sparkline renders a neutral 24-point trend as an inline SVG polyline.
// Neutral by design (DESIGN.md): trends inform, they do not alarm.
export function Sparkline({ points }: { points: number[] }) {
  if (points.length < 2 || points.every((point) => point === 0)) return null;
  const width = 72;
  const height = 20;
  const max = Math.max(...points);
  const step = width / (points.length - 1);
  const path = points
    .map((point, index) => {
      const x = (index * step).toFixed(1);
      const y = (height - 2 - (point / max) * (height - 4)).toFixed(1);
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
