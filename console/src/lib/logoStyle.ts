// Provider and author mark rendering. The bundled catalog marks are a
// mix of full-color brand SVGs and currentColor monochrome glyphs, so a
// grid of them reads inconsistently. This setting names the treatment:
// "color" keeps each mark as shipped; "mono" flattens every mark to a
// single-tone glyph so the catalog reads as one set.

export type LogoStyle = "color" | "mono";

const STORAGE_KEY = "starport.logos";

// Storage access stays defensive: a test DOM or a locked-down browser
// context may not provide localStorage, and the setting is a preference,
// not state the app depends on.
export function savedLogoStyle(): LogoStyle {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(STORAGE_KEY);
  } catch {
    // Fall through to the default.
  }
  return raw === "mono" ? "mono" : "color";
}

export function setLogoStyle(style: LogoStyle): void {
  try {
    localStorage.setItem(STORAGE_KEY, style);
  } catch {
    // The choice still applies for this page via the listeners.
  }
  for (const listener of listeners) listener();
}

const listeners = new Set<() => void>();

export function onLogoStyleChange(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
