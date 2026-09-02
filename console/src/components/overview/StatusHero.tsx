import { useQuery } from "@tanstack/react-query";

import { queries } from "@/lib/queries";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

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

  const ready = health.data === true;
  const facts: string[] = [location.origin.replace(/^https?:\/\//, "")];
  if (info.data?.version) facts.push(`v${info.data.version}`);
  if (info.data?.storage) facts.push(`${info.data.storage} storage`);
  if (info.data?.uptime && info.data.uptime !== "unavailable") {
    facts.push(`up ${info.data.uptime}`);
  }
  if (models.data !== undefined) facts.push(`${models.data} models`);

  return (
    <div className="mb-6 flex items-center gap-3">
      <span
        aria-hidden="true"
        className={`size-2.5 rounded-full ${
          health.isPending ? "bg-text-4" : ready ? "bg-success" : "bg-error"
        }`}
      />
      <div>
        <div className="text-lg font-semibold text-text-1">
          {health.isPending
            ? "Gateway"
            : ready
              ? "Gateway ready"
              : "Gateway not ready"}
        </div>
        <div className="flex flex-wrap gap-x-3 text-sm text-text-3">
          {facts.map((fact) => (
            <span key={fact}>{fact}</span>
          ))}
        </div>
      </div>
    </div>
  );
}
