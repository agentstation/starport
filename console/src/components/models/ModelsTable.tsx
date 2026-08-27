import {
  createColumnHelper,
  createSortedRowModel,
  rowSortingFeature,
  sortFn_alphanumeric,
  sortFn_basic,
  tableFeatures,
  useTable,
} from "@tanstack/react-table";
import { Link, useNavigate } from "@tanstack/react-router";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { ArrowDown, ArrowUp } from "lucide-react";
import { useRef, type ReactNode } from "react";

import { CopyButton } from "@/components/ui/CopyButton";
import type { Model } from "@/lib/api";
import { formatContext, formatPricePerM } from "@/lib/format";

const ROW_HEIGHT = 40;
const GRID = "grid grid-cols-[minmax(320px,1fr)_220px_90px_170px] items-center";

const features = tableFeatures({
  rowSortingFeature,
  sortedRowModel: createSortedRowModel(),
  sortFns: { alphanumeric: sortFn_alphanumeric, basic: sortFn_basic },
});

const helper = createColumnHelper<typeof features, Model>();

function CapabilityBadge({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex h-5 items-center rounded-xs bg-bg-raised px-1.5 text-xs text-text-3">
      {children}
    </span>
  );
}

// capabilityLabels turns modalities and supported parameters into the
// labeled badges DESIGN.md requires instead of the legacy unlabeled icons.
//
// The modality halves read the catalog rather than a list written here. A
// fixed list drops whatever the catalog learns next: the one it replaced
// named three kinds and already omitted video, so a video model read as a
// text model. Text stays out of both halves, because every model handles it
// and a badge every row carries says nothing.
function capabilityLabels(model: Model): string[] {
  const labels: string[] = [];
  const architecture = model.architecture;
  for (const modality of architecture?.input_modalities ?? []) {
    if (modality !== "text") labels.push(modality);
  }
  for (const modality of architecture?.output_modalities ?? []) {
    if (modality !== "text") labels.push(`${modality} out`);
  }
  const params = model.supported_parameters ?? [];
  if (params.includes("tools")) labels.push("tools");
  if (params.includes("reasoning") || params.includes("include_reasoning")) {
    labels.push("reasoning");
  }
  if (params.includes("structured_outputs")) labels.push("structured");
  return labels;
}

// Unknown prices and contexts return undefined so sortUndefined: "last"
// keeps them at the bottom in both sort directions.
function promptPriceNumber(model: Model): number | undefined {
  const parsed = Number.parseFloat(model.pricing?.prompt ?? "");
  return Number.isFinite(parsed) ? parsed : undefined;
}

const columns = helper.columns([
  helper.accessor("id", {
    id: "model",
    header: "model",
    sortFn: "alphanumeric",
  }),
  helper.display({ id: "capabilities", header: "capabilities" }),
  helper.accessor((row) => row.context_length ?? undefined, {
    id: "context",
    header: "context",
    sortFn: "basic",
    sortUndefined: "last",
  }),
  helper.accessor(promptPriceNumber, {
    id: "price",
    header: "price / 1M",
    sortFn: "basic",
    sortUndefined: "last",
  }),
]);

function ModelCell({ model }: { model: Model }) {
  return (
    <div className="group flex min-w-0 items-center gap-2">
      <Link
        to="/models/$modelId"
        params={{ modelId: model.id }}
        onClick={(event) => event.stopPropagation()}
        className="flex min-w-0 items-center rounded-xs border border-border-1 bg-bg-raised px-1.5 py-0.5 transition-colors duration-150 ease-standard hover:border-border-2"
      >
        <span className="truncate font-mono text-xs text-text-1">{model.id}</span>
      </Link>
      <span className="opacity-0 transition-opacity duration-150 group-hover:opacity-100">
        <CopyButton text={model.id} />
      </span>
      {model.name && model.name !== model.id && (
        <span className="hidden min-w-0 truncate text-xs text-text-4 lg:inline">
          {model.name}
        </span>
      )}
    </div>
  );
}

