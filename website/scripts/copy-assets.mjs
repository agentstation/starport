// copy-assets copies the repository files that the site serves into public/
// before the build. The repository keeps one copy of each file. Thus the
// site cannot drift from the README demo or the console icon. public/ is not
// in git.
import { copyFileSync, mkdirSync } from "node:fs";
import path from "node:path";

import { repoRoot } from "./release-facts.mjs";

const publicDir = path.join(repoRoot, "website", "public");

// The published path under public/, then the repository source.
export const ASSETS = [
  ["demo/first-use.gif", "docs/assets/first-use-v1.2.0/first-use.gif"],
  ["demo/poster.png", "docs/assets/first-use-v1.2.0/poster.png"],
  ["favicon.svg", "console/src/favicon.svg"],
];

for (const [published, source] of ASSETS) {
  const target = path.join(publicDir, published);
  mkdirSync(path.dirname(target), { recursive: true });
  copyFileSync(path.join(repoRoot, source), target);
}
console.log(`assets: copied ${ASSETS.length} files into public/`);
