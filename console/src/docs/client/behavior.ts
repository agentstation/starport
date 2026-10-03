// The documentation client behavior. Every function takes the document and
// window it acts on, so the tests run each one against a rendered page in
// jsdom. Each feature only adds to a page that already reads well without
// JavaScript: the copy controls and the theme toggle stay hidden until this
// code can make them work.
import type MiniSearch from "minisearch";

import { appliedTheme, setTheme } from "../../lib/theme";
import { loadSearchIndex, searchDocs, type SearchDocument, type SearchHit } from "../search";

declare global {
  interface Window {
    __STARPORT_DOCS_SEARCH__?: string;
  }
}

// docsRoot is the relative path from this page to the docs root, as the
// build wrote it.
export function docsRoot(doc: Document): string {
  return doc.querySelector<HTMLMetaElement>('meta[name="starport-docs-root"]')?.content ?? "./";
}

function isRelative(href: string): boolean {
  return href !== "" && !href.startsWith("#") && !href.startsWith("/") && !/^[a-z][a-z0-9+.-]*:/i.test(href);
}

// withIndexFile makes a directory href name its index.html. A browser that
// reads the site from disk does not resolve a directory to its index.
export function withIndexFile(href: string): string {
  const hashAt = href.indexOf("#");
  const path = hashAt < 0 ? href : href.slice(0, hashAt);
  const hash = hashAt < 0 ? "" : href.slice(hashAt);
  if (path === "" || path === "." || path === "./" || path.endsWith("/") || path.endsWith("..")) {
    const directory = path === "" || path === "." ? "./" : path.endsWith("/") ? path : `${path}/`;
    return `${directory}index.html${hash}`;
  }
  return href;
}

export function fixFileLinks(doc: Document, win: Window): void {
  if (win.location.protocol !== "file:") return;
  for (const link of Array.from(doc.querySelectorAll<HTMLAnchorElement>("a[href]"))) {
    const href = link.getAttribute("href") ?? "";
    if (isRelative(href)) link.setAttribute("href", withIndexFile(href));
  }
  for (const form of Array.from(doc.querySelectorAll<HTMLFormElement>("form[action]"))) {
    const action = form.getAttribute("action") ?? "";
    if (isRelative(action)) form.setAttribute("action", withIndexFile(action));
  }
}

// ---- Theme ----

function themeLabel(): string {
  return appliedTheme() === "light" ? "Switch to dark theme" : "Switch to light theme";
}

export function initThemeToggle(doc: Document): void {
  const button = doc.querySelector<HTMLButtonElement>("[data-theme-toggle]");
  if (!button) return;
  const label = () => {
    button.textContent = appliedTheme() === "light" ? "Dark theme" : "Light theme";
    button.setAttribute("aria-label", themeLabel());
  };
  label();
  button.hidden = false;
  button.addEventListener("click", () => {
    setTheme(appliedTheme() === "light" ? "dark" : "light");
    label();
  });
}

// ---- Copy controls ----

