import type { ComponentProps, ReactNode } from "react";

import { cn } from "@/lib/utils";

// Skeleton is the loading placeholder of DESIGN.md: it takes the geometry
// of what it stands in for, sits on the raised ground, and carries a faint
// 1.8 s shimmer that reduced-motion turns off. A bare Skeleton is
// decoration and says nothing to a screen reader; LoadingStatus gives a
// group of them its one announcement.
export function Skeleton({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      aria-hidden="true"
      data-slot="skeleton"
      className={cn(
        "animate-shimmer rounded-sm bg-bg-raised bg-linear-90 from-transparent from-35% via-bg-hover via-50% to-transparent to-65% bg-size-[200%_100%] motion-reduce:animate-none",
        className,
      )}
      {...props}
    />
  );
}

// LoadingStatus wraps a shaped skeleton in the one polite status a screen
// reader hears, so a page never announces every bar it draws.
export function LoadingStatus({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <div role="status" aria-live="polite" aria-busy="true" className={className}>
      <span className="sr-only">Loading</span>
      {children}
    </div>
  );
}

const WIDTHS = ["w-2/5", "w-1/4", "w-1/5", "w-1/6", "w-1/3", "w-1/4"] as const;

// TableSkeleton stands in for a dense table: a header row and `rows` body
// rows inside the panel frame the table takes when it arrives.
export function TableSkeleton({
  rows = 6,
  columns = 4,
  className,
}: {
  rows?: number;
  columns?: number;
  className?: string;
}) {
  const grid = { gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` };
  return (
    <LoadingStatus
      className={cn("overflow-hidden rounded-md border border-border-1 bg-bg-panel", className)}
    >
      <div className="grid gap-6 border-b border-border-1 px-4 py-3" style={grid}>
        {Array.from({ length: columns }, (_, column) => (
          <Skeleton key={column} className="h-3 w-16" />
        ))}
      </div>
      {Array.from({ length: rows }, (_, row) => (
        <div
          key={row}
          className="grid gap-6 border-b border-border-1 px-4 py-3.5 last:border-b-0"
          style={grid}
        >
          {Array.from({ length: columns }, (_, column) => (
            <Skeleton
              key={column}
              className={cn("h-3.5", WIDTHS[(row + column) % WIDTHS.length])}
            />
          ))}
        </div>
      ))}
    </LoadingStatus>
  );
}

// CardSkeleton stands in for a Card with a title and `lines` rows of text.
export function CardSkeleton({ lines = 3, className }: { lines?: number; className?: string }) {
  return (
    <LoadingStatus
      className={cn("rounded-md border border-border-1 bg-bg-panel p-5", className)}
    >
      <Skeleton className="mb-4 h-3.5 w-28" />
      <div className="flex flex-col gap-3">
        {Array.from({ length: lines }, (_, line) => (
          <Skeleton key={line} className={cn("h-3.5", WIDTHS[line % WIDTHS.length])} />
        ))}
      </div>
    </LoadingStatus>
  );
}

// CardGridSkeleton stands in for the two-column card grid of the author and
// provider lists: a logo, a name, and two lines per card.
export function CardGridSkeleton({ count = 4, className }: { count?: number; className?: string }) {
  return (
    <LoadingStatus className={cn("grid grid-cols-1 gap-3 @3xl:grid-cols-2", className)}>
      {Array.from({ length: count }, (_, card) => (
        <div
          key={card}
          className="flex gap-3 rounded-md border border-border-1 bg-bg-panel p-4"
        >
          <Skeleton className="size-9 shrink-0 rounded-md" />
          <div className="flex min-w-0 flex-1 flex-col gap-2 pt-1">
            <Skeleton className="h-3.5 w-1/2" />
            <Skeleton className="h-3 w-3/4" />
            <Skeleton className="h-3 w-1/3" />
          </div>
        </div>
      ))}
    </LoadingStatus>
  );
}

// DetailSkeleton stands in for a detail page: the back link, the logo and
// title row, and the first card of facts.
export function DetailSkeleton({ className }: { className?: string }) {
  return (
    <LoadingStatus className={cn("flex flex-col gap-5", className)}>
      <Skeleton className="h-3.5 w-20" />
      <div className="flex items-center gap-3">
        <Skeleton className="size-10 rounded-md" />
        <div className="flex flex-col gap-2">
          <Skeleton className="h-5 w-56" />
          <Skeleton className="h-3.5 w-36" />
        </div>
      </div>
      <div className="rounded-md border border-border-1 bg-bg-panel p-5">
        <div className="flex flex-col gap-3">
          <Skeleton className="h-3.5 w-2/5" />
          <Skeleton className="h-3.5 w-1/4" />
          <Skeleton className="h-3.5 w-1/3" />
          <Skeleton className="h-3.5 w-1/5" />
        </div>
      </div>
    </LoadingStatus>
  );
}

// StatSkeleton stands in for a row of `count` stats: label, value, and the
// sparkline band under them.
export function StatSkeleton({ count = 6, className }: { count?: number; className?: string }) {
  return (
    <LoadingStatus
      className={cn("rounded-md border border-border-1 bg-bg-panel p-5", className)}
    >
      <div className="grid grid-cols-2 gap-x-6 gap-y-5 @2xl:grid-cols-3 @4xl:grid-cols-6">
        {Array.from({ length: count }, (_, stat) => (
          <div key={stat} className="flex flex-col gap-2">
            <Skeleton className="h-3 w-20" />
            <Skeleton className="h-6 w-16" />
            <Skeleton className="h-8 w-full" />
          </div>
        ))}
      </div>
    </LoadingStatus>
  );
}
