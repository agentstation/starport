import { useSyncExternalStore } from "react";

// The shell has three shapes, because the 240 px sidebar is only affordable
// on a wide viewport:
//
// - phone (below 768 px): the sidebar is a sheet behind a 48 px top bar, the
//   catalog chip lives in the top bar, the model picker is a bottom sheet.
// - compact (768–1023 px): the sidebar is the 64 px icon rail and expands
//   as an overlay; the chip and the page actions sit on the title line.
// - wide (1024 px and up): the 240 px sidebar with the persisted collapse
//   preference.
//
// Page grids do not read these tiers. They use container queries against
// the content column, so they react to the space the shell leaves them.
export const PHONE_SCREEN = "(max-width: 767px)";
export const COMPACT_SCREEN = "(max-width: 1023px)";

export type ShellTier = "phone" | "compact" | "wide";

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

// useShellTier names the shell shape for the current viewport. Without a
// matchMedia answer (server render, tests) the tier is wide.
export function useShellTier(): ShellTier {
  const phone = useMediaQuery(PHONE_SCREEN);
  const compact = useMediaQuery(COMPACT_SCREEN);
  if (phone) return "phone";
  if (compact) return "compact";
  return "wide";
}
