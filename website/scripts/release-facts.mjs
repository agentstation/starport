// release-facts computes the identity of a documentation build. The identity
// has five parts: the release name, the Starmap module version, the content
// revision, the time, and the git reference that repository links pin.
//
// These functions repeat the definitions in console/scripts/build-docs.mjs.
// That script runs its build when a module imports it. Thus this package
// cannot import the functions from it. test/release-facts.test.ts runs both
// definitions over the real tree and fails when they differ.
//
// Environment:
//   STARPORT_DOCS_RELEASE  release name, for example v1.3.0 (default: the
//                          tag that GitHub Actions builds, else "dev")
//   STARPORT_DOCS_COMMIT   commit for repository links in a dev build
//                          (default: GITHUB_SHA, else `git rev-parse HEAD`)
//   SOURCE_DATE_EPOCH      build time in seconds (default: the commit time)
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
export const siteDir = path.join(repoRoot, "docs", "site");

export function sha256(data) {
  return createHash("sha256").update(data).digest("hex");
}

// Paths are repository-relative with forward slashes, sorted by code unit,
// so every machine reads the tree in the same order.
export function listFiles(dir) {
  const files = [];
  const walk = (current) => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const full = path.join(current, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.isFile()) files.push(path.relative(repoRoot, full).split(path.sep).join("/"));
    }
  };
  walk(dir);
  return files.sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
}

// content_revision is the sha256 of one line per content file, in path
// order: the path, a space, and the sha256 of the file bytes.
export function contentRevision(files) {
  const lines = files.map((file) => `${file} ${sha256(readFileSync(path.join(repoRoot, file)))}\n`);
  return sha256(lines.join(""));
}

export function starmapVersion() {
  const goMod = readFileSync(path.join(repoRoot, "go.mod"), "utf8");
  const match = /^\s*github\.com\/agentstation\/starmap\s+(\S+)/m.exec(goMod);
  if (!match) throw new Error("go.mod does not require github.com/agentstation/starmap");
  return match[1];
}

function git(...args) {
  try {
    return execFileSync("git", args, { cwd: repoRoot, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim();
  } catch {
    return "";
  }
}

export function release() {
  let value = process.env.STARPORT_DOCS_RELEASE?.trim() ?? "";
  if (!value && process.env.GITHUB_REF_TYPE === "tag") value = process.env.GITHUB_REF_NAME?.trim() ?? "";
  if (!value || value === "dev") return "dev";
  return /^\d+\.\d+\.\d+/.test(value) ? `v${value}` : value;
}

// The reference that repository links pin. A release pins its tag. A dev
// build pins its commit, and a build without git has no pin, so its
// repository links render as plain text.
export function linkRef(releaseName) {
  if (releaseName !== "dev") return releaseName;
  return process.env.STARPORT_DOCS_COMMIT?.trim() || process.env.GITHUB_SHA?.trim() || git("rev-parse", "HEAD") || null;
}

export function generatedAt() {
  const epoch = process.env.SOURCE_DATE_EPOCH?.trim();
  if (epoch && /^\d+$/.test(epoch)) return new Date(Number(epoch) * 1000).toISOString();
  const committed = git("log", "-1", "--format=%cI");
  if (committed) return new Date(committed).toISOString();
  return new Date().toISOString();
}
