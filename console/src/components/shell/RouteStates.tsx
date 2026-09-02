import { Link, useLocation, useRouter, type ErrorComponentProps } from "@tanstack/react-router";
import { ArrowLeft, RotateCw } from "lucide-react";

import { Skeleton, TableSkeleton } from "@/components/ui/skeleton";

// RouteStates owns what a route shows when it has no page to show: the
// placeholder while a loader runs, the card when a loader or a render fails,
// and the page for an address that names nothing.

// RoutePending holds the page area while a loader runs past the router's
// pending threshold: a title, a lede, and the table most pages open with.
export function RoutePending() {
  return (
    <div className="flex flex-col gap-4">
      <Skeleton className="h-6 w-48" />
      <Skeleton className="h-4 w-80" />
      <TableSkeleton rows={5} className="mt-2" />
    </div>
  );
}

// RouteError is the last boundary before a blank page. It names the failure
// and offers one action: run the route again, which reruns its loader and
// re-renders from scratch.
export function RouteError({ error, reset }: ErrorComponentProps) {
  const router = useRouter();
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div
      role="alert"
      className="flex max-w-prose flex-col gap-3 rounded-md border border-border-1 bg-bg-panel p-5"
    >
      <h1 className="text-base font-semibold text-text-1">This page failed to load</h1>
      <p className="break-words font-mono text-xs text-text-3">{message}</p>
      <div>
        <button
          type="button"
          onClick={() => {
            reset();
            void router.invalidate();
          }}
          className="inline-flex h-8 items-center gap-1.5 rounded-sm border border-border-1 bg-bg-raised px-3 text-sm text-text-1 transition-colors duration-150 ease-standard hover:border-border-2"
        >
          <RotateCw aria-hidden="true" className="size-3.5" />
          Try again
        </button>
      </div>
    </div>
  );
}

// The list a missing entity belongs to, keyed by the first path segment. An
// address outside these lists goes back to the overview.
const LISTS: Record<string, { noun: string; label: string; to: "/models" | "/providers" | "/authors" }> = {
  models: { noun: "model", label: "Models", to: "/models" },
  providers: { noun: "provider", label: "Providers", to: "/providers" },
  authors: { noun: "author", label: "Authors", to: "/authors" },
};

// RouteNotFound renders for an address the router cannot match and for a
// detail route whose loader found no record. It names what is missing and
// points at the list that holds the rest.
export function RouteNotFound() {
  const pathname = useLocation({ select: (location) => location.pathname });
  const segment = pathname.split("/")[1] ?? "";
  const list = LISTS[segment];
  const rest = decodeURIComponent(pathname.slice(segment.length + 2));
  return (
    <div className="flex flex-col gap-4">
      <Link
        to={list?.to ?? "/"}
        className="flex items-center gap-1.5 text-xs text-text-3 transition-colors duration-150 ease-standard hover:text-text-1"
      >
        <ArrowLeft aria-hidden="true" className="size-3.5" />
        {list?.label ?? "Overview"}
      </Link>
      <div className="flex flex-col gap-1">
        <h1 className="text-base font-semibold text-text-1">
          {list ? `No ${list.noun} at this address` : "No page at this address"}
        </h1>
        <p className="text-sm text-text-3">
          {list ? (
            <>
              The catalog has no {list.noun} named{" "}
              <span className="font-mono text-xs text-text-2">{rest}</span>.
            </>
          ) : (
            <>
              Nothing lives at <span className="font-mono text-xs text-text-2">{pathname}</span>.
            </>
          )}
        </p>
      </div>
    </div>
  );
}
