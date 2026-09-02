import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  DataTable,
  DataTableFooter,
  dataColumns,
  selectionColumn,
  VIRTUAL_THRESHOLD,
} from "./DataTable";

type Item = { id: string; name: string; size: number };

const helper = dataColumns<Item>();
const columns = helper.columns([
  helper.accessor("name", {
    id: "name",
    header: "Name",
    sortFn: "alphanumeric",
    cell: ({ row }) => <a href={`/items/${row.original.id}`}>{row.original.name}</a>,
  }),
  helper.accessor("size", {
    id: "size",
    header: "Size",
    sortFn: "basic",
    meta: { align: "end" },
  }),
  helper.display({ id: "note", header: "Note", cell: () => "n" }),
]);

function items(count: number): Item[] {
  return Array.from({ length: count }, (_, index) => ({
    id: `item-${index}`,
    name: `Item ${String(index).padStart(3, "0")}`,
    size: (index * 7) % 13,
  }));
}

afterEach(() => {
  cleanup();
});

describe("DataTable", () => {
  it("cycles the sort direction and announces it through aria-sort", () => {
    render(<DataTable columns={columns} data={items(3)} getRowId={(row) => row.id} />);
    const header = screen.getByRole("columnheader", { name: /name/i });
    expect(header.getAttribute("aria-sort")).toBe("none");
    const control = screen.getByRole("button", { name: "Name" });
    fireEvent.click(control);
    expect(header.getAttribute("aria-sort")).toBe("ascending");
    fireEvent.click(control);
    expect(header.getAttribute("aria-sort")).toBe("descending");
    fireEvent.click(control);
    expect(header.getAttribute("aria-sort")).toBe("none");
    // A display column has no sort and no aria-sort.
    expect(screen.getByRole("columnheader", { name: "Note" }).getAttribute("aria-sort")).toBeNull();
  });

  it("sorts from the keyboard because the sort control is a button", () => {
    render(<DataTable columns={columns} data={items(3)} getRowId={(row) => row.id} />);
    const control = screen.getByRole("button", { name: "Size" });
    control.focus();
    expect(document.activeElement).toBe(control);
    fireEvent.click(control);
    // A numeric column sorts largest first on its first toggle.
    expect(screen.getByRole("columnheader", { name: /size/i }).getAttribute("aria-sort")).toBe(
      "descending",
    );
    const cells = screen
      .getAllByRole("row")
      .slice(1)
      .map((row) => row.querySelectorAll('[role="cell"]')[1]?.textContent);
    expect(cells).toEqual(["7", "1", "0"]);
  });

  it("activates a row with Enter and Space but not through an inner link", () => {
    const activate = vi.fn();
    render(
      <DataTable
        columns={columns}
        data={items(2)}
        getRowId={(row) => row.id}
        onRowActivate={activate}
      />,
    );
    const [, first] = screen.getAllByRole("row");
    expect(first!.getAttribute("tabindex")).toBe("0");
    fireEvent.keyDown(first!, { key: "Enter" });
    fireEvent.keyDown(first!, { key: " " });
    expect(activate).toHaveBeenCalledTimes(2);
    expect(activate).toHaveBeenLastCalledWith(expect.objectContaining({ id: "item-0" }));
    fireEvent.click(screen.getByText("Item 000"));
    expect(activate).toHaveBeenCalledTimes(2);
    fireEvent.click(first!);
    expect(activate).toHaveBeenCalledTimes(3);
  });

  it("renders only the rows near the viewport past the threshold", () => {
    const count = VIRTUAL_THRESHOLD + 50;
    render(<DataTable columns={columns} data={items(count)} getRowId={(row) => row.id} />);
    const table = screen.getByRole("table");
    expect(table.getAttribute("aria-rowcount")).toBe(String(count + 1));
    const rows = screen.getAllByRole("row");
    expect(rows.length).toBeGreaterThan(1);
    expect(rows.length).toBeLessThan(count);
    expect(rows[0]!.getAttribute("aria-rowindex")).toBe("1");
    expect(rows[1]!.getAttribute("aria-rowindex")).toBe("2");
  });

  it("selects rows through the selection column", () => {
    const changed = vi.fn();
    render(
      <DataTable
        columns={[selectionColumn<Item>(), ...columns]}
        data={items(2)}
        getRowId={(row) => row.id}
        enableRowSelection
        onSelectionChange={changed}
      />,
    );
    const boxes = screen.getAllByRole("checkbox", { name: "Select row" });
    fireEvent.click(boxes[0]!);
    expect(changed).toHaveBeenLastCalledWith(["item-0"]);
    expect(screen.getAllByRole("row")[1]!.getAttribute("aria-selected")).toBe("true");
    fireEvent.click(screen.getByRole("checkbox", { name: "Select all rows" }));
    expect(changed).toHaveBeenLastCalledWith(["item-0", "item-1"]);
  });

  it("shows the empty message under the header", () => {
    render(
      <DataTable
        columns={columns}
        data={[]}
        getRowId={(row) => row.id}
        emptyMessage="No items match."
      />,
    );
    expect(screen.getByText("No items match.")).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "Note" })).toBeTruthy();
  });
});

describe("DataTableFooter", () => {
  it("states the loaded count, the bound, and the way to the rest", () => {
    const more = vi.fn();
    render(
      <DataTableFooter
        loaded={100}
        unit={{ one: "record", other: "records" }}
        bound={100}
        hasMore
        onLoadMore={more}
        loadLabel="Load older records"
      />,
    );
    expect(screen.getByTestId("table-footer").textContent).toBe("100 records loaded · 100 per request");
    fireEvent.click(screen.getByRole("button", { name: "Load older records" }));
    expect(more).toHaveBeenCalledTimes(1);
  });

  it("names the rows past the bound when the route cannot page", () => {
    render(
      <DataTableFooter loaded={1} unit={{ one: "file", other: "files" }} bound={1000} hasMore />,
    );
    expect(screen.getByTestId("table-footer").textContent).toBe("1 file loaded · 1,000 per request · more exist past the bound");
    expect(screen.queryByRole("button")).toBeNull();
  });
});