function PriceCell({ model }: { model: Model }) {
  const prompt = formatPricePerM(model.pricing?.prompt);
  const completion = formatPricePerM(model.pricing?.completion);
  if (prompt === null && completion === null) {
    return <span className="text-text-4">—</span>;
  }
  return (
    <span className="font-mono text-xs tabular-nums text-text-2">
      {prompt ?? "—"} in · {completion ?? "—"} out
    </span>
  );
}

// ModelsTable renders the filtered catalog: TanStack Table owns column
// order and sorting, TanStack Virtual keeps 400+ rows cheap through the
// window scroller, and the header stays sticky above the virtual list.
export function ModelsTable({ models }: { models: Model[] }) {
  const navigate = useNavigate();
  const table = useTable({
    features,
    columns,
    data: models,
    getRowId: (model) => model.id,
    initialState: { sorting: [{ id: "model", desc: false }] },
  });

  const rows = table.getRowModel().rows;
  const listRef = useRef<HTMLDivElement>(null);
  const virtualizer = useWindowVirtualizer({
    count: rows.length,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
    scrollMargin: listRef.current?.offsetTop ?? 0,
  });

  return (
    <div role="table" aria-rowcount={rows.length} className="text-sm">
      <div role="rowgroup" className="sticky top-0 z-10 bg-bg-canvas">
        {table.getHeaderGroups().map((headerGroup) => (
          <div
            role="row"
            key={headerGroup.id}
            className={`${GRID} h-8 border-b border-border-1`}
          >
            {headerGroup.headers.map((header) => {
              const sorted = header.column.getIsSorted();
              const numeric = header.column.id === "context" || header.column.id === "price";
              return (
                <div
                  role="columnheader"
                  key={header.id}
                  aria-sort={
                    sorted === "asc"
                      ? "ascending"
                      : sorted === "desc"
                        ? "descending"
                        : undefined
                  }
                  className={numeric ? "text-right" : ""}
                >
                  <button
                    type="button"
                    onClick={header.column.getToggleSortingHandler()}
                    className={`group inline-flex h-8 items-center gap-1 px-2.5 text-xs font-medium text-text-3 transition-colors duration-150 ease-standard hover:text-text-1 ${
                      numeric ? "flex-row-reverse" : ""
                    }`}
                  >
                    {typeof header.column.columnDef.header === "string"
                      ? header.column.columnDef.header
                      : header.column.id}
                    {sorted === "asc" && <ArrowUp className="size-3" />}
                    {sorted === "desc" && <ArrowDown className="size-3" />}
                    {!sorted && (
                      <ArrowUp className="size-3 opacity-0 transition-opacity group-hover:opacity-40" />
                    )}
                  </button>
                </div>
              );
            })}
          </div>
        ))}
      </div>
      <div
        ref={listRef}
        role="rowgroup"
        className="relative"
        style={{ height: virtualizer.getTotalSize() }}
      >
        {virtualizer.getVirtualItems().map((item) => {
          const row = rows[item.index];
          if (!row) return null;
          const model = row.original;
          return (
            <div
              role="row"
              key={row.id}
              onClick={(event) => {
                // Inner links and the copy button own their own clicks.
                const target = event.target as HTMLElement | null;
                if (target?.closest("a,button")) return;
                void navigate({
                  to: "/models/$modelId",
                  params: { modelId: model.id },
                });
              }}
              className={`${GRID} absolute left-0 top-0 w-full cursor-pointer border-b border-border-1 transition-colors duration-150 ease-standard hover:bg-bg-hover`}
              style={{
                height: item.size,
                transform: `translateY(${item.start - virtualizer.options.scrollMargin}px)`,
              }}
            >
              <div role="cell" className="min-w-0 px-2.5">
                <ModelCell model={model} />
              </div>
              <div role="cell" className="flex gap-1 overflow-hidden px-2.5">
                {capabilityLabels(model).map((label) => (
                  <CapabilityBadge key={label}>{label}</CapabilityBadge>
                ))}
              </div>
              <div
                role="cell"
                className="px-2.5 text-right font-mono text-xs tabular-nums text-text-2"
              >
                {formatContext(model.context_length)}
              </div>
              <div role="cell" className="px-2.5 text-right">
                <PriceCell model={model} />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
