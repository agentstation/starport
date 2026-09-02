import { useQuery } from "@tanstack/react-query";
import { Brain, Check, Eye, Globe, Star, Wrench } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { type Model } from "@/lib/api";
import { queries } from "@/lib/queries";
import { formatContext, formatPricePerM, providerLabel } from "@/lib/format";
import { chattableModels } from "@/lib/modelFilter";

// ModelPicker is the chat model popover (DESIGN.md): search, pinned
// models first, presets, then provider groups. Rows show capability
// badges and catalog facts from the live /models response — the picker
// never carries model data of its own. Opens upward from the composer.

export type PickerItem =
  | { kind: "model"; id: string; model: Model }
  | { kind: "preset"; id: string; description?: string };

type Section = { label: string; items: PickerItem[] };

function modelCapabilities(model: Model): {
  vision: boolean;
  reasoning: boolean;
  tools: boolean;
  webSearch: boolean;
} {
  const params = model.supported_parameters ?? [];
  return {
    vision: (model.architecture?.input_modalities ?? []).includes("image"),
    reasoning: params.some((p) => p === "reasoning" || p === "include_reasoning"),
    tools: params.includes("tools"),
    webSearch: params.includes("web_search_options"),
  };
}

export function supportsReasoning(model: Model | undefined): boolean {
  return model ? modelCapabilities(model).reasoning : false;
}

export function supportsVision(model: Model | undefined): boolean {
  return model ? modelCapabilities(model).vision : false;
}

function CapabilityBadges({ model }: { model: Model }) {
  const caps = modelCapabilities(model);
  if (!caps.vision && !caps.reasoning && !caps.tools && !caps.webSearch) return null;
  return (
    <span className="flex items-center gap-1 text-text-4">
      {caps.vision && <Eye className="size-3" aria-label="Vision" />}
      {caps.reasoning && <Brain className="size-3" aria-label="Reasoning" />}
      {caps.tools && <Wrench className="size-3" aria-label="Tools" />}
      {caps.webSearch && <Globe className="size-3" aria-label="Web search" />}
    </span>
  );
}

function modelFacts(model: Model): string {
  const parts: string[] = [];
  const context = formatContext(model.context_length);
  if (context !== "—") parts.push(context);
  const input = formatPricePerM(model.pricing?.prompt);
  const output = formatPricePerM(model.pricing?.completion);
  if (input !== null && output !== null) parts.push(`${input}/${output} per M`);
  return parts.join(" · ");
}

