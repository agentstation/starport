import { createFileRoute } from "@tanstack/react-router";

import { CatalogCard } from "@/components/overview/CatalogCard";
import { EndpointsCard } from "@/components/overview/EndpointsCard";
import { ProvidersCard } from "@/components/overview/ProvidersCard";
import { QuickstartCard } from "@/components/overview/QuickstartCard";
import { StatsRow } from "@/components/overview/StatsRow";
import { StatusHero } from "@/components/overview/StatusHero";

export const Route = createFileRoute("/")({
  component: OverviewPage,
});

// Overview is mission control for the local gateway: identity, endpoints,
// quickstart, live metrics, provider posture, and the Starmap snapshot.
function OverviewPage() {
  return (
    <div className="flex flex-col gap-4">
      <StatusHero />
      <div className="grid gap-4 lg:grid-cols-2">
        <EndpointsCard />
        <QuickstartCard />
      </div>
      <StatsRow />
      <div className="grid gap-4 lg:grid-cols-2">
        <ProvidersCard />
        <CatalogCard />
      </div>
    </div>
  );
}
