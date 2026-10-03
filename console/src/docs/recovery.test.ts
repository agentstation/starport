import { expect, test } from "vitest";

import { buildTestSite, parsePage, SITE_FILES } from "@/test/docsSite";

import { loadSearchIndex, searchDocs } from "./search";

// CSP18 (CPL-V45). The recovery topic is the page an operator reads when the
// console does not open, so it must name the real health routes, open
// without a session, and be easy to find.
const site = buildTestSite();
const source = SITE_FILES.find((file) => file.path === "docs/site/troubleshoot/recovery.md")?.text ?? "";

function page(path: string): Document {
  const entry = site.pages.find((candidate) => candidate.path === path);
  if (!entry) throw new Error(`no page ${path}`);
  return parsePage(entry.html);
}

test("the recovery topic is a page under Troubleshoot", () => {
  expect(source).not.toBe("");
  const recovery = page("troubleshoot/recovery/index.html");
  expect(recovery.querySelector("h1")?.textContent).toMatch(/recover/i);
});

test("the recovery topic names the liveness and readiness routes", () => {
  const text = page("troubleshoot/recovery/index.html").querySelector("main")?.textContent ?? "";
  expect(text).toContain("GET /health/live");
  expect(text).toContain("GET /health/ready");
  // The gateway has no bare /health route.
  expect(text).not.toMatch(/GET \/health(?![/\w])/);
  expect(text).toContain("Retry-After");
});

test("the Troubleshoot area and the navigation link the recovery topic", () => {
  for (const path of ["troubleshoot/index.html", "index.html"]) {
    const links = Array.from(page(path).querySelectorAll("a")).map((link) => link.getAttribute("href") ?? "");
    expect(links.some((href) => /(^|\/)troubleshoot\/recovery\/$|^recovery\/$/.test(href)), path).toBe(true);
  }
});

test("search finds the recovery topic by the health routes", () => {
  const index = loadSearchIndex(site.searchIndex);
  const hrefs = searchDocs(index, "health ready").map((hit) => hit.href);
  expect(hrefs.some((href) => href.startsWith("troubleshoot/recovery/"))).toBe(true);
});
