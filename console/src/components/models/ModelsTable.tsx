import {
  columnResizingFeature,
  columnSizingFeature,
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
import { formatContext, formatPricePair } from "@/lib/format";
import { operationsOf } from "@/lib/modelFilter";

const ROW_HEIGHT = 40;

const features = tableFeatures({
  rowSortingFeature,
  columnSizingFeature,
  columnResizingFeature,
  sortedRowModel: createSortedRowModel(),
  sortFns: { alphanumeric: sortFn_alphanumeric, basic: sortFn_basic },
});

const helper = createColumnHelper<typeof features, Model>();

function CapabilityBadge({
  children,
  title,
}: {
  children: ReactNode;
  title?: string;
}) {
  return (
    <span
      title={title}
      className="inline-flex h-5 shrink-0 items-center rounded-xs bg-bg-raised px-1.5 text-xs text-text-3"
    >
      {children}
    </span>
  );
}

// BadgeList caps a badge row at a count and folds the rest into "+N"
// (full list in the title tooltip). The previous overflow-hidden clip
// cut badges mid-word, which read as a label that does not exist.
function BadgeList({ labels, max }: { labels: string[]; max: number }) {
  const shown = labels.slice(0, max);
  const extra = labels.length - shown.length;
  return (
    <>
      {shown.map((label) => (
        <CapabilityBadge key={label}>{label}</CapabilityBadge>
      ))}
      {extra > 0 && (
        <CapabilityBadge title={labels.slice(max).join(", ")}>
          +{extra}
        </CapabilityBadge>
      )}
    </>
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

// operationLabel reads the catalog's own operation name and changes only its
// separator. Shortening it to the last word would render images-generations
// and videos-generations as one label, and mapping the known names through a
// table would drop whatever the catalog learns next. An operation this console
// has never seen renders under its catalog name, because a badge missing from
// the row would read as a model that serves nothing.
export function operationLabel(operation: string): string {
  return operation.replaceAll("-", " ");
}

// Unknown prices and contexts return undefined so sortUndefined: "last"
// keeps them at the bottom in both sort directions.
function promptPriceNumber(model: Model): number | undefined {
  const parsed = Number.parseFloat(model.pricing?.prompt ?? "");
  return Number.isFinite(parsed) ? parsed : undefined;
}

const columns = helper.columns([
  helper.accessor((row) => row.name ?? row.id, {
    id: "model",
    header: "Model",
    sortFn: "alphanumeric",
    size: 260,
    minSize: 160,
  }),
  // The routable id is its own column so every id starts at the same
  // x-position; trailing it after the name left the ids ragged and
  // unscannable. "ID" is the console-wide name for this string — the
  // field both API surfaces return and callers copy into `model` —
  // never "slug", which is OpenRouter doc vocabulary for the same
  // thing (their wire field is also `id`).
  helper.accessor((row) => row.id, {
    id: "id",
    header: "ID",
    sortFn: "alphanumeric",
    size: 260,
    minSize: 160,
  }),
  helper.display({
    id: "capabilities",
    header: "Capabilities",
    size: 190,
    minSize: 120,
  }),
  // Sorting by the joined operation list groups the models that serve the
  // same set, which is the order a reader scanning for one of them wants.
  helper.accessor((row) => operationsOf(row).join(" "), {
    id: "operations",
    header: "Operations",
    sortFn: "alphanumeric",
    size: 190,
    minSize: 120,
  }),
  helper.accessor((row) => row.context_length ?? undefined, {
    id: "context",
    header: "Context",
    sortFn: "basic",
    sortUndefined: "last",
    size: 90,
    minSize: 70,
  }),
  helper.accessor(promptPriceNumber, {
    id: "price",
    header: "Price / 1M",
    sortFn: "basic",
    sortUndefined: "last",
    size: 180,
    minSize: 120,
  }),
]);

// ModelCell holds only the display name (what a reader scans for); the
// routable id lives in its own aligned column. An unnamed model shows
// its id here in mono, because a blank cell reads as a missing model.
function ModelCell({ model }: { model: Model }) {
  const named = Boolean(model.name) && model.name !== model.id;
  return (
    <Link
      to="/models/$modelId"
      params={{ modelId: model.id }}
      onClick={(event) => event.stopPropagation()}
      className={`block min-w-0 truncate ${
        named ? "text-sm text-text-1" : "font-mono text-xs text-text-1"
      }`}
    >
      {named ? model.name : model.id}
    </Link>
  );
}

// IdCell renders the routable id in dim mono with a copy affordance
// that appears on row hover.
function IdCell({ model }: { model: Model }) {
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <span className="min-w-0 truncate font-mono text-xs text-text-4">
        {model.id}
      </span>
      <span className="shrink-0 opacity-0 transition-opacity duration-150 group-hover:opacity-100">
        <CopyButton text={model.id} />
      </span>
    </div>
  );
}

function PriceCell({ model }: { model: Model }) {
  const pair = formatPricePair(model.pricing?.prompt, model.pricing?.completion);
  if (pair === null) {
    return <span className="text-text-4">—</span>;
  }
  return (
    <span className="font-mono text-xs tabular-nums text-text-2">{pair}</span>
  );
}

// ModelsTable renders the filtered catalog: TanStack Table owns column
// order, sorting, and sizing; TanStack Virtual keeps 400+ rows cheap
// through the window scroller; the header stays sticky above the list.
export function ModelsTable({ models }: { models: Model[] }) {
  const navigate = useNavigate();
  const table = useTable({
    features,
    columns,
    data: models,
    getRowId: (model) => model.id,
    columnResizeMode: "onChange",
    initialState: { sorting: [{ id: "model", desc: false }] },
  });

  // The first column flexes to fill the viewport; the rest hold their
  // dragged size. One template string serves the header and every row.
  const template = table
    .getAllLeafColumns()
    .map((column, index) =>
      index === 0 ? `minmax(${column.getSize()}px, 1fr)` : `${column.getSize()}px`,
    )
    .join(" ");

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
            className="grid h-8 items-center border-b border-border-1"
            style={{ gridTemplateColumns: template }}
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
                  className={`relative ${numeric ? "text-right" : ""}`}
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
                  {header.column.getCanResize() && (
                    <div
                      role="separator"
                      aria-orientation="vertical"
                      aria-label={`Resize ${header.column.id} column`}
                      onMouseDown={header.getResizeHandler()}
                      onTouchStart={header.getResizeHandler()}
                      onDoubleClick={() => header.column.resetSize()}
                      className={`absolute -right-0.5 top-1/2 z-10 h-5 w-1 -translate-y-1/2 cursor-col-resize rounded-full transition-colors duration-150 ${
                        header.column.getIsResizing()
                          ? "bg-accent"
                          : "hover:bg-border-3"
                      }`}
                    />
                  )}
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
              className="group absolute left-0 top-0 grid w-full cursor-pointer items-center border-b border-border-1 transition-colors duration-150 ease-standard hover:bg-bg-hover"
              style={{
                gridTemplateColumns: template,
                height: item.size,
                transform: `translateY(${item.start - virtualizer.options.scrollMargin}px)`,
              }}
            >
              <div role="cell" className="min-w-0 px-2.5">
                <ModelCell model={model} />
              </div>
              <div role="cell" className="min-w-0 px-2.5">
                <IdCell model={model} />
              </div>
              <div role="cell" className="flex gap-1 overflow-hidden px-2.5">
                <BadgeList labels={capabilityLabels(model)} max={3} />
              </div>
              <div role="cell" className="flex gap-1 overflow-hidden px-2.5">
                <BadgeList
                  labels={operationsOf(model).map(operationLabel)}
                  max={2}
                />
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
