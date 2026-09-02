import {
  columnResizingFeature,
  columnSizingFeature,
  createColumnHelper,
  createSortedRowModel,
  flexRender,
  rowSelectionFeature,
  rowSortingFeature,
  sortFn_alphanumeric,
  sortFn_basic,
  tableFeatures,
  useTable,
  type CellData,
  type ColumnDef,
  type Row,
  type RowData,
  type RowSelectionState,
  type SortingState,
  type TableFeatures,
  type TableMeta,
} from "@tanstack/react-table";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { ArrowDown, ArrowUp } from "lucide-react";
import {
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type MouseEvent,
} from "react";

import { Button } from "@/components/ui/button";
import { formatCount } from "@/lib/format";
import { cn } from "@/lib/utils";

// DataTable is the console's one dense table (DESIGN.md: 40px rows, 12px
// sentence-case headers, sortable columns that show their direction, drag
// resizing on catalog tables, virtualization above a hundred rows). TanStack
// Table owns column order, sorting, sizing, and selection; TanStack Virtual
// keeps a long list cheap through the window scroller; this component owns
// the ARIA grid, the sticky header, the keyboard paths, and the footer that
// states how many rows the reader holds against the request bound.
//
// Every consumer declares its columns with dataColumns() and renders each
// cell through flexRender, so a column definition is the whole contract
// between a page and its table: no consumer positions a cell by hand.
//
// Declare columns once at module level. flexRender mounts a cell function
// as a component, so a column built inside a render remounts every cell
// on each render and drops focus from a control inside the table. A page
// hands its callbacks and lookups to the cells through the meta option and
// reads them back from table.options.meta.

declare module "@tanstack/react-table" {
  interface ColumnMeta<
    in out TFeatures extends TableFeatures,
    in out TData extends RowData,
    TValue extends CellData = CellData,
  > {
    // align "end" right-aligns a numeric column and its header.
    align?: "end";
    // flex marks the column that absorbs spare width. Without one, the
    // first column after any selection column flexes.
    flex?: boolean;
    // className is added to every body cell of the column.
    className?: string;
  }
}

export const dataTableFeatures = tableFeatures({
  rowSortingFeature,
  columnSizingFeature,
  columnResizingFeature,
  rowSelectionFeature,
  sortedRowModel: createSortedRowModel(),
  sortFns: { alphanumeric: sortFn_alphanumeric, basic: sortFn_basic },
});

export type DataTableFeatures = typeof dataTableFeatures;

// The value type is any because a table mixes accessor types.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type DataColumn<TData extends RowData> = ColumnDef<DataTableFeatures, TData, any>;

export function dataColumns<TData extends RowData>() {
  return createColumnHelper<DataTableFeatures, TData>();
}

const ROW_HEIGHT = 40;
// Above this count the body renders only the rows near the viewport.
export const VIRTUAL_THRESHOLD = 100;

const SELECT_COLUMN_ID = "select";

// selectionColumn is the leading checkbox column a table adds when its rows
// can be selected together. The header checkbox reads indeterminate while
// only some rows are selected.
export function selectionColumn<TData extends RowData>(): DataColumn<TData> {
  return {
    id: SELECT_COLUMN_ID,
    size: 40,
    minSize: 40,
    maxSize: 40,
    enableSorting: false,
    enableResizing: false,
    header: ({ table }) => (
      <input
        type="checkbox"
        aria-label="Select all rows"
        checked={table.getIsAllRowsSelected()}
        ref={(element) => {
          if (element) {
            element.indeterminate =
              !table.getIsAllRowsSelected() && table.getIsSomeRowsSelected();
          }
        }}
        onChange={table.getToggleAllRowsSelectedHandler()}
        className="accent-accent"
      />
    ),
    cell: ({ row }) => (
      <input
        type="checkbox"
        aria-label="Select row"
        checked={row.getIsSelected()}
        disabled={!row.getCanSelect()}
        onChange={row.getToggleSelectedHandler()}
        className="accent-accent"
      />
    ),
  };
}

export type DataTableProps<TData extends RowData> = {
  columns: DataColumn<TData>[];
  data: TData[];
  getRowId: (row: TData, index: number) => string;
  initialSorting?: SortingState;
  // resizable turns on drag handles between headers (catalog tables).
  resizable?: boolean;
  // onRowActivate makes every row a click and keyboard target. Links,
  // buttons, and inputs inside the row keep their own activation.
  onRowActivate?: (row: TData) => void;
  enableRowSelection?: boolean;
  onSelectionChange?: (ids: string[]) => void;
  // meta reaches every header and cell through table.options.meta.
  meta?: TableMeta<DataTableFeatures, TData>;
  rowTestId?: string;
  emptyMessage?: string;
  // aria-label names the table for a screen reader.
  "aria-label"?: string;
  className?: string;
};

