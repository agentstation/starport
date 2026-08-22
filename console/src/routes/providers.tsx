import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { RefreshCw } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { EntityLogo } from "@/components/catalog/EntityLogo";
import { ConnectCard } from "@/components/overview/ConnectCard";
import {
  ApiError,
  listProviderCatalog,
  providerStatus,
  refreshProviders,
  type ProviderCatalogEntry,
  type ProviderRuntimeStatus,
} from "@/lib/api";
import { formatCount, formatRelativeTime, providerLabel } from "@/lib/format";
import { useHasApiKey } from "@/lib/useApiKey";

export const Route = createFileRoute("/providers")({
  component: ProvidersPage,
});

// One status vocabulary (DESIGN.md): the dot is adapter liveness — can
// this build route to the provider at all — and the pill is the operator
// credential lifecycle. The two signals are orthogonal and never mixed.
function AdapterDot({ state }: { state: string | undefined }) {
  const ready = state === "ready";
  const label = ready ? "ready" : (state ?? "unknown").replaceAll("_", " ");
  return (
    <span className="flex items-center gap-1.5 text-xs text-text-3">
      <span
        aria-hidden="true"
        className={`size-2 shrink-0 rounded-full ${
          ready ? "bg-success" : state === "no_offerings" ? "bg-text-4" : "bg-error"
        }`}
      />
      {label}
    </span>
  );
}

const CREDENTIAL_TONES: Record<string, string> = {
  ready: "bg-success-tint text-success",
  not_configured: "bg-bg-raised text-text-3",
  refreshing: "bg-info-tint text-text-2",
  denied: "bg-error-tint text-error",
  invalid: "bg-error-tint text-error",
  unavailable: "bg-warning-tint text-warning",
};

function CredentialPill({
  credential,
}: {
  credential: ProviderRuntimeStatus["operator_credential"];
}) {
  const state = credential?.state ?? "unknown";
  const label =
    state === "not_configured" ? "no credential" : state.replaceAll("_", " ");
  return (
    <span
      title={
        credential?.updated_at
          ? `updated ${formatRelativeTime(credential.updated_at)}`
          : undefined
      }
      className={`inline-flex h-5 shrink-0 items-center whitespace-nowrap rounded-xs px-1.5 text-xs font-medium ${
        CREDENTIAL_TONES[state] ?? "bg-bg-raised text-text-3"
      }`}
    >
      {label}
    </span>
  );
}

function ProviderCard({
  status,
  entry,
}: {
  status: ProviderRuntimeStatus;
  entry: ProviderCatalogEntry | undefined;
}) {
  const credential = status.operator_credential;
  const offerings = status.offerings ?? [];
  const available = offerings.filter(
    (offering) => offering.state === "available",
  ).length;
  const credentialReason =
    credential &&
    !credential.usable &&
    credential.state !== "not_configured" &&
    credential.reason;
  const adapterReason =
    status.adapter?.state !== "ready" &&
    status.adapter?.state !== "no_offerings" &&
    status.adapter?.reason;
  return (
    <div className="flex flex-col gap-3 rounded-md border border-border-1 bg-bg-panel p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-start gap-2.5">
          <EntityLogo
            kind="providers"
            id={status.provider_id}
            name={providerLabel(status.provider_id, entry?.name)}
            size={28}
            className="mt-0.5"
          />
          <div className="flex min-w-0 flex-col gap-1">
            <div className="truncate text-sm font-medium text-text-1">
              {providerLabel(status.provider_id, entry?.name)}
            </div>
            <span className="self-start rounded-xs border border-border-1 bg-bg-raised px-1.5 py-0.5 font-mono text-xs text-text-2">
              {status.provider_id}
            </span>
          </div>
        </div>
        <CredentialPill credential={credential} />
      </div>
      {(credentialReason || adapterReason) && (
        <p className="text-xs text-text-3">
          {(credentialReason || adapterReason || "").replaceAll("_", " ")}
        </p>
      )}
      <div className="flex items-center gap-4 text-xs text-text-3">
        <AdapterDot state={status.adapter?.state} />
        <Link
          to="/models"
          search={{ provider: status.provider_id }}
          className="tabular-nums text-text-2 transition-colors duration-150 ease-standard hover:text-accent-link hover:underline"
        >
          {formatCount(offerings.length)} offerings
        </Link>
        <span className="tabular-nums">{formatCount(available)} available</span>
      </div>
    </div>
  );
}

// Credentialed providers sort first: a usable credential ranks above a
// configured-but-broken one, which ranks above no credential at all.
function credentialRank(status: ProviderRuntimeStatus): number {
  if (status.operator_credential?.usable) return 0;
  if (status.operator_credential?.state === "not_configured") return 2;
  return 1;
}

