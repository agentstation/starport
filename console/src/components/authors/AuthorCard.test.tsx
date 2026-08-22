import { expect, test } from "vitest";

import type { CatalogAuthor, Model } from "@/lib/api";

import {
  authorExternalLinks,
  authorLabel,
  matchesAuthorQuery,
  modelCountsByAuthor,
  sortAuthors,
} from "./AuthorCard";

const models: Model[] = [
  {
    id: "openai/gpt-4o",
    authors: [{ id: "openai", name: "OpenAI" }],
  },
  {
    id: "openai/gpt-oss-120b",
    authors: [{ id: "openai", name: "OpenAI" }],
  },
  // No declared authors: the id prefix is the fallback attribution,
  // matching the models-page author facet.
  { id: "mistral/mistral-large" },
];

test("model counts derive from declared authors with a prefix fallback", () => {
  const counts = modelCountsByAuthor(models);
  expect(counts.get("openai")).toBe(2);
  expect(counts.get("mistral")).toBe(1);
  expect(counts.get("meta")).toBeUndefined();
});

test("external links flatten only the populated catalog fields", () => {
  const author: CatalogAuthor = {
    id: "openai",
    website: "https://openai.com",
    github: "https://github.com/openai",
  };
  expect(authorExternalLinks(author)).toEqual([
    { label: "website", href: "https://openai.com" },
    { label: "github", href: "https://github.com/openai" },
  ]);
  expect(authorExternalLinks({ id: "bare" })).toEqual([]);
});

test("search matches id, name, and description", () => {
  const author: CatalogAuthor = {
    id: "meta",
    name: "Meta",
    description: "Llama models",
  };
  expect(matchesAuthorQuery("llama", author)).toBe(true);
  expect(matchesAuthorQuery("meta", author)).toBe(true);
  expect(matchesAuthorQuery("anthropic", author)).toBe(false);
  expect(matchesAuthorQuery("", author)).toBe(true);
});

test("authors sort by model count, then name", () => {
  const counts = new Map([
    ["openai", 2],
    ["mistral", 1],
    ["a-lab", 1],
  ]);
  const sorted = sortAuthors(
    [
      { id: "mistral", name: "Mistral" },
      { id: "a-lab", name: "A Lab" },
      { id: "openai", name: "OpenAI" },
    ],
    counts,
  );
  expect(sorted.map((author) => author.id)).toEqual([
    "openai",
    "a-lab",
    "mistral",
  ]);
});

test("the label prefers the display name over the id", () => {
  expect(authorLabel({ id: "openai", name: "OpenAI" })).toBe("OpenAI");
  expect(authorLabel({ id: "z-ai" })).toBe("z-ai");
});