export function ModelPicker({
  value,
  favorites,
  onToggleFavorite,
  onSelect,
  onClose,
}: {
  value: string;
  favorites: Set<string>;
  onToggleFavorite: (id: string) => void;
  onSelect: (id: string) => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const [cursor, setCursor] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const models = useQuery({
    ...queries.models(),
  });
  // Presets are optional context; a locked endpoint just hides the group.
  const presets = useQuery({
    ...queries.presets(),
  });
  const catalog = useQuery({
    ...queries.providerCatalog(),
  });

  const sections = useMemo<Section[]>(() => {
    const needle = query.trim().toLowerCase();
    const matches = (haystack: string) =>
      !needle || haystack.toLowerCase().includes(needle);

    const all = chattableModels(models.data ?? []);
    const pinned: PickerItem[] = [];
    const byProvider = new Map<string, PickerItem[]>();
    for (const model of all) {
      if (!matches(`${model.id} ${model.name ?? ""}`)) continue;
      const item: PickerItem = { kind: "model", id: model.id, model };
      if (favorites.has(model.id)) {
        pinned.push(item);
        continue;
      }
      const provider = model.id.split("/")[0] ?? "other";
      const group = byProvider.get(provider);
      if (group) {
        group.push(item);
      } else {
        byProvider.set(provider, [item]);
      }
    }

    const presetItems: PickerItem[] = (presets.data ?? [])
      .map((preset) => ({
        kind: "preset" as const,
        id: `@preset/${preset.name}`,
        description: preset.description,
      }))
      .filter((item) => matches(item.id));

    const result: Section[] = [];
    if (pinned.length) result.push({ label: "Pinned", items: pinned });
    if (presetItems.length) result.push({ label: "Presets", items: presetItems });
    const names = new Map(
      (catalog.data ?? []).map((entry) => [entry.id, entry.name]),
    );
    for (const provider of [...byProvider.keys()].sort()) {
      result.push({
        label: providerLabel(provider, names.get(provider)),
        items: byProvider.get(provider) ?? [],
      });
    }
    return result;
  }, [models.data, presets.data, catalog.data, favorites, query]);

  const flat = useMemo(() => sections.flatMap((section) => section.items), [sections]);

  useEffect(() => {
    setCursor(0);
  }, [query]);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // Close on outside pointer-down; the composer trigger stops its own
  // event so reopening toggles cleanly.
  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) onClose();
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [onClose]);

  useEffect(() => {
    listRef.current
      ?.querySelector(`[data-index="${cursor}"]`)
      ?.scrollIntoView({ block: "nearest" });
  }, [cursor]);

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setCursor((prev) => Math.min(prev + 1, flat.length - 1));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setCursor((prev) => Math.max(prev - 1, 0));
    } else if (event.key === "Home") {
      event.preventDefault();
      setCursor(0);
    } else if (event.key === "End") {
      event.preventDefault();
      setCursor(flat.length - 1);
    } else if (event.key === "Enter") {
      event.preventDefault();
      const chosen = flat[cursor];
      if (chosen) onSelect(chosen.id);
    } else if (event.key === "Escape") {
      event.preventDefault();
      onClose();
    }
  };

  let index = -1;
  return (
    <div
      ref={rootRef}
      role="dialog"
      aria-label="Choose model"
      onKeyDown={onKeyDown}
      className="absolute bottom-full right-0 z-20 mb-2 flex w-[400px] max-w-[calc(100vw-2rem)] flex-col rounded-md border border-border-2 bg-bg-raised shadow-[0_12px_32px_rgba(0,0,0,0.45)]"
    >
      <div className="border-b border-border-1 p-2">
        <input
          ref={inputRef}
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-autocomplete="list"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search models and presets…"
          autoComplete="off"
          spellCheck={false}
          className="h-8 w-full rounded-sm bg-bg-canvas px-2.5 text-sm text-text-1 outline-none placeholder:text-text-4"
        />
      </div>
      <div
        ref={listRef}
        role="listbox"
        aria-label="Models"
        className="max-h-[min(60vh,26rem)] overflow-y-auto py-1"
      >
        {models.isPending && (
          <p className="px-3 py-2 text-sm text-text-3">Loading models…</p>
        )}
        {models.isError && (
          <p className="px-3 py-2 text-sm text-text-3">
            Models unavailable. Check the API key in Settings.
          </p>
        )}
        {!models.isPending && !models.isError && flat.length === 0 && (
          <p className="px-3 py-2 text-sm text-text-3">No matches.</p>
        )}
        {sections.map((section) => (
          <div key={section.label}>
            <p className="px-3 pb-1 pt-2 text-xs uppercase tracking-wide text-text-4">
              {section.label}
            </p>
            {section.items.map((item) => {
              index += 1;
              const rowIndex = index;
              const active = rowIndex === cursor;
              const selected = item.id === value;
              const pinnable = item.kind === "model";
              return (
                <div
                  key={`${section.label}:${item.id}`}
                  data-index={rowIndex}
                  role="option"
                  aria-selected={selected}
                  onMouseEnter={() => setCursor(rowIndex)}
                  onClick={() => onSelect(item.id)}
                  className={`group flex cursor-pointer items-start gap-2 px-3 py-1.5 ${
                    active ? "bg-bg-hover" : ""
                  }`}
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5">
                      <span className="truncate text-sm text-text-1">
                        {item.kind === "model"
                          ? (item.model.name ?? item.id)
                          : item.id}
                      </span>
                      {item.kind === "model" && (
                        <CapabilityBadges model={item.model} />
                      )}
                      {selected && (
                        <Check className="size-3.5 shrink-0 text-accent" />
                      )}
                    </div>
                    <p className="truncate font-mono text-xs text-text-4">
                      {item.kind === "model"
                        ? item.id
                        : (item.description ?? "Preset")}
                    </p>
                  </div>
                  <div className="flex shrink-0 items-center gap-1.5 pt-0.5">
                    {item.kind === "model" && (
                      <span className="text-xs text-text-3">
                        {modelFacts(item.model)}
                      </span>
                    )}
                    {pinnable && (
                      <button
                        type="button"
                        onClick={(event) => {
                          event.stopPropagation();
                          onToggleFavorite(item.id);
                        }}
                        aria-label={
                          favorites.has(item.id) ? "Unpin model" : "Pin model"
                        }
                        className={`rounded-xs p-0.5 transition-colors duration-150 ease-standard ${
                          favorites.has(item.id)
                            ? "text-accent"
                            : "text-text-4 opacity-0 hover:text-text-2 group-hover:opacity-100"
                        }`}
                      >
                        <Star
                          className="size-3.5"
                          fill={favorites.has(item.id) ? "currentColor" : "none"}
                        />
                      </button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}
