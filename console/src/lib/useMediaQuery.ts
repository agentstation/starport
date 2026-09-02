import { useSyncExternalStore } from "react";

// SMALL_SCREEN is the console's one layout breakpoint: below Tailwind's
// `sm` (640 px) the sidebar becomes a sheet, the model picker becomes a
// bottom sheet, and row actions grow to a 44 px touch target.
export const SMALL_SCREEN = "(max-width: 639px)";

function subscribe(query: string, onChange: () => void): () => void {
  if (typeof window.matchMedia !== "function") return () => {};
  const list = window.matchMedia(query);
  list.addEventListener("change", onChange);
  return () => list.removeEventListener("change", onChange);
}

function matches(query: string): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia(query).matches;
}

// useMediaQuery answers whether the viewport matches a CSS media query and
// re-renders when the answer changes. The stylesheet handles most breakpoint
// styling; this hook exists for the cases where the component tree itself
// differs, so a sheet is mounted on one side of the breakpoint and not the
// other.
export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => subscribe(query, onChange),
    () => matches(query),
    () => false,
  );
}
