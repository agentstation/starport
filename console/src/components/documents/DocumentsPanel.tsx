import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";

import {
  accessMessage,
  ApiError,
  RECOGNITION_OPERATION,
  type ActivityRecord,
  type Model,
} from "@/lib/api";
import { DOCUMENT_ACTIVITY_LIMIT, queries } from "@/lib/queries";
import {
  formatMs,
  formatNanoUSD,
} from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

import { DataTableFooter } from "@/components/ui/DataTable";
import { TableSkeleton } from "@/components/ui/skeleton";
import { RelativeTime } from "@/components/ui/RelativeTime";

// Recognition charges use the provider's declared page or token units.

function perThousand(value: string | undefined): string | null {
  if (value === undefined || value.trim() === "") return null;
  const amount = Number(value) * 1000;
  return Number.isFinite(amount) && amount >= 0
    ? amount.toLocaleString("en-US", { maximumFractionDigits: 9 }) : null;
}

// extractions keeps the turns that attached a document. The engine name is the
// marker: the gateway writes it only when it read something, so a record
// without one is an ordinary chat turn rather than a document turn of no pages.
function extractions(records: ActivityRecord[]): ActivityRecord[] {
  return records.filter((record) => Boolean(record.parser_engine));
}

// pageReading describes one turn's document read in the terms a reader asks
// about: which engine, how many pages, and what it cost.
type pageReading = {
  cached: boolean;
  pages: number;
  detail: string;
};

// readingOf derives the page breakdown from the counts the record carries. The
// engine name is never consulted, because the gateway may add engines this
// console has not heard of and the counts stay true for all of them.
function readingOf(record: ActivityRecord): pageReading {
  const pages = record.document_pages ?? 0;
  const recognized = record.recognized_pages ?? 0;
  const native = record.native_pages ?? 0;
  const parts: string[] = [];
  if (recognized) parts.push(`${recognized} recognized`);
  if (native) parts.push(`${native} read in process`);
  return {
    cached: record.extraction_cached === true,
    pages,
    detail: parts.join(" · "),
  };
}

// Recognition offerings report actual billing units and optional input estimates.
type recognitionOffering = {
  model: string;
  provider: string;
  providerModelID: string;
  basis?: string;
  prompt?: string;
  completion?: string;
  estimate?: { tokens: number; source: string; assumptions: string };
  pagePrice?: string;
  currency?: string;
};

function recognitionOfferings(models: Model[]): recognitionOffering[] {
  const offerings: recognitionOffering[] = [];
  for (const model of models) {
    for (const offering of model.offerings ?? []) {
      if (!(offering.operations ?? []).includes(RECOGNITION_OPERATION)) continue;
      offerings.push({
        model: model.id,
        provider: offering.provider,
        providerModelID: offering.provider_model_id,
        basis: offering.billing?.recognition?.basis,
        prompt: offering.pricing?.prompt,
        completion: offering.pricing?.completion,
        estimate: offering.billing?.recognition?.input_page_estimate,
        pagePrice: offering.pricing?.page_input,
        currency: offering.pricing?.currency,
      });
    }
  }
  return offerings;
}

// CostCell answers the one question the console exists to answer here: what
// this document read cost. The four answers are unlike each other, and a cell
// that showed a bare zero for three of them would tell a reader a paid page
// was free.
function CostCell({ record }: { record: ActivityRecord }) {
  const reading = readingOf(record);
  if (reading.cached) {
    return (
      <span data-testid="document-cached" className="text-success">
        cached — no charge
      </span>
    );
  }
  if (record.extraction_cost) {
    return (
      <span className="tabular-nums text-text-2">
        {formatNanoUSD(record.extraction_cost.nano_usd)}{" "}
        {record.extraction_cost.currency ?? "USD"}
      </span>
    );
  }
  if (record.recognized_pages || record.extractions?.length) {
    return (
      <span className="text-warning">
        unpriced — {record.extractions?.find((entry) => !entry.cost)?.cost_unavailable_reason ?? record.cost_unavailable_reason ?? "unknown"}
      </span>
    );
  }
  return <span className="text-text-3">free — read in process</span>;
}

