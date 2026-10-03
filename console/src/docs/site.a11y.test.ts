import { afterEach, expect, test, vi } from "vitest";

import tokens from "@/styles/tokens.css?raw";
import { buildTestSite, loadPage, parsePage, TEST_FACTS } from "@/test/docsSite";

import { initDocs, initTableOfContents, restoreFragment, trackStickyOffsets } from "./client/behavior";
import sheet from "./site.css?raw";

// CSP18 accessibility. The specification section 10.3 sets the targets.
// These tests read every page that the real docs/site/ tree builds, the
// docs stylesheet, and the color tokens. The browser review covers zoom,
// text spacing, a screen reader, and focus by eye.
const site = buildTestSite();
const pages = new Map(site.pages.map((page) => [page.path, parsePage(page.html)]));
const SEARCH = "search/index.html";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  history.replaceState(null, "", "/docs/");
});

// pageOf is the page that an href from a page opens, or undefined when the
// href leaves the site.
function pageOf(from: string, href: string): string | undefined {
  const url = new URL(href, `https://docs.test/docs/${from}`);
  if (!url.pathname.startsWith("/docs/")) return undefined;
  const target = url.pathname.slice("/docs/".length);
  return target === "" || target.endsWith("/") ? `${target}index.html` : target;
}

// accessibleText is the text a screen reader reads for an element: its
// text without the parts hidden from assistive technology.
function accessibleText(element: Element): string {
  const copy = element.cloneNode(true) as Element;
  for (const hidden of Array.from(copy.querySelectorAll('[aria-hidden="true"]'))) hidden.remove();
  return (copy.textContent ?? "").replace(/\s+/g, " ").trim();
}

// --- Stylesheet ---

type Rule = { media: string | null; selectors: string[]; declarations: Map<string, string> };

function parseRules(source: string): Rule[] {
  const text = source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/@import[^;]*;/g, "");
  const rules: Rule[] = [];
  const walk = (body: string, media: string | null) => {
    let at = 0;
    for (;;) {
      const open = body.indexOf("{", at);
      if (open < 0) return;
      let depth = 1;
      let close = open + 1;
      for (; depth > 0 && close < body.length; close++) {
        if (body[close] === "{") depth++;
        else if (body[close] === "}") depth--;
      }
      const prelude = body.slice(at, open).trim();
      const inner = body.slice(open + 1, close - 1);
      if (prelude.startsWith("@media")) walk(inner, prelude.slice("@media".length).trim());
      else {
        const declarations = new Map<string, string>();
        for (const match of inner.matchAll(/([\w-]+)\s*:\s*([^;]+);/g)) {
          declarations.set(match[1] ?? "", (match[2] ?? "").trim());
        }
        rules.push({ media, selectors: prelude.split(",").map((selector) => selector.trim()), declarations });
      }
      at = close;
    }
  };
  walk(text, null);
  return rules;
}

const RULES = parseRules(sheet);

// declared is the value of a property in the last rule for a selector, or
// undefined when no rule sets it.
function declared(selector: string, property: string, media: string | null = null): string | undefined {
  let value: string | undefined;
  for (const rule of RULES) {
    if (rule.media === media && rule.selectors.includes(selector) && rule.declarations.has(property)) {
      value = rule.declarations.get(property);
    }
  }
  return value;
}

// px is a rem or percent font length in CSS pixels at the default 16 px
// root size.
function px(value: string | undefined): number {
  const match = /^([\d.]+)(rem|%)$/.exec(value ?? "");
  if (!match) throw new Error(`${value} is not a rem or percent length`);
  return match[2] === "%" ? (Number(match[1]) * 16) / 100 : Number(match[1]) * 16;
}

// --- Color ---

type Theme = "dark" | "light";
type RGBA = [number, number, number, number];

function block(selector: string): string {
  const start = tokens.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`tokens.css has no ${selector} block`);
  return tokens.slice(start, tokens.indexOf("\n}", start));
}

