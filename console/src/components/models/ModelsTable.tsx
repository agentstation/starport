import { Link, useNavigate } from "@tanstack/react-router";
import { type ReactNode } from "react";

import { CopyButton } from "@/components/ui/CopyButton";
import { DataTable, dataColumns } from "@/components/ui/DataTable";
import type { Model } from "@/lib/api";
import { formatContext, formatPricePair } from "@/lib/format";
import { operationsOf } from "@/lib/modelFilter";

const helper = dataColumns<Model>();

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

// Default widths sum to 1,100px so the table fits a 1,440px viewport beside
// the sidebar without a horizontal scrollbar; a reader drags a column wider.
const columns = helper.columns([
  helper.accessor((row) => row.name ?? row.id, {
    id: "model",
    header: "Model",
    sortFn: "alphanumeric",
    size: 240,
    minSize: 160,
    meta: { flex: true },
    cell: ({ row }) => <ModelCell model={row.original} />,
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
    size: 210,
    minSize: 160,
    cell: ({ row }) => <IdCell model={row.original} />,
  }),
  helper.display({
    id: "capabilities",
    header: "Capabilities",
    size: 170,
    minSize: 120,
    cell: ({ row }) => (
      <div className="flex gap-1 overflow-hidden">
        <BadgeList labels={capabilityLabels(row.original)} max={3} />
      </div>
    ),
  }),
  // Sorting by the joined operation list groups the models that serve the
  // same set, which is the order a reader scanning for one of them wants.
  helper.accessor((row) => operationsOf(row).join(" "), {
    id: "operations",
    header: "Operations",
    sortFn: "alphanumeric",
    size: 170,
    minSize: 120,
    cell: ({ row }) => (
      <div className="flex gap-1 overflow-hidden">
        <BadgeList labels={operationsOf(row.original).map(operationLabel)} max={2} />
      </div>
    ),
  }),
  helper.accessor((row) => row.context_length ?? undefined, {
    id: "context",
    header: "Context",
    sortFn: "basic",
    sortUndefined: "last",
    size: 90,
    minSize: 80,
    meta: { align: "end" },
    cell: ({ row }) => (
      <span className="font-mono text-xs tabular-nums text-text-2">
        {formatContext(row.original.context_length)}
      </span>
    ),
  }),
  helper.accessor(promptPriceNumber, {
    id: "price",
    header: "Price / 1M",
    sortFn: "basic",
    sortUndefined: "last",
    size: 220,
    minSize: 120,
    meta: { align: "end" },
    cell: ({ row }) => <PriceCell model={row.original} />,
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
      <span className="min-w-0 truncate font-mono text-xs text-text-3">
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
    return <span className="text-text-3">—</span>;
  }
  return (
    <span className="whitespace-nowrap font-mono text-xs tabular-nums text-text-2">
      {pair}
    </span>
  );
}

// ModelsTable renders the filtered catalog through the shared DataTable:
// the column definitions above own every cell, the table owns sorting,
// dragged sizes, virtualization, and the keyboard path, and a row opens
// the model detail page.
export function ModelsTable({ models }: { models: Model[] }) {
  const navigate = useNavigate();
  return (
    <DataTable
      aria-label="Models"
      columns={columns}
      data={models}
      getRowId={(model) => model.id}
      initialSorting={[{ id: "model", desc: false }]}
      resizable
      onRowActivate={(model) =>
        void navigate({ to: "/models/$modelId", params: { modelId: model.id } })
      }
    />
  );
}
