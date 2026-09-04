import { createFileRoute } from "@tanstack/react-router";

import { EndpointsCard } from "@/components/overview/EndpointsCard";
import { ProvidersCard } from "@/components/overview/ProvidersCard";
import { QuickstartCard } from "@/components/overview/QuickstartCard";
import { StatsRow } from "@/components/overview/StatsRow";
import { StatusHero } from "@/components/overview/StatusHero";
import { queries, settle } from "@/lib/queries";

export const Route = createFileRoute("/")({
  component: OverviewPage,
  loader: ({ context }) =>
    settle(
      context.queryClient.ensureQueryData(queries.health()),
      context.queryClient.ensureQueryData(queries.providerStatus()),
    ),
});

// Overview is mission control for the local gateway: identity, endpoints,
// quickstart, live metrics, and provider posture. The catalog is not a card
// here: the shell carries one catalog chip on every route, so a reader who
// wants the snapshot opens it from where the reader already is.
function OverviewPage() {
  return (
    <div className="flex flex-col gap-4">
      <StatusHero />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <EndpointsCard />
        <QuickstartCard />
      </div>
      <StatsRow />
      <ProvidersCard />
    </div>
  );
}
