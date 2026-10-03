import { expect, test } from "vitest";

import { buildTestSite, parsePage, pinnedRepositoryUrl, SITE_FILES, TEST_ASSETS, TEST_FACTS } from "@/test/docsSite";

import { AREAS } from "./areas";
import { loadSearchIndex, searchDocs } from "./search";
import { buildSite } from "./site";

// CSP18. The documentation site is static HTML. These tests read every
// page the build writes from the real content tree, so a topic that breaks
// a heading, a link, or the page frame fails here before it ships.
const site = buildTestSite();
const pages = new Map(site.pages.map((page) => [page.path, parsePage(page.html)]));

test("every content file becomes one page at its area and topic path", () => {
  const expected = SITE_FILES.filter((file) => !file.path.startsWith("docs/site/generated/")).map((file) => {
    const relative = file.path.slice("docs/site/".length, -".md".length);
    if (relative === "index") return "index.html";
    return relative.endsWith("/index") ? `${relative}.html` : `${relative}/index.html`;
  });
  for (const path of expected) expect(pages.has(path), path).toBe(true);
  for (const area of AREAS) expect(pages.has(`${area.slug}/index.html`), area.slug).toBe(true);
  expect(pages.has("search/index.html")).toBe(true);
});

test("every h2, h3, and h4 has a unique id and an anchor to itself", () => {
  for (const [path, page] of pages) {
    const ids = new Set<string>();
    for (const heading of Array.from(page.querySelectorAll("main h2, main h3, main h4"))) {
      expect(heading.id, `${path}: ${heading.textContent}`).not.toBe("");
      expect(ids.has(heading.id), `${path}: duplicate id ${heading.id}`).toBe(false);
      ids.add(heading.id);
      const anchor = heading.querySelector("a.heading-anchor");
      expect(anchor?.getAttribute("href"), `${path}: ${heading.id}`).toBe(`#${heading.id}`);
      expect(anchor?.getAttribute("aria-label")).toMatch(/^Link to section: /);
    }
  }
});

test("each table of contents lists the h2 and h3 headings in order and every entry resolves", () => {
  for (const [path, page] of pages) {
    const headings = Array.from(page.querySelectorAll("main h2, main h3")).map((heading) => heading.id);
    const toc = page.querySelector('nav[aria-label="On this page"]');
    if (headings.length === 0) {
      expect(toc, path).toBeNull();
      continue;
    }
    const entries = Array.from(toc?.querySelectorAll("a") ?? []).map((link) => link.getAttribute("href") ?? "");
    expect(entries, path).toEqual(headings.map((id) => `#${id}`));
    for (const entry of entries) expect(page.getElementById(entry.slice(1)), `${path} ${entry}`).not.toBeNull();
  }
});

test("every page has a skip link, the landmarks, and one h1", () => {
  for (const [path, page] of pages) {
    const skip = page.body.firstElementChild;
    expect(skip?.getAttribute("href"), path).toBe("#content");
    expect(page.querySelector("main#content"), path).not.toBeNull();
    expect(page.querySelector("header.site-header"), path).not.toBeNull();
    expect(page.querySelector('nav[aria-label="Documentation"]'), path).not.toBeNull();
    expect(page.querySelector('form[role="search"]'), path).not.toBeNull();
    expect(page.querySelector("footer"), path).not.toBeNull();
    expect(page.querySelectorAll("h1"), path).toHaveLength(1);
    expect(page.documentElement.lang).toBe("en");
  }
});

test("the header shows the release, the Starmap version, and the content revision", () => {
  for (const [path, page] of pages) {
    expect(page.querySelector('[data-fact="release"]')?.textContent, path).toBe(TEST_FACTS.release);
    expect(page.querySelector('[data-fact="starmap"]')?.textContent, path).toBe(TEST_FACTS.starmapVersion);
    expect(page.querySelector('[data-fact="content"]')?.textContent, path).toBe(TEST_FACTS.contentRevision.slice(0, 12));
    // The embedded build knows only itself.
    expect(page.querySelectorAll(".version-selector a"), path).toHaveLength(1);
  }
});

test("pages carry no inline script and load only the build's own assets", () => {
  for (const [path, page] of pages) {
    const scripts = Array.from(page.querySelectorAll("script"));
    expect(scripts, path).toHaveLength(1);
    expect(scripts[0]?.textContent, path).toBe("");
    expect(scripts[0]?.getAttribute("src")?.endsWith(TEST_ASSETS.script), path).toBe(true);
    const sheets = Array.from(page.querySelectorAll('link[rel="stylesheet"]'));
    expect(sheets.map((sheet) => sheet.getAttribute("href")?.endsWith(TEST_ASSETS.stylesheet)), path).toEqual([true]);
    expect(page.querySelectorAll("[style]"), path).toHaveLength(0);
  }
});

