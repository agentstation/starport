import { AREAS, GENERATED_AREA, HOME_AREA, isAreaSlug } from "./areas";

// SITE_ROOT is the content tree, relative to the repository root. Source
// paths in this module are repository-relative POSIX paths.
export const SITE_ROOT = "docs/site/";
const GENERATED_ROOT = `${SITE_ROOT}generated/`;

export type SourceFile = { path: string; text: string };

export type Topic = {
  source: string;
  // "home" for docs/site/index.md, otherwise an area slug.
  area: string;
  // "index" for the home page and the area pages.
  slug: string;
  // The page directory under the docs root: "", "start/", or
  // "start/keys-and-roles/". The page itself is <route>index.html.
  route: string;
  title: string;
  order: number;
  summary: string;
  body: string;
  generated: boolean;
};

export class ContentError extends Error {
  constructor(source: string, message: string) {
    super(`${source}: ${message}`);
    this.name = "ContentError";
  }
}

const FRONT_MATTER_KEYS = ["title", "area", "order", "summary"] as const;

// parseFrontMatter reads the leading "---" block. The block holds flat
// "key: value" lines only; a value may be quoted. It returns null when the
// text has no front matter.
export function parseFrontMatter(
  source: string,
  text: string,
): { data: Record<string, string>; body: string } | null {
  const normalized = text.replace(/\r\n/g, "\n");
  if (!normalized.startsWith("---\n")) return null;
  const end = normalized.indexOf("\n---\n", 3);
  if (end < 0) throw new ContentError(source, "front matter has no closing --- line");
  const data: Record<string, string> = {};
  for (const line of normalized.slice(4, end).split("\n")) {
    if (line.trim() === "" || line.trimStart().startsWith("#")) continue;
    const match = /^([A-Za-z_][\w-]*):\s*(.*)$/.exec(line);
    if (!match) throw new ContentError(source, `front matter line is not "key: value": ${line}`);
    const [, key = "", raw = ""] = match;
    data[key] = unquote(raw.trim());
  }
  return { data, body: normalized.slice(end + 5) };
}

function unquote(value: string): string {
  if (value.length >= 2 && value.startsWith('"') && value.endsWith('"')) {
    return value.slice(1, -1).replace(/\\"/g, '"').replace(/\\\\/g, "\\");
  }
  if (value.length >= 2 && value.startsWith("'") && value.endsWith("'")) {
    return value.slice(1, -1).replace(/''/g, "'");
  }
  return value;
}

function parseOrder(source: string, value: string | undefined, fallback?: number): number {
  if (value === undefined && fallback !== undefined) return fallback;
  if (value === undefined || !/^\d+$/.test(value)) {
    throw new ContentError(source, `front matter "order" must be a whole number, got ${JSON.stringify(value)}`);
  }
  return Number(value);
}

function authoredTopic(file: SourceFile, area: string, slug: string): Topic {
  const parsed = parseFrontMatter(file.path, file.text);
  if (!parsed) throw new ContentError(file.path, "topic has no front matter");
  for (const key of FRONT_MATTER_KEYS) {
    if (!parsed.data[key]) throw new ContentError(file.path, `front matter is missing "${key}"`);
  }
  for (const key of Object.keys(parsed.data)) {
    if (!(FRONT_MATTER_KEYS as readonly string[]).includes(key)) {
      throw new ContentError(file.path, `front matter has an unknown key "${key}"`);
    }
  }
  if (parsed.data.area !== area) {
    throw new ContentError(file.path, `front matter area "${parsed.data.area}" does not match the directory "${area}"`);
  }
  return {
    source: file.path,
    area,
    slug,
    route: routeFor(area, slug),
    title: parsed.data.title ?? "",
    order: parseOrder(file.path, parsed.data.order),
    summary: parsed.data.summary ?? "",
    body: parsed.body,
    generated: false,
  };
}

// generatedTopic accepts a page that a generator wrote. Front matter is
// optional: without it the first level-one heading is the title and the
// page joins the Configure area.
function generatedTopic(file: SourceFile): Topic {
  const slug = file.path.slice(file.path.lastIndexOf("/") + 1, -".md".length);
  const parsed = parseFrontMatter(file.path, file.text);
  const data = parsed?.data ?? {};
  let body = parsed?.body ?? file.text.replace(/\r\n/g, "\n");
  let title = data.title ?? "";
  const heading = /^# (.+)$/m.exec(body);
  if (heading) {
    if (!title) title = heading[1]?.trim() ?? "";
    body = body.slice(0, heading.index) + body.slice(heading.index + heading[0].length);
  }
  if (!title) throw new ContentError(file.path, "generated page has no title and no level-one heading");
  const area = data.area ?? GENERATED_AREA;
  if (!isAreaSlug(area)) throw new ContentError(file.path, `unknown area "${area}"`);
  return {
    source: file.path,
    area,
    slug,
    route: routeFor(area, slug),
    title,
    order: parseOrder(file.path, data.order, 1000),
    summary: data.summary ?? "Generated reference. The build creates this page from the release source.",
    body,
    generated: true,
  };
}

function routeFor(area: string, slug: string): string {
  if (area === HOME_AREA) return "";
  return slug === "index" ? `${area}/` : `${area}/${slug}/`;
}

// loadTopics turns the content tree into topics, sorted by area order and
// then by front matter order. Files that are not Markdown are data for a
// generator and are not pages.
export function loadTopics(files: readonly SourceFile[]): Topic[] {
  const topics: Topic[] = [];
  for (const file of files) {
    if (!file.path.startsWith(SITE_ROOT) || !file.path.endsWith(".md")) continue;
    if (file.path.startsWith(GENERATED_ROOT)) {
      topics.push(generatedTopic(file));
      continue;
    }
    const segments = file.path.slice(SITE_ROOT.length).split("/");
    if (segments.length === 1 && segments[0] === "index.md") {
      topics.push(authoredTopic(file, HOME_AREA, "index"));
      continue;
    }
    const [area = "", name = ""] = segments;
    if (segments.length !== 2) {
      throw new ContentError(file.path, "a topic must sit directly inside an area directory");
    }
    if (!isAreaSlug(area)) throw new ContentError(file.path, `"${area}" is not a documentation area`);
    topics.push(authoredTopic(file, area, name.slice(0, -".md".length)));
  }

  const routes = new Map<string, string>();
  for (const topic of topics) {
    const existing = routes.get(topic.route);
    if (existing) throw new ContentError(topic.source, `page /${topic.route} is also written by ${existing}`);
    routes.set(topic.route, topic.source);
  }
  if (!routes.has("")) throw new ContentError(`${SITE_ROOT}index.md`, "the home page is missing");
  for (const area of AREAS) {
    if (!routes.has(`${area.slug}/`)) {
      throw new ContentError(`${SITE_ROOT}${area.slug}/index.md`, "the area page is missing");
    }
  }

  const areaRank = (area: string) => (area === HOME_AREA ? -1 : AREAS.findIndex((entry) => entry.slug === area));
  return topics.sort(
    (a, b) =>
      areaRank(a.area) - areaRank(b.area) ||
      a.order - b.order ||
      a.slug.localeCompare(b.slug, "en"),
  );
}
