import { RelativeTime } from "@/components/ui/RelativeTime";
import type { BudgetUsage } from "@/lib/api";

// BudgetLine is the one meter for a budget on any holder: a key, an
// account, or a team. It shows what the current window has left against a
// thin consumption bar, and when the window resets. An exhausted window
// reads in error red; a window past four fifths reads in warning. A budget
// whose meter the gateway could not read says so rather than drawing an
// empty bar that would read as "nothing spent".
export function BudgetLine({
  usage,
  render,
  unit,
}: {
  usage: BudgetUsage;
  render: (value: number) => string;
  unit: string;
}) {
  const limit = usage.limit ?? 0;
  if (usage.error || usage.used === undefined) {
    return (
      <div className="flex items-center gap-2 text-xs text-text-3">
        <span>
          {render(limit)} {unit} budget
        </span>
        <span className="text-text-4">· usage unavailable</span>
      </div>
    );
  }
  const used = usage.used;
  const remaining = usage.remaining ?? Math.max(0, limit - used);
  const exhausted = limit > 0 && remaining === 0;
  const fraction = limit > 0 ? Math.min(1, used / limit) : 0;
  const percent = Math.round(fraction * 100);
  return (
    <div className="flex items-center gap-2">
      <span
        role="meter"
        aria-label={`${unit} budget`}
        aria-valuemin={0}
        aria-valuemax={limit}
        aria-valuenow={used}
        aria-valuetext={`${render(used)} of ${render(limit)}, ${percent}%`}
        className="h-1 w-12 shrink-0 overflow-hidden rounded-full bg-border-3"
      >
        <span
          className={`block h-full ${exhausted ? "bg-error" : fraction > 0.8 ? "bg-warning" : "bg-success"}`}
          style={{ width: `${percent}%` }}
        />
      </span>
      <span
        className={`text-xs tabular-nums ${exhausted ? "text-error" : "text-text-3"}`}
      >
        {exhausted ? `${unit} exhausted` : `${render(remaining)} left`}
      </span>
      {usage.window_end && (
        <span className="text-xs text-text-4">
          · resets <RelativeTime iso={usage.window_end} />
        </span>
      )}
    </div>
  );
}
