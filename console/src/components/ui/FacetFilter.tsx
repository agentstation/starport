import { Check, ChevronDown, Search, X } from "lucide-react";
import { useMemo, useRef, useState, type KeyboardEvent } from "react";

import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

// FacetFilter is the console's searchable multi-select: a trigger
// chip that opens a popover with a quick-search input and a checkable
// option list. It replaces native <select> facets, which cannot
// search, cannot hold more than one value, and carry platform chrome.
//
// Selection state lives with the caller (the models page keeps it in
// the URL); this component owns only open/close, the search draft, and
// the roving focus inside the option list. The clear control sits
// beside the trigger, not inside it, so no button nests in a button.

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
  const [focused, setFocused] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

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

  // focusOption moves the roving tab stop and the real focus together.
  const focusOption = (index: number) => {
    if (visible.length === 0) return;
    const next = Math.max(0, Math.min(index, visible.length - 1));
    setFocused(next);
    listRef.current
      ?.querySelector<HTMLElement>(`[data-index="${next}"]`)
      ?.focus();
  };

  const onListKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      focusOption(focused + 1);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      if (focused === 0 && searchable) {
        inputRef.current?.focus();
      } else {
        focusOption(focused - 1);
      }
    } else if (event.key === "Home") {
      event.preventDefault();
      focusOption(0);
    } else if (event.key === "End") {
      event.preventDefault();
      focusOption(visible.length - 1);
    }
  };

  const summary =
    selected.length === 0
      ? label
      : selected.length === 1
        ? (options.find((option) => option.value === selected[0])?.label ??
          selected[0])
        : `${label} · ${selected.length}`;

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) {
          setSearch("");
          setFocused(0);
        }
      }}
    >
      <div
        className={`flex h-8 max-w-56 items-center rounded-sm border text-xs transition-colors duration-150 ease-standard ${
          selected.length > 0
            ? "border-border-3 bg-bg-raised text-text-1"
            : "border-border-1 bg-bg-panel text-text-2 hover:border-border-2"
        }`}
      >
        <PopoverTrigger className="flex h-full min-w-0 items-center gap-1.5 rounded-sm px-2.5 outline-none focus-visible:ring-2 focus-visible:ring-accent/50">
          <span className="truncate">{summary}</span>
          {selected.length === 0 && (
            <ChevronDown className="size-3.5 shrink-0 text-text-4" />
          )}
        </PopoverTrigger>
        {selected.length > 0 && (
          <button
            type="button"
            aria-label={`Clear ${label} filter`}
            onClick={() => onChange([])}
            className="flex h-full shrink-0 items-center pr-2 pl-0.5 text-text-4 outline-none hover:text-text-2 focus-visible:text-text-2"
          >
            <X className="size-3" />
          </button>
        )}
      </div>
      <PopoverContent
        align="start"
        aria-label={`${label} filter`}
        initialFocus={searchable ? inputRef : undefined}
        className="flex max-h-80 w-64 flex-col gap-0 overflow-hidden p-0"
      >
        {searchable && (
          <div className="flex items-center gap-2 border-b border-border-1 px-2.5">
            <Search className="size-3.5 shrink-0 text-text-4" />
            <input
              ref={inputRef}
              value={search}
              onChange={(event) => {
                setSearch(event.target.value);
                setFocused(0);
              }}
              onKeyDown={(event) => {
                if (event.key === "ArrowDown") {
                  event.preventDefault();
                  focusOption(0);
                }
              }}
              placeholder={`Filter ${label.toLowerCase()}…`}
              aria-label={`Search ${label}`}
              className="h-8 w-full bg-transparent text-xs text-text-1 outline-none placeholder:text-text-4"
            />
          </div>
        )}
        <div
          ref={listRef}
          role="listbox"
          aria-multiselectable="true"
          aria-label={label}
          onKeyDown={onListKeyDown}
          className="overflow-y-auto p-1"
        >
          {visible.length === 0 && (
            <p className="px-2.5 py-2 text-xs text-text-4">No matches.</p>
          )}
          {visible.map((option, index) => {
            const checked = selected.includes(option.value);
            return (
              <button
                key={option.value}
                type="button"
                role="option"
                aria-selected={checked}
                data-index={index}
                tabIndex={index === focused ? 0 : -1}
                onFocus={() => setFocused(index)}
                onClick={() => toggle(option.value)}
                className="flex h-8 w-full items-center gap-2 rounded-xs px-2 text-left text-xs text-text-2 outline-none hover:bg-bg-hover focus-visible:bg-bg-hover"
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
      </PopoverContent>
    </Popover>
  );
}
