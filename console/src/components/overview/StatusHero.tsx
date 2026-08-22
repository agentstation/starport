import { useQuery } from "@tanstack/react-query";

import { healthReady, listModels, systemInfo } from "@/lib/api";
import { useApiKeyUsable } from "@/lib/useApiKey";

// StatusHero is the gateway identity strip: readiness, origin, version,
// storage, uptime, and model count.
export function StatusHero() {
  const health = useQuery({
    queryKey: ["health"],
    queryFn: healthReady,
    refetchInterval: 30_000,
    retry: false,
  });
  const keyUsable = useApiKeyUsable();
  const info = useQuery({
    queryKey: ["system-info"],
    queryFn: systemInfo,
    enabled: keyUsable,
    retry: false,
  });
  const models = useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    enabled: keyUsable,
    retry: false,
  });

  const ready = health.data?.status === "ok";
  const facts: string[] = [location.origin.replace(/^https?:\/\//, "")];
  if (info.data?.version) facts.push(`v${info.data.version}`);
  if (info.data?.storage?.type) facts.push(`${info.data.storage.type} storage`);
  if (info.data?.uptime && info.data.uptime !== "unavailable") {
    facts.push(`up ${info.data.uptime}`);
  }
  if (models.data) facts.push(`${models.data.length} models`);

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
