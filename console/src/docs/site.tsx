import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { AREAS, HOME_AREA } from "./areas";
import { loadTopics, type SourceFile, type Topic } from "./content";
import { relativeHref, renderMarkdown, type Heading, type RenderedTopic } from "./render";
import { buildSearchIndex, type SearchDocument } from "./search";

// BuildFacts identify the build on every page and in the manifest.
export type BuildFacts = {
  release: string;
  starmapVersion: string;
  contentRevision: string;
};

// SiteAssets are the client script and stylesheet, as paths from the docs
// root, for example "assets/docs-1a2b3c.js".
export type SiteAssets = { script: string; stylesheet: string };

export type SiteInput = {
  files: readonly SourceFile[];
  facts: BuildFacts;
  assets: SiteAssets;
  // The pinned URL of a repository file outside the content tree, or null
  // when the build has no reference to pin.
  repositoryUrl: (repositoryPath: string) => string | null;
};

export type SitePage = { path: string; html: string };

export type SiteOutput = {
  pages: SitePage[];
  searchDocuments: SearchDocument[];
  searchIndex: string;
};

export const SEARCH_ROUTE = "search/";
export const SITE_NAME = "Starport documentation";

type RenderedPage = { topic: Topic; rendered: RenderedTopic };

function areaTitle(slug: string): string {
  return AREAS.find((area) => area.slug === slug)?.title ?? "Starport";
}

// shortRevision is the revision the page header prints. The manifest keeps
// the full digest.
export function shortRevision(revision: string): string {
  return revision.slice(0, 12);
}

function HeadingAnchor({ id, text }: { id: string; text: string }) {
  return (
    <a className="heading-anchor" href={`#${id}`} aria-label={`Link to section: ${text}`}>
      <span aria-hidden="true">#</span>
    </a>
  );
}