function declarations(source: string): Map<string, string> {
  const found = new Map<string, string>();
  for (const match of source.matchAll(/--([\w-]+):\s*([^;]+);/g)) {
    found.set(match[1] ?? "", (match[2] ?? "").trim());
  }
  return found;
}

const ROLE: Record<Theme, Map<string, string>> = {
  dark: declarations(block(":root")),
  light: declarations(block(':root[data-theme="light"]')),
};

function parseColor(value: string): RGBA | null {
  const hex = /^#([0-9a-f]{6})$/i.exec(value);
  if (hex) {
    const number = Number.parseInt(hex[1] ?? "", 16);
    return [(number >> 16) & 255, (number >> 8) & 255, number & 255, 1];
  }
  const rgba = /^rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)$/.exec(value);
  if (rgba) return [Number(rgba[1]), Number(rgba[2]), Number(rgba[3]), rgba[4] === undefined ? 1 : Number(rgba[4])];
  return null;
}

function token(name: string, theme: Theme): RGBA {
  const parsed = parseColor(ROLE[theme].get(name) ?? "");
  if (!parsed) throw new Error(`tokens.css has no ${theme} color --${name}`);
  return parsed;
}

function over(top: RGBA, below: RGBA): RGBA {
  const alpha = top[3];
  return [0, 1, 2].map((index) => (top[index] ?? 0) * alpha + (below[index] ?? 0) * (1 - alpha)).concat(1) as RGBA;
}

