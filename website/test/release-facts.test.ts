import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

import * as site from '../scripts/release-facts.mjs';

/*
 * console/scripts/build-docs.mjs starts its build when a module imports it.
 * This test copies the part of that script that defines the release facts,
 * which ends before dropTailwindTheme, into a temporary module. The copy has
 * no vite import and gets the console directory as a literal. Then the test
 * runs both definitions and compares the results.
 */
const consoleScript = path.join(site.repoRoot, 'console', 'scripts', 'build-docs.mjs');
const EXPORTS = ['sha256', 'listFiles', 'contentRevision', 'starmapVersion', 'release', 'linkRef', 'generatedAt'];

type Facts = Pick<typeof site, 'sha256' | 'listFiles' | 'contentRevision' | 'starmapVersion' | 'release' | 'linkRef' | 'generatedAt'> & {
  siteDir: string;
};

let tempDir: string;
let console_: Facts;

function consoleFactsModule(): string {
  const text = readFileSync(consoleScript, 'utf8');
  const end = text.indexOf('\nfunction dropTailwindTheme');
  if (end < 0) throw new Error('build-docs.mjs no longer defines dropTailwindTheme; update this test');
  const viteImport = 'import { build, runnerImport } from "vite";\n';
  const directory = 'path.dirname(fileURLToPath(import.meta.url))';
  let facts = text.slice(0, end);
  for (const part of [viteImport, directory]) {
    if (!facts.includes(part)) throw new Error(`build-docs.mjs no longer contains ${JSON.stringify(part)}; update this test`);
  }
  facts = facts.replace(viteImport, '').replace(directory, JSON.stringify(path.dirname(consoleScript)));
  return `${facts}\nexport { siteDir, ${EXPORTS.join(', ')} };\n`;
}

beforeAll(async () => {
  tempDir = mkdtempSync(path.join(os.tmpdir(), 'starport-site-facts-'));
  const file = path.join(tempDir, 'build-docs-facts.mjs');
  writeFileSync(file, consoleFactsModule());
  console_ = (await import(/* @vite-ignore */ pathToFileURL(file).href)) as Facts;
});

afterAll(() => rmSync(tempDir, { recursive: true, force: true }));

afterEach(() => vi.unstubAllEnvs());

const RELEASE_ENV = ['STARPORT_DOCS_RELEASE', 'GITHUB_REF_TYPE', 'GITHUB_REF_NAME', 'STARPORT_DOCS_COMMIT', 'GITHUB_SHA', 'SOURCE_DATE_EPOCH'];

function stubEnv(values: Record<string, string>) {
  for (const name of RELEASE_ENV) vi.stubEnv(name, values[name] ?? '');
}

describe('release facts', () => {
  it('reads the same content tree', () => {
    expect(site.siteDir).toBe(console_.siteDir);
    const files = site.listFiles(site.siteDir);
    expect(files.length).toBeGreaterThan(40);
    expect(files).toEqual(console_.listFiles(console_.siteDir));
  });

  it('computes the same content revision as the console build', () => {
    const files = site.listFiles(site.siteDir);
    const revision = site.contentRevision(files);
    expect(revision).toMatch(/^[0-9a-f]{64}$/);
    expect(revision).toBe(console_.contentRevision(console_.listFiles(console_.siteDir)));
  });

  it('reads the same Starmap version from go.mod', () => {
    expect(site.starmapVersion()).toBe(console_.starmapVersion());
    expect(site.sha256('starport')).toBe(console_.sha256('starport'));
  });

  const cases: Array<[string, Record<string, string>, string]> = [
    ['no release', {}, 'dev'],
    ['an explicit release', { STARPORT_DOCS_RELEASE: 'v1.3.0' }, 'v1.3.0'],
    ['a release without a v', { STARPORT_DOCS_RELEASE: '1.3.0' }, 'v1.3.0'],
    ['an explicit dev release', { STARPORT_DOCS_RELEASE: 'dev', GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'v9.9.9' }, 'dev'],
    ['a tag build', { GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'v1.4.0' }, 'v1.4.0'],
    ['a branch build', { GITHUB_REF_TYPE: 'branch', GITHUB_REF_NAME: 'main' }, 'dev'],
    ['an override of a tag build', { STARPORT_DOCS_RELEASE: 'v2.0.0', GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'v1.4.0' }, 'v2.0.0'],
  ];

  it.each(cases)('names the same release for %s', (_name, env, expected) => {
    stubEnv(env);
    expect(site.release()).toBe(expected);
    expect(console_.release()).toBe(expected);
  });

  it('pins repository links to the same reference', () => {
    stubEnv({});
    expect(site.linkRef('v1.3.0')).toBe('v1.3.0');
    expect(site.linkRef('dev')).toBe(console_.linkRef('dev'));
    stubEnv({ GITHUB_SHA: 'abc123' });
    expect(site.linkRef('dev')).toBe('abc123');
    expect(console_.linkRef('dev')).toBe('abc123');
    stubEnv({ GITHUB_SHA: 'abc123', STARPORT_DOCS_COMMIT: 'def456' });
    expect(site.linkRef('dev')).toBe('def456');
    expect(console_.linkRef('dev')).toBe('def456');
  });

  it('gives the same build time', () => {
    stubEnv({ SOURCE_DATE_EPOCH: '1790000000' });
    expect(site.generatedAt()).toBe('2026-09-21T14:13:20.000Z');
    expect(console_.generatedAt()).toBe(site.generatedAt());
    stubEnv({});
    expect(site.generatedAt()).toBe(console_.generatedAt());
  });
});
