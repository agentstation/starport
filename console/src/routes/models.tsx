import { useQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { FreshnessBar } from "@/components/models/FreshnessBar";
import { ModelsTable } from "@/components/models/ModelsTable";
import { ConnectCard } from "@/components/overview/ConnectCard";
import { accessMessage, ApiError, listModels, listProviderCatalog } from "@/lib/api";
import { formatCount, providerLabel } from "@/lib/format";
import {
  authorIdsOf,
  matches,
  type ModelsSearch,
  providerOf,
} from "@/lib/modelFilter";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

// Filter state lives in the URL so a filtered view survives reload and
// pastes as a link. Empty params are dropped from the search string.
const MODALITIES = ["text", "image", "audio", "file"] as const;
const CAPABILITIES = ["tools", "reasoning", "structured_outputs"] as const;

export const Route = createFileRoute("/models")({
  component: ModelsPage,
  validateSearch: (search: Record<string, unknown>): ModelsSearch => {
    const str = (value: unknown) =>
      typeof value === "string" && value !== "" ? value : undefined;
    return {
      q: str(search.q),
      provider: str(search.provider),
      author: str(search.author),
      tag: str(search.tag),
      modality: str(search.modality),
      capability: str(search.capability),
    };
  },
});


function FilterSelect({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string | undefined;
  options: Array<{ value: string; label: string }>;
  onChange: (value: string | undefined) => void;
}) {
  return (
    <select
      aria-label={label}
      value={value ?? ""}
      onChange={(event) => onChange(event.target.value || undefined)}
      className="h-8 rounded-sm border border-border-1 bg-bg-panel px-2 text-xs text-text-2 outline-none transition-colors duration-150 ease-standard hover:border-border-2 focus:border-accent"
    >
      <option value="">{label}</option>
      {options.map((option) => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  );
}

const SEARCH_DEBOUNCE_MS = 200;

function ModelsPage() {
  const keyUsable = useGatewayAccess();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const searchRef = useRef<HTMLInputElement>(null);

  // The input edits a local draft; the URL (and the filter pass over
  // 400+ rows) follows after a debounce.
  const [draftQuery, setDraftQuery] = useState(search.q ?? "");
  useEffect(() => {
    setDraftQuery(search.q ?? "");
  }, [search.q]);
  useEffect(() => {
    const timer = setTimeout(() => {
      if ((search.q ?? "") !== draftQuery) {
        setSearch({ q: draftQuery || undefined });
      }
    }, SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  });

  const models = useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    enabled: keyUsable,
    retry: false,
  });
  const catalog = useQuery({
    queryKey: ["provider-catalog"],
    queryFn: listProviderCatalog,
    enabled: keyUsable,
    retry: false,
  });
  const providerNames = useMemo(
    () => new Map((catalog.data ?? []).map((entry) => [entry.id, entry.name])),
    [catalog.data],
  );

  // "/" focuses catalog search from anywhere on the page (DESIGN.md).
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.tagName === "SELECT" ||
          target.isContentEditable)
      ) {
        return;
      }
      event.preventDefault();
      searchRef.current?.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const setSearch = (patch: Partial<ModelsSearch>) => {
    void navigate({
      search: (previous: ModelsSearch) => ({ ...previous, ...patch }),
      replace: true,
    });
  };

  const all = useMemo(() => models.data ?? [], [models.data]);

  const providers = useMemo(() => {
    const counts = new Map<string, number>();
    for (const model of all) {
      const provider = providerOf(model);
      if (provider) counts.set(provider, (counts.get(provider) ?? 0) + 1);
    }
    return [...counts.entries()]
      .sort((a, b) => a[0].localeCompare(b[0]))
      .map(([provider, count]) => ({
        value: provider,
        label: `${providerLabel(provider, providerNames.get(provider))} (${count})`,
      }));
  }, [all, providerNames]);

  const authors = useMemo(() => {
    const counts = new Map<string, { name: string; count: number }>();
    for (const model of all) {
      const named = new Map(
        (model.authors ?? []).map((author) => [author.id, author.name]),
      );
      for (const id of authorIdsOf(model)) {
        const entry = counts.get(id) ?? { name: named.get(id) ?? id, count: 0 };
        entry.count += 1;
        counts.set(id, entry);
      }
    }
    return [...counts.entries()]
      .sort((a, b) => a[0].localeCompare(b[0]))
      .map(([id, entry]) => ({
        value: id,
        label: `${entry.name} (${entry.count})`,
      }));
  }, [all]);

  const tags = useMemo(() => {
    const counts = new Map<string, number>();
    for (const model of all) {
      for (const tag of model.tags ?? []) {
        counts.set(tag, (counts.get(tag) ?? 0) + 1);
      }
    }
    return [...counts.entries()]
      .sort((a, b) => a[0].localeCompare(b[0]))
      .map(([tag, count]) => ({ value: tag, label: `${tag} (${count})` }));
  }, [all]);

  const filtered = useMemo(
    () => all.filter((model) => matches(model, search)),
    [all, search],
  );

  if (!keyUsable) {
    return (
      <div className="flex flex-col gap-4">
        <Header />
        <ConnectCard />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <Header />
      <FreshnessBar />
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-text-4" />
          <input
            ref={searchRef}
            type="search"
            placeholder="Search models  /"
            aria-label="Search models"
            value={draftQuery}
            onChange={(event) => setDraftQuery(event.target.value)}
            className="h-8 w-64 rounded-sm border border-border-1 bg-bg-panel pl-8 pr-2 text-sm text-text-1 outline-none transition-colors duration-150 ease-standard placeholder:text-text-4 hover:border-border-2 focus:border-accent"
          />
        </div>
        <FilterSelect
          label="All providers"
          value={search.provider}
          options={providers}
          onChange={(provider) => setSearch({ provider })}
        />
        <FilterSelect
          label="All authors"
          value={search.author}
          options={authors}
          onChange={(author) => setSearch({ author })}
        />
        {tags.length > 0 && (
          <FilterSelect
            label="All tags"
            value={search.tag}
            options={tags}
            onChange={(tag) => setSearch({ tag })}
          />
        )}
        <FilterSelect
          label="All modalities"
          value={search.modality}
          options={MODALITIES.map((modality) => ({ value: modality, label: modality }))}
          onChange={(modality) => setSearch({ modality })}
        />
        <FilterSelect
          label="All capabilities"
          value={search.capability}
          options={CAPABILITIES.map((capability) => ({
            value: capability,
            label: capability === "structured_outputs" ? "structured outputs" : capability,
          }))}
          onChange={(capability) => setSearch({ capability })}
        />
        <span className="ml-auto text-xs tabular-nums text-text-3">
          {models.isPending
            ? "loading…"
            : `${formatCount(filtered.length)} of ${formatCount(all.length)} models`}
        </span>
      </div>
      {models.error ? (
        <p className="text-base text-text-3">
          {models.error instanceof ApiError && models.error.needsKey
            ? accessMessage(models.error, "models:read")
            : `Failed to load models: ${models.error.message}`}
        </p>
      ) : models.isPending ? (
        <p className="text-base text-text-3">Loading the model catalog…</p>
      ) : filtered.length === 0 ? (
        <p className="text-base text-text-3">
          No models match these filters.{" "}
          <button
            type="button"
            onClick={() =>
              setSearch({
                q: undefined,
                provider: undefined,
                author: undefined,
                tag: undefined,
                modality: undefined,
                capability: undefined,
              })
            }
            className="text-accent hover:underline"
          >
            Clear filters
          </button>
        </p>
      ) : (
        <ModelsTable models={filtered} />
      )}
    </div>
  );
}

function Header() {
  return (
    <div>
      <h1 className="text-xl font-semibold tracking-[-0.01em]">Models</h1>
      <p className="mt-1 text-sm text-text-3">
        Every model this gateway can route, from the current Starmap snapshot.
      </p>
    </div>
  );
}
