import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";

import { INPUT_CLASS } from "@/components/ui/Form";
import { listModels } from "@/lib/api";
import { chattableModels } from "@/lib/modelFilter";

const MAX_SUGGESTIONS = 8;

// ModelPicker is a combobox over the live model catalog: a mono text
// input with substring-filtered ID suggestions. It never carries model
// facts of its own — every suggestion comes from /models at runtime.
export function ModelPicker({
  value,
  onChange,
  placeholder,
  id,
}: {
  value: string;
  onChange: (next: string) => void;
  placeholder?: string;
  id?: string;
}) {
  const [open, setOpen] = useState(false);
  const [cursor, setCursor] = useState(-1);
  const rootRef = useRef<HTMLDivElement>(null);

  const models = useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    staleTime: 60_000,
    retry: false,
  });

  const suggestions = useMemo(() => {
    const ids = chattableModels(models.data ?? []).map((model) => model.id);
    const query = value.trim().toLowerCase();
    const matches = query
      ? ids.filter((modelId) => modelId.toLowerCase().includes(query))
      : ids;
    return matches.slice(0, MAX_SUGGESTIONS);
  }, [models.data, value]);

  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, []);

  const pick = (modelId: string) => {
    onChange(modelId);
    setOpen(false);
    setCursor(-1);
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setOpen(true);
      setCursor((prev) => Math.min(prev + 1, suggestions.length - 1));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setCursor((prev) => Math.max(prev - 1, -1));
    } else if (event.key === "Enter" && open && cursor >= 0) {
      event.preventDefault();
      const chosen = suggestions[cursor];
      if (chosen !== undefined) pick(chosen);
    } else if (event.key === "Escape") {
      setOpen(false);
      setCursor(-1);
    }
  };

  return (
    <div ref={rootRef} className="relative">
      <input
        id={id}
        type="text"
        role="combobox"
        aria-expanded={open && suggestions.length > 0}
        aria-autocomplete="list"
        value={value}
        placeholder={placeholder}
        autoComplete="off"
        spellCheck={false}
        onChange={(event) => {
          onChange(event.target.value);
          setOpen(true);
          setCursor(-1);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={onKeyDown}
        className={`${INPUT_CLASS} w-full font-mono`}
      />
      {open && suggestions.length > 0 && (
        <ul
          role="listbox"
          className="absolute inset-x-0 top-full z-10 mt-1 max-h-64 overflow-y-auto rounded-sm border border-border-2 bg-bg-raised py-1 shadow-[0_8px_24px_rgba(0,0,0,0.4)]"
        >
          {suggestions.map((modelId, index) => (
            <li key={modelId} role="option" aria-selected={index === cursor}>
              <button
                type="button"
                onClick={() => pick(modelId)}
                onMouseEnter={() => setCursor(index)}
                className={`block w-full px-3 py-1.5 text-left font-mono text-xs text-text-2 ${
                  index === cursor ? "bg-bg-hover text-text-1" : ""
                }`}
              >
                {modelId}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
