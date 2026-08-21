import { useQuery } from "@tanstack/react-query";
import {
  ArrowUp,
  Brain,
  ChevronDown,
  Plus,
  SlidersHorizontal,
  Square,
} from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";

import {
  ModelPicker,
  supportsReasoning,
} from "@/components/chat/ModelPicker";
import { INPUT_CLASS, TEXTAREA_CLASS } from "@/components/ui/Form";
import { listModels, listPresets } from "@/lib/api";
import type { ChatParams } from "@/lib/chatStore";

// Composer is the DESIGN.md chat input card: one rounded surface with
// the textarea on top and a control bar below — plus-menu left; model
// picker, effort selector, params, and send/stop right. Enter sends,
// Shift+Enter inserts a newline, Escape stops a stream.

const MAX_TEXTAREA_HEIGHT = 240;

const EFFORT_CHOICES = [
  { value: "", label: "Default effort" },
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium" },
  { value: "high", label: "High" },
];

function shortModelName(model: string): string {
  if (!model) return "Choose model";
  if (model.startsWith("@preset/")) return model;
  const slash = model.indexOf("/");
  return slash >= 0 ? model.slice(slash + 1) : model;
}

function BarButton({
  onClick,
  label,
  active,
  children,
}: {
  onClick: () => void;
  label: string;
  active?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={label}
      className={`flex h-8 items-center gap-1 rounded-sm px-2 text-sm transition-colors duration-150 ease-standard ${
        active
          ? "bg-bg-hover text-text-1"
          : "text-text-3 hover:bg-bg-hover hover:text-text-2"
      }`}
    >
      {children}
    </button>
  );
}

// Popover anchors a panel above the control bar (the composer sits at
// the bottom of the page, so everything opens upward).
function Popover({
  onClose,
  label,
  children,
  wide,
}: {
  onClose: () => void;
  label: string;
  children: ReactNode;
  wide?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      if (!ref.current?.contains(event.target as Node)) onClose();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [onClose]);
  return (
    <div
      ref={ref}
      role="dialog"
      aria-label={label}
      className={`absolute bottom-full right-0 z-20 mb-2 rounded-md border border-border-2 bg-bg-raised p-3 shadow-[0_12px_32px_rgba(0,0,0,0.45)] ${
        wide ? "w-80" : "w-56"
      }`}
    >
      {children}
    </div>
  );
}

function ParamsPopover({
  params,
  onChange,
  onClose,
}: {
  params: ChatParams;
  onChange: (next: ChatParams) => void;
  onClose: () => void;
}) {
  const field = (label: string, control: ReactNode) => (
    <label className="block">
      <span className="mb-1 block text-xs text-text-3">{label}</span>
      {control}
    </label>
  );
  return (
    <Popover onClose={onClose} label="Request parameters" wide>
      <div className="flex flex-col gap-3">
        {field(
          "System prompt",
          <textarea
            value={params.system}
            onChange={(event) => onChange({ ...params, system: event.target.value })}
            rows={3}
            className={`${TEXTAREA_CLASS} w-full text-sm`}
          />,
        )}
        <div className="grid grid-cols-2 gap-3">
          {field(
            "Temperature",
            <input
              type="number"
              step="0.1"
              min="0"
              max="2"
              value={params.temperature ?? ""}
              onChange={(event) =>
                onChange({
                  ...params,
                  temperature:
                    event.target.value === ""
                      ? null
                      : Number(event.target.value),
                })
              }
              placeholder="default"
              className={`${INPUT_CLASS} w-full`}
            />,
          )}
          {field(
            "Max tokens",
            <input
              type="number"
              min="1"
              value={params.maxTokens ?? ""}
              onChange={(event) =>
                onChange({
                  ...params,
                  maxTokens:
                    event.target.value === ""
                      ? null
                      : Number(event.target.value),
                })
              }
              placeholder="default"
              className={`${INPUT_CLASS} w-full`}
            />,
          )}
        </div>
        <p className="text-xs text-text-4">
          Provider routing — comma-separated provider IDs.
        </p>
        <div className="grid grid-cols-2 gap-3">
          {field(
            "Order",
            <input
              type="text"
              value={params.order}
              onChange={(event) => onChange({ ...params, order: event.target.value })}
              placeholder="groq, openai"
              className={`${INPUT_CLASS} w-full font-mono text-xs`}
            />,
          )}
          {field(
            "Only",
            <input
              type="text"
              value={params.only}
              onChange={(event) => onChange({ ...params, only: event.target.value })}
              className={`${INPUT_CLASS} w-full font-mono text-xs`}
            />,
          )}
          {field(
            "Ignore",
            <input
              type="text"
              value={params.ignore}
              onChange={(event) => onChange({ ...params, ignore: event.target.value })}
              className={`${INPUT_CLASS} w-full font-mono text-xs`}
            />,
          )}
          {field(
            "Sort",
            <select
              value={params.sort}
              onChange={(event) => onChange({ ...params, sort: event.target.value })}
              className={`${INPUT_CLASS} w-full`}
            >
              <option value="">default</option>
              <option value="price">price</option>
              <option value="throughput">throughput</option>
              <option value="latency">latency</option>
            </select>,
          )}
        </div>
      </div>
    </Popover>
  );
}