function isInnerControl(target: EventTarget | null): boolean {
  return Boolean(
    (target as HTMLElement | null)?.closest("a,button,input,label,select,textarea"),
  );
}

export function DataTable<TData extends RowData>({
  columns,
  data,
  getRowId,
  initialSorting,
  resizable = false,
  onRowActivate,
  enableRowSelection = false,
  onSelectionChange,
  meta,
  rowTestId,
  emptyMessage = "Nothing to show.",
  "aria-label": ariaLabel,
  className,
}: DataTableProps<TData>) {
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({});
  const table = useTable({
    features: dataTableFeatures,
    columns,
    data,
    getRowId,
    columnResizeMode: "onChange",
    enableColumnResizing: resizable,
    enableRowSelection,
    meta,
    state: { rowSelection },
    onRowSelectionChange: (updater) => {
      setRowSelection((previous) => {
        const next = typeof updater === "function" ? updater(previous) : updater;
        onSelectionChange?.(Object.keys(next));
        return next;
      });
    },
    initialState: initialSorting ? { sorting: initialSorting } : undefined,
  });

  const leafColumns = table.getAllLeafColumns();
  const flexIndex = Math.max(
    0,
    leafColumns.findIndex((column) => column.columnDef.meta?.flex) === -1
      ? leafColumns.findIndex((column) => column.id !== SELECT_COLUMN_ID)
      : leafColumns.findIndex((column) => column.columnDef.meta?.flex),
  );
  // One template string serves the header and every row: the flex column
  // absorbs spare width, the rest hold their declared or dragged size.
  const template = leafColumns
    .map((column, index) =>
      index === flexIndex
        ? `minmax(${column.getSize()}px, 1fr)`
        : `${column.getSize()}px`,
    )
    .join(" ");
  const minWidth = table.getTotalSize();

  const rows = table.getRowModel().rows;
  const virtual = rows.length > VIRTUAL_THRESHOLD;

  // The window virtualizer needs the body's offset from the top of the
  // document. It is measured in a layout effect and again whenever the
  // page above the table changes height, never read during render.
  const bodyRef = useRef<HTMLDivElement>(null);
  const headRef = useRef<HTMLDivElement>(null);
  const [scrollMargin, setScrollMargin] = useState(0);
  useLayoutEffect(() => {
    const body = bodyRef.current;
    if (!virtual || !body) return;
    const measure = () => {
      setScrollMargin(Math.round(body.getBoundingClientRect().top + window.scrollY));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(document.body);
    return () => observer.disconnect();
  }, [virtual]);

  const virtualizer = useWindowVirtualizer({
    count: rows.length,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
    scrollMargin,
    enabled: virtual,
  });

  const interactive = Boolean(onRowActivate);
  const activate = (row: Row<DataTableFeatures, TData>) => onRowActivate?.(row.original);
  const onRowClick = (row: Row<DataTableFeatures, TData>) => (event: MouseEvent) => {
    if (isInnerControl(event.target)) return;
    activate(row);
  };
  const onRowKeyDown = (row: Row<DataTableFeatures, TData>) => (event: KeyboardEvent) => {
    if (event.target !== event.currentTarget) return;
    if (event.key !== "Enter" && event.key !== " ") return;
    event.preventDefault();
    activate(row);
  };

  const renderRow = (
    row: Row<DataTableFeatures, TData>,
    index: number,
    style?: CSSProperties,
  ) => (
    <div
      role="row"
      key={row.id}
      aria-rowindex={index + 2}
      aria-selected={enableRowSelection ? row.getIsSelected() : undefined}
      data-testid={rowTestId}
      tabIndex={interactive ? 0 : undefined}
      onClick={interactive ? onRowClick(row) : undefined}
      onKeyDown={interactive ? onRowKeyDown(row) : undefined}
      className={cn(
        "group grid min-h-10 items-center border-b border-border-1 transition-colors duration-150 ease-standard last:border-b-0 hover:bg-bg-hover",
        interactive &&
          "cursor-pointer outline-none focus-visible:bg-bg-hover focus-visible:[box-shadow:inset_2px_0_0_var(--color-accent)]",
        row.getIsSelected() && "bg-accent-tint/40",
      )}
      style={{ gridTemplateColumns: template, ...style }}
    >
      {row.getAllCells().map((cell) => {
        const meta = cell.column.columnDef.meta;
        return (
          <div
            role="cell"
            key={cell.id}
            className={cn(
              "min-w-0 px-4 py-2.5",
              meta?.align === "end" && "text-right",
              meta?.className,
            )}
          >
            {flexRender(cell.column.columnDef.cell, cell.getContext())}
          </div>
        );
      })}
    </div>
  );

  return (
    <div
      role="table"
      aria-label={ariaLabel}
      aria-rowcount={rows.length + 1}
      className={cn("rounded-md border border-border-1 bg-bg-panel text-sm", className)}
    >
      <div
        ref={headRef}
        role="rowgroup"
        className="sticky top-0 z-10 overflow-x-hidden rounded-t-md bg-bg-panel"
      >
        {table.getHeaderGroups().map((headerGroup) => (
          <div
            role="row"
            key={headerGroup.id}
            aria-rowindex={1}
            className="grid h-10 items-center border-b border-border-1"
            style={{ gridTemplateColumns: template, minWidth }}
          >
            {headerGroup.headers.map((header) => {
              const column = header.column;
              const sorted = column.getIsSorted();
              const sortable = column.getCanSort();
              const end = column.columnDef.meta?.align === "end";
              const content = header.isPlaceholder
                ? null
                : flexRender(column.columnDef.header, header.getContext());
              return (
                <div
                  role="columnheader"
                  key={header.id}
                  aria-sort={
                    !sortable
                      ? undefined
                      : sorted === "asc"
                        ? "ascending"
                        : sorted === "desc"
                          ? "descending"
                          : "none"
                  }
                  className={cn(
                    "relative flex h-10 min-w-0 items-center text-xs font-medium text-text-3",
                    end && "justify-end",
                  )}
                >
                  {sortable ? (
                    <button
                      type="button"
                      onClick={column.getToggleSortingHandler()}
                      className={cn(
                        "group/sort inline-flex h-10 min-w-0 items-center gap-1 px-4 text-left transition-colors duration-150 ease-standard hover:text-text-1",
                        end && "flex-row-reverse text-right",
                      )}
                    >
                      <span className="truncate">{content}</span>
                      {sorted === "asc" && <ArrowUp className="size-3 shrink-0" />}
                      {sorted === "desc" && <ArrowDown className="size-3 shrink-0" />}
                      {!sorted && (
                        <ArrowUp className="size-3 shrink-0 opacity-0 transition-opacity group-hover/sort:opacity-40" />
                      )}
                    </button>
                  ) : (
                    <span className="truncate px-4">{content}</span>
                  )}
                  {column.getCanResize() && (
                    <div
                      role="separator"
                      aria-orientation="vertical"
                      aria-label={`Resize ${column.id} column`}
                      onMouseDown={header.getResizeHandler()}
                      onTouchStart={header.getResizeHandler()}
                      onDoubleClick={() => column.resetSize()}
                      className={cn(
                        "absolute -right-0.5 top-1/2 z-10 h-5 w-1 -translate-y-1/2 cursor-col-resize rounded-full transition-colors duration-150",
                        column.getIsResizing() ? "bg-accent" : "hover:bg-border-3",
                      )}
                    />
                  )}
                </div>
              );
            })}
          </div>
        ))}
      </div>
      {rows.length === 0 ? (
        <p className="px-4 py-6 text-sm text-text-3">{emptyMessage}</p>
      ) : (
        <div
          ref={bodyRef}
          role="rowgroup"
          onScroll={(event) => {
            if (headRef.current) {
              headRef.current.scrollLeft = event.currentTarget.scrollLeft;
            }
          }}
          className="overflow-x-auto"
        >
          <div
            className={virtual ? "relative" : undefined}
            style={{ minWidth, height: virtual ? virtualizer.getTotalSize() : undefined }}
          >
            {virtual
              ? virtualizer.getVirtualItems().map((item) => {
                  const row = rows[item.index];
                  if (!row) return null;
                  return renderRow(row, item.index, {
                    position: "absolute",
                    top: 0,
                    left: 0,
                    width: "100%",
                    height: item.size,
                    transform: `translateY(${item.start - scrollMargin}px)`,
                  });
                })
              : rows.map((row, index) => renderRow(row, index))}
          </div>
        </div>
      )}
    </div>
  );
}

// DataTableFooter states what the reader holds: the loaded count, the bound
// one request returns, and the way to the rest when the route pages.
export function DataTableFooter({
  loaded,
  unit,
  bound,
  hasMore = false,
  loading = false,
  onLoadMore,
  loadLabel = "Load more",
}: {
  loaded: number;
  unit: { one: string; other: string };
  // bound is the largest count one request returns.
  bound?: number;
  hasMore?: boolean;
  loading?: boolean;
  onLoadMore?: () => void;
  loadLabel?: string;
}) {
  const parts = [`${formatCount(loaded)} ${loaded === 1 ? unit.one : unit.other} loaded`];
  if (bound !== undefined) parts.push(`${formatCount(bound)} per request`);
  if (hasMore && !onLoadMore) parts.push("more exist past the bound");
  return (
    <div className="flex flex-wrap items-center gap-3">
      <p className="text-xs tabular-nums text-text-3" data-testid="table-footer">
        {parts.join(" · ")}
      </p>
      {hasMore && onLoadMore && (
        <Button
          type="button"
          variant="outline"
          size="xs"
          onClick={onLoadMore}
          disabled={loading}
        >
          {loading ? "Loading…" : loadLabel}
        </Button>
      )}
    </div>
  );
}
