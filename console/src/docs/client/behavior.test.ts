import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { buildTestSite, loadPage } from "@/test/docsSite";

import {
  fixFileLinks,
  initCopyButtons,
  initDocs,
  initSearchPage,
  initTableOfContents,
  initThemeToggle,
  withIndexFile,
} from "./behavior";

// CSP18. The client script only adds to a page that already works: these
// tests load a built page into the test document and run each feature
// against it.
const site = buildTestSite();

function pageHtml(path: string): string {
  const page = site.pages.find((entry) => entry.path === path);
  if (!page) throw new Error(`no page ${path}`);
  return page.html;
}

const RECOVERY = "troubleshoot/recovery/index.html";

function currentEntries(): string[] {
  return Array.from(document.querySelectorAll('[data-toc] a[aria-current="location"]')).map(
    (link) => link.getAttribute("href") ?? "",
  );
}

// placeHeadings gives each heading a viewport position, because the test
// DOM does no layout. The ids listed are above the read line.
function placeHeadings(above: readonly string[]) {
  for (const heading of Array.from(document.querySelectorAll("main h2, main h3"))) {
    const top = above.includes(heading.id) ? -100 : 500;
    vi.spyOn(heading, "getBoundingClientRect").mockReturnValue({ top } as DOMRect);
  }
}

beforeEach(() => {
  history.replaceState(null, "", "/docs/troubleshoot/recovery/");
  try {
    localStorage.removeItem("starport.theme");
  } catch {
    // The theme falls back to the default.
  }
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  document.documentElement.removeAttribute("data-theme");
});

test("the table of contents marks the section of the fragment on load", () => {
  history.replaceState(null, "", "/docs/troubleshoot/recovery/#cli-inspection-commands");
  loadPage(pageHtml(RECOVERY));
  initTableOfContents(document, window);
  expect(currentEntries()).toEqual(["#cli-inspection-commands"]);
});

test("a fragment that names an h4 marks the h2 or h3 above it", () => {
  history.replaceState(null, "", "/docs/troubleshoot/recovery/#steps");
  loadPage(pageHtml(RECOVERY));
  initTableOfContents(document, window);
  expect(document.getElementById("steps")?.tagName).toBe("H3");
  expect(currentEntries()).toEqual(["#steps"]);
});

test("the marker follows fragment links, back, and forward", async () => {
  loadPage(pageHtml(RECOVERY));
  placeHeadings([]);
  initTableOfContents(document, window);
  expect(currentEntries()).toEqual(["#health-checks"]);

  const hashChanged = () => new Promise((resolve) => window.addEventListener("hashchange", resolve, { once: true }));

  let changed = hashChanged();
  window.location.hash = "#logs-and-request-ids";
  await changed;
  expect(currentEntries()).toEqual(["#logs-and-request-ids"]);

  changed = hashChanged();
  window.location.hash = "#coordinated-recovery";
  await changed;
  expect(currentEntries()).toEqual(["#coordinated-recovery"]);

  changed = hashChanged();
  history.back();
  await changed;
  expect(window.location.hash).toBe("#logs-and-request-ids");
  expect(currentEntries()).toEqual(["#logs-and-request-ids"]);

  changed = hashChanged();
  history.forward();
  await changed;
  expect(currentEntries()).toEqual(["#coordinated-recovery"]);
});

test("the marker follows the scroll position", async () => {
  loadPage(pageHtml(RECOVERY));
  placeHeadings(["health-checks", "cli-inspection-commands"]);
  initTableOfContents(document, window);
  expect(currentEntries()).toEqual(["#cli-inspection-commands"]);

  placeHeadings(["health-checks", "cli-inspection-commands", "local-admin-token-and-the-console"]);
  window.dispatchEvent(new Event("scroll"));
  await new Promise((resolve) => requestAnimationFrame(resolve));
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(currentEntries()).toEqual(["#local-admin-token-and-the-console"]);
});

test("a reload of the same address marks the same entry", () => {
  history.replaceState(null, "", "/docs/troubleshoot/recovery/#health-checks");
  loadPage(pageHtml(RECOVERY));
  initTableOfContents(document, window);
  const first = currentEntries();
  loadPage(pageHtml(RECOVERY));
  initTableOfContents(document, window);
  expect(currentEntries()).toEqual(first);
  expect(first).toEqual(["#health-checks"]);
});

test("the theme toggle appears with the script and switches the theme", () => {
  loadPage(pageHtml(RECOVERY));
  const button = document.querySelector<HTMLButtonElement>("[data-theme-toggle]");
  expect(button?.hidden).toBe(true);
  initThemeToggle(document);
  expect(button?.hidden).toBe(false);
  expect(button?.getAttribute("aria-label")).toBe("Switch to light theme");
  button?.click();
  expect(document.documentElement.dataset.theme).toBe("light");
  expect(button?.getAttribute("aria-label")).toBe("Switch to dark theme");
  button?.click();
  expect(document.documentElement.dataset.theme).toBe("dark");
});

