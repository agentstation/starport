import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { INPUT_CLASS } from "@/components/ui/Form";
import { TokenInput } from "@/components/ui/TokenInput";
import { queries } from "@/lib/queries";
import { chattableModels } from "@/lib/modelFilter";

const MAX_SUGGESTIONS = 8;

// matchModelIds filters the chattable catalog IDs by a substring of the
// query, keeping the first few so the list stays short enough to scan.
function matchModelIds(ids: string[], query: string): string[] {
  const needle = query.trim().toLowerCase();
  const matches = needle ? ids.filter((modelId) => modelId.toLowerCase().includes(needle)) : ids;
  return matches.slice(0, MAX_SUGGESTIONS);
}

// ModelMultiPicker selects several model IDs as chips over the same
// catalog: an allowlist on a key, or the fallback list on a preset. An ID
// outside the catalog still commits, so a key can name a model before the
// catalog lists it.
export function ModelMultiPicker({
  values,
  onChange,
  placeholder,
  id,
  "aria-label": ariaLabel,
}: {
  values: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
  id?: string;
  "aria-label"?: string;
}) {
  const models = useQuery({ ...queries.models() });
  const ids = useMemo(
    () => chattableModels(models.data ?? []).map((model) => model.id),
    [models.data],
  );
  const suggest = useCallback((draft: string) => matchModelIds(ids, draft), [ids]);
  return (
    <TokenInput
      id={id}
      values={values}
      onChange={onChange}
      placeholder={placeholder}
      suggest={suggest}
      aria-label={ariaLabel}
    />
  );
}

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
    ...queries.models(),
  });

  const suggestions = useMemo(
    () => matchModelIds(chattableModels(models.data ?? []).map((model) => model.id), value),
    [models.data, value],
  );

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
