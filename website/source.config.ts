import path from 'node:path';
import { defineConfig, defineDocs } from 'fumadocs-mdx/config';
import { metaSchema } from 'fumadocs-core/source/schema';
import { z } from 'zod';

import { linkRef, release } from './scripts/release-facts.mjs';
import { remarkDocsLinks } from './src/lib/remark-docs-links';
import { remarkPageFrontMatter } from './src/lib/remark-page-front-matter';

// fumadocs-mdx bundles this file into .source/ and runs it from the package
// directory, so the repository root is the parent of the working directory.
const repoRoot = path.resolve(process.cwd(), '..');

export const docs = defineDocs({
  // The repository content tree. The site reads it in place, so the
  // in-binary renderer and this site render the same files.
  dir: '../docs/site',
  docs: {
    files: ['**/*.md'],
    /*
     * The four front matter keys that the in-binary renderer accepts. The
     * summary is the Fumadocs description, which the page header and the
     * search index read. A generated page has no front matter, so every key is
     * optional here and remarkPageFrontMatter completes the page or stops the
     * build.
     */
    schema: z
      .strictObject({
        title: z.string().min(1).optional(),
        area: z.string().min(1).optional(),
        order: z.number().int().nonnegative().optional(),
        summary: z.string().min(1).optional(),
      })
      .transform(({ summary, ...data }) => ({ ...data, description: summary })),
  },
  meta: {
    // docs/site holds no meta files. The JSON files beside the generated
    // pages are generator data, so no file is a meta file.
    files: [],
    schema: metaSchema,
  },
});

export default defineConfig({
  mdxOptions: {
    remarkPlugins: (plugins) => [
      [remarkPageFrontMatter, { repoRoot }],
      [remarkDocsLinks, { repoRoot, ref: linkRef(release()) }],
      ...plugins,
    ],
  },
});
