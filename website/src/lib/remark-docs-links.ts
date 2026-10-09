import { existsSync, statSync } from 'node:fs';
import path from 'node:path';
import type { Link, Root, RootContent } from 'mdast';
import type { VFile } from 'vfile';

import { SITE_ROOT, docsUrl } from './docs-routes';

const REPOSITORY = 'https://github.com/agentstation/starport';

export type DocsLinkOptions = {
  // The absolute path of the repository root.
  repoRoot: string;
  // The git reference that repository links pin: a release tag or a commit.
  // When it is null, a link to a repository file keeps its text and loses
  // its target, as in the in-binary renderer.
  ref: string | null;
};

// remarkDocsLinks rewrites the relative Markdown links in docs/site to site
// URLs. A link to a page under docs/site becomes /docs/<route> and keeps its
// fragment. A link to another repository file becomes a GitHub URL at the
// pinned reference. External links and fragment links do not change. A link
// that leaves the repository or names a missing file stops the build.
export function remarkDocsLinks(options: DocsLinkOptions) {
  return (tree: Root, file: VFile) => {
    const source = path.relative(options.repoRoot, file.path).split(path.sep).join('/');
    visitLinks(tree, (link) => {
      const url = link.url;
      if (/github\.com\/agentstation\/starport\/(blob|tree)\/main\b/.test(url)) {
        throw new Error(`${source}: link ${url} names an unpinned branch; use a relative repository path`);
      }
      if (url === '' || url.startsWith('#') || isExternal(url)) return undefined;
      const hashAt = url.indexOf('#');
      const target = resolvePath(source, decodeURI(hashAt < 0 ? url : url.slice(0, hashAt)));
      const fragment = hashAt < 0 ? '' : url.slice(hashAt);
      if (target === null) throw new Error(`${source}: link ${url} leaves the repository`);
      const full = path.join(options.repoRoot, target);
      if (!existsSync(full)) throw new Error(`${source}: link ${url} names ${target}, which does not exist`);
      if (target.startsWith(SITE_ROOT)) {
        if (!target.endsWith('.md')) throw new Error(`${source}: link ${url} does not name a documentation page`);
        link.url = docsUrl(target.slice(SITE_ROOT.length)) + fragment;
        return undefined;
      }
      if (options.ref === null) return link.children;
      const kind = statSync(full).isDirectory() ? 'tree' : 'blob';
      link.url = `${REPOSITORY}/${kind}/${options.ref}/${target}${fragment}`;
      return undefined;
    });
  };
}

// resolvePath joins a relative link to the directory of its source file and
// normalizes it. It returns null when the link leaves the repository.
export function resolvePath(source: string, target: string): string | null {
  const segments = source.split('/').slice(0, -1);
  for (const part of target.split('/')) {
    if (part === '' || part === '.') continue;
    if (part === '..') {
      if (segments.length === 0) return null;
      segments.pop();
      continue;
    }
    segments.push(part);
  }
  return segments.join('/');
}

function isExternal(url: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(url) || url.startsWith('//');
}

type Parent = { children: RootContent[] };

// visitLinks calls visit for each link. A returned list replaces the link.
function visitLinks(node: Parent, visit: (link: Link) => RootContent[] | undefined) {
  for (let index = 0; index < node.children.length; index++) {
    const child = node.children[index];
    if (!child) continue;
    if (child.type === 'link') {
      const replacement = visit(child);
      if (replacement) {
        node.children.splice(index, 1, ...replacement);
        index += replacement.length - 1;
      }
      continue;
    }
    if ('children' in child) visitLinks(child as Parent, visit);
  }
}
