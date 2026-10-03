// build-docs prerenders the documentation site from docs/site into
// internal/console/dist/docs, next to the console build, so go:embed ships
// both. It runs after `vite build`, which empties dist.
//
// The pages come from src/docs/site.tsx, which is pure: this script owns
// every file read, environment value, and write, and passes the results in.
// The build makes no network request.
//
// Environment:
//   STARPORT_DOCS_RELEASE  release name, for example v1.3.0 (default: the
//                          tag that GitHub Actions builds, else "dev")
//   STARPORT_DOCS_COMMIT   commit for repository links in a dev build
//                          (default: GITHUB_SHA, else `git rev-parse HEAD`)
//   SOURCE_DATE_EPOCH      build time in seconds (default: the commit time)
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { build, runnerImport } from "vite";

const consoleDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = path.resolve(consoleDir, "..");
const siteDir = path.join(repoRoot, "docs", "site");
const outDir = path.join(repoRoot, "internal", "console", "dist", "docs");
const repository = "https://github.com/agentstation/starport";

function sha256(data) {
  return createHash("sha256").update(data).digest("hex");
}

// Paths are repository-relative with forward slashes, sorted by code unit,
// so every machine reads the tree in the same order.
function listFiles(dir) {
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
function contentRevision(files) {
  const lines = files.map((file) => `${file} ${sha256(readFileSync(path.join(repoRoot, file)))}\n`);
  return sha256(lines.join(""));
}

function starmapVersion() {
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

function release() {
  let value = process.env.STARPORT_DOCS_RELEASE?.trim() ?? "";
  if (!value && process.env.GITHUB_REF_TYPE === "tag") value = process.env.GITHUB_REF_NAME?.trim() ?? "";
  if (!value || value === "dev") return "dev";
  return /^\d+\.\d+\.\d+/.test(value) ? `v${value}` : value;
}

// The reference that repository links pin. A release pins its tag. A dev
// build pins its commit, and a build without git has no pin, so its
// repository links render as plain text.
function linkRef(releaseName) {
  if (releaseName !== "dev") return releaseName;
  return process.env.STARPORT_DOCS_COMMIT?.trim() || process.env.GITHUB_SHA?.trim() || git("rev-parse", "HEAD") || null;
}

function generatedAt() {
  const epoch = process.env.SOURCE_DATE_EPOCH?.trim();
  if (epoch && /^\d+$/.test(epoch)) return new Date(Number(epoch) * 1000).toISOString();
  const committed = git("log", "-1", "--format=%cI");
  if (committed) return new Date(committed).toISOString();
  return new Date().toISOString();
}

// dropTailwindTheme removes the @theme block that site.css takes in with
// tokens.css. The block maps the role tokens to Tailwind utilities, and the
// site does not run Tailwind, so the CSS minifier would otherwise reject the
// unknown at-rule. It runs after Vite inlines the import.
function dropTailwindTheme() {
  return {
    name: "starport-docs-drop-tailwind-theme",
    transform(code, id) {
      if (!id.split("?")[0].endsWith("/src/docs/site.css")) return null;
      const start = code.search(/^@theme\b/m);
      if (start < 0) return null;
      let depth = 0;
      for (let index = code.indexOf("{", start); index < code.length; index++) {
        if (code[index] === "{") depth++;
        else if (code[index] === "}" && --depth === 0) {
          return { code: code.slice(0, start) + code.slice(index + 1), map: null };
        }
      }
      throw new Error("site.css has an unclosed @theme block");
    },
  };
}

// buildClient bundles the client script and the stylesheet with its fonts.
// The script is one IIFE because the console CSP allows scripts from 'self'
// only and the pages must work from a directory on disk.
async function buildClient() {
  const result = await build({
    configFile: false,
    root: consoleDir,
    base: "./",
    logLevel: "warn",
    publicDir: false,
    plugins: [dropTailwindTheme()],
    resolve: { alias: { "@": path.join(consoleDir, "src") } },
    build: {
      write: false,
      outDir,
      emptyOutDir: false,
      assetsInlineLimit: 0,
      cssCodeSplit: false,
      modulePreload: false,
      reportCompressedSize: false,
      rolldownOptions: {
        input: path.join(consoleDir, "src", "docs", "client", "main.ts"),
        output: {
          format: "iife",
          entryFileNames: "assets/docs-[hash].js",
          assetFileNames: (asset) =>
            asset.names.some((name) => name.endsWith(".css"))
              ? "assets/docs-[hash][extname]"
              : "assets/[name]-[hash][extname]",
        },
      },
    },
  });
  const outputs = (Array.isArray(result) ? result : [result]).flatMap((entry) => entry.output);
  const files = new Map();
  let script = "";
  let stylesheet = "";
  for (const item of outputs) {
    files.set(item.fileName, item.type === "chunk" ? item.code : item.source);
    if (item.type === "chunk" && item.isEntry) script = item.fileName;
    if (item.type === "asset" && item.fileName.endsWith(".css")) stylesheet = item.fileName;
  }
  if (!script || !stylesheet) throw new Error("the docs client build did not produce one script and one stylesheet");
  if (outputs.filter((item) => item.type === "chunk").length !== 1) {
    throw new Error("the docs client build split into more than one script");
  }
  return { files, script, stylesheet };
}

function repositoryUrl(ref) {
  return (repositoryPath) => {
    const full = path.join(repoRoot, repositoryPath);
    if (!existsSync(full)) throw new Error(`a documentation link names ${repositoryPath}, which does not exist`);
    if (ref === null) return null;
    const kind = statSync(full).isDirectory() ? "tree" : "blob";
    return `${repository}/${kind}/${ref}/${repositoryPath}`;
  };
}

function write(relativePath, data) {
  const full = path.join(outDir, relativePath);
  mkdirSync(path.dirname(full), { recursive: true });
  writeFileSync(full, data);
}

async function main() {
  const contentFiles = listFiles(siteDir);
  const facts = {
    release: release(),
    starmapVersion: starmapVersion(),
    contentRevision: contentRevision(contentFiles),
  };
  const ref = linkRef(facts.release);
  const client = await buildClient();

  const { module: site } = await runnerImport(path.join(consoleDir, "src", "docs", "site.tsx"), {
    configFile: false,
    root: consoleDir,
    logLevel: "warn",
  });
  const output = site.buildSite({
    files: contentFiles
      .filter((file) => file.endsWith(".md"))
      .map((file) => ({ path: file, text: readFileSync(path.join(repoRoot, file), "utf8") })),
    facts,
    assets: { script: client.script, stylesheet: client.stylesheet },
    repositoryUrl: repositoryUrl(ref),
  });

  rmSync(outDir, { recursive: true, force: true });
  const written = new Map();
  const add = (relativePath, data) => {
    write(relativePath, data);
    written.set(relativePath, sha256(data));
  };
  for (const [fileName, data] of client.files) add(fileName, data);
  for (const page of output.pages) add(page.path, page.html);
  add("search-index.json", output.searchIndex);
  // A page opened from disk cannot fetch the JSON index, so the client
  // loads this script form instead.
  add("search-index.js", `window.__STARPORT_DOCS_SEARCH__ = ${JSON.stringify(output.searchIndex)};\n`);

  const files = Object.fromEntries([...written].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)));
  const manifest = {
    starport_release: facts.release,
    starmap_module_version: facts.starmapVersion,
    content_revision: facts.contentRevision,
    generated_at: generatedAt(),
    files,
  };
  write("manifest.json", `${JSON.stringify(manifest, null, 2)}\n`);
  console.log(
    `docs: ${output.pages.length} pages, ${output.searchDocuments.length} search sections, release ${facts.release}, content ${facts.contentRevision.slice(0, 12)}`,
  );
}

await main();