export function Composer({
  draft,
  onDraftChange,
  onSend,
  streaming,
  onStop,
  model,
  onModelChange,
  favorites,
  onToggleFavorite,
  params,
  onParamsChange,
  pickerOpen,
  onPickerOpenChange,
  autoFocus,
}: {
  draft: string;
  onDraftChange: (next: string) => void;
  onSend: (text: string) => void;
  streaming: boolean;
  onStop: () => void;
  model: string;
  onModelChange: (next: string) => void;
  favorites: Set<string>;
  onToggleFavorite: (id: string) => void;
  params: ChatParams;
  onParamsChange: (next: ChatParams) => void;
  pickerOpen: boolean;
  onPickerOpenChange: (open: boolean) => void;
  autoFocus?: boolean;
}) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [menu, setMenu] = useState<"none" | "plus" | "effort" | "params">("none");

  const models = useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    staleTime: 60_000,
    retry: false,
  });
  const presets = useQuery({
    queryKey: ["presets"],
    queryFn: listPresets,
    staleTime: 60_000,
    retry: false,
  });

  const selectedModel = models.data?.find((entry) => entry.id === model);
  const reasoning = supportsReasoning(selectedModel);
  const effort = params.effort ?? "";

  // Auto-grow the textarea between one and ~10 rows.
  useEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    textarea.style.height = "auto";
    textarea.style.height = `${Math.min(textarea.scrollHeight, MAX_TEXTAREA_HEIGHT)}px`;
  }, [draft]);

  useEffect(() => {
    if (autoFocus) textareaRef.current?.focus();
  }, [autoFocus]);

  const send = () => {
    const text = draft.trim();
    if (!text || streaming || !model) return;
    onSend(text);
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      send();
    } else if (event.key === "Escape" && streaming) {
      event.preventDefault();
      onStop();
    }
  };

  const canSend = Boolean(draft.trim()) && Boolean(model);

  return (
    <div className="relative rounded-xl border border-border-2 bg-bg-raised">
      <textarea
        ref={textareaRef}
        value={draft}
        onChange={(event) => onDraftChange(event.target.value)}
        onKeyDown={onKeyDown}
        rows={1}
        placeholder="Message the model…"
        aria-label="Message"
        className="w-full resize-none bg-transparent px-4 pb-1 pt-3.5 text-base text-text-1 outline-none placeholder:text-text-4"
      />
      <div className="flex items-center gap-1 px-2 pb-2">
        <div className="relative">
          <BarButton
            onClick={() => setMenu(menu === "plus" ? "none" : "plus")}
            label="Insert"
            active={menu === "plus"}
          >
            <Plus className="size-4" />
          </BarButton>
          {menu === "plus" && (
            <div className="absolute bottom-full left-0 z-20 mb-2 w-64 rounded-md border border-border-2 bg-bg-raised py-1 shadow-[0_12px_32px_rgba(0,0,0,0.45)]">
              <p className="px-3 pb-1 pt-1.5 text-xs uppercase tracking-wide text-text-4">
                Presets
              </p>
              {(presets.data ?? []).length === 0 && (
                <p className="px-3 pb-2 text-sm text-text-3">
                  No presets yet. Create one on the Presets page.
                </p>
              )}
              {(presets.data ?? []).map((preset) => (
                <button
                  key={preset.name}
                  type="button"
                  onClick={() => {
                    onModelChange(`@preset/${preset.name}`);
                    setMenu("none");
                  }}
                  className="block w-full px-3 py-1.5 text-left font-mono text-xs text-text-2 hover:bg-bg-hover hover:text-text-1"
                >
                  @preset/{preset.name}
                </button>
              ))}
            </div>
          )}
        </div>

        <div className="ml-auto flex items-center gap-1">
          <div className="relative">
            <BarButton
              onClick={() => onPickerOpenChange(!pickerOpen)}
              label="Choose model (⌘K)"
              active={pickerOpen}
            >
              <span className="max-w-48 truncate">{shortModelName(model)}</span>
              <ChevronDown className="size-3.5" />
            </BarButton>
            {pickerOpen && (
              <ModelPicker
                value={model}
                favorites={favorites}
                onToggleFavorite={onToggleFavorite}
                onSelect={(id) => {
                  onModelChange(id);
                  onPickerOpenChange(false);
                  textareaRef.current?.focus();
                }}
                onClose={() => {
                  onPickerOpenChange(false);
                  textareaRef.current?.focus();
                }}
              />
            )}
          </div>

          {reasoning && (
            <div className="relative">
              <BarButton
                onClick={() => setMenu(menu === "effort" ? "none" : "effort")}
                label="Reasoning effort"
                active={menu === "effort"}
              >
                <Brain className="size-4" />
                {effort && <span className="capitalize">{effort}</span>}
              </BarButton>
              {menu === "effort" && (
                <Popover
                  onClose={() => setMenu("none")}
                  label="Reasoning effort"
                >
                  <div className="flex flex-col">
                    {EFFORT_CHOICES.map((choice) => (
                      <button
                        key={choice.value}
                        type="button"
                        onClick={() => {
                          onParamsChange({ ...params, effort: choice.value });
                          setMenu("none");
                        }}
                        className={`rounded-sm px-2 py-1.5 text-left text-sm transition-colors duration-150 ease-standard hover:bg-bg-hover ${
                          effort === choice.value ? "text-text-1" : "text-text-3"
                        }`}
                      >
                        {choice.label}
                      </button>
                    ))}
                  </div>
                </Popover>
              )}
            </div>
          )}

          <div className="relative">
            <BarButton
              onClick={() => setMenu(menu === "params" ? "none" : "params")}
              label="Request parameters"
              active={menu === "params"}
            >
              <SlidersHorizontal className="size-4" />
            </BarButton>
            {menu === "params" && (
              <ParamsPopover
                params={params}
                onChange={onParamsChange}
                onClose={() => setMenu("none")}
              />
            )}
          </div>

          {streaming ? (
            <button
              type="button"
              onClick={onStop}
              aria-label="Stop generating"
              className="flex size-8 items-center justify-center rounded-sm bg-bg-hover text-text-1 transition-colors duration-150 ease-standard hover:bg-border-2"
            >
              <Square className="size-3.5" fill="currentColor" />
            </button>
          ) : (
            <button
              type="button"
              onClick={send}
              disabled={!canSend}
              aria-label="Send message"
              className={`flex size-8 items-center justify-center rounded-sm transition-colors duration-150 ease-standard ${
                canSend
                  ? "bg-accent text-white hover:opacity-90"
                  : "bg-bg-hover text-text-4"
              }`}
            >
              <ArrowUp className="size-4" />
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
