import { execFileSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeAll, describe, expect, it } from 'vitest';

import { contentRevision, listFiles, repoRoot, siteDir } from '../scripts/release-facts.mjs';
import { docsUrl } from '../src/lib/docs-routes';

const websiteDir = path.join(repoRoot, 'website');
const outDir = path.join(websiteDir, 'out');

// outputFile is the exported HTML file of a site URL. With trailingSlash
// false, Next writes /docs as docs.html and /docs/start as docs/start.html.
function outputFile(url: string): string {
  return path.join(outDir, `${url === '/' ? 'index' : url.slice(1)}.html`);
}

function pageUrls(): string[] {
  return listFiles(siteDir)
    .filter((file) => file.endsWith('.md'))
    .map((file) => docsUrl(file.slice('docs/site/'.length)));
}

function htmlFiles(dir: string): string[] {
  return readdirSync(dir, { recursive: true, encoding: 'utf8' })
    .filter((file) => file.endsWith('.html'))
    .map((file) => path.join(dir, file));
}

// The build runs once for the whole file, in the same steps as `pnpm build`.
beforeAll(() => {
  const run = (...args: string[]) => execFileSync(process.execPath, args, { cwd: websiteDir, stdio: 'inherit' });
  run('scripts/copy-assets.mjs');
  run(path.join('node_modules', 'next', 'dist', 'bin', 'next'), 'build');
  run('scripts/build-manifest.mjs');
});

describe('site build', () => {
  it('exports the exact Console media and transcript referenced by the splash', () => {
    for (const file of ['console.mp4', 'console.webm', 'console.gif', 'poster.png', 'title-preview.png', 'record.json', 'TRANSCRIPT.md']) {
      const source = readFileSync(path.join(repoRoot, 'docs/assets/console-demo', file));
      const exported = readFileSync(path.join(outDir, 'demo/console', file));
      expect(exported.equals(source), file).toBe(true);
    }
  });

  it('writes the splash page, the docs home, the start page, and the 404 page', () => {
    for (const file of ['index.html', 'docs.html', 'docs/start.html', '404.html']) {
      expect(existsSync(path.join(outDir, file)), file).toBe(true);
    }
    expect(readFileSync(path.join(outDir, 'index.html'), 'utf8')).toContain('<html lang="en"');
  });

  it('writes one page for each docs/site page', () => {
    const urls = pageUrls();
    expect(urls.length).toBeGreaterThan(40);
    expect(urls).toContain('/docs/configure/settings');
    const missing = urls.filter((url) => !existsSync(outputFile(url)));
    expect(missing).toEqual([]);
  });

  it('writes the static search index', () => {
    const index = JSON.parse(readFileSync(path.join(outDir, 'docs', 'search.json'), 'utf8'));
    expect(index).toBeTypeOf('object');
    expect(JSON.stringify(index)).toContain('/docs/start');
  });

  it('writes the manifest with the console content revision', () => {
    const manifest = JSON.parse(readFileSync(path.join(outDir, 'docs', 'manifest.json'), 'utf8'));
    expect(Object.keys(manifest)).toEqual([
      'starport_release',
      'starmap_module_version',
      'content_revision',
      'generated_at',
      'files',
    ]);
    expect(manifest.content_revision).toBe(contentRevision(listFiles(siteDir)));
    expect(manifest.files['index.html']).toMatch(/^[0-9a-f]{64}$/);
    expect(manifest.files['docs/search.json']).toMatch(/^[0-9a-f]{64}$/);
    expect(manifest.files['docs/manifest.json']).toBeUndefined();
  });

  it('gives every page one h1', () => {
    const pages = ['/', ...pageUrls()].map(outputFile).concat(path.join(outDir, '404.html'));
    const wrong = pages.filter((file) => (readFileSync(file, 'utf8').match(/<h1[\s>]/g) ?? []).length !== 1);
    expect(wrong).toEqual([]);
  });

  it('leaves no Markdown link and resolves every documentation anchor', () => {
    const problems: string[] = [];
    for (const file of htmlFiles(path.join(outDir, 'docs')).concat(outputFile('/docs'))) {
      const html = readFileSync(file, 'utf8');
      for (const [, href] of html.matchAll(/href="([^"]+)"/g)) {
        if (!href) continue;
        if (/\.md(#|$)/.test(href) && !href.startsWith('https://github.com/')) problems.push(`${file}: ${href}`);
        const match = /^(\/docs[^#]*)?#(.+)$/.exec(href);
        if (!match) continue;
        const target = match[1] ? outputFile(match[1]) : file;
        if (!existsSync(target)) {
          problems.push(`${file}: ${href} names a missing page`);
          continue;
        }
        const id = decodeURIComponent(match[2] ?? '');
        if (!readFileSync(target, 'utf8').includes(`id="${id}"`)) problems.push(`${file}: ${href} names a missing anchor`);
      }
    }
    expect(problems).toEqual([]);
  });
});
