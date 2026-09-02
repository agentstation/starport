import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";

import {
  accessMessage,
  ApiError,
  RECOGNITION_OPERATION,
  type ActivityRecord,
  type Model,
} from "@/lib/api";
import { queries } from "@/lib/queries";
import {
  formatMs,
  formatNanoUSD,
  formatPricePerK,
  formatRelativeTime,
} from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

import { TableSkeleton } from "@/components/ui/skeleton";

// Reading a document is the one provider call this gateway makes that the
// request never named. It runs before the model the caller asked for, it bills
// by the page rather than by the token, and the request cost alone reports the
// two together. An operator watching spend rise has no other place to learn
// that documents caused it.

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

// recognitionOfferings lists what this deployment can pay to read a document
// with, and what each one charges for a page. The list comes from the catalog,
// so a deployment whose providers serve no recognition says so rather than
// naming a model no request could reach.
type recognitionOffering = {
  model: string;
  provider: string;
  providerModelID: string;
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
  if (record.recognized_pages) {
    return (
      <span className="text-warning">
        unpriced — {record.cost_unavailable_reason ?? "unknown"}
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
            <th className="px-4 py-2.5">When</th>
            <th className="px-4 py-2.5">Model</th>
            <th className="px-4 py-2.5">Engine</th>
            <th className="px-4 py-2.5">Pages</th>
            <th className="px-4 py-2.5">Took</th>
            <th className="px-4 py-2.5">Cost</th>
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
                <td className="px-4 py-2 text-xs text-text-3">
                  {formatRelativeTime(record.timestamp)}
                </td>
                <td className="px-4 py-2 font-mono text-xs text-text-2">
                  {record.model_used ?? record.model_requested ?? "—"}
                </td>
                <td
                  data-testid="document-engine"
                  className="px-4 py-2 text-xs text-text-2"
                >
                  {record.parser_engine}
                </td>
                <td
                  data-testid="document-pages"
                  className="px-4 py-2 tabular-nums text-text-2"
                >
                  {reading.pages}
                  {reading.detail && (
                    <span className="ml-2 text-xs text-text-3">
                      {reading.detail}
                    </span>
                  )}
                </td>
                <td className="px-4 py-2 tabular-nums text-xs text-text-3">
                  {formatMs(record.extraction_millis)}
                </td>
                <td data-testid="document-cost" className="px-4 py-2 text-xs">
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
            <th className="px-4 py-2.5">Model</th>
            <th className="px-4 py-2.5">Provider</th>
            <th className="px-4 py-2.5">Provider model</th>
            <th className="px-4 py-2.5">Per 1K pages</th>
          </tr>
        </thead>
        <tbody>
          {offerings.map((offering) => (
            <tr
              key={`${offering.provider}/${offering.providerModelID}`}
              data-testid="recognition-row"
              className="border-b border-border-1 last:border-0"
            >
              <td className="px-4 py-2 font-mono text-xs text-text-2">
                {offering.model}
              </td>
              <td className="px-4 py-2 text-text-2">{offering.provider}</td>
              <td className="px-4 py-2 font-mono text-xs text-text-3">
                {offering.providerModelID}
              </td>
              <td className="px-4 py-2 font-mono tabular-nums text-xs text-text-2">
                {formatPricePerK(offering.pagePrice) !== null ? (
                  `${formatPricePerK(offering.pagePrice)} ${offering.currency ?? "USD"}`
                ) : (
                  <span className="text-warning">unpriced</span>
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
    body = <ExtractionRows records={rows} />;
  }

  return (
    <div className="flex flex-col gap-6">
      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-medium text-text-2">Document reads</h2>
        {body}
      </section>
      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-medium text-text-2">
          What a page costs here
        </h2>
        <p className="text-sm text-text-3">
          The engine that runs inside this gateway charges nothing. A page sent
          to one of these models is billed by the page, and the catalog
          publishes the price.
        </p>
        <RecognitionPrices offerings={recognitionOfferings(models.data ?? [])} />
      </section>
    </div>
  );
}