function ExtractionRows({ records }: { records: ActivityRecord[] }) {
  return (
    <div className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel">
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
            <th scope="col" className="px-4 py-2.5">When</th>
            <th scope="col" className="px-4 py-2.5">Model</th>
            <th scope="col" className="px-4 py-2.5">Engine</th>
            <th scope="col" className="px-4 py-2.5 text-right">Pages</th>
            <th scope="col" className="px-4 py-2.5 text-right">Took</th>
            <th scope="col" className="px-4 py-2.5">Cost</th>
          </tr>
        </thead>
        <tbody>
          {records.map((record) => {
            const reading = readingOf(record);
            return (
              <tr
                key={record.request_id ?? record.timestamp}
                data-testid="document-row"
                className="border-b border-border-1 last:border-0"
              >
                <td className="px-4 py-2.5 text-xs text-text-3">
                  <RelativeTime iso={record.timestamp} />
                </td>
                <td className="px-4 py-2.5 font-mono text-xs text-text-2">
                  {record.model_used ?? record.model_requested ?? "—"}
                </td>
                <td
                  data-testid="document-engine"
                  className="px-4 py-2.5 text-xs text-text-2"
                >
                  {record.parser_engine}
                </td>
                <td
                  data-testid="document-pages"
                  className="px-4 py-2.5 text-right tabular-nums text-text-2"
                >
                  {reading.pages}
                  {reading.detail && (
                    <span className="ml-2 text-xs text-text-3">
                      {reading.detail}
                    </span>
                  )}
                </td>
                <td className="px-4 py-2.5 text-right tabular-nums text-xs text-text-3">
                  {formatMs(record.extraction_millis)}
                </td>
                <td data-testid="document-cost" className="px-4 py-2.5 text-xs">
                  <CostCell record={record} />
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function RecognitionPrices({ offerings }: { offerings: recognitionOffering[] }) {
  if (offerings.length === 0) {
    return (
      <p data-testid="recognition-models" className="text-sm text-text-3">
        No provider in this catalog reads documents. Every attachment reaches
        the engine that runs inside this gateway.
      </p>
    );
  }
  return (
    <div
      data-testid="recognition-models"
      className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel"
    >
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
            <th scope="col" className="px-4 py-2.5">Model</th>
            <th scope="col" className="px-4 py-2.5">Provider</th>
            <th scope="col" className="px-4 py-2.5">Provider model</th>
            <th scope="col" className="px-4 py-2.5 text-right">Billing</th>
          </tr>
        </thead>
        <tbody>
          {offerings.map((offering) => (
            <tr
              key={`${offering.provider}/${offering.providerModelID}`}
              data-testid="recognition-row"
              className="border-b border-border-1 last:border-0"
            >
              <td className="px-4 py-2.5 font-mono text-xs text-text-2">
                {offering.model}
              </td>
              <td className="px-4 py-2.5 text-text-2">{offering.provider}</td>
              <td className="px-4 py-2.5 font-mono text-xs text-text-3">
                {offering.providerModelID}
              </td>
              <td className="px-4 py-2.5 text-right font-mono tabular-nums text-xs text-text-2">
                {offering.basis === "pages" && perThousand(offering.pagePrice) !== null ? (
                  `${perThousand(offering.pagePrice)} ${offering.currency ?? "unknown currency"} / 1K pages`
                ) : offering.basis === "tokens" ? (
                  <>
                    <div>Token billing · {offering.currency ?? "unknown currency"}</div>
                    <div>Input: {perThousand(offering.prompt) ?? "unknown"} / 1K tokens</div>
                    <div>Output: {perThousand(offering.completion) ?? "unknown"} / 1K tokens</div>
                    {offering.estimate && (
                      <div className="font-sans text-text-3">
                        Input estimate: {offering.estimate.tokens} tokens / page; excludes output.
                        {" "}{offering.estimate.assumptions} Source: {offering.estimate.source}
                      </div>
                    )}
                  </>
                ) : (
                  <span className="text-warning">unpriced — billing basis or rate unknown</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// DocumentsPanel shows what this gateway read and what reading it cost.
export function DocumentsPanel() {
  const enabled = useGatewayAccess();
  const activity = useQuery({
    ...queries.documentActivity(),
    enabled,
  });
  const models = useQuery({
    ...queries.models(),
    enabled,
  });

  const rows = extractions(activity.data ?? []);

  let body: ReactNode;
  if (!enabled) {
    body = (
      <p className="text-base text-text-3">
        Connect this console to the gateway to read its document activity.
      </p>
    );
  } else if (activity.error) {
    body = (
      <p className="text-base text-text-3">
        {activity.error instanceof ApiError && activity.error.needsKey
          ? accessMessage(activity.error, "activity:read")
          : `Failed to load activity: ${(activity.error as Error).message}`}
      </p>
    );
  } else if (activity.isPending) {
    body = <TableSkeleton columns={6} />;
  } else if (rows.length === 0) {
    body = (
      <p className="text-base text-text-3">
        No request in this window attached a document. A chat request carries
        one by naming the file-parser plugin.
      </p>
    );
  } else {
    body = (
      <div className="flex flex-col gap-3">
        <ExtractionRows records={rows} />
        <DataTableFooter
          loaded={rows.length}
          unit={{ one: "read", other: "reads" }}
          bound={DOCUMENT_ACTIVITY_LIMIT}
          hasMore={(activity.data ?? []).length >= DOCUMENT_ACTIVITY_LIMIT}
        />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-medium text-text-2">Document reads</h2>
        {body}
      </section>
      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-medium text-text-2">
          Recognition billing
        </h2>
        <p className="text-sm text-text-3">
          The local engine has no provider charge. Provider recognition uses the
          catalog’s declared page or token billing units. Input estimates exclude
          output and never determine the billed amount.
        </p>
        <RecognitionPrices offerings={recognitionOfferings(models.data ?? [])} />
      </section>
    </div>
  );
}
