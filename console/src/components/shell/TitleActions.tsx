// TitleActions places a page's title-line controls (its primary action, a
// refresh button) at the right end of the title line. On a wide screen the
// shell floats one row over the title line, catalog chip first and page
// actions last, so the primary action keeps the far right and the chip
// reads as secondary information beside it. Without that row (a small
// screen, or a test that renders a page alone) the controls render in
// place, where the page put them.
import { createContext, useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";

export const TitleActionsTarget = createContext<HTMLElement | null>(null);

export function TitleActions({ children }: { children: ReactNode }) {
  const target = useContext(TitleActionsTarget);
  if (target === null) return <>{children}</>;
  return createPortal(children, target);
}