function CatalogOnly({ catalog }: { catalog: ProviderCatalogEntry[] }) {
  if (catalog.length === 0) {
    return (
      <p className="text-base text-text-3">
        Provider status needs an admin-scoped key.
      </p>
    );
  }
  return (
    <>
      <p className="text-sm text-text-3">
        Credential and availability detail needs an admin-scoped key. Showing
        the catalog view.
      </p>
      <div className="grid gap-3 md:grid-cols-2">
        {catalog.map((entry) => (
          <div
            key={entry.id}
            className="flex flex-col gap-2 rounded-md border border-border-1 bg-bg-panel p-4"
          >
            <div className="flex items-center gap-2.5">
              <EntityLogo
                kind="providers"
                id={entry.id}
                name={providerLabel(entry.id, entry.name)}
                size={24}
              />
              <div className="truncate text-sm font-medium text-text-1">
                {providerLabel(entry.id, entry.name)}
              </div>
            </div>
            <span className="self-start rounded-xs border border-border-1 bg-bg-raised px-1.5 py-0.5 font-mono text-xs text-text-2">
              {entry.id}
            </span>
            <Link
              to="/models"
              search={{ provider: entry.id }}
              className="text-xs tabular-nums text-text-2 transition-colors duration-150 ease-standard hover:text-accent-link hover:underline"
            >
              {formatCount(entry.models?.length ?? 0)} models
            </Link>
          </div>
        ))}
      </div>
    </>
  );
}

function ProvidersPage() {
  const hasKey = useHasApiKey();
  const queryClient = useQueryClient();
  const [refreshing, setRefreshing] = useState(false);
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );
  const noticeTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(noticeTimer.current), []);

  const status = useQuery({
    queryKey: ["provider-status"],
    queryFn: providerStatus,
    enabled: hasKey,
    retry: false,
  });
  const catalog = useQuery({
    queryKey: ["provider-catalog"],
    queryFn: listProviderCatalog,
    enabled: hasKey,
    retry: false,
  });

  const byId = useMemo(
    () => new Map((catalog.data ?? []).map((entry) => [entry.id, entry])),
    [catalog.data],
  );

  const sorted = useMemo(
    () =>
      [...(status.data?.providers ?? [])].sort(
        (a, b) =>
          credentialRank(a) - credentialRank(b) ||
          a.provider_id.localeCompare(b.provider_id),
      ),
    [status.data],
  );

  const say = (text: string, error = false) => {
    setNotice({ text, error });
    clearTimeout(noticeTimer.current);
    noticeTimer.current = setTimeout(() => setNotice(null), 6000);
  };

  const refresh = async () => {
    setRefreshing(true);
    try {
      const report = await refreshProviders();
      if (report?.failure_count) {
        const count = report.failure_count;
        say(`Refresh finished with ${count} failure${count === 1 ? "" : "s"}`, true);
      } else {
        say(report?.changed ? "Provider state updated" : "Provider state unchanged");
      }
      await queryClient.invalidateQueries({ queryKey: ["provider-status"] });
    } catch (error) {
      if (error instanceof ApiError && error.needsKey) {
        say("Refresh needs an admin-scoped key", true);
      } else {
        say(
          `Refresh failed: ${error instanceof Error ? error.message : error}`,
          true,
        );
      }
    } finally {
      setRefreshing(false);
    }
  };

  if (!hasKey) {
    return (
      <div className="flex flex-col gap-4">
        <Header />
        <ConnectCard />
      </div>
    );
  }

  let body: ReactNode;
  if (status.error) {
    if (status.error instanceof ApiError && status.error.needsKey) {
      body = <CatalogOnly catalog={catalog.data ?? []} />;
    } else {
      body = (
        <p className="text-base text-text-3">
          Failed to load providers: {status.error.message}
        </p>
      );
    }
  } else if (status.isPending) {
    body = <p className="text-base text-text-3">Loading providers…</p>;
  } else if (sorted.length === 0) {
    body = (
      <p className="text-base text-text-3">
        No providers in this catalog snapshot.
      </p>
    );
  } else {
    body = (
      <div className="grid gap-3 md:grid-cols-2">
        {sorted.map((provider) => (
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
          {notice && (
            <span
              className={`text-xs ${notice.error ? "text-error" : "text-success"}`}
            >
              {notice.text}
            </span>
          )}
          <button
            type="button"
            onClick={refresh}
            disabled={refreshing}
            className="flex h-8 items-center gap-1.5 rounded-sm border border-border-2 bg-bg-raised px-3 text-xs text-text-2 transition-colors duration-150 ease-standard hover:bg-bg-hover disabled:opacity-50"
          >
            <RefreshCw className={`size-3.5 ${refreshing ? "animate-spin" : ""}`} />
            refresh
          </button>
        </div>
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
