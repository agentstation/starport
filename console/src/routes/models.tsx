import { useQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useEffect, useMemo, useRef } from "react";

import { FreshnessBar } from "@/components/models/FreshnessBar";
import { ModelsTable } from "@/components/models/ModelsTable";
import { ConnectCard } from "@/components/overview/ConnectCard";
import { ApiError, listModels, type Model } from "@/lib/api";
import { formatCount } from "@/lib/format";
import { useHasApiKey } from "@/lib/useApiKey";

// Filter state lives in the URL so a filtered view survives reload and
// pastes as a link. Empty params are dropped from the search string.
type ModelsSearch = {
  q?: string;
  provider?: string;
  modality?: string;
  capability?: string;
};

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
      modality: str(search.modality),
      capability: str(search.capability),
    };
  },
});

// providerOf mirrors the gateway's model ID shape: "<provider>/<model>".
function providerOf(model: Model): string {
  const slash = model.id.indexOf("/");
  return slash > 0 ? model.id.slice(0, slash) : "";
}

function hasCapability(model: Model, capability: string): boolean {
  const params = model.supported_parameters ?? [];
  if (capability === "reasoning") {
    return params.includes("reasoning") || params.includes("include_reasoning");
  }
  return params.includes(capability);
}

function matches(model: Model, search: ModelsSearch): boolean {
  if (search.provider && providerOf(model) !== search.provider) return false;
  if (
    search.modality &&
    !(model.architecture?.input_modalities ?? []).includes(search.modality)
  ) {
    return false;
  }
  if (search.capability && !hasCapability(model, search.capability)) return false;
  if (search.q) {
    const haystack = `${model.id} ${model.name ?? ""}`.toLowerCase();
    if (!haystack.includes(search.q.toLowerCase())) return false;
  }
  return true;
}

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

function ModelsPage() {
  const hasKey = useHasApiKey();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const searchRef = useRef<HTMLInputElement>(null);

  const models = useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    enabled: hasKey,
    retry: false,
  });

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
        label: `${provider} (${count})`,
      }));
  }, [all]);

  const filtered = useMemo(
    () => all.filter((model) => matches(model, search)),
    [all, search],
  );

  if (!hasKey) {
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
            value={search.q ?? ""}
            onChange={(event) => setSearch({ q: event.target.value || undefined })}
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
            ? "Your API key was rejected. Update it in Settings."
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