function luminance([red, green, blue]: RGBA): number {
  const channel = (value: number) => {
    const scaled = value / 255;
    return scaled <= 0.03928 ? scaled / 12.92 : ((scaled + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(red) + 0.7152 * channel(green) + 0.0722 * channel(blue);
}

function ratio(first: RGBA, second: RGBA): number {
  const [light, dark] = [luminance(first), luminance(second)].sort((a, b) => b - a);
  return ((light ?? 0) + 0.05) / ((dark ?? 0) + 0.05);
}

// ground resolves a docs background. A tint sits on the canvas.
function ground(name: string, theme: Theme): RGBA {
  const color = token(name, theme);
  return color[3] < 1 ? over(color, token("bg-canvas", theme)) : color;
}

// The text color and ground pairs of the docs, with where each occurs.
const TEXT_ON_GROUND: readonly [text: string, ground: string, where: string][] = [
  ["text-1", "bg-canvas", "body text and headings"],
  ["text-2", "bg-canvas", "summaries, table headers, and the footer"],
  ["text-3", "bg-canvas", "area labels, the anchor mark, and the placeholder"],
  ["accent-link", "bg-canvas", "links"],
  ["accent-hover", "bg-canvas", "hovered links"],
  ["text-1", "bg-panel", "the header and code"],
  ["text-2", "bg-panel", "build facts and the copy control"],
  ["text-3", "bg-panel", "build fact terms and the code language"],
  ["text-1", "bg-raised", "inline code, buttons, and the version list"],
  ["accent-hover", "bg-raised", "inline code inside a link"],
  ["text-1", "bg-hover", "hovered navigation links and buttons"],
  ["text-1", "accent-tint", "the current navigation link"],
  ["text-1", "info-tint", "the generated-page note"],
  ["accent-ink", "accent", "the skip link"],
];

// The focus outline, the current-page bar, and the current-section bar use
// the accent against these grounds.
const ACCENT_GROUNDS = ["bg-canvas", "bg-panel", "bg-raised", "bg-hover", "accent-tint"];

test("every docs page has one banner, one main, one contentinfo, named navigation, a labelled search, one h1, a skip link to #content, and lang", () => {
  for (const [path, page] of pages) {
    expect(page.documentElement.lang, path).toBe("en");
    const skip = page.body.firstElementChild;
    expect(skip?.matches("a.skip-link"), path).toBe(true);
    expect(skip?.getAttribute("href"), path).toBe("#content");
    expect(page.getElementById("content")?.tagName, path).toBe("MAIN");

    // A header or footer inside the article is not a landmark.
    const outside = (selector: string) =>
      Array.from(page.querySelectorAll(selector)).filter((element) => !element.closest("article, main"));
    expect(outside("header, [role='banner']"), path).toHaveLength(1);
    expect(outside("footer, [role='contentinfo']"), path).toHaveLength(1);
    expect(page.querySelectorAll("main, [role='main']"), path).toHaveLength(1);
    expect(page.querySelectorAll("aside, [role='complementary']"), path).toHaveLength(0);

    const navs = Array.from(page.querySelectorAll("nav"));
    const names = navs.map((nav) => nav.getAttribute("aria-label") ?? "");
    for (const name of names) expect(name, path).not.toBe("");
    expect(new Set(names).size, path).toBe(names.length);
    expect(names.filter((name) => name === "Documentation"), path).toHaveLength(1);

    const searches = Array.from(page.querySelectorAll('[role="search"]'));
    expect(searches.length, path).toBeGreaterThan(0);
    const searchNames = searches.map((search) => search.getAttribute("aria-label") ?? "");
    for (const name of searchNames) expect(name, path).not.toBe("");
    expect(new Set(searchNames).size, path).toBe(searchNames.length);
    for (const search of searches) {
      for (const input of Array.from(search.querySelectorAll("input"))) {
        const label = page.querySelector(`label[for="${input.id}"]`);
        expect(accessibleText(label ?? page.createElement("label")), `${path} #${input.id}`).not.toBe("");
      }
    }

    const headings = page.querySelectorAll("h1");
    expect(headings, path).toHaveLength(1);
    expect(headings[0]?.closest("main"), path).not.toBeNull();
  }
});

test("every table and every code block scrolls in a focusable container with a role and an accessible name", () => {
  let tables = 0;
  let blocks = 0;
  for (const [path, page] of pages) {
    const regionNames = new Set<string>();
    for (const table of Array.from(page.querySelectorAll("main table"))) {
      tables++;
      const region = table.parentElement;
      expect(region?.matches(".table-scroll"), path).toBe(true);
      expect(region?.getAttribute("tabindex"), path).toBe("0");
      expect(region?.getAttribute("role"), path).toBe("region");
      const name = region?.getAttribute("aria-label") ?? "";
      expect(name, path).not.toBe("");
      expect(regionNames.has(name), `${path}: duplicate region ${name}`).toBe(false);
      regionNames.add(name);
    }
    for (const pre of Array.from(page.querySelectorAll("main pre"))) {
      blocks++;
      expect(pre.parentElement?.matches(".code-block"), path).toBe(true);
      expect(pre.getAttribute("tabindex"), path).toBe("0");
      expect(pre.getAttribute("role"), path).toBe("group");
      expect(pre.getAttribute("aria-label") ?? "", path).toMatch(/code example$/i);
    }
  }
  // The content tree has both, so the test checks real containers.
  expect(tables).toBeGreaterThan(0);
  expect(blocks).toBeGreaterThan(0);
  // The containers scroll on their own, so the page does not.
  expect(declared(".table-scroll", "overflow-x")).toBe("auto");
  expect(declared(".code-block pre", "overflow-x")).toBe("auto");
});

test("every h2, h3, and h4 has an id and an anchor with an accessible name", () => {
  for (const [path, page] of pages) {
    for (const heading of Array.from(page.querySelectorAll("main h2, main h3, main h4"))) {
      expect(heading.id, path).not.toBe("");
      const anchor = heading.lastElementChild;
      expect(anchor?.matches("a.heading-anchor"), `${path} #${heading.id}`).toBe(true);
      expect(anchor?.getAttribute("href"), path).toBe(`#${heading.id}`);
      expect(anchor?.getAttribute("aria-label"), path).toBe(`Link to section: ${accessibleText(heading)}`);
      // The visible mark is hidden from a screen reader, so the name comes
      // from the label alone.
      expect(accessibleText(anchor ?? heading), path).toBe("");
    }
  }
});

test("the table of contents, the navigation, and the version selector have accessible names, and the current page carries aria-current=page", () => {
  for (const [path, page] of pages) {
    expect(page.querySelector('nav[aria-label="Documentation"]'), path).not.toBeNull();
    if (page.querySelector("main h2")) {
      expect(page.querySelector('nav[aria-label="On this page"][data-toc]'), path).not.toBeNull();
    }

    const selector = page.querySelector("details.version-selector");
    const summary = selector?.querySelector("summary");
    expect(summary?.parentElement, path).toBe(selector);
    expect(accessibleText(summary ?? page.body), path).toBe(`Documentation version: ${TEST_FACTS.release}`);
    expect(selector?.querySelector("ul")?.getAttribute("aria-label"), path).toBe("Documentation versions");
    const current = selector?.querySelector('a[aria-current="true"]');
    expect(current?.textContent, path).toContain(TEST_FACTS.release);
    expect(pageOf(path, current?.getAttribute("href") ?? ""), path).toBe("index.html");

    const marked = Array.from(page.querySelectorAll('[aria-current="page"]'));
    if (path === SEARCH) {
      // The search page has no entry in the navigation.
      expect(marked, path).toHaveLength(0);
      continue;
    }
    expect(marked, path).toHaveLength(1);
    expect(marked[0]?.tagName, path).toBe("A");
    expect(pageOf(path, marked[0]?.getAttribute("href") ?? ""), path).toBe(path);
  }
});

test("the docs stylesheet states the typography, measure, and target sizes, a reduced-motion rule, and no fixed height on text", () => {
  // Body 16 px with a 26 px line, section headings 20 px, the title 28 to
  // 32 px, and code 14 px with a 21 px line.
  expect(px(declared("body", "font-size"))).toBe(16);
  expect(declared("body", "line-height")).toBe("1.625");
  expect(px(declared("article h2", "font-size"))).toBe(20);
  const title = px(declared("h1", "font-size"));
  expect(title).toBeGreaterThanOrEqual(28);
  expect(title).toBeLessThanOrEqual(32);
  expect(px(declared("article h3", "font-size"))).toBeLessThan(20);
  expect(px(declared("article h3", "font-size"))).toBeGreaterThan(px(declared("article h4", "font-size")));
  expect(px(declared(".code-block pre", "font-size"))).toBe(14);
  expect(declared(".code-block pre", "line-height")).toBe("1.5");
  expect(px(declared(".prose :not(pre) > code", "font-size"))).toBe(14);

  // No text falls under 14 px.
  for (const rule of RULES) {
    const size = rule.declarations.get("font-size");
    if (size) expect(px(size), rule.selectors.join(", ")).toBeGreaterThanOrEqual(14);
  }

  // A 68ch measure with a 720 px cap and 16 px gutters.
  expect(declared(":root", "--docs-measure")).toBe("min(68ch, 720px)");
  expect(declared("article", "max-width")).toBe("var(--docs-measure)");
  expect(px(declared(":root", "--docs-gutter"))).toBe(16);

  // 44 px targets on every control.
  expect(px(declared(":root", "--docs-target"))).toBe(44);
  for (const selector of [
    "button",
    ".site-brand",
    ".site-search input",
    ".version-selector summary",
    ".version-selector a",
    ".site-nav a",
    ".toc a",
    ".topic-list a",
    ".search-results a",
    ".heading-anchor",
  ]) {
    expect(declared(selector, "min-height"), selector).toBe("var(--docs-target)");
  }
  expect(declared(".heading-anchor", "min-width")).toBe("var(--docs-target)");

  // Reduced motion turns off smooth scrolling and every transition.
  const reduce = "(prefers-reduced-motion: reduce)";
  expect(declared("html", "scroll-behavior", reduce)).toBe("auto");
  expect(declared("*", "transition", reduce)).toMatch(/^none/);
  expect(declared("*", "animation", reduce)).toMatch(/^none/);

  // Only the visually hidden class fixes a height, hides overflow, or stops
  // wrapping. A sidebar that caps its height scrolls instead of clipping.
  for (const rule of RULES) {
    const where = `${rule.media ?? ""} ${rule.selectors.join(", ")}`;
    const hidden = rule.selectors.includes(".sr-only");
    if (rule.declarations.has("height")) expect(hidden, where).toBe(true);
    if (rule.declarations.get("white-space") === "nowrap") expect(hidden, where).toBe(true);
    for (const property of ["overflow", "overflow-x", "overflow-y"]) {
      if (rule.declarations.get(property) === "hidden") expect(hidden, where).toBe(true);
    }
    if (rule.declarations.has("max-height")) expect(rule.declarations.get("overflow-y"), where).toBe("auto");
  }

  // Focus is a solid accent outline, and the skip link shows on focus.
  expect(declared(":focus-visible", "outline")).toBe("2px solid var(--accent)");
  expect(declared(".skip-link:focus", "transform")).toBe("none");

  // The current page and the current section show a bar and a weight, not
  // only a color.
  expect(declared('.site-nav a[aria-current="page"]', "border-left-color")).toBe("var(--accent)");
  expect(Number(declared('.site-nav a[aria-current="page"]', "font-weight"))).toBeGreaterThan(
    Number(declared(".site-nav a.nav-area", "font-weight")),
  );
  expect(declared('.toc a[aria-current="location"]', "border-left-color")).toBe("var(--accent)");
  expect(declared('.toc a[aria-current="location"]', "font-weight")).toBe("600");
});

test("both themes meet 4.5:1 for the docs text, code, link, and help colors over the docs grounds, and 3:1 for the focus outline", () => {
  // Every color and ground the stylesheet uses is in the checked pairs.
  const texts = new Set(TEXT_ON_GROUND.map(([text]) => text));
  const grounds = new Set(TEXT_ON_GROUND.map(([, groundName]) => groundName));
  for (const rule of RULES) {
    const color = /^var\(--([\w-]+)\)$/.exec(rule.declarations.get("color") ?? "");
    if (color) expect(texts.has(color[1] ?? ""), `${rule.selectors.join(", ")} color`).toBe(true);
    const background = /^var\(--([\w-]+)\)$/.exec(rule.declarations.get("background") ?? "");
    if (background) expect(grounds.has(background[1] ?? ""), `${rule.selectors.join(", ")} background`).toBe(true);
  }

  for (const theme of ["dark", "light"] as const) {
    for (const [text, groundName, where] of TEXT_ON_GROUND) {
      const value = ratio(token(text, theme), ground(groundName, theme));
      expect(value, `${theme}: ${text} on ${groundName} (${where}) is ${value.toFixed(2)}:1`).toBeGreaterThanOrEqual(4.5);
    }
    for (const groundName of ACCENT_GROUNDS) {
      const value = ratio(token("accent", theme), ground(groundName, theme));
      expect(value, `${theme}: accent on ${groundName} is ${value.toFixed(2)}:1`).toBeGreaterThanOrEqual(3);
    }
  }
});

test("a link to a heading fragment resolves on every page, and the client brings the target into view after load", () => {
  const scrolled = vi.spyOn(Element.prototype, "scrollIntoView");
  for (const page of site.pages) {
    const route = page.path.slice(0, -"index.html".length);
    loadPage(page.html);
    const ids = Array.from(document.querySelectorAll("main h2, main h3, main h4")).map((heading) => heading.id);
    for (const link of Array.from(document.querySelectorAll('a[href^="#"]'))) {
      const id = decodeURIComponent(link.getAttribute("href")?.slice(1) ?? "");
      expect(document.getElementById(id), `${page.path} #${id}`).not.toBeNull();
    }
    for (const id of ids) {
      history.replaceState(null, "", `/docs/${route}#${id}`);
      scrolled.mockClear();
      restoreFragment(document, window);
      expect(scrolled.mock.contexts, `${page.path} #${id}`).toEqual([document.getElementById(id)]);
      expect(scrolled).toHaveBeenCalledWith({ block: "start" });
    }
  }

  // A page that is still loading waits for the load event, and the full
  // client start sends no request.
  history.replaceState(null, "", "/docs/troubleshoot/recovery/#steps");
  loadPage(site.pages.find((page) => page.path === "troubleshoot/recovery/index.html")?.html ?? "");
  vi.stubGlobal("fetch", vi.fn());
  vi.spyOn(document, "readyState", "get").mockReturnValue("loading");
  scrolled.mockClear();
  initDocs(document, window);
  expect(scrolled).not.toHaveBeenCalled();
  window.dispatchEvent(new Event("load"));
  expect(scrolled.mock.contexts).toEqual([document.getElementById("steps")]);
  expect(fetch).not.toHaveBeenCalled();
});

test("the header search form and its input can shrink below their content width, so the header fits a 320 px viewport", () => {
  // A flex item keeps its content width unless it sets min-width: 0. The
  // input alone cannot shrink when the form around it does not.
  expect(declared(".site-search", "min-width")).toBe("0");
  expect(declared(".site-search input", "min-width")).toBe("0");
  expect(px(declared(".site-search", "flex")?.split(" ")[2])).toBeLessThanOrEqual(320 - 2 * 16);
  expect(declared(".site-header", "flex-wrap")).toBe("wrap");
  for (const page of pages.values()) {
    const form = page.querySelector("header form.site-search");
    expect(form?.querySelector("input")).not.toBeNull();
    expect(form?.querySelector("button")).not.toBeNull();
    expect(form?.getAttribute("style")).toBeNull();
  }
});

test("the scroll padding and the sticky columns follow the measured height of the sticky header", () => {
  // The stylesheet reads one offset. Its fallback is one header row.
  const below = "calc(var(--docs-header-offset) + 1rem)";
  expect(declared(":root", "--docs-header-offset")).toBe("var(--docs-header)");
  expect(declared("html", "scroll-padding-top", "(min-width: 64rem)")).toBe(below);
  expect(declared(".site-nav", "top", "(min-width: 64rem)")).toBe(below);
  expect(declared(".toc", "top", "(min-width: 80rem)")).toBe(below);
  for (const rule of RULES) {
    for (const [property, value] of rule.declarations) {
      if (!value.includes("var(--docs-header)")) continue;
      const where = `${rule.selectors.join(", ")} ${property}`;
      expect([":root --docs-header-offset", ".site-header min-height"], where).toContain(where);
    }
  }

  // The client writes the measured height while the header is sticky, and
  // it follows each resize.
  loadPage(site.pages.find((page) => page.path === "troubleshoot/recovery/index.html")?.html ?? "");
  const observed: Array<() => void> = [];
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(callback: () => void) {
        observed.push(callback);
      }
      observe() {}
      disconnect() {}
    },
  );
  const header = document.querySelector<HTMLElement>(".site-header");
  if (!header) throw new Error("no header");
  const height = vi.spyOn(header, "getBoundingClientRect").mockReturnValue({ height: 108.4 } as DOMRect);
  header.style.position = "sticky";
  const offset = () => document.documentElement.style.getPropertyValue("--docs-header-offset");
  trackStickyOffsets(document, window);
  expect(offset()).toBe("109px");
  height.mockReturnValue({ height: 56 } as DOMRect);
  for (const callback of observed) callback();
  expect(offset()).toBe("56px");

  // The header can grow before the observer reports it. The fragment
  // restore measures it again before it scrolls.
  height.mockReturnValue({ height: 114 } as DOMRect);
  history.replaceState(null, "", "/docs/troubleshoot/recovery/#steps");
  let offsetAtScroll = "";
  vi.spyOn(Element.prototype, "scrollIntoView").mockImplementation(() => {
    offsetAtScroll = offset();
  });
  restoreFragment(document, window);
  expect(offsetAtScroll).toBe("114px");
  history.replaceState(null, "", "/docs/troubleshoot/recovery/");

  // A heading that a fragment brings to 16 px under a two-row header counts
  // as reached, so the table of contents marks it.
  height.mockReturnValue({ height: 109 } as DOMRect);
  const entries = Array.from(document.querySelectorAll<HTMLAnchorElement>("[data-toc] a"));
  const second = document.getElementById(decodeURIComponent(entries[1]?.hash.slice(1) ?? ""));
  for (const link of entries) {
    const target = document.getElementById(decodeURIComponent(link.hash.slice(1)));
    if (!target) continue;
    const top = target === second ? 109 + 16 : link === entries[0] ? -100 : 500;
    vi.spyOn(target, "getBoundingClientRect").mockReturnValue({ top } as DOMRect);
  }
  initTableOfContents(document, window);
  expect(document.querySelector('[data-toc] a[aria-current="location"]')).toBe(entries[1]);

  // The header scrolls with the page at narrow widths, so no offset applies.
  header.style.position = "static";
  for (const callback of observed) callback();
  expect(offset()).toBe("");
  document.documentElement.removeAttribute("style");
});

test("a sticky column fits between the sticky header and the footer at the page end", () => {
  // A sticky column cannot leave .site-body. At the page end the body ends
  // its bottom padding above the footer. The column starts 1rem under the
  // header, so its maximum height leaves the header, the footer, the 1rem,
  // and the body padding.
  const fit = "calc(100vh - var(--docs-header-offset) - var(--docs-footer-offset) - 4rem)";
  expect(declared(".site-nav", "max-height", "(min-width: 64rem)")).toBe(fit);
  expect(declared(".toc", "max-height", "(min-width: 80rem)")).toBe(fit);
  const bodyEnd = px(declared(".site-body", "padding")?.split(" ")[2]);
  const gap = px(/\+ ([\d.]+rem)\)$/.exec(declared(".site-nav", "top", "(min-width: 64rem)") ?? "")?.[1]);
  expect(gap + bodyEnd).toBe(px("4rem"));
  expect(declared(":root", "--docs-footer-offset")).toBe("5rem");
  expect(declared(".site-footer", "height")).toBeUndefined();

  // The client writes the measured footer height while the header is
  // sticky, and it follows each footer resize.
  loadPage(site.pages.find((page) => page.path === "troubleshoot/recovery/index.html")?.html ?? "");
  const observed: Element[] = [];
  const callbacks: Array<() => void> = [];
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(callback: () => void) {
        callbacks.push(callback);
      }
      observe(element: Element) {
        observed.push(element);
      }
      disconnect() {}
    },
  );
  const header = document.querySelector<HTMLElement>(".site-header");
  const footer = document.querySelector<HTMLElement>(".site-footer");
  if (!header || !footer) throw new Error("no header or footer");
  vi.spyOn(header, "getBoundingClientRect").mockReturnValue({ height: 67 } as DOMRect);
  const height = vi.spyOn(footer, "getBoundingClientRect").mockReturnValue({ height: 80 } as DOMRect);
  header.style.position = "sticky";
  const offset = () => document.documentElement.style.getPropertyValue("--docs-footer-offset");
  trackStickyOffsets(document, window);
  expect(observed).toEqual([header, footer]);
  expect(offset()).toBe("80px");
  height.mockReturnValue({ height: 103.2 } as DOMRect);
  for (const callback of callbacks) callback();
  expect(offset()).toBe("104px");

  // No column is sticky at narrow widths, so no offset applies.
  header.style.position = "static";
  for (const callback of callbacks) callback();
  expect(offset()).toBe("");
  document.documentElement.removeAttribute("style");
});
