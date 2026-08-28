import { Check, ChevronDown, Search, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

// FacetFilter is the console's searchable multi-select: a trigger
// chip that opens a popover with a quick-search input and a checkable
// option list. It replaces native <select> facets, which cannot
// search, cannot hold more than one value, and carry platform chrome.
//
// Selection state lives with the caller (the models page keeps it in
// the URL); this component owns only open/close and the search draft.

export type FacetOption = {
  value: string;
  label: string;
  count?: number;
};

export function FacetFilter({
  label,
  options,
  selected,
  onChange,
  searchable = true,
}: {
  label: string;
  options: FacetOption[];
  selected: string[];
  onChange: (values: string[]) => void;
  searchable?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    setSearch("");
    inputRef.current?.focus();
    const onPointerDown = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const visible = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return options;
    return options.filter(
      (option) =>
        option.label.toLowerCase().includes(query) ||
        option.value.toLowerCase().includes(query),
    );
  }, [options, search]);

  const toggle = (value: string) => {
    onChange(
      selected.includes(value)
        ? selected.filter((entry) => entry !== value)
        : [...selected, value],
    );
  };

  const summary =
    selected.length === 0
      ? label
      : selected.length === 1
        ? (options.find((option) => option.value === selected[0])?.label ??
          selected[0])
        : `${label} · ${selected.length}`;

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className={`flex h-8 max-w-56 items-center gap-1.5 rounded-sm border px-2.5 text-xs transition-colors duration-150 ease-standard ${
          selected.length > 0
            ? "border-border-3 bg-bg-raised text-text-1"
            : "border-border-1 bg-bg-panel text-text-2 hover:border-border-2"
        }`}
      >
        <span className="truncate">{summary}</span>
        {selected.length > 0 ? (
          <span
            role="button"
            tabIndex={0}
            aria-label={`Clear ${label} filter`}
            onClick={(event) => {
              event.stopPropagation();
              onChange([]);
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === " ") {
                event.stopPropagation();
                onChange([]);
              }
            }}
            className="flex size-4 shrink-0 items-center justify-center rounded-xs text-text-4 hover:bg-bg-hover hover:text-text-2"
          >
            <X className="size-3" />
          </span>
        ) : (
          <ChevronDown className="size-3.5 shrink-0 text-text-4" />
        )}
      </button>
      {open && (
        <div className="absolute left-0 top-full z-30 mt-1 flex max-h-80 w-64 flex-col overflow-hidden rounded-md border border-border-2 bg-bg-raised shadow-[0_8px_24px_rgba(0,0,0,0.4)]">
          {searchable && (
            <div className="flex items-center gap-2 border-b border-border-1 px-2.5">
              <Search className="size-3.5 shrink-0 text-text-4" />
              <input
                ref={inputRef}
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder={`Filter ${label.toLowerCase()}…`}
                aria-label={`Search ${label}`}
                className="h-8 w-full bg-transparent text-xs text-text-1 outline-none placeholder:text-text-4"
              />
            </div>
          )}
          <div role="listbox" aria-multiselectable="true" className="overflow-y-auto p-1">
            {visible.length === 0 && (
              <p className="px-2.5 py-2 text-xs text-text-4">No matches.</p>
            )}
            {visible.map((option) => {
              const checked = selected.includes(option.value);
              return (
                <button
                  key={option.value}
                  type="button"
                  role="option"
                  aria-selected={checked}
                  onClick={() => toggle(option.value)}
                  className="flex h-8 w-full items-center gap-2 rounded-xs px-2 text-left text-xs text-text-2 hover:bg-bg-hover"
                >
                  <span
                    aria-hidden="true"
                    className={`flex size-3.5 shrink-0 items-center justify-center rounded-xs border ${
                      checked
                        ? "border-accent bg-accent text-accent-ink"
                        : "border-border-3"
                    }`}
                  >
                    {checked && <Check className="size-2.5" strokeWidth={3} />}
                  </span>
                  <span className="min-w-0 flex-1 truncate">{option.label}</span>
                  {option.count !== undefined && (
                    <span className="shrink-0 font-mono text-[10px] tabular-nums text-text-4">
                      {option.count}
                    </span>
                  )}
                </button>
              );
            })}
          </div>
          {selected.length > 0 && (
            <div className="border-t border-border-1 p-1">
              <button
                type="button"
                onClick={() => onChange([])}
                className="flex h-7 w-full items-center justify-center rounded-xs text-xs text-text-3 hover:bg-bg-hover hover:text-text-2"
              >
                Clear
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
