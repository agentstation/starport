import { Link } from "@tanstack/react-router";
import { Globe } from "lucide-react";
import type { ComponentType } from "react";

import { EntityLogo } from "@/components/catalog/EntityLogo";
import { ExternalLink } from "@/components/ui/ExternalLink";
import { GitHubMark, HuggingFaceMark, XMark } from "@/components/ui/icons";
import type { CatalogAuthor, Model } from "@/lib/api";
import { authorIdsOf } from "@/lib/modelFilter";
import { formatCount } from "@/lib/format";

// authorLabel prefers the catalog display name over the raw id.
export function authorLabel(author: CatalogAuthor): string {
  return author.name || author.id;
}

// authorExternalLinks flattens the optional catalog link fields into
// labeled entries so cards and detail headers render the same set.
export function authorExternalLinks(
  author: CatalogAuthor,
): { label: string; href: string }[] {
  return [
    { label: "website", href: author.website },
    { label: "github", href: author.github },
    { label: "hugging face", href: author.huggingface },
    { label: "twitter", href: author.twitter },
  ].filter((entry): entry is { label: string; href: string } =>
    Boolean(entry.href),
  );
}

// modelCountsByAuthor derives per-author model counts from the models
// list, because the authors list endpoint leaves its `models` arrays
// empty. Counting mirrors the models-page author facet (`authorIdsOf`),
// so both surfaces agree on what belongs to an author.
export function modelCountsByAuthor(models: Model[]): Map<string, number> {
  const counts = new Map<string, number>();
  for (const model of models) {
    for (const id of authorIdsOf(model)) {
      counts.set(id, (counts.get(id) ?? 0) + 1);
    }
  }
  return counts;
}

// matchesAuthorQuery searches the id, display name, and description.
export function matchesAuthorQuery(
  query: string,
  author: CatalogAuthor,
): boolean {
  if (!query) return true;
  return [author.id, author.name, author.description]
    .filter(Boolean)
    .join(" ")
    .toLowerCase()
    .includes(query);
}

// sortAuthors orders by model count, then name, so the majors lead.
export function sortAuthors(
  authors: CatalogAuthor[],
  counts: Map<string, number>,
): CatalogAuthor[] {
  return [...authors].sort(
    (a, b) =>
      (counts.get(b.id) ?? 0) - (counts.get(a.id) ?? 0) ||
      authorLabel(a).localeCompare(authorLabel(b)),
  );
}

// Each destination renders under its own mark so the row reads at a
// glance; the shared anchor still appends the new-tab glyph.
const LINK_MARKS: Record<string, ComponentType<{ className?: string }>> = {
  website: Globe,
  github: GitHubMark,
  "hugging face": HuggingFaceMark,
  twitter: XMark,
};

export function AuthorLinks({ author }: { author: CatalogAuthor }) {
  const links = authorExternalLinks(author);
  if (links.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-3">
      {links.map((link) => (
        <ExternalLink
          key={link.label}
          href={link.href}
          icon={LINK_MARKS[link.label]}
          iconClassName="size-3 shrink-0"
          // relative lifts these above the card's stretched detail link,
          // which would otherwise swallow the click.
          className="relative text-xs text-text-3 transition-colors duration-150 ease-standard hover:text-text-1"
        >
          {link.label}
        </ExternalLink>
      ))}
    </div>
  );
}

export function AuthorCard({
  author,
  modelCount,
}: {
  author: CatalogAuthor;
  modelCount: number;
}) {
  // The card holds external anchors, so it cannot itself be an anchor
  // (nested <a> is invalid HTML). The detail link stretches over the
  // card through its ::after overlay instead.
  return (
    <div
      data-testid="author-card"
      className="relative flex flex-col gap-2 rounded-md border border-border-1 bg-bg-panel p-4 transition-colors duration-150 ease-standard hover:border-border-2 hover:bg-bg-raised"
    >
      <div className="flex min-w-0 items-center gap-2.5">
        <EntityLogo
          kind="authors"
          id={author.id}
          name={authorLabel(author)}
          size={28}
        />
        <div className="flex min-w-0 items-baseline gap-2">
          <Link
            to="/authors/$authorId"
            params={{ authorId: author.id }}
            // truncate lives on the inner span: overflow-hidden on the
            // anchor itself would clip the stretched ::after overlay.
            className="min-w-0 text-sm font-medium text-text-1 after:absolute after:inset-0 after:rounded-md"
          >
            <span className="block truncate">{authorLabel(author)}</span>
          </Link>
          <span className="shrink-0 font-mono text-xs text-text-3">
            {author.id}
          </span>
        </div>
      </div>
      {author.description && (
        <p className="line-clamp-2 text-xs leading-relaxed text-text-3">
          {author.description}
        </p>
      )}
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs tabular-nums text-text-3">
          {formatCount(modelCount)} model{modelCount === 1 ? "" : "s"}
        </span>
        <AuthorLinks author={author} />
      </div>
    </div>
  );
}