test("every link is relative or a pinned repository link, and every relative link resolves", () => {
  const base = "https://docs.test/docs/";
  for (const [path, page] of pages) {
    for (const element of Array.from(page.querySelectorAll("a[href], form[action]"))) {
      const href = element.getAttribute("href") ?? element.getAttribute("action") ?? "";
      expect(href, path).not.toMatch(/\/(blob|tree)\/main\//);
      if (/^https?:/.test(href)) {
        expect(href.startsWith(`https://github.com/agentstation/starport/blob/${TEST_FACTS.release}/`) ||
          href.startsWith(`https://github.com/agentstation/starport/tree/${TEST_FACTS.release}/`), `${path}: ${href}`).toBe(true);
        continue;
      }
      expect(href.startsWith("/"), `${path}: ${href} is root-absolute`).toBe(false);
      if (href.startsWith("#")) {
        expect(page.getElementById(decodeURIComponent(href.slice(1))), `${path}: ${href}`).not.toBeNull();
        continue;
      }
      const url = new URL(href, base + path);
      expect(url.pathname.startsWith("/docs/"), `${path}: ${href} leaves the site`).toBe(true);
      let target = url.pathname.slice("/docs/".length);
      if (target === "" || target.endsWith("/")) target += "index.html";
      const targetPage = pages.get(target);
      expect(targetPage, `${path}: ${href}`).toBeDefined();
      if (url.hash) {
        expect(targetPage?.getElementById(decodeURIComponent(url.hash.slice(1))), `${path}: ${href}`).not.toBeNull();
      }
    }
  }
});

test("the same input gives the same site", () => {
  const again = buildTestSite();
  expect(again.pages).toEqual(site.pages);
  expect(again.searchIndex).toBe(site.searchIndex);
});

test("without a pinned reference, repository links render as text", () => {
  const unpinned = buildSite({ files: SITE_FILES, facts: TEST_FACTS, assets: TEST_ASSETS, repositoryUrl: () => null });
  for (const page of unpinned.pages) expect(page.html).not.toContain("github.com/agentstation/starport/");
  const pinned = site.pages.find((page) => page.html.includes(pinnedRepositoryUrl("docs/RECOVERY.md")));
  expect(pinned).toBeDefined();
});

test("search covers headings, prose, and setting names", () => {
  const index = loadSearchIndex(site.searchIndex);
  const hrefs = (query: string) => searchDocs(index, query).map((hit) => hit.href);

  expect(hrefs("Health checks")).toContain("troubleshoot/recovery/#health-checks");
  expect(hrefs("STARPORT_SERVER_PORT")).toContain("troubleshoot/recovery/#related-settings");
  expect(hrefs("fenced until activation")).toContain("troubleshoot/recovery/#coordinated-recovery");
  // A part of a setting name also finds it.
  expect(hrefs("logging format").some((href) => href.startsWith("troubleshoot/recovery/"))).toBe(true);
  for (const document of site.searchDocuments) {
    const [route = "", fragment] = document.href.split("#");
    const page = pages.get(`${route}index.html`);
    expect(page, document.href).toBeDefined();
    if (fragment) expect(page?.getElementById(fragment), document.href).not.toBeNull();
  }
});

test("a generated page joins the site and the search index", () => {
  // The committed pages stay in the fixture because the Configure content
  // links to the generated settings reference.
  const files = [
    ...SITE_FILES,
    {
      path: "docs/site/generated/settings-reference.md",
      text: "# Settings reference\n\n## Server\n\n`STARPORT_TEST_SETTING` sets a test value.\n",
    },
  ];
  const output = buildTestSite(files);
  const page = output.pages.find((entry) => entry.path === "configure/settings-reference/index.html");
  expect(page).toBeDefined();
  const doc = parsePage(page?.html ?? "");
  expect(doc.querySelector("h1")?.textContent).toBe("Settings reference");
  expect(doc.querySelector("main h2")?.id).toBe("server");
  const index = loadSearchIndex(output.searchIndex);
  expect(searchDocs(index, "STARPORT_TEST_SETTING").map((hit) => hit.href)).toContain(
    "configure/settings-reference/#server",
  );
});

test("the site states that discovery readiness stays unknown and that Starport has no removal targets", () => {
  const text = SITE_FILES.map((file) => file.text).join("\n");
  expect(text).toMatch(/readiness[^.\n]*\bunknown\b/i);
  expect(text).toMatch(/no removal target/i);
});
