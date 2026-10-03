import type { Element, ElementContent, Root as HastRoot, RootContent as HastContent } from "hast";
import type { Link, Root as MdastRoot, RootContent as MdastContent } from "mdast";
import rehypeAutolinkHeadings from "rehype-autolink-headings";
import rehypeSlug from "rehype-slug";
import rehypeStringify from "rehype-stringify";
import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import remarkRehype from "remark-rehype";
import { unified } from "unified";

import { ContentError } from "./content";

export type Heading = { depth: 2 | 3 | 4; id: string; text: string };

// A section is the unit the search index holds: the prose under one heading
// up to the next heading. The section before the first heading has no id.
export type Section = { id: string | null; heading: string | null; text: string; terms: string[] };

// Each section carries its inline code values, such as setting names and
// routes. The index boosts them so a search for an environment name finds
// the section that names it.
export type RenderedTopic = {
  html: string;
  headings: Heading[];
  sections: Section[];
};

export type LinkResolver = {
  // The repository-relative path of the Markdown file being rendered.
  source: string;
  // The page directory of the file being rendered, for relative hrefs.
  route: string;
  // The page directory of a content-tree Markdown file, or undefined when
  // the path is not a page.
  routeOf: (repositoryPath: string) => string | undefined;
  // The URL of a repository file outside the content tree at the pinned
  // reference, or null when the build has no reference to pin.
  repositoryUrl: (repositoryPath: string) => string | null;
};

const SITE_PREFIX = "docs/site/";
const HEADING_TAGS = new Set(["h2", "h3", "h4"]);

// relativeHref is the href from one page directory to another. Both are
// page directories under the docs root, so the result works at /docs/, at
// /<release>/, and from a directory on disk.
export function relativeHref(fromRoute: string, toRoute: string): string {
  const depth = fromRoute.split("/").length - 1;
  const href = "../".repeat(depth) + toRoute;
  return href === "" ? "./" : href;
}

// resolvePath joins a relative link to the directory of its source file and
// normalizes it. It returns null when the link leaves the repository.
export function resolvePath(source: string, target: string): string | null {
  const segments = source.split("/").slice(0, -1);
  for (const part of target.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") {
      if (segments.length === 0) return null;
      segments.pop();
      continue;
    }
    segments.push(part);
  }
  return segments.join("/");
}

function isExternal(url: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(url) || url.startsWith("//");
}

function rewriteLinks(resolver: LinkResolver) {
  return (tree: MdastRoot) => {
    visitLinks(tree, (link) => {
      const url = link.url;
      if (/github\.com\/agentstation\/starport\/(blob|tree)\/main\b/.test(url)) {
        throw new ContentError(resolver.source, `link ${url} names an unpinned branch; use a relative repository path`);
      }
      if (url === "" || url.startsWith("#") || isExternal(url)) return undefined;
      const hashAt = url.indexOf("#");
      const path = decodeURI(hashAt < 0 ? url : url.slice(0, hashAt));
      const fragment = hashAt < 0 ? "" : url.slice(hashAt);
      const target = resolvePath(resolver.source, path);
      if (target === null) {
        throw new ContentError(resolver.source, `link ${url} leaves the repository`);
      }
      if (target.startsWith(SITE_PREFIX)) {
        const route = resolver.routeOf(target);
        if (route === undefined) {
          throw new ContentError(resolver.source, `link ${url} does not name a documentation page`);
        }
        link.url = relativeHref(resolver.route, route) + fragment;
        return undefined;
      }
      const repositoryUrl = resolver.repositoryUrl(target);
      // Without a pinned reference the text stays and the link goes.
      if (repositoryUrl === null) return link.children as MdastContent[];
      link.url = repositoryUrl + fragment;
      return undefined;
    });
  };
}

type MdastParent = { children: MdastContent[] };

// visitLinks calls visit for each link. A returned list replaces the link.
function visitLinks(node: MdastParent, visit: (link: Link) => MdastContent[] | undefined) {
  for (let index = 0; index < node.children.length; index++) {
    const child = node.children[index];
    if (!child) continue;
    if (child.type === "link") {
      const replacement = visit(child);
      if (replacement) {
        node.children.splice(index, 1, ...replacement);
        index += replacement.length - 1;
      }
      continue;
    }
    if ("children" in child) visitLinks(child as MdastParent, visit);
  }
}

