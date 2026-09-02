import type { PaletteItem } from "./paletteIndex";

// The palette remembers the last five destinations it opened, per
// browser, so a return trip is one keystroke. Actions are not
// destinations and never enter the list.
const STORAGE_KEY = "starport.palette.recents";
export const RECENT_LIMIT = 5;

function sameItem(a: PaletteItem, b: PaletteItem): boolean {
  return a.kind === b.kind && a.id === b.id;
}

export function readRecents(storage: Storage = localStorage): PaletteItem[] {
  try {
    const raw = storage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter(
        (entry): entry is PaletteItem =>
          typeof entry === "object" &&
          entry !== null &&
          typeof (entry as PaletteItem).kind === "string" &&
          typeof (entry as PaletteItem).id === "string" &&
          typeof (entry as PaletteItem).label === "string",
      )
      .slice(0, RECENT_LIMIT);
  } catch {
    return [];
  }
}

export function rememberRecent(
  item: PaletteItem,
  storage: Storage = localStorage,
): PaletteItem[] {
  if (item.kind === "action") return readRecents(storage);
  const next = [
    { kind: item.kind, id: item.id, label: item.label, hint: item.hint },
    ...readRecents(storage).filter((entry) => !sameItem(entry, item)),
  ].slice(0, RECENT_LIMIT);
  try {
    storage.setItem(STORAGE_KEY, JSON.stringify(next));
  } catch {
    // A full or blocked store only loses the convenience.
  }
  return next;
}
