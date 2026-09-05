import { useQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useMemo, type ReactNode } from "react";

import {
  AuthorCard,
  matchesAuthorQuery,
  modelCountsByAuthor,
  sortAuthors,
} from "@/components/authors/AuthorCard";
import { queries, settle } from "@/lib/queries";
import { optionalString } from "@/lib/search";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

import { CardGridSkeleton } from "@/components/ui/skeleton";

// The search text lives in the address, so Back and a reload keep it.
type AuthorsSearch = { q?: string };

export const Route = createFileRoute("/authors")({
  component: AuthorsPage,
  loader: ({ context }) =>
    settle(
      context.queryClient.ensureQueryData(queries.authors()),
      context.queryClient.ensureQueryData(queries.models()),
    ),
  validateSearch: (search: Record<string, unknown>): AuthorsSearch => ({
    q: optionalString(search.q),
  }),
});

function AuthorsPage() {
  const keyUsable = useGatewayAccess();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const query = search.q ?? "";
  const setQuery = (value: string) =>
    void navigate({ search: { q: value || undefined }, replace: true });

  const authors = useQuery({
    ...queries.authors(),
    enabled: keyUsable,
  });
  // The authors list endpoint leaves `models` empty, so counts come
  // from the models list — the same query the models page caches.
  const models = useQuery({
    ...queries.models(),
    enabled: keyUsable,
  });

  const counts = useMemo(
    () => modelCountsByAuthor(models.data ?? []),
    [models.data],
  );

  const trimmed = query.trim().toLowerCase();
  const visible = useMemo(
    () =>
      sortAuthors(
        (authors.data ?? []).filter((author) =>
          matchesAuthorQuery(trimmed, author),
        ),
        counts,
      ),
    [authors.data, trimmed, counts],
  );

  let body: ReactNode;
  if (authors.error) {
    body = (
      <p className="text-base text-text-3">
        Failed to load authors: {authors.error.message}
      </p>
    );
  } else if (authors.isPending) {
    body = <CardGridSkeleton />;
  } else if ((authors.data ?? []).length === 0) {
    body = (
      <p className="text-base text-text-3">
        No authors in this catalog snapshot.
      </p>
    );
  } else if (visible.length === 0) {
    body = (
      <p className="text-base text-text-3">
        No authors match “{query.trim()}”.
      </p>
    );
  } else {
    body = (
      <div className="grid grid-cols-1 gap-3 @3xl:grid-cols-2">
        {visible.map((author) => (
          <AuthorCard
            key={author.id}
            author={author}
            modelCount={counts.get(author.id) ?? 0}
          />
        ))}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <Header count={authors.data?.length ?? 0} />
      <label className="flex h-8 max-w-md items-center gap-2 rounded-sm border border-border-2 bg-bg-raised px-2.5 focus-within:border-accent">
        <Search className="size-3.5 shrink-0 text-text-4" />
        <input
          type="search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search authors"
          aria-label="Search authors"
          className="w-full bg-transparent text-sm text-text-1 outline-none placeholder:text-text-4"
        />
      </label>
      {body}
    </div>
  );
}

function Header({ count }: { count: number }) {
  return (
    <div className="flex flex-col gap-1">
      <h1 className="text-xl font-semibold tracking-[-0.01em]">Authors</h1>
      <p className="text-sm text-text-3">
        {count > 0
          ? `${count} model authors in the current catalog snapshot.`
          : "Model authors in the current catalog snapshot."}
      </p>
    </div>
  );
}
