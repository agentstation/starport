import { expect, test } from "vitest";

import { TEST_ASSETS, TEST_FACTS } from "@/test/docsSite";

import { AREAS } from "./areas";
import { ContentError, loadTopics, parseFrontMatter, type SourceFile } from "./content";
import { relativeHref, renderMarkdown, resolvePath } from "./render";
import { buildSite } from "./site";

function page(path: string, title: string, area: string, order = 1, body = "Text.\n"): SourceFile {
  return { path, text: `---\ntitle: ${title}\narea: ${area}\norder: ${order}\nsummary: About ${title}.\n---\n\n${body}` };
}

// The smallest tree that loads: the home page and one page per area.
function minimalTree(): SourceFile[] {
  return [
    page("docs/site/index.md", "Home", "home"),
    ...AREAS.map((area) => page(`docs/site/${area.slug}/index.md`, area.title, area.slug)),
  ];
}

function build(files: SourceFile[]) {
  return buildSite({
    files,
    facts: TEST_FACTS,
    assets: TEST_ASSETS,
    repositoryUrl: (path) => `https://github.com/agentstation/starport/blob/v9.9.9/${path}`,
  });
}

test("front matter reads flat keys and quoted values", () => {
  const parsed = parseFrontMatter("x.md", '---\ntitle: "A: b"\norder: 2\n---\nBody\n');
  expect(parsed?.data).toEqual({ title: "A: b", order: "2" });
  expect(parsed?.body).toBe("Body\n");
  expect(parseFrontMatter("x.md", "No front matter")).toBeNull();
  expect(() => parseFrontMatter("x.md", "---\ntitle: x\n")).toThrow(ContentError);
});

test("an authored topic must have exactly the four front matter keys", () => {
  const missing = { path: "docs/site/start/a.md", text: "---\ntitle: A\narea: start\norder: 1\n---\nText\n" };
  expect(() => loadTopics([...minimalTree(), missing])).toThrow(/missing "summary"/);
  const extra = page("docs/site/start/a.md", "A", "start");
  extra.text = extra.text.replace("order: 1", "order: 1\ndraft: yes");
  expect(() => loadTopics([...minimalTree(), extra])).toThrow(/unknown key "draft"/);
  const order = page("docs/site/start/a.md", "A", "start");
  order.text = order.text.replace("order: 1", "order: first");
  expect(() => loadTopics([...minimalTree(), order])).toThrow(/"order" must be a whole number/);
});

test("the front matter area must match the directory", () => {
  expect(() => loadTopics([...minimalTree(), page("docs/site/start/a.md", "A", "storage")])).toThrow(
    /does not match the directory/,
  );
  expect(() => loadTopics([...minimalTree(), page("docs/site/extra/a.md", "A", "extra")])).toThrow(
    /not a documentation area/,
  );
});

test("the home page and every area page must exist", () => {
  expect(() => loadTopics(minimalTree().slice(1))).toThrow(/home page is missing/);
  expect(() => loadTopics(minimalTree().filter((file) => file.path !== "docs/site/storage/index.md"))).toThrow(
    /area page is missing/,
  );
});

test("topics sort by area, then order, then name", () => {
  const topics = loadTopics([
    ...minimalTree(),
    page("docs/site/storage/b.md", "B", "storage", 2),
    page("docs/site/storage/a.md", "A", "storage", 2),
    page("docs/site/start/z.md", "Z", "start", 9),
  ]);
  expect(topics.map((topic) => topic.route)).toEqual(
    expect.arrayContaining(["start/z/", "storage/a/", "storage/b/"]),
  );
  const routes = topics.map((topic) => topic.route);
  expect(routes[0]).toBe("");
  expect(routes.indexOf("start/z/")).toBeLessThan(routes.indexOf("storage/"));
  expect(routes.indexOf("storage/a/")).toBeLessThan(routes.indexOf("storage/b/"));
});

