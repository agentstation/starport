import { readFileSync } from 'node:fs';
import path from 'node:path';

import { release, starmapVersion } from '../../scripts/release-facts.mjs';

import { README } from './splash-facts';

// The facts that the splash page reads from the repository files at build
// time. src/lib/splash-facts.ts holds the static facts, and this module
// gives them to the server components and the tests with the same import.
export * from './splash-facts';

const repoRoot = path.resolve(process.cwd(), '..');

export function readRepoFile(file: string): string {
  return readFileSync(path.join(repoRoot, file), 'utf8').replace(/\r\n/g, '\n');
}

// lede returns the first paragraph of README.md that starts with "Starport
// is", joined into one line.
export function lede(): string {
  const paragraph = readRepoFile(README)
    .split('\n\n')
    .find((block) => block.startsWith('Starport is '));
  if (!paragraph) throw new Error('README.md has no paragraph that starts with "Starport is"');
  return paragraph.replace(/\n/g, ' ');
}

// posterSize reads the width and the height from the PNG header of the demo
// poster. The page uses them to keep the space of the demo before it loads.
export function posterSize(): { width: number; height: number } {
  const png = readFileSync(path.join(repoRoot, 'docs/assets/first-use-v1.3.0/poster.png'));
  return { width: png.readUInt32BE(16), height: png.readUInt32BE(20) };
}

// siteBuild gives the release name and the Starmap module version of this
// build. The manifest reads the same helper.
export function siteBuild(): { release: string; starmap: string } {
  return { release: release(), starmap: starmapVersion() };
}
