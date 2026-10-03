import type { SourceFile } from "@/docs/content";
import { buildSite, type BuildFacts, type SiteOutput } from "@/docs/site";

// The real content tree, read through Vite, so the structural tests cover
// every page that the build ships, including docs/site/generated when a
// generator has written it.
const raw = import.meta.glob<string>("../../../docs/site/**/*.md", {
  query: "?raw",
  import: "default",
  eager: true,
});

export const SITE_FILES: SourceFile[] = Object.entries(raw)
  .map(([key, text]) => ({ path: key.replace(/^(\.\.\/)+/, ""), text }))
  .sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));

export const TEST_FACTS: BuildFacts = {
  release: "v9.9.9",
  starmapVersion: "v0.0.0-test",
  contentRevision: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
};

export const TEST_ASSETS = { script: "assets/docs-test.js", stylesheet: "assets/docs-test.css" };

export function pinnedRepositoryUrl(repositoryPath: string): string {
  return `https://github.com/agentstation/starport/blob/${TEST_FACTS.release}/${repositoryPath}`;
}

export function buildTestSite(files: readonly SourceFile[] = SITE_FILES): SiteOutput {
  return buildSite({ files, facts: TEST_FACTS, assets: TEST_ASSETS, repositoryUrl: pinnedRepositoryUrl });
}

export function parsePage(html: string): Document {
  return new DOMParser().parseFromString(html, "text/html");
}

// loadPage puts a built page into the test document, the way a browser
// that opened it would hold it.
export function loadPage(html: string): void {
  const page = parsePage(html);
  document.documentElement.innerHTML = page.documentElement.innerHTML;
}
