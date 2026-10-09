import { docs } from 'collections/server';
import { loader, update, type MetaData } from 'fumadocs-core/source';

import { AREAS, DOCS_BASE, pagePath } from './docs-routes';

const base = docs.toFumadocsSource();
type DocsPage = Extract<(typeof base.files)[number], { type: 'page' }>;

/*
 * The page tree follows the areas, then the front matter order, then the
 * file name. The in-binary renderer uses the same order. docs/site holds no
 * meta files. Thus this source adds one meta file per area and one for the
 * root. Each generated page also moves into its area here.
 */
const content = update(base)
  .files<DocsPage['data'], MetaData>((files) => {
    const pages = files.flatMap((file): DocsPage[] =>
      file.type === 'page' ? [{ ...file, path: pagePath(file.path) }] : [],
    );
    const ordered = (area: string) =>
      pages
        .filter((page) => page.path.startsWith(`${area}/`) && !page.path.endsWith('/index.md'))
        .sort((a, b) => (a.data.order ?? 0) - (b.data.order ?? 0) || a.path.localeCompare(b.path, 'en'))
        .map((page) => page.path.slice(area.length + 1, -'.md'.length));
    const metas = [
      { type: 'meta' as const, path: 'meta.json', data: { pages: ['index', ...AREAS.map((area) => area.slug)] } },
      ...AREAS.map((area) => ({
        type: 'meta' as const,
        path: `${area.slug}/meta.json`,
        data: { title: area.title, pages: ordered(area.slug) },
      })),
    ];
    return [...pages, ...metas];
  })
  .build();

export const source = loader({
  baseUrl: DOCS_BASE,
  source: content,
});
