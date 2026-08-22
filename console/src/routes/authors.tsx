import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import {
  AuthorCard,
  matchesAuthorQuery,
  modelCountsByAuthor,
  sortAuthors,
} from "@/components/authors/AuthorCard";
import { ConnectCard } from "@/components/overview/ConnectCard";
import { listAuthors, listModels } from "@/lib/api";
import { useApiKeyUsable } from "@/lib/useApiKey";

export const Route = createFileRoute("/authors")({
  component: AuthorsPage,
});

function AuthorsPage() {
  const keyUsable = useApiKeyUsable();
  const [query, setQuery] = useState("");

  const authors = useQuery({
    queryKey: ["authors"],
    queryFn: listAuthors,
    enabled: keyUsable,
    retry: false,
  });
  // The authors list endpoint leaves `models` empty, so counts come
  // from the models list — the same query the models page caches.
  const models = useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    enabled: keyUsable,
    retry: false,
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

  if (!keyUsable) {
    return (
      <div className="flex flex-col gap-4">
        <Header count={0} />
        <ConnectCard />
      </div>
    );
  }

  let body: ReactNode;
  if (authors.error) {
    body = (
      <p className="text-base text-text-3">
        Failed to load authors: {authors.error.message}
      </p>
    );
  } else if (authors.isPending) {
    body = <p className="text-base text-text-3">Loading authors…</p>;
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
      <div className="grid gap-3 md:grid-cols-2">
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
