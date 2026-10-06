// build-manifest writes out/docs/manifest.json after `next build`. The
// manifest has the keys of the docs archive manifest that
// console/scripts/build-docs.mjs writes. For the same docs/site tree, it has
// the same content_revision. The files key maps each file of the exported
// site, relative to out/, to its sha256. The manifest does not list itself.
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import path from "node:path";

import {
  contentRevision,
  generatedAt,
  listFiles,
  release,
  repoRoot,
  sha256,
  siteDir,
  starmapVersion,
} from "./release-facts.mjs";

const outDir = path.join(repoRoot, "website", "out");
const manifestPath = path.join(outDir, "docs", "manifest.json");

function listOutput(dir) {
  const files = [];
  const walk = (current) => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const full = path.join(current, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.isFile() && full !== manifestPath) files.push(path.relative(outDir, full).split(path.sep).join("/"));
    }
  };
  walk(dir);
  return files.sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
}

const files = Object.fromEntries(listOutput(outDir).map((file) => [file, sha256(readFileSync(path.join(outDir, file)))]));
const manifest = {
  starport_release: release(),
  starmap_module_version: starmapVersion(),
  content_revision: contentRevision(listFiles(siteDir)),
  generated_at: generatedAt(),
  files,
};
writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
console.log(
  `manifest: ${Object.keys(files).length} files, release ${manifest.starport_release}, content ${manifest.content_revision.slice(0, 12)}`,
);