async function copyText(doc: Document, win: Window, text: string): Promise<boolean> {
  try {
    if (win.navigator.clipboard) {
      await win.navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall through to the selection copy.
  }
  const area = doc.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.className = "sr-only";
  doc.body.append(area);
  area.select();
  let copied = false;
  try {
    copied = doc.execCommand("copy");
  } catch {
    copied = false;
  }
  area.remove();
  return copied;
}

export function initCopyButtons(doc: Document, win: Window): void {
  const buttons = Array.from(doc.querySelectorAll<HTMLButtonElement>("[data-copy]"));
  if (buttons.length === 0) return;
  const status = doc.createElement("p");
  status.className = "sr-only";
  status.setAttribute("role", "status");
  doc.body.append(status);
  for (const button of buttons) {
    const code = button.closest(".code-block")?.querySelector("pre");
    if (!code) continue;
    const language = button.closest(".code-block")?.querySelector(".code-lang")?.textContent ?? "code";
    button.setAttribute("aria-label", `Copy ${language} code`);
    button.hidden = false;
    button.addEventListener("click", () => {
      void copyText(doc, win, code.textContent ?? "").then((copied) => {
        button.textContent = copied ? "Copied" : "Copy failed";
        status.textContent = copied ? "Code copied." : "The browser did not allow the copy.";
        win.setTimeout(() => {
          button.textContent = "Copy";
        }, 2000);
      });
    });
  }
}

// ---- Table of contents ----

// The offset below the sticky header where a heading counts as reached.
const READ_LINE = 96;

export function initTableOfContents(doc: Document, win: Window): void {
  const toc = doc.querySelector("[data-toc]");
  if (!toc) return;
  const links = Array.from(toc.querySelectorAll<HTMLAnchorElement>('a[href^="#"]'));
  const entries = links
    .map((link) => ({ link, target: doc.getElementById(decodeURIComponent(link.hash.slice(1))) }))
    .filter((entry): entry is { link: HTMLAnchorElement; target: HTMLElement } => entry.target !== null);
  if (entries.length === 0) return;

  const mark = (id: string | null) => {
    for (const { link, target } of entries) {
      if (target.id === id) link.setAttribute("aria-current", "location");
      else link.removeAttribute("aria-current");
    }
  };

  // The section of an element is the last table-of-contents heading at or
  // before it in document order. An h4 belongs to the h2 or h3 above it.
  const sectionOf = (element: Element): string | null => {
    let found: string | null = null;
    for (const { target } of entries) {
      if (target === element || target.compareDocumentPosition(element) & Node.DOCUMENT_POSITION_FOLLOWING) {
        found = target.id;
      }
    }
    return found;
  };

  const fromHash = (): boolean => {
    const id = decodeURIComponent(win.location.hash.slice(1));
    const element = id ? doc.getElementById(id) : null;
    if (!element) return false;
    mark(sectionOf(element));
    return true;
  };

  const fromScroll = () => {
    let current: string | null = null;
    for (const { target } of entries) {
      if (target.getBoundingClientRect().top <= READ_LINE) current = target.id;
    }
    mark(current ?? entries[0]?.target.id ?? null);
  };

  if (!fromHash()) fromScroll();
  win.addEventListener("hashchange", () => {
    if (!fromHash()) fromScroll();
  });
  let pending = false;
  win.addEventListener(
    "scroll",
    () => {
      if (pending) return;
      pending = true;
      win.requestAnimationFrame(() => {
        pending = false;
        fromScroll();
      });
    },
    { passive: true },
  );
}

// ---- Search ----

export async function loadIndex(doc: Document, win: Window): Promise<MiniSearch<SearchDocument>> {
  const root = docsRoot(doc);
  if (win.location.protocol !== "file:") {
    try {
      const response = await win.fetch(`${root}search-index.json`);
      if (response.ok) return loadSearchIndex(await response.text());
    } catch {
      // Fall back to the script form of the index.
    }
  }
  // A page read from disk cannot fetch, but it can load a script.
  return new Promise((resolve, reject) => {
    const script = doc.createElement("script");
    script.src = `${root}search-index.js`;
    script.addEventListener("load", () => {
      const serialized = win.__STARPORT_DOCS_SEARCH__;
      if (serialized) resolve(loadSearchIndex(serialized));
      else reject(new Error("the search index script did not define the index"));
    });
    script.addEventListener("error", () => reject(new Error("the search index did not load")));
    doc.head.append(script);
  });
}

function resultHref(root: string, hit: SearchHit, win: Window): string {
  const href = `${root}${hit.href}`;
  return win.location.protocol === "file:" ? withIndexFile(href) : href;
}

export function renderResults(doc: Document, win: Window, list: HTMLElement, hits: readonly SearchHit[]): void {
  const root = docsRoot(doc);
  list.replaceChildren(
    ...hits.map((hit) => {
      const item = doc.createElement("li");
      const link = doc.createElement("a");
      link.href = resultHref(root, hit, win);
      link.textContent = hit.heading ? `${hit.title}: ${hit.heading}` : hit.title;
      const area = doc.createElement("p");
      area.textContent = hit.area;
      item.append(link, area);
      return item;
    }),
  );
}

export function initSearchPage(doc: Document, win: Window): void {
  const list = doc.getElementById("search-results");
  const status = doc.getElementById("search-status");
  const input = doc.querySelector<HTMLInputElement>("#search-page-input");
  if (!list || !status || !input) return;
  const form = input.form;
  const query = new URLSearchParams(win.location.search).get("q") ?? "";
  input.value = query;
  const header = doc.querySelector<HTMLInputElement>("#site-search-input");
  if (header) header.value = query;

  status.textContent = "Loading the search index.";
  loadIndex(doc, win).then(
    (index) => {
      const run = () => {
        const value = input.value;
        const hits = searchDocs(index, value);
        renderResults(doc, win, list, hits);
        if (!value.trim()) status.textContent = "Type a word, a heading, or a setting name.";
        else if (hits.length === 0) status.textContent = `No results for "${value.trim()}".`;
        else status.textContent = `${hits.length} result${hits.length === 1 ? "" : "s"} for "${value.trim()}".`;
      };
      let timer: number | undefined;
      input.addEventListener("input", () => {
        win.clearTimeout(timer);
        timer = win.setTimeout(() => {
          const value = input.value.trim();
          // The query stays in the address, so a reload or a copied link
          // shows the same results.
          win.history.replaceState(null, "", value ? `?q=${encodeURIComponent(value)}` : win.location.pathname);
          run();
        }, 150);
      });
      form?.addEventListener("submit", (event) => {
        event.preventDefault();
        const value = input.value.trim();
        win.history.replaceState(null, "", value ? `?q=${encodeURIComponent(value)}` : win.location.pathname);
        run();
      });
      run();
    },
    () => {
      status.textContent = "The search index did not load. Use the documentation navigation to find a topic.";
    },
  );
}

export function initDocs(doc: Document, win: Window): void {
  fixFileLinks(doc, win);
  initThemeToggle(doc);
  initCopyButtons(doc, win);
  initTableOfContents(doc, win);
  initSearchPage(doc, win);
}
