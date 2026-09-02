import { lazy, Suspense, useEffect, useState } from "react";

// The shell's Search button and the ⌘K shortcut share one entry point:
// a window event, so no state needs lifting into the shell.
export const PALETTE_EVENT = "starport:palette";

export function openCommandPalette(): void {
  window.dispatchEvent(new Event(PALETTE_EVENT));
}

const PaletteDialog = lazy(() => import("./PaletteDialog"));

// CommandPalette owns the shortcut and the open state, and nothing else.
// The dialog, cmdk, and the catalog queries load on the first open, so
// the shell entry chunk stays free of them.
export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOpen((current) => !current);
      }
    };
    const onOpen = () => setOpen(true);
    document.addEventListener("keydown", onKey);
    window.addEventListener(PALETTE_EVENT, onOpen);
    return () => {
      document.removeEventListener("keydown", onKey);
      window.removeEventListener(PALETTE_EVENT, onOpen);
    };
  }, []);

  useEffect(() => {
    if (open) setLoaded(true);
  }, [open]);

  if (!loaded) return null;
  return (
    <Suspense fallback={null}>
      <PaletteDialog open={open} onOpenChange={setOpen} />
    </Suspense>
  );
}
