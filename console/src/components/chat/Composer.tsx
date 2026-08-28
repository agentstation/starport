import { useQuery } from "@tanstack/react-query";
import {
  ArrowUp,
  AudioLines,
  Brain,
  ChevronDown,
  Columns2,
  FileText,
  Plus,
  SlidersHorizontal,
  Square,
  X,
} from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";

import { ModelPicker, supportsReasoning } from "@/components/chat/ModelPicker";
import { INPUT_CLASS, TEXTAREA_CLASS } from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { listModels } from "@/lib/api";
import {
  ATTACHMENT_ACCEPT,
  ATTACHMENT_KINDS,
  modelAccepts,
  readAttachment,
} from "@/lib/attachments";
import type { Attachment, AttachmentKind } from "@/lib/attachments";
import type { ChatParams } from "@/lib/chatStore";

// Composer is the DESIGN.md chat input card: one rounded surface with
// the textarea on top and a control bar below — attach button left;
// model picker, effort selector, params, and send/stop right. Presets
// live in the model picker, not here. Enter sends, Shift+Enter inserts
// a newline, Escape stops a stream.

const MAX_TEXTAREA_HEIGHT = 240;
const MAX_ATTACHMENTS = 4;

// ATTACHMENT_LABELS names each control. A disabled control states why it
// is disabled, because the two reasons ask for different things from the
// reader: a compare session has to end, and an unsupported kind needs
// another model.
const ATTACHMENT_LABELS: Record<
  AttachmentKind,
  { button: string; input: string; compare: string; unsupported: string }
> = {
  image: {
    button: "Attach image",
    input: "Attach images",
    compare: "Image attachments are unavailable in compare mode",
    unsupported: "This model does not accept image input",
  },
  audio: {
    button: "Attach audio",
    input: "Attach audio files",
    compare: "Audio attachments are unavailable in compare mode",
    unsupported: "This model does not accept audio input",
  },
  document: {
    button: "Attach document",
    input: "Attach documents",
    compare: "Document attachments are unavailable in compare mode",
    unsupported: "This model does not accept document input",
  },
};

const ATTACHMENT_ICONS: Record<AttachmentKind, typeof Plus> = {
  image: Plus,
  audio: AudioLines,
  document: FileText,
};

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
  disabled,
  children,
}: {
  onClick: () => void;
  label: string;
  active?: boolean;
  disabled?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={label}
      disabled={disabled}
      className={`flex h-8 items-center gap-1 rounded-sm px-2 text-sm transition-colors duration-150 ease-standard ${
        active
          ? "bg-bg-hover text-text-1"
          : "text-text-3 hover:bg-bg-hover hover:text-text-2"
      } disabled:cursor-not-allowed disabled:text-text-4 disabled:hover:bg-transparent`}
    >
      {children}
    </button>
  );
}

