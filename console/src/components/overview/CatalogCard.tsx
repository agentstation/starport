import { useQuery } from "@tanstack/react-query";

import { Card, CardTitle } from "@/components/ui/Card";
import { LoadFailed } from "@/components/ui/LoadFailed";
import { CardSkeleton } from "@/components/ui/skeleton";
import { ApiError } from "@/lib/api";
import { queries } from "@/lib/queries";
import { formatRelativeTime, shortGenerationID } from "@/lib/format";

// CatalogCard shows Starmap catalog freshness with its two counters named
// distinctly: the catalog sequence counts accepted generations, and the
// availability revision counts provider availability flips.
export function CatalogCard() {
  const metadata = useQuery({
    ...queries.catalogMetadata(),
  });

  if (metadata.isPending) return <CardSkeleton lines={4} />;
  if (metadata.error) {
    if (metadata.error instanceof ApiError && metadata.error.needsKey) {
      return (
        <Card>
          <CardTitle>Starmap catalog</CardTitle>
          <p className="text-base text-text-3">
            Catalog freshness needs a key with the models:read scope.
          </p>
        </Card>
      );
    }
    return (
      <LoadFailed
        what="the Starmap catalog"
        error={metadata.error}
        onRetry={() => void metadata.refetch()}
      />
    );
  }

  const data = metadata.data ?? {};
  const rows: Array<[string, string, string | undefined]> = [
    ["Generation", shortGenerationID(data.generation_id), data.generation_id],
    [
      "Generated",
      data.generated_at ? formatRelativeTime(data.generated_at) : "—",
      data.generated_at,
    ],
    ["Catalog sequence", String(data.catalog_sequence ?? "—"), undefined],
    ["Availability revision", String(data.availability_revision ?? "—"), undefined],
  ];
  return (
    <Card>
      <CardTitle>Starmap catalog</CardTitle>
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2">
        {rows.map(([label, value, title]) => (
          <div key={label} className="col-span-2 grid grid-cols-subgrid">
            <dt className="text-sm text-text-3">{label}</dt>
            <dd className="font-mono text-sm tabular-nums text-text-2" title={title}>
              {value}
            </dd>
          </div>
        ))}
      </dl>
    </Card>
  );
}
