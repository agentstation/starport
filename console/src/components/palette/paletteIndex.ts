import { fuzzyIncludes } from "@/lib/modelFilter";

// The palette indexes five kinds. Kind order is display order: pages
// and actions lead because they are the cheap, always-known entries;
// catalog entities follow.
export type PaletteItemKind =
  | "page"
  | "action"
  | "model"
  | "provider"
  | "author";

export type PaletteItem = {
  kind: PaletteItemKind;
  // The navigable identity: a route path for pages, an entity id for
  // catalog kinds, an action id for actions.
  id: string;
  label: string;
  hint?: string;
  keywords?: string[];
};

export const KIND_ORDER: PaletteItemKind[] = [
  "page",
  "action",
  "model",
  "provider",
  "author",
];

export const KIND_LABELS: Record<PaletteItemKind, string> = {
  page: "Pages",
  action: "Actions",
  model: "Models",
  provider: "Providers",
  author: "Authors",
};

// matchesPaletteQuery reuses the models-list fuzzy matcher, so the
// palette accepts the same in-order subsequences the search box does
// (`gptoss120` finds `openai/gpt-oss-120b`).
export function matchesPaletteQuery(query: string, item: PaletteItem): boolean {
  if (!query) return true;
  return [item.label, item.hint, ...(item.keywords ?? [])]
    .filter((candidate): candidate is string => Boolean(candidate))
    .some((candidate) => fuzzyIncludes(candidate, query));
}

// paletteRank orders matches within a kind: label prefix, then label
// substring, then substring anywhere, then subsequence-only. Without
// this, a short query like `meta` buries `meta/llama-…` under long
// names that merely contain the letters in order.
export function paletteRank(query: string, item: PaletteItem): number {
  const label = item.label.toLowerCase();
  if (label.startsWith(query)) return 0;
  if (label.includes(query)) return 1;
  const rest = [item.hint, ...(item.keywords ?? [])].filter(
    (candidate): candidate is string => Boolean(candidate),
  );
  if (rest.some((candidate) => candidate.toLowerCase().includes(query))) {
    return 2;
  }
  return 3;
}

// searchPalette filters each kind by the fuzzy query, ranks within the
// kind, and caps it, so a broad query never floods the list with one
// kind. An empty query shows only pages and actions — the browsable
// surface; entities need at least one character.
export function searchPalette(
  query: string,
  items: PaletteItem[],
  perKindLimit = 6,
): PaletteItem[] {
  const trimmed = query.trim().toLowerCase();
  const results: PaletteItem[] = [];
  for (const kind of KIND_ORDER) {
    if (!trimmed && kind !== "page" && kind !== "action") continue;
    const matched = items.filter(
      (item) => item.kind === kind && matchesPaletteQuery(trimmed, item),
    );
    if (trimmed) {
      matched.sort(
        (a, b) => paletteRank(trimmed, a) - paletteRank(trimmed, b),
      );
    }
    results.push(...matched.slice(0, perKindLimit));
  }
  return results;
}
