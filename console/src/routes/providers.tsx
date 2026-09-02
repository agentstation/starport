import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { RefreshCw, Search } from "lucide-react";
import { useMemo, type ReactNode } from "react";

import {
  CatalogProviderCard,
  ProviderCard,
  credentialRank,
} from "@/components/providers/ProviderCard";
import { IconButton } from "@/components/ui/IconButton";
import { Select } from "@/components/ui/Select";
import { CardGridSkeleton } from "@/components/ui/skeleton";
import {
  accessMessage,
  ApiError,
  refreshProviders,
  type ProviderCatalogEntry,
  type ProviderRuntimeStatus,
} from "@/lib/api";
import { queries, settle } from "@/lib/queries";
import { oneOf, optionalString } from "@/lib/search";
import { providerLabel } from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";
import { announce, errorText, report } from "@/lib/mutations";

const SORT_KEYS = ["status", "name", "models"] as const;
type SortKey = (typeof SORT_KEYS)[number];

// The search text and the sort order live in the address, so Back and a
// reload keep them. The default sort stays out of the address.
type ProvidersSearch = { q?: string; sort?: SortKey };

export const Route = createFileRoute("/providers")({
  component: ProvidersPage,
  loader: ({ context }) =>
    settle(
      context.queryClient.ensureQueryData(queries.providerCatalog()),
      context.queryClient.ensureQueryData(queries.providerStatus()),
    ),
  validateSearch: (search: Record<string, unknown>): ProvidersSearch => ({
    q: optionalString(search.q),
    sort: oneOf(SORT_KEYS, search.sort),
  }),
});

function matchesQuery(
  query: string,
  id: string,
  entry: ProviderCatalogEntry | undefined,
): boolean {
  if (!query) return true;
  const haystack = [id, entry?.name, entry?.description]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  return haystack.includes(query);
}

function sortProviders(
  providers: ProviderRuntimeStatus[],
  byId: Map<string, ProviderCatalogEntry>,
  sort: SortKey,
): ProviderRuntimeStatus[] {
  const name = (status: ProviderRuntimeStatus) =>
    providerLabel(status.provider_id, byId.get(status.provider_id)?.name);
  return [...providers].sort((a, b) => {
    if (sort === "models") {
      return (
        (b.offerings?.length ?? 0) - (a.offerings?.length ?? 0) ||
        name(a).localeCompare(name(b))
      );
    }
    if (sort === "name") return name(a).localeCompare(name(b));
    return credentialRank(a) - credentialRank(b) || name(a).localeCompare(name(b));
  });
}

function CatalogOnly({
  catalog,
  query,
}: {
  catalog: ProviderCatalogEntry[];
  query: string;
}) {
  if (catalog.length === 0) {
    return (
      <p className="text-base text-text-3">
        Provider status needs an admin-scoped key.
      </p>
    );
  }
  const visible = catalog.filter((entry) =>
    matchesQuery(query, entry.id, entry),
  );
  return (
    <>
      <p className="text-sm text-text-3">
        Credential and availability detail needs an admin-scoped key. Showing
        the catalog view.
      </p>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        {visible.map((entry) => (
          <CatalogProviderCard key={entry.id} entry={entry} />
        ))}
      </div>
    </>
  );
}

function ProvidersPage() {
  const keyUsable = useGatewayAccess();
  const queryClient = useQueryClient();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const setSearch = (patch: Partial<ProvidersSearch>) =>
    void navigate({
      search: (previous: ProvidersSearch) => ({ ...previous, ...patch }),
      replace: true,
    });
  const query = search.q ?? "";
  const sort = search.sort ?? "status";
  const setQuery = (value: string) => setSearch({ q: value || undefined });
  const setSort = (value: SortKey) => setSearch({ sort: value === "status" ? undefined : value });

  const status = useQuery({
    ...queries.providerStatus(),
    enabled: keyUsable,
  });
  const catalog = useQuery({
    ...queries.providerCatalog(),
    enabled: keyUsable,
  });

  const byId = useMemo(
    () => new Map((catalog.data ?? []).map((entry) => [entry.id, entry])),
    [catalog.data],
  );

  const trimmed = query.trim().toLowerCase();
  const visible = useMemo(
    () =>
      sortProviders(
        (status.data?.providers ?? []).filter((provider) =>
          matchesQuery(trimmed, provider.provider_id, byId.get(provider.provider_id)),
        ),
        byId,
        sort,
      ),
    [status.data, byId, trimmed, sort],
  );


  const refresh = useMutation({
    mutationFn: () => refreshProviders(),
    onSuccess: async (result) => {
      if (result?.failure_count) {
        const count = result.failure_count;
        report(`Refresh finished with ${count} failure${count === 1 ? "" : "s"}`);
      } else {
        announce(result?.changed ? "Provider state updated" : "Provider state unchanged");
      }
      await queryClient.invalidateQueries({ queryKey: queries.providerStatus().queryKey });
    },
    onError: (error) => {
      if (error instanceof ApiError && error.needsKey) {
        report(accessMessage(error, "admin"));
      } else {
        report(`Refresh failed: ${errorText(error)}`);
      }
    },
  });

  let body: ReactNode;
  if (status.error) {
    if (status.error instanceof ApiError && status.error.needsKey) {
      body = <CatalogOnly catalog={catalog.data ?? []} query={trimmed} />;
    } else {
      body = (
        <p className="text-base text-text-3">
          Failed to load providers: {status.error.message}
        </p>
      );
    }
  } else if (status.isPending) {
    body = <CardGridSkeleton />;
  } else if ((status.data?.providers ?? []).length === 0) {
    body = (
      <p className="text-base text-text-3">
        No providers in this catalog snapshot.
      </p>
    );
  } else if (visible.length === 0) {
    body = (
      <p className="text-base text-text-3">
        No providers match “{query.trim()}”.
      </p>
    );
  } else {
    body = (
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        {visible.map((provider) => (
          <ProviderCard
            key={provider.provider_id}
            status={provider}
            entry={byId.get(provider.provider_id)}
          />
        ))}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <Header />
        <div className="flex items-center gap-3">
          <IconButton
            label="Refresh provider status"
            onClick={() => refresh.mutate()}
            disabled={refresh.isPending}
            className="size-8 rounded-sm border border-border-2 bg-bg-raised text-text-2 hover:bg-bg-hover disabled:opacity-50"
          >
            <RefreshCw className={`size-3.5 ${refresh.isPending ? "animate-spin" : ""}`} />
          </IconButton>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <label className="flex h-8 flex-1 basis-56 items-center gap-2 rounded-sm border border-border-2 bg-bg-raised px-2.5 focus-within:border-accent">
          <Search className="size-3.5 shrink-0 text-text-4" />
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search providers"
            aria-label="Search providers"
            className="w-full bg-transparent text-sm text-text-1 outline-none placeholder:text-text-4"
          />
        </label>
        <Select
          uiSize="sm"
          value={sort}
          onChange={(event) => setSort(event.target.value as SortKey)}
          aria-label="Sort providers"
        >
          <option value="status">Sort: status</option>
          <option value="name">Sort: name</option>
          <option value="models">Sort: models</option>
        </Select>
      </div>
      {body}
    </div>
  );
}

function Header() {
  return (
    <div>
      <h1 className="text-xl font-semibold tracking-[-0.01em]">Providers</h1>
      <p className="mt-1 text-sm text-text-3">
        Upstream services this gateway can route to, and whether it can reach
        them.
      </p>
    </div>
  );
}
