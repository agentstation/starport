// The nine documentation areas, in reading order. The slug is the content
// directory under docs/site/ and the first URL segment under /docs/. A topic
// whose front matter names another area fails the build.
export const AREAS = [
  { slug: "start", title: "Start" },
  { slug: "architecture", title: "Choose an architecture" },
  { slug: "configure", title: "Configure" },
  { slug: "catalog-lifecycle", title: "Catalog lifecycle" },
  { slug: "operate-starmap", title: "Operate Starmap" },
  { slug: "operate-starport", title: "Operate Starport" },
  { slug: "storage", title: "Storage" },
  { slug: "api-compatibility", title: "API compatibility" },
  { slug: "troubleshoot", title: "Troubleshoot" },
] as const;

export type AreaSlug = (typeof AREAS)[number]["slug"];

// The home page is docs/site/index.md. Its front matter names this area.
export const HOME_AREA = "home";

// Generated reference pages under docs/site/generated/ belong to this area
// unless their front matter names another one.
export const GENERATED_AREA: AreaSlug = "configure";

export function isAreaSlug(value: string): value is AreaSlug {
  return AREAS.some((area) => area.slug === value);
}
