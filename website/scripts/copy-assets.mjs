// copy-assets copies the repository files that the site serves into public/
// before the build. The repository keeps one copy of each file. Thus the
// site cannot drift from the README demo, the console icon, or the console
// fonts. public/ is not in git.
import { copyFileSync, mkdirSync } from "node:fs";
import path from "node:path";

import { repoRoot } from "./release-facts.mjs";

const publicDir = path.join(repoRoot, "website", "public");

// The published path under public/, then the repository source.
export const ASSETS = [
  ["demo/first-use.gif", "docs/assets/first-use-v1.3.0/first-use.gif"],
  ["demo/poster.png", "docs/assets/first-use-v1.3.0/poster.png"],
  ...["console.mp4", "console.webm", "console.gif", "poster.png", "title-preview.png", "record.json", "TRANSCRIPT.md"].map((file) => [
    `demo/console/${file}`, `docs/assets/console-demo/${file}`,
  ]),
  ["favicon.svg", "console/src/favicon.svg"],
  // The splash page uses the console fonts and ships their license.
  ...[
    "Geist-Regular.woff2",
    "Geist-Medium.woff2",
    "Geist-SemiBold.woff2",
    "GeistMono-Regular.woff2",
    "GeistMono-Medium.woff2",
    "GeistMono-SemiBold.woff2",
    "LICENSE.txt",
  ].map((file) => [`fonts/${file}`, `console/src/fonts/${file}`]),
];

for (const [published, source] of ASSETS) {
  const target = path.join(publicDir, published);
  mkdirSync(path.dirname(target), { recursive: true });
  copyFileSync(path.join(repoRoot, source), target);
}
console.log(`assets: copied ${ASSETS.length} files into public/`);
