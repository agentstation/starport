import { X } from "lucide-react";
import { useEffect, useRef, useState, type ClipboardEvent, type KeyboardEvent } from "react";

import { INPUT_CLASS } from "@/components/ui/Form";
import { cn } from "@/lib/utils";

// TokenInput edits a list of short strings as chips inside one field.
// Enter or a comma commits the typed text as a token, Backspace on an
// empty draft removes the last one, and a paste splits on commas. It
// replaces the comma-separated text fields, whose split rules a reader
// had to know from a hint. A caller that can suggest values hands in a
// function of the draft, and the field becomes a combobox over them.
export function TokenInput({
  values,
  onChange,
  placeholder,
  id,
  mono = true,
  suggest,
  "aria-label": ariaLabel,
  ...control
}: {
  values: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
  id?: string;
  mono?: boolean;
  suggest?: (draft: string) => string[];
  "aria-label"?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean | "true" | "false";
  "aria-required"?: boolean | "true" | "false";
}) {
  const [draft, setDraft] = useState("");
  const [open, setOpen] = useState(false);
  const [cursor, setCursor] = useState(-1);
  const inputRef = useRef<HTMLInputElement>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  const suggestions = suggest ? suggest(draft).filter((item) => !values.includes(item)) : [];
  // The list answers typing alone. A bare focus, or a pick that clears the
  // draft, leaves the fields below it reachable.
  const listOpen = open && draft.trim() !== "" && suggestions.length > 0;

  useEffect(() => {
    if (!suggest) return;
    const onPointerDown = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [suggest]);

  const commit = (text: string) => {
    const tokens = text
      .split(",")
      .map((part) => part.trim())
      .filter((part) => part && !values.includes(part));
    if (tokens.length > 0) onChange([...values, ...tokens]);
    setDraft("");
    setCursor(-1);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (listOpen && event.key === "ArrowDown") {
      event.preventDefault();
      setCursor((prev) => Math.min(prev + 1, suggestions.length - 1));
    } else if (listOpen && event.key === "ArrowUp") {
      event.preventDefault();
      setCursor((prev) => Math.max(prev - 1, -1));
    } else if (event.key === "Escape" && listOpen) {
      setOpen(false);
      setCursor(-1);
    } else if (event.key === "Enter" && listOpen && cursor >= 0) {
      event.preventDefault();
      const chosen = suggestions[cursor];
      if (chosen !== undefined) commit(chosen);
    } else if (event.key === "Enter" || event.key === ",") {
      if (draft.trim()) {
        event.preventDefault();
        commit(draft);
      } else if (event.key === ",") {
        event.preventDefault();
      }
    } else if (event.key === "Backspace" && draft === "" && values.length > 0) {
      onChange(values.slice(0, -1));
    }
  };

  const onPaste = (event: ClipboardEvent<HTMLInputElement>) => {
    const text = event.clipboardData.getData("text");
    if (text.includes(",")) {
      event.preventDefault();
      commit(`${draft}${text}`);
    }
  };

  return (
    <div ref={rootRef} className="relative">
    <div
      className={cn(
        INPUT_CLASS,
        "flex h-auto min-h-9 flex-wrap items-center gap-1 py-1 focus-within:border-accent",
      )}
      onClick={() => inputRef.current?.focus()}
    >
      {values.map((token) => (
        <span
          key={token}
          className={cn(
            "inline-flex h-6 items-center gap-1 rounded-xs bg-bg-hover pl-1.5 text-xs text-text-1",
            mono && "font-mono",
          )}
        >
          {token}
          <button
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              onChange(values.filter((value) => value !== token));
            }}
            aria-label={`Remove ${token}`}
            className="flex h-6 w-5 items-center justify-center rounded-r-xs text-text-3 hover:text-text-1"
          >
            <X className="size-3" />
          </button>
        </span>
      ))}
      <input
        ref={inputRef}
        id={id}
        type="text"
        value={draft}
        role={suggest ? "combobox" : undefined}
        aria-expanded={suggest ? listOpen : undefined}
        aria-autocomplete={suggest ? "list" : undefined}
        onChange={(event) => {
          setDraft(event.target.value);
          setOpen(true);
          setCursor(-1);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={onKeyDown}
        onPaste={onPaste}
        onBlur={() => draft.trim() && !listOpen && commit(draft)}
        placeholder={values.length === 0 ? placeholder : undefined}
        aria-label={ariaLabel}
        autoComplete="off"
        spellCheck={false}
        className={cn(
          "h-6 min-w-24 flex-1 bg-transparent text-sm text-text-1 outline-none placeholder:text-text-4",
          mono && "font-mono",
        )}
        {...control}
      />
    </div>
      {listOpen && (
        <ul
          role="listbox"
          className="absolute inset-x-0 top-full z-10 mt-1 max-h-64 overflow-y-auto rounded-sm border border-border-2 bg-bg-raised py-1 shadow-overlay"
        >
          {suggestions.map((item, index) => (
            <li key={item} role="option" aria-selected={index === cursor}>
              <button
                type="button"
                onClick={() => commit(item)}
                onMouseEnter={() => setCursor(index)}
                className={cn(
                  "block w-full px-3 py-1.5 text-left text-xs text-text-2",
                  mono && "font-mono",
                  index === cursor && "bg-bg-hover text-text-1",
                )}
              >
                {item}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