// AttachControl is one attachment kind: a hidden file input and the bar
// button that opens it. Each kind owns its own input so the picker filter
// matches the control the reader pressed.
function AttachControl({
  kind,
  enabled,
  compareActive,
  onFiles,
}: {
  kind: AttachmentKind;
  enabled: boolean;
  compareActive: boolean;
  onFiles: (files: FileList | null) => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const labels = ATTACHMENT_LABELS[kind];
  const Icon = ATTACHMENT_ICONS[kind];
  return (
    <>
      <input
        ref={inputRef}
        type="file"
        accept={ATTACHMENT_ACCEPT[kind]}
        multiple
        aria-label={labels.input}
        className="hidden"
        onChange={(event) => {
          onFiles(event.target.files);
          event.target.value = "";
        }}
      />
      <BarButton
        onClick={() => inputRef.current?.click()}
        label={
          enabled
            ? labels.button
            : compareActive
              ? labels.compare
              : labels.unsupported
        }
        disabled={!enabled}
      >
        <Icon className="size-4" />
      </BarButton>
    </>
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
            <Select
              value={params.sort}
              onChange={(event) => onChange({ ...params, sort: event.target.value })}
              className="w-full"
            >
              <option value="">default</option>
              <option value="price">price</option>
              <option value="throughput">throughput</option>
              <option value="latency">latency</option>
            </Select>,
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
  compareActive,
  compareModels,
  onCompareToggle,
  onCompareAdd,
  onCompareRemove,
}: {
  draft: string;
  onDraftChange: (next: string) => void;
  onSend: (text: string, attachments?: Attachment[]) => void;
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
  // Compare mode (CM12): when active, the picker attaches models as
  // chips beside its trigger instead of switching the session model.
  compareActive?: boolean;
  compareModels?: string[];
  onCompareToggle?: () => void;
  onCompareAdd?: (id: string) => void;
  onCompareRemove?: (id: string) => void;
}) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [menu, setMenu] = useState<"none" | "effort" | "params">("none");
  // Attached media; it ships with the next send.
  const [attachments, setAttachments] = useState<Attachment[]>([]);

  const models = useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    staleTime: 60_000,
    retry: false,
  });

  const selectedModel = models.data?.find((entry) => entry.id === model);
  const reasoning = supportsReasoning(selectedModel);
  const effort = params.effort ?? "";
  // A control is live only for a model that reads its kind. Compare mode
  // sends one text prompt to several models, so it accepts none.
  const accepts = (kind: AttachmentKind) =>
    !compareActive && modelAccepts(selectedModel, kind);
  // A switch to a model that reads less drops what it cannot read, rather
  // than silently sending content the model rejects. The stored draft
  // keeps whatever the new model still accepts.
  const acceptedKinds = ATTACHMENT_KINDS.filter(accepts).join(",");
  useEffect(() => {
    const live = new Set(acceptedKinds ? acceptedKinds.split(",") : []);
    setAttachments((current) => {
      const kept = current.filter((item) => live.has(item.kind));
      return kept.length === current.length ? current : kept;
    });
  }, [acceptedKinds]);

  const attachFiles = (files: FileList | null) => {
    for (const file of [...(files ?? [])].slice(0, MAX_ATTACHMENTS)) {
      void readAttachment(file).then((attachment) => {
        if (!attachment || !accepts(attachment.kind)) return;
        setAttachments((current) =>
          current.length >= MAX_ATTACHMENTS ? current : [...current, attachment],
        );
      });
    }
  };

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

  const compareCount = compareModels?.length ?? 0;

  const send = () => {
    const text = draft.trim();
    if (!text || streaming) return;
    if (compareActive ? compareCount < 2 : !model) return;
    onSend(text, attachments.length ? attachments : undefined);
    setAttachments([]);
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

  const canSend =
    Boolean(draft.trim()) &&
    (compareActive ? compareCount >= 2 : Boolean(model));

  return (
    <div className="relative rounded-xl border border-border-2 bg-bg-raised">
      <textarea
        ref={textareaRef}
        value={draft}
        onChange={(event) => onDraftChange(event.target.value)}
        onKeyDown={onKeyDown}
        rows={1}
        placeholder={compareActive ? "Message every attached model…" : "Message the model…"}
        aria-label="Message"
        className="w-full resize-none bg-transparent px-4 pb-1 pt-3.5 text-base text-text-1 outline-none placeholder:text-text-4"
      />
      {attachments.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 px-3 pb-1">
          {attachments.map((attachment, index) => (
            <span
              key={`${index}-${attachment.url.slice(-24)}`}
              className="relative inline-flex items-center overflow-hidden rounded-md border border-border-2"
            >
              {attachment.kind === "image" ? (
                <img
                  src={attachment.url}
                  alt={`Attachment ${index + 1}`}
                  className="size-14 object-cover"
                />
              ) : (
                // A sound file and a document have nothing to show, so the
                // chip carries the name the reader picked the file by.
                <span className="flex h-14 items-center gap-1.5 bg-bg-panel pl-2 pr-6 text-xs text-text-2">
                  {attachment.kind === "audio" ? (
                    <AudioLines className="size-3.5 shrink-0 text-text-4" />
                  ) : (
                    <FileText className="size-3.5 shrink-0 text-text-4" />
                  )}
                  <span className="max-w-32 truncate">{attachment.name}</span>
                </span>
              )}
              <button
                type="button"
                onClick={() =>
                  setAttachments((current) =>
                    current.filter((_, position) => position !== index),
                  )
                }
                aria-label={`Remove attachment ${index + 1}`}
                className="absolute right-0.5 top-0.5 rounded-full bg-black/60 p-0.5 text-white transition-colors duration-150 ease-standard hover:bg-black/80"
              >
                <X className="size-3" />
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-1 px-2 pb-2">
        {ATTACHMENT_KINDS.map((kind) => (
          <AttachControl
            key={kind}
            kind={kind}
            enabled={accepts(kind)}
            compareActive={Boolean(compareActive)}
            onFiles={attachFiles}
          />
        ))}

        {onCompareToggle && (
          <BarButton
            onClick={onCompareToggle}
            label={compareActive ? "Exit compare" : "Compare models"}
            active={compareActive}
          >
            <Columns2 className="size-4" />
            {compareActive && <span>Compare</span>}
          </BarButton>
        )}

        <div className="ml-auto flex flex-wrap items-center justify-end gap-1">
          {compareActive &&
            (compareModels ?? []).map((id) => (
              <span
                key={id}
                className="flex h-7 items-center gap-1 rounded-full border border-border-2 bg-bg-panel py-0.5 pl-2.5 pr-1 font-mono text-xs text-text-2"
                title={id}
              >
                <span className="max-w-36 truncate">{shortModelName(id)}</span>
                <button
                  type="button"
                  onClick={() => onCompareRemove?.(id)}
                  disabled={streaming}
                  aria-label={`Remove ${id} from comparison`}
                  className="rounded-full p-0.5 text-text-4 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1 disabled:opacity-40"
                >
                  <X className="size-3" />
                </button>
              </span>
            ))}
          <div className="relative">
            <BarButton
              onClick={() => onPickerOpenChange(!pickerOpen)}
              label={compareActive ? "Add model to comparison" : "Choose model"}
              active={pickerOpen}
            >
              <span className="max-w-48 truncate">
                {compareActive
                  ? `Add model (${compareCount}/4)`
                  : shortModelName(model)}
              </span>
              <ChevronDown className="size-3.5" />
            </BarButton>
            {pickerOpen && (
              <ModelPicker
                value={compareActive ? "" : model}
                favorites={favorites}
                onToggleFavorite={onToggleFavorite}
                onSelect={(id) => {
                  if (compareActive) {
                    // Stay open so a set of models can be attached in
                    // one visit; chips give immediate feedback.
                    onCompareAdd?.(id);
                    return;
                  }
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

          {reasoning && !compareActive && (
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
