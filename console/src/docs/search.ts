import MiniSearch, { type Options, type SearchResult } from "minisearch";

// One search document is one section of one page. The build writes the
// serialized index, and the client loads it with the same options, so both
// sides must import them from here.
export type SearchDocument = {
  id: string;
  // The href from the docs root to the section, for example
  // "start/keys-and-roles/#scopes".
  href: string;
  title: string;
  area: string;
  heading: string;
  text: string;
  terms: string;
};

export type StoredFields = Pick<SearchDocument, "href" | "title" | "area" | "heading">;

export const SEARCH_OPTIONS: Options<SearchDocument> = {
  fields: ["title", "heading", "text", "terms"],
  storeFields: ["href", "title", "area", "heading"],
  searchOptions: {
    boost: { title: 4, heading: 3, terms: 3 },
    prefix: true,
    fuzzy: 0.15,
    combineWith: "AND",
  },
  // Setting names such as STARPORT_SERVER_PORT stay one token, and the
  // pieces between underscores are tokens too, so a reader can type either.
  tokenize: (text) => {
    const tokens: string[] = [];
    for (const word of text.split(/[\s,;:()[\]{}"'`<>|=!?]+/)) {
      const trimmed = word.replace(/^[./-]+|[./-]+$/g, "");
      if (!trimmed) continue;
      tokens.push(trimmed);
      if (/[_./-]/.test(trimmed)) tokens.push(...trimmed.split(/[_./-]+/).filter(Boolean));
    }
    return tokens;
  },
  processTerm: (term) => term.toLowerCase(),
};

export function buildSearchIndex(documents: readonly SearchDocument[]): string {
  const index = new MiniSearch<SearchDocument>(SEARCH_OPTIONS);
  index.addAll(documents);
  return JSON.stringify(index);
}

export function loadSearchIndex(serialized: string): MiniSearch<SearchDocument> {
  return MiniSearch.loadJSON<SearchDocument>(serialized, SEARCH_OPTIONS);
}

export type SearchHit = StoredFields & { id: string; score: number };

export function searchDocs(index: MiniSearch<SearchDocument>, query: string, limit = 20): SearchHit[] {
  const trimmed = query.trim();
  if (!trimmed) return [];
  return index
    .search(trimmed)
    .slice(0, limit)
    .map((result: SearchResult) => ({
      id: String(result.id),
      score: result.score,
      href: String(result.href),
      title: String(result.title),
      area: String(result.area),
      heading: String(result.heading ?? ""),
    }));
}
