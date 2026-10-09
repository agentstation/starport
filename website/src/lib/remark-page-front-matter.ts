import path from 'node:path';
import type { Heading, Root, RootContent } from 'mdast';
import type { VFile } from 'vfile';

import { SITE_ROOT, isGenerated } from './docs-routes';

// The order and the description that the in-binary renderer gives a
// generated page without front matter.
export const GENERATED_ORDER = 1000;
export const GENERATED_DESCRIPTION = 'Generated reference. The build creates this page from the release source.';

type FrontMatter = { title?: string; description?: string; order?: number };

// remarkPageFrontMatter completes the front matter of each page before
// Fumadocs exports it. A generated page has no front matter. Its first
// level-one heading becomes the title and leaves the body. The page layout
// writes the title as the one h1. An authored page must state a title and a
// summary.
export function remarkPageFrontMatter(options: { repoRoot: string }) {
  const siteDir = path.join(options.repoRoot, SITE_ROOT);
  return (tree: Root, file: VFile) => {
    const contentPath = path.relative(siteDir, file.path).split(path.sep).join('/');
    const data = (file.data.frontmatter ??= {}) as FrontMatter;
    if (isGenerated(contentPath)) {
      const index = tree.children.findIndex((node) => node.type === 'heading' && node.depth === 1);
      if (index >= 0) {
        data.title ||= textOf(tree.children[index] as Heading).trim();
        tree.children.splice(index, 1);
      }
      data.order ??= GENERATED_ORDER;
      data.description ||= GENERATED_DESCRIPTION;
    }
    if (!data.title) throw new Error(`${SITE_ROOT}${contentPath}: page has no title`);
    if (!data.description) throw new Error(`${SITE_ROOT}${contentPath}: front matter is missing "summary"`);
  };
}

function textOf(node: RootContent): string {
  if ('value' in node && typeof node.value === 'string') return node.value;
  if ('children' in node) return node.children.map((child) => textOf(child as RootContent)).join('');
  return '';
}

declare module 'vfile' {
  interface DataMap {
    frontmatter: Record<string, unknown>;
  }
}
