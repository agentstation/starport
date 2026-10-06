import path from 'node:path';
import type { Link, Root } from 'mdast';
import remarkParse from 'remark-parse';
import { unified } from 'unified';
import { VFile } from 'vfile';
import { describe, expect, it } from 'vitest';

import { remarkDocsLinks, resolvePath } from '../src/lib/remark-docs-links';
import { repoRoot } from '../scripts/release-facts.mjs';

const REF = 'v1.3.0';

// rewrite runs the link plugin over one Markdown string as if the string were
// the docs/site file at source.
function rewrite(markdown: string, source = 'docs/site/start/index.md', ref: string | null = REF): Root {
  const processor = unified().use(remarkParse).use(remarkDocsLinks, { repoRoot, ref });
  const file = new VFile({ path: path.join(repoRoot, source), value: markdown });
  const tree = processor.parse(file);
  processor.runSync(tree, file);
  return tree;
}

function links(tree: Root): Link[] {
  const found: Link[] = [];
  const walk = (node: { type: string; children?: unknown[] }) => {
    if (node.type === 'link') found.push(node as Link);
    for (const child of node.children ?? []) walk(child as { type: string; children?: unknown[] });
  };
  walk(tree);
  return found;
}

function firstUrl(markdown: string, source?: string): string {
  const [link] = links(rewrite(markdown, source));
  if (!link) throw new Error('no link');
  return link.url;
}

describe('remarkDocsLinks', () => {
  it('rewrites a relative page link in the same directory', () => {
    expect(firstUrl('[Keys](keys-and-roles.md)')).toBe('/docs/start/keys-and-roles');
  });

  it('rewrites a parent link to a page in another area', () => {
    expect(firstUrl('[Backends](../storage/backends.md)')).toBe('/docs/storage/backends');
  });

  it('rewrites an index link to its directory', () => {
    expect(firstUrl('[Storage](../storage/index.md)')).toBe('/docs/storage');
    expect(firstUrl('[Home](../index.md)')).toBe('/docs');
    expect(firstUrl('[Start](start/index.md)', 'docs/site/index.md')).toBe('/docs/start');
  });

  it('keeps the anchor of a page link', () => {
    expect(firstUrl('[Roles](keys-and-roles.md#roles)')).toBe('/docs/start/keys-and-roles#roles');
    expect(firstUrl('[Start](index.md#two-kinds-of-credential)')).toBe('/docs/start#two-kinds-of-credential');
  });

  it('does not change a fragment link or an external link', () => {
    expect(firstUrl('[Here](#choose-a-first-gateway)')).toBe('#choose-a-first-gateway');
    expect(firstUrl('[Site](https://example.com/a.md)')).toBe('https://example.com/a.md');
    expect(firstUrl('[Mail](mailto:ops@example.com)')).toBe('mailto:ops@example.com');
  });

  it('moves a generated page link into its area', () => {
    expect(firstUrl('[Settings](../generated/settings.md#server)')).toBe('/docs/configure/settings#server');
  });

  it('pins a repository file link to the reference', () => {
    expect(firstUrl('[Guide](../../OPERATOR-GUIDE.md#backups)')).toBe(
      `https://github.com/agentstation/starport/blob/${REF}/docs/OPERATOR-GUIDE.md#backups`,
    );
  });

  it('pins a repository directory link as a tree', () => {
    expect(firstUrl('[Proof](../../proof)')).toBe(`https://github.com/agentstation/starport/tree/${REF}/docs/proof`);
  });

  it('keeps the text of a repository link when no reference exists', () => {
    const tree = rewrite('See [the guide](../../OPERATOR-GUIDE.md).', undefined, null);
    expect(links(tree)).toEqual([]);
    const paragraph = tree.children[0];
    expect(paragraph?.type).toBe('paragraph');
    const text = paragraph && 'children' in paragraph ? paragraph.children.map((child) => ('value' in child ? child.value : '')).join('') : '';
    expect(text).toBe('See the guide.');
  });

  it('stops the build for a link that leaves the repository', () => {
    expect(() => rewrite('[Out](../../../../outside.md)')).toThrow(/leaves the repository/);
  });

  it('stops the build for a link to a missing file', () => {
    expect(() => rewrite('[Gone](missing-page.md)')).toThrow(/does not exist/);
  });

  it('stops the build for a docs/site link that is not a page', () => {
    expect(() => rewrite('[Data](../generated/settings.json)')).toThrow(/does not name a documentation page/);
  });

  it('stops the build for a link to an unpinned branch', () => {
    expect(() => rewrite('[Main](https://github.com/agentstation/starport/blob/main/README.md)')).toThrow(
      /unpinned branch/,
    );
  });
});

describe('resolvePath', () => {
  it('normalizes dot segments', () => {
    expect(resolvePath('docs/site/start/index.md', './a/../b.md')).toBe('docs/site/start/b.md');
  });

  it('returns null above the repository root', () => {
    expect(resolvePath('README.md', '../x.md')).toBeNull();
  });
});