function SiteNav({ route, topics }: { route: string; topics: readonly Topic[] }) {
  return (
    <nav className="site-nav" aria-label="Documentation">
      <ul>
        {AREAS.map((area) => {
          const areaRoute = `${area.slug}/`;
          const children = topics.filter((topic) => topic.area === area.slug && topic.slug !== "index");
          return (
            <li key={area.slug}>
              <a
                className="nav-area"
                href={relativeHref(route, areaRoute)}
                aria-current={route === areaRoute ? "page" : undefined}
              >
                {area.title}
              </a>
              <ul>
                {children.map((topic) => (
                  <li key={topic.route}>
                    <a href={relativeHref(route, topic.route)} aria-current={route === topic.route ? "page" : undefined}>
                      {topic.title}
                    </a>
                  </li>
                ))}
              </ul>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

function TableOfContents({ headings }: { headings: readonly Heading[] }) {
  const entries = headings.filter((heading) => heading.depth <= 3);
  if (entries.length === 0) return null;
  return (
    <div className="toc">
      <nav aria-label="On this page" data-toc="">
        <p className="toc-title">On this page</p>
        <ol>
          {entries.map((heading) => (
            <li key={heading.id} className={heading.depth === 3 ? "toc-sub" : undefined}>
              <a href={`#${heading.id}`}>{heading.text}</a>
            </li>
          ))}
        </ol>
      </nav>
    </div>
  );
}

// BuildFactsList names its group on a wrapper, because a list of terms
// takes no accessible name of its own.
function BuildFactsList({ facts }: { facts: BuildFacts }) {
  return (
    <div role="group" aria-label="Build information">
      <dl className="build-facts">
        <div>
          <dt>Release</dt>
          <dd data-fact="release">{facts.release}</dd>
        </div>
        <div>
          <dt>Starmap</dt>
          <dd data-fact="starmap">{facts.starmapVersion}</dd>
        </div>
        <div>
          <dt>Content</dt>
          <dd data-fact="content" title={facts.contentRevision}>
            {shortRevision(facts.contentRevision)}
          </dd>
        </div>
      </dl>
    </div>
  );
}

// VersionSelector lists the builds a reader can open. The embedded build
// knows only itself. The summary names the current version for a screen
// reader, and the disclosure works from the keyboard without the script.
function VersionSelector({ route, facts }: { route: string; facts: BuildFacts }) {
  return (
    <details className="version-selector">
      <summary>
        <span className="sr-only">Documentation version: </span>
        {facts.release}
      </summary>
      <ul aria-label="Documentation versions">
        <li>
          <a href={relativeHref(route, "")} aria-current="true">
            {facts.release} (this build)
          </a>
        </li>
      </ul>
    </details>
  );
}

function Page({
  route,
  title,
  facts,
  assets,
  topics,
  headings,
  description,
  children,
}: {
  route: string;
  title: string;
  facts: BuildFacts;
  assets: SiteAssets;
  topics: readonly Topic[];
  headings: readonly Heading[];
  description: string;
  children: ReactNode;
}) {
  const root = relativeHref(route, "");
  return (
    <html lang="en">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <title>{route === "" ? SITE_NAME : `${title} | ${SITE_NAME}`}</title>
        <meta name="description" content={description} />
        <meta name="starport-docs-root" content={root} />
        <meta name="starport-docs-release" content={facts.release} />
        <link rel="stylesheet" href={relativeHref(route, assets.stylesheet)} />
        <script src={relativeHref(route, assets.script)} />
      </head>
      <body>
        <a className="skip-link" href="#content">
          Skip to content
        </a>
        <header className="site-header">
          <a className="site-brand" href={root} aria-current={route === "" ? "page" : undefined}>
            Starport docs
          </a>
          <form
            className="site-search"
            role="search"
            aria-label="Documentation search"
            action={relativeHref(route, SEARCH_ROUTE)}
            method="get"
          >
            <label className="sr-only" htmlFor="site-search-input">
              Search the documentation
            </label>
            <input id="site-search-input" type="search" name="q" placeholder="Search the docs" autoComplete="off" />
            <button type="submit">Search</button>
          </form>
          <div className="site-meta">
            <BuildFactsList facts={facts} />
            <VersionSelector route={route} facts={facts} />
            <button type="button" className="theme-toggle" data-theme-toggle="" hidden>
              Theme
            </button>
          </div>
        </header>
        <div className="site-body">
          <SiteNav route={route} topics={topics} />
          <main id="content" tabIndex={-1}>
            <article>{children}</article>
          </main>
          <TableOfContents headings={headings} />
        </div>
        <footer className="site-footer">
          <p>
            Starport {facts.release}. Starmap module {facts.starmapVersion}. Content revision{" "}
            <code>{shortRevision(facts.contentRevision)}</code>.
          </p>
        </footer>
      </body>
    </html>
  );
}

const TOPIC_LIST_ID = "topics-in-this-area";
const AREA_LIST_ID = "documentation-areas";

function TopicList({ route, topics }: { route: string; topics: readonly Topic[] }) {
  return (
    <ul className="topic-list">
      {topics.map((topic) => (
        <li key={topic.route}>
          <a href={relativeHref(route, topic.route)}>{topic.title}</a>
          <p>{topic.summary}</p>
        </li>
      ))}
    </ul>
  );
}

function topicPage(page: RenderedPage, topics: readonly Topic[], input: SiteInput): SitePage {
  const { topic, rendered } = page;
  const headings = [...rendered.headings];
  let listing: ReactNode = null;
  if (topic.area === HOME_AREA) {
    const areaPages = topics.filter((entry) => entry.slug === "index" && entry.area !== HOME_AREA);
    headings.push({ depth: 2, id: AREA_LIST_ID, text: "Documentation areas" });
    listing = (
      <section aria-labelledby={AREA_LIST_ID}>
        <h2 id={AREA_LIST_ID}>
          Documentation areas
          <HeadingAnchor id={AREA_LIST_ID} text="Documentation areas" />
        </h2>
        <TopicList route={topic.route} topics={areaPages} />
      </section>
    );
  } else if (topic.slug === "index") {
    const areaTopics = topics.filter((entry) => entry.area === topic.area && entry.slug !== "index");
    headings.push({ depth: 2, id: TOPIC_LIST_ID, text: "Topics in this area" });
    listing = (
      <section aria-labelledby={TOPIC_LIST_ID}>
        <h2 id={TOPIC_LIST_ID}>
          Topics in this area
          <HeadingAnchor id={TOPIC_LIST_ID} text="Topics in this area" />
        </h2>
        {areaTopics.length > 0 ? (
          <TopicList route={topic.route} topics={areaTopics} />
        ) : (
          <p>This build has no topics in this area.</p>
        )}
      </section>
    );
  }
  const eyebrow = topic.area === HOME_AREA ? null : areaTitle(topic.area);
  const markup = renderToStaticMarkup(
    <Page
      route={topic.route}
      title={topic.title}
      facts={input.facts}
      assets={input.assets}
      topics={topics}
      headings={headings}
      description={topic.summary}
    >
      <header className="article-header">
        {eyebrow && <p className="article-area">{eyebrow}</p>}
        <h1>{topic.title}</h1>
        <p className="article-summary">{topic.summary}</p>
        {topic.generated && <p className="article-note">The build generates this page. Do not edit it by hand.</p>}
      </header>
      <div className="prose" dangerouslySetInnerHTML={{ __html: rendered.html }} />
      {listing}
    </Page>,
  );
  return { path: `${topic.route}index.html`, html: `<!doctype html>\n${markup}\n` };
}

function searchPage(topics: readonly Topic[], input: SiteInput): SitePage {
  const route = SEARCH_ROUTE;
  const markup = renderToStaticMarkup(
    <Page
      route={route}
      title="Search"
      facts={input.facts}
      assets={input.assets}
      topics={topics}
      headings={[]}
      description="Search the Starport documentation."
    >
      <header className="article-header">
        <h1>Search</h1>
        <p className="article-summary">Search the headings, prose, and setting names in this build.</p>
      </header>
      <form className="search-page-form" role="search" aria-label="Search this build" action="./" method="get">
        <label htmlFor="search-page-input">Search terms</label>
        <input id="search-page-input" type="search" name="q" autoComplete="off" />
        <button type="submit">Search</button>
      </form>
      <p className="search-status" id="search-status" role="status" aria-live="polite" />
      <ol className="search-results" id="search-results" />
      <noscript>
        <p>Search needs JavaScript. Use the documentation navigation to find a topic.</p>
      </noscript>
    </Page>,
  );
  return { path: `${route}index.html`, html: `<!doctype html>\n${markup}\n` };
}

function searchDocuments(pages: readonly RenderedPage[]): SearchDocument[] {
  const documents: SearchDocument[] = [];
  for (const { topic, rendered } of pages) {
    const area = topic.area === HOME_AREA ? SITE_NAME : areaTitle(topic.area);
    for (const section of rendered.sections) {
      const fragment = section.id ? `#${section.id}` : "";
      documents.push({
        id: `${topic.route}${fragment}`,
        href: `${topic.route}${fragment}`,
        title: topic.title,
        area,
        heading: section.heading ?? "",
        text: section.id ? section.text : `${topic.summary} ${section.text}`.trim(),
        terms: section.terms.join(" "),
      });
    }
    if (!rendered.sections.some((section) => section.id === null)) {
      documents.push({
        id: topic.route,
        href: topic.route,
        title: topic.title,
        area,
        heading: "",
        text: topic.summary,
        terms: "",
      });
    }
  }
  return documents;
}

// buildSite renders every page of the documentation site. It reads no file
// and no environment: the caller supplies the content tree, the build facts,
// and the asset names, so the same input always gives the same output.
export function buildSite(input: SiteInput): SiteOutput {
  const topics = loadTopics(input.files);
  const routes = new Map(topics.map((topic) => [topic.source, topic.route]));
  const pages: RenderedPage[] = topics.map((topic) => ({
    topic,
    rendered: renderMarkdown(topic.body, {
      source: topic.source,
      route: topic.route,
      routeOf: (path) => routes.get(path),
      repositoryUrl: input.repositoryUrl,
    }),
  }));
  const documents = searchDocuments(pages);
  return {
    pages: [...pages.map((page) => topicPage(page, topics, input)), searchPage(topics, input)],
    searchDocuments: documents,
    searchIndex: buildSearchIndex(documents),
  };
}
