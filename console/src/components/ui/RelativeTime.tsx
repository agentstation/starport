import { formatRelativeTime, utcTooltip } from "@/lib/format";
import { cn } from "@/lib/utils";

// RelativeTime is the one element for a moment in time: the relative phrase
// that never wraps, the exact UTC instant in the tooltip, and the machine
// stamp on the element. An absent or zero-value stamp renders the dash with
// no tooltip.
export function RelativeTime({
  iso,
  className,
}: {
  iso: string | null | undefined;
  className?: string;
}) {
  const stamp = utcTooltip(iso);
  return (
    <time dateTime={stamp} title={stamp} className={cn("whitespace-nowrap", className)}>
      {formatRelativeTime(iso)}
    </time>
  );
}
