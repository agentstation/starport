import { useQuery } from "@tanstack/react-query";

import { queries } from "@/lib/queries";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

// Readiness is what /health/ready answers: the gateway is ready, it is not,
// or the console has not heard from it yet.
type Readiness = "ready" | "not_ready" | "connecting";

const TITLES: Record<Readiness, string> = {
  ready: "Status: Ready",
  not_ready: "Status: Not ready",
  connecting: "Status: Connecting",
};

// The badge speaks the same liveness vocabulary as the sidebar and the
// provider cards: a solid dot and a plain word (DESIGN.md).
const BADGES: Record<Readiness, { dot: string; label: string }> = {
  ready: { dot: "bg-success", label: "healthy" },
  not_ready: { dot: "bg-error", label: "unreachable" },
  connecting: { dot: "bg-text-4", label: "connecting" },
};

// StatusHero is the gateway identity strip: readiness, origin, version,
// storage, uptime, and model count.
export function StatusHero() {
  // Each read narrows to the one fact the strip renders, so a refetch that
  // changes anything else leaves this component alone.
  const health = useQuery({
    ...queries.health(),
    select: (report) => report.status === "ok",
  });
  const keyUsable = useGatewayAccess();
  const info = useQuery({
    ...queries.systemInfo(),
    enabled: keyUsable,
    select: (report) => ({
      version: report.version,
      storage: report.storage?.type,
      uptime: report.uptime,
    }),
  });
  const models = useQuery({
    ...queries.models(),
    enabled: keyUsable,
    select: (records) => records.length,
  });

  const readiness: Readiness = health.isPending
    ? "connecting"
    : health.data === true
      ? "ready"
      : "not_ready";
  const badge = BADGES[readiness];
  const facts: string[] = [location.origin.replace(/^https?:\/\//, "")];
  if (info.data?.version) facts.push(`v${info.data.version}`);
  if (info.data?.storage) facts.push(`${info.data.storage} storage`);
  if (info.data?.uptime && info.data.uptime !== "unavailable") {
    facts.push(`up ${info.data.uptime}`);
  }
  if (models.data !== undefined) facts.push(`${models.data} models`);

  return (
    <div className="mb-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h1 className="text-xl font-semibold tracking-[-0.01em] text-text-1">
          {TITLES[readiness]}
        </h1>
        <span
          data-testid="gateway-readiness"
          data-readiness={readiness}
          className="flex shrink-0 items-center gap-1.5 text-xs text-text-2"
        >
          <span aria-hidden="true" className={`size-2 shrink-0 rounded-full ${badge.dot}`} />
          {badge.label}
        </span>
      </div>
      <div className="mt-1 flex flex-wrap gap-x-3 text-sm text-text-3">
        {facts.map((fact) => (
          <span key={fact}>{fact}</span>
        ))}
      </div>
    </div>
  );
}
