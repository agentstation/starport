import { AREAS, GENERATED_AREA } from '../../../console/src/docs/areas';

// The areas and their reading order come from the console module that the
// in-binary renderer reads. Thus both renderers put the pages in the same
// order.
export { AREAS };

// SITE_ROOT is the content tree, relative to the repository root.
export const SITE_ROOT = 'docs/site/';
const GENERATED_DIR = 'generated/';

// The URL prefix of every documentation page.
export const DOCS_BASE = '/docs';

// pagePath maps a content path under docs/site/ to the page path that the
// site uses. A generated reference page moves into the generated area, so
// docs/site/generated/settings.md reads as configure/settings.md. This is the
// same route that the in-binary renderer gives the page.
export function pagePath(contentPath: string): string {
  if (contentPath.startsWith(GENERATED_DIR)) {
    return `${GENERATED_AREA}/${contentPath.slice(GENERATED_DIR.length)}`;
  }
  return contentPath;
}

export function isGenerated(contentPath: string): boolean {
  return contentPath.startsWith(GENERATED_DIR);
}

// docsUrl is the site URL of a Markdown file under docs/site/. An index file
// names its directory: index.md is /docs and start/index.md is /docs/start.
export function docsUrl(contentPath: string): string {
  const segments = pagePath(contentPath).replace(/\.md$/, '').split('/');
  if (segments.at(-1) === 'index') segments.pop();
  return [DOCS_BASE, ...segments].join('/');
}