test("copy controls appear with the script and copy the code block", async () => {
  loadPage(pageHtml(RECOVERY));
  const button = document.querySelector<HTMLButtonElement>("[data-copy]");
  expect(button?.hidden).toBe(true);
  const writeText = vi.fn(async () => {});
  const win = { navigator: { clipboard: { writeText } }, setTimeout: () => 0 } as unknown as Window;
  initCopyButtons(document, win);
  expect(button?.hidden).toBe(false);
  expect(button?.getAttribute("aria-label")).toBe("Copy bash code");
  button?.click();
  await vi.waitFor(() => expect(button?.textContent).toBe("Copied"));
  expect(writeText).toHaveBeenCalledWith(expect.stringContaining("<gateway-url>/health/live"));
});

test("a page read from disk links to index files", () => {
  expect(withIndexFile("../")).toBe("../index.html");
  expect(withIndexFile("./")).toBe("./index.html");
  expect(withIndexFile("../../storage/b/#copy")).toBe("../../storage/b/index.html#copy");
  expect(withIndexFile("..")).toBe("../index.html");
  expect(withIndexFile("assets/docs.css")).toBe("assets/docs.css");

  loadPage(pageHtml(RECOVERY));
  fixFileLinks(document, { location: { protocol: "file:" } } as unknown as Window);
  expect(document.querySelector(".site-brand")?.getAttribute("href")).toBe("../../index.html");
  expect(document.querySelector(".site-search")?.getAttribute("action")).toBe("../../search/index.html");
  expect(document.querySelector('[data-toc] a')?.getAttribute("href")).toBe("#health-checks");
  const repository = Array.from(document.querySelectorAll("a")).find((link) =>
    link.getAttribute("href")?.startsWith("https://"),
  );
  expect(repository?.getAttribute("href")).not.toContain("index.html");
});

test("a served page keeps its directory links", () => {
  loadPage(pageHtml(RECOVERY));
  fixFileLinks(document, window);
  expect(document.querySelector(".site-brand")?.getAttribute("href")).toBe("../../");
});

test("the search page reads the query from the address and lists results", async () => {
  history.replaceState(null, "", "/docs/search/?q=STARPORT_SERVER_PORT");
  loadPage(pageHtml("search/index.html"));
  const fetchIndex = vi.fn(async () => new Response(site.searchIndex, { status: 200 }));
  vi.stubGlobal("fetch", fetchIndex);

  initSearchPage(document, window);

  const status = document.getElementById("search-status");
  await vi.waitFor(() => expect(status?.textContent).toMatch(/results? for "STARPORT_SERVER_PORT"/));
  expect(fetchIndex).toHaveBeenCalledWith("../search-index.json");
  expect(document.querySelector<HTMLInputElement>("#search-page-input")?.value).toBe("STARPORT_SERVER_PORT");
  const hrefs = Array.from(document.querySelectorAll("#search-results a")).map((link) => link.getAttribute("href"));
  expect(hrefs).toContain("../troubleshoot/recovery/#related-settings");

  // A new query replaces the address, so a reload shows the same results.
  const input = document.querySelector<HTMLInputElement>("#search-page-input");
  if (!input) throw new Error("no search input");
  input.value = "health ready";
  input.form?.dispatchEvent(new Event("submit", { cancelable: true }));
  expect(window.location.search).toBe("?q=health%20ready");
  await vi.waitFor(() => expect(status?.textContent).toMatch(/for "health ready"/));
});

test("the search page reports a query without results and an index that does not load", async () => {
  history.replaceState(null, "", "/docs/search/?q=zzzzqqqq");
  loadPage(pageHtml("search/index.html"));
  vi.stubGlobal("fetch", vi.fn(async () => new Response(site.searchIndex, { status: 200 })));
  initSearchPage(document, window);
  const status = document.getElementById("search-status");
  await vi.waitFor(() => expect(status?.textContent).toBe('No results for "zzzzqqqq".'));

  loadPage(pageHtml("search/index.html"));
  vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 404 })));
  // The script fallback fails too: the test DOM loads no scripts, so the
  // error event stands in for a missing file.
  const append = vi.spyOn(document.head, "append").mockImplementation((...nodes) => {
    for (const node of nodes) if (node instanceof HTMLElement) node.dispatchEvent(new Event("error"));
  });
  initSearchPage(document, window);
  const failed = document.getElementById("search-status");
  await vi.waitFor(() => expect(failed?.textContent).toMatch(/did not load/));
  expect(append).toHaveBeenCalled();
});

test("the client sends no request on a topic page", () => {
  const fetchSpy = vi.fn();
  vi.stubGlobal("fetch", fetchSpy);
  loadPage(pageHtml(RECOVERY));
  initDocs(document, window);
  expect(fetchSpy).not.toHaveBeenCalled();
});