function rejectTitleHeading(source: string) {
  return (tree: MdastRoot) => {
    for (const child of tree.children) {
      if (child.type === "heading" && child.depth === 1) {
        throw new ContentError(source, "the body has a level-one heading; the front matter title is the page title");
      }
    }
  };
}

export function textOf(node: HastContent | HastRoot): string {
  if (node.type === "text") return node.value;
  if ("children" in node) return node.children.map((child) => textOf(child)).join("");
  return "";
}

function element(tagName: string, properties: Element["properties"], children: ElementContent[]): Element {
  return { type: "element", tagName, properties, children };
}

// decorate wraps each code block with its language label and a copy
// control, and wraps each table in a region that scrolls on its own. The
// copy control stays hidden until the client script can make it work.
function decorate() {
  return (tree: HastRoot) => {
    const walk = (parent: HastRoot | Element) => {
      parent.children.forEach((child, index) => {
        if (child.type !== "element") return;
        if (child.tagName === "pre") {
          const code = child.children.find((node): node is Element => node.type === "element" && node.tagName === "code");
          const classes = (code?.properties.className as string[] | undefined) ?? [];
          const language = classes.find((name) => name.startsWith("language-"))?.slice("language-".length) ?? "text";
          child.properties = { ...child.properties, tabIndex: 0 };
          parent.children[index] = element("div", { className: ["code-block"] }, [
            element("div", { className: ["code-head"] }, [
              element("span", { className: ["code-lang"] }, [{ type: "text", value: language }]),
              element(
                "button",
                { type: "button", className: ["copy-button"], hidden: true, dataCopy: "" },
                [{ type: "text", value: "Copy" }],
              ),
            ]),
            child,
          ]);
          return;
        }
        if (child.tagName === "table") {
          parent.children[index] = element(
            "div",
            { className: ["table-scroll"], role: "region", ariaLabel: "Table", tabIndex: 0 },
            [child],
          );
          return;
        }
        walk(child);
      });
    };
    walk(tree);
  };
}

function collect(out: Omit<RenderedTopic, "html">) {
  return (tree: HastRoot) => {
    let current: Section = { id: null, heading: null, text: "", terms: [] };
    out.sections.push(current);
    const findTerms = (node: HastContent, inPre: boolean) => {
      if (node.type !== "element") return;
      if (node.tagName === "code" && !inPre) {
        const value = textOf(node).trim();
        if (value.length > 1 && value.length <= 80 && !current.terms.includes(value)) current.terms.push(value);
      }
      for (const child of node.children) findTerms(child, inPre || node.tagName === "pre");
    };
    for (const child of tree.children) {
      if (child.type === "element" && HEADING_TAGS.has(child.tagName)) {
        const id = String(child.properties.id ?? "");
        const text = textOf(child).trim();
        out.headings.push({ depth: Number(child.tagName.slice(1)) as Heading["depth"], id, text });
        current = { id, heading: text, text: "", terms: [] };
        out.sections.push(current);
      }
      findTerms(child, false);
      if (child.type === "element" && HEADING_TAGS.has(child.tagName)) continue;
      const text = textOf(child).replace(/\s+/g, " ").trim();
      if (text) current.text = current.text ? `${current.text} ${text}` : text;
    }
  };
}

// renderMarkdown turns one topic body into HTML. Every h2, h3, and h4 gets a
// stable id from its text and an anchor link to itself.
export function renderMarkdown(markdown: string, resolver: LinkResolver): RenderedTopic {
  const out: Omit<RenderedTopic, "html"> = { headings: [], sections: [] };
  const html = unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(() => rejectTitleHeading(resolver.source))
    .use(() => rewriteLinks(resolver))
    .use(remarkRehype)
    .use(rehypeSlug)
    .use(() => collect(out))
    .use(rehypeAutolinkHeadings, {
      behavior: "append",
      test: ["h2", "h3", "h4"],
      properties: (heading) => ({
        className: ["heading-anchor"],
        ariaLabel: `Link to section: ${textOf(heading).trim()}`,
      }),
      content: element("span", { ariaHidden: "true" }, [{ type: "text", value: "#" }]),
    })
    .use(decorate)
    .use(rehypeStringify)
    .processSync(markdown)
    .toString();
  out.sections = out.sections.filter((section) => section.heading !== null || section.text !== "" || section.terms.length > 0);
  return { html, ...out };
}