test("a generated page needs no front matter and joins Configure", () => {
  const topics = loadTopics([
    ...minimalTree(),
    { path: "docs/site/generated/env.md", text: "# Environment reference\n\nText.\n" },
  ]);
  const generated = topics.find((topic) => topic.generated);
  expect(generated).toMatchObject({ route: "configure/env/", title: "Environment reference", area: "configure" });
  expect(generated?.body).not.toContain("# Environment reference");
  // Two writers of one page are an error.
  expect(() =>
    loadTopics([
      ...minimalTree(),
      page("docs/site/configure/env.md", "Env", "configure"),
      { path: "docs/site/generated/env.md", text: "# Env\n" },
    ]),
  ).toThrow(/also written by/);
});

test("a level-one heading in a topic body is an error", () => {
  expect(() => build([...minimalTree(), page("docs/site/start/a.md", "A", "start", 1, "# Title\n")])).toThrow(
    /level-one heading/,
  );
});

test("links to unpinned branches and to missing pages are errors", () => {
  const blob = page(
    "docs/site/start/a.md",
    "A",
    "start",
    1,
    "[Guide](https://github.com/agentstation/starport/blob/main/docs/OPERATOR-GUIDE.md)\n",
  );
  expect(() => build([...minimalTree(), blob])).toThrow(/unpinned branch/);
  const missing = page("docs/site/start/a.md", "A", "start", 1, "[B](b.md)\n");
  expect(() => build([...minimalTree(), missing])).toThrow(/does not name a documentation page/);
  const outside = page("docs/site/start/a.md", "A", "start", 1, "[X](../../../../x.md)\n");
  expect(() => build([...minimalTree(), outside])).toThrow(/leaves the repository/);
});

test("content links become page-relative hrefs and repository links pin the release", () => {
  const output = build([
    ...minimalTree(),
    page("docs/site/start/a.md", "A", "start", 1, "[B](../storage/b.md#copy) and [guide](../../OPERATOR-GUIDE.md#modes).\n"),
    page("docs/site/storage/b.md", "B", "storage", 1, "## Copy\n\nText.\n"),
  ]);
  const html = output.pages.find((entry) => entry.path === "start/a/index.html")?.html ?? "";
  expect(html).toContain('href="../../storage/b/#copy"');
  expect(html).toContain('href="https://github.com/agentstation/starport/blob/v9.9.9/docs/OPERATOR-GUIDE.md#modes"');
});

test("relative paths between page directories", () => {
  expect(relativeHref("", "")).toBe("./");
  expect(relativeHref("", "start/")).toBe("start/");
  expect(relativeHref("start/", "")).toBe("../");
  expect(relativeHref("start/keys/", "storage/")).toBe("../../storage/");
  expect(resolvePath("docs/site/start/a.md", "../../RECOVERY.md")).toBe("docs/RECOVERY.md");
  expect(resolvePath("docs/a.md", "../../x.md")).toBeNull();
});

test("code blocks get a language label and a hidden copy control, tables scroll on their own", () => {
  const rendered = renderMarkdown("## Run\n\n```bash\nstarport doctor\n```\n\n| A | B |\n| - | - |\n| 1 | 2 |\n", {
    source: "docs/site/start/a.md",
    route: "start/a/",
    routeOf: () => undefined,
    repositoryUrl: () => null,
  });
  const doc = new DOMParser().parseFromString(rendered.html, "text/html");
  const block = doc.querySelector(".code-block");
  expect(block?.querySelector(".code-lang")?.textContent).toBe("bash");
  const button = block?.querySelector("button");
  expect(button?.hasAttribute("hidden")).toBe(true);
  expect(button?.getAttribute("type")).toBe("button");
  expect(block?.querySelector("pre")?.getAttribute("tabindex")).toBe("0");
  const region = doc.querySelector(".table-scroll");
  expect(region?.getAttribute("role")).toBe("region");
  expect(region?.querySelector("table")).not.toBeNull();
  expect(rendered.sections.find((section) => section.id === "run")?.text).toContain("starport doctor");
});
