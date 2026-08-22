// Chat message rendering (CM11). Assistant turns render markdown through
// Streamdown (Shiki code blocks, KaTeX math, Mermaid diagrams — all
// bundled locally, no CDN), with a reasoning disclosure, a persistent
// metadata line, and hover actions. User turns get copy and edit.

import { code } from "@streamdown/code";
import { math } from "@streamdown/math";
import { mermaid } from "@streamdown/mermaid";
import {
  Check,
  ChevronRight,
  Copy,
  Loader2,
  Pencil,
  RefreshCcw,
} from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Streamdown, type ControlsConfig } from "streamdown";

import type { Model } from "@/lib/api";
import type { ChatMessage } from "@/lib/chatStore";
import { formatCount, formatMs, formatNanoUSD } from "@/lib/format";

const PLUGINS = { code, math, mermaid };

// Downloads stay off: the console is a gateway surface, not a file
// manager, and copy covers the same need.
const CONTROLS: ControlsConfig = {
  code: { copy: true, download: false },
  table: { copy: true, download: false, fullscreen: false },
  mermaid: { copy: true, download: false, fullscreen: true, panZoom: true },
};

function Markdown({ text, streaming }: { text: string; streaming: boolean }) {
  return (
    <Streamdown
      mode={streaming ? "streaming" : "static"}
      plugins={PLUGINS}
      controls={CONTROLS}
      isAnimating={streaming}
      className="space-y-3 leading-relaxed"
    >
      {text}
    </Streamdown>
  );
}

function ActionIcon({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={label}
      className="rounded-sm p-1.5 text-text-4 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-2"
    >
      {children}
    </button>
  );
}

function CopyAction({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [copied]);
  return (
    <ActionIcon
      label="Copy message"
      onClick={() => {
        void navigator.clipboard.writeText(text).then(() => setCopied(true));
      }}
    >
      {copied ? (
        <Check className="size-3.5 text-success" />
      ) : (
        <Copy className="size-3.5" />
      )}
    </ActionIcon>
  );
}

// RetryMenu offers a plain retry plus retry-with-model for the current
// model and pinned favorites.
function RetryMenu({
  models,
  onRetry,
}: {
  models: string[];
  onRetry: (model?: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (event: MouseEvent) => {
      if (!ref.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <ActionIcon label="Retry" onClick={() => setOpen((value) => !value)}>
        <RefreshCcw className="size-3.5" />
      </ActionIcon>
      {open && (
        <div className="absolute bottom-full left-0 z-20 mb-1 w-64 rounded-md border border-border-2 bg-bg-panel p-1 shadow-lg">
          <button
            type="button"
            onClick={() => {
              setOpen(false);
              onRetry();
            }}
            className="block w-full rounded-sm px-2.5 py-1.5 text-left text-sm text-text-1 hover:bg-bg-hover"
          >
            Retry
          </button>
          {models.length > 0 && (
            <>
              <div className="my-1 border-t border-border-1" />
              <p className="px-2.5 py-1 text-xs uppercase tracking-wide text-text-4">
                Retry with
              </p>
              {models.map((model) => (
                <button
                  key={model}
                  type="button"
                  onClick={() => {
                    setOpen(false);
                    onRetry(model);
                  }}
                  className="block w-full truncate rounded-sm px-2.5 py-1.5 text-left font-mono text-xs text-text-2 hover:bg-bg-hover hover:text-text-1"
                >
                  {model}
                </button>
              ))}
            </>
          )}
        </div>
      )}
    </div>
  );
}

// ReasoningFold auto-opens while the model is reasoning and collapses
// when the answer starts, unless the user has toggled it by hand
// (legacy behavior).
function ReasoningFold({
  reasoning,
  active,
  reasoningMs,
}: {
  reasoning: string;
  active: boolean;
  reasoningMs?: number;
}) {
  const [open, setOpen] = useState(active);
  const userToggled = useRef(false);

  useEffect(() => {
    if (!userToggled.current) setOpen(active);
  }, [active]);

  const label = active
    ? "Thinking…"
    : reasoningMs !== undefined
      ? `Thought for ${(reasoningMs / 1000).toFixed(1)}s`
      : "Reasoning";

  return (
    <div className="mb-2">
      <button
        type="button"
        onClick={() => {
          userToggled.current = true;
          setOpen((value) => !value);
        }}
        className="flex items-center gap-1 text-sm text-text-3 transition-colors duration-150 ease-standard hover:text-text-2"
      >
        <ChevronRight
          className={`size-3.5 transition-transform duration-150 ${open ? "rotate-90" : ""}`}
        />
        {active && <Loader2 className="size-3 animate-spin" />}
        {label}
      </button>
      {open && (
        <div className="mt-2 whitespace-pre-wrap border-l-2 border-border-2 pl-3 text-sm leading-relaxed text-text-3">
          {reasoning}
        </div>
      )}
    </div>
  );
}

// costText derives a dollar figure from catalog per-token prices and the
// turn's token counts. No price data lives in the frontend (law: catalog
// facts come from the live /models response).
function costText(message: ChatMessage, model: Model | undefined): string | null {
  const stats = message.stats;
  const pricing = model?.pricing;
  if (!stats || !pricing) return null;
  const promptRate = Number.parseFloat(pricing.prompt ?? "");
  const completionRate = Number.parseFloat(pricing.completion ?? "");
  let dollars = 0;
  let known = false;
  if (Number.isFinite(promptRate) && stats.promptTokens !== undefined) {
    dollars += promptRate * stats.promptTokens;
    known = true;
  }
  if (Number.isFinite(completionRate) && stats.completionTokens !== undefined) {
    dollars += completionRate * stats.completionTokens;
    known = true;
  }
  return known ? formatNanoUSD(dollars * 1_000_000_000) : null;
}

// MetadataLine carries the legacy badge set: TTFT, total latency, tok/s,
// token counts, reasoning tokens, cache state, cost, stopped, unenforced.
function MetadataLine({
  message,
  model,
}: {
  message: ChatMessage;
  model: Model | undefined;
}) {
  const stats = message.stats;
  const items: { key: string; text: string; tone?: string }[] = [];
  if (stats?.ttftMs !== undefined) {
    items.push({ key: "ttft", text: `TTFT ${formatMs(stats.ttftMs)}` });
  }
  if (stats?.latencyMs !== undefined) {
    items.push({ key: "total", text: `${formatMs(stats.latencyMs)} total` });
  }
  if (stats?.tps) {
    items.push({ key: "tps", text: `${stats.tps.toFixed(1)} tok/s` });
  }
  if (stats?.promptTokens !== undefined) {
    items.push({ key: "in", text: `↓${formatCount(stats.promptTokens)}` });
  }
  if (stats?.completionTokens !== undefined) {
    items.push({ key: "out", text: `↑${formatCount(stats.completionTokens)}` });
  }
  if (stats?.reasoningTokens) {
    items.push({
      key: "reasoning",
      text: `${formatCount(stats.reasoningTokens)} reasoning`,
    });
  }
  const cost = costText(message, model);
  if (cost) items.push({ key: "cost", text: cost });
  if (stats?.cache) {
    items.push({
      key: "cache",
      text: `cache ${stats.cache.toLowerCase()}${stats.cacheAge ? ` · ${stats.cacheAge}` : ""}`,
      tone: stats.cache.toUpperCase() === "HIT" ? "text-success" : undefined,
    });
  }
  if (message.stopped) {
    items.push({ key: "stopped", text: "stopped", tone: "text-warning" });
  }
  if (stats?.unenforced) {
    items.push({
      key: "unenforced",
      text: `unenforced: ${stats.unenforced}`,
      tone: "text-warning",
    });
  }
  if (!items.length) return null;
  return (
    <p className="mt-2 flex flex-wrap gap-x-3 gap-y-0.5 font-mono text-xs text-text-4">
      {items.map((item) => (
        <span key={item.key} className={item.tone}>
          {item.text}
        </span>
      ))}
    </p>
  );
}

export function AssistantMessage({
  message,
  streaming,
  liveStartedAt,
  modelRecord,
  retryModels,
  onRetry,
}: {
  message: ChatMessage;
  // This message is receiving stream deltas right now.
  streaming: boolean;
  // performance.now() when the first content token arrived; drives the
  // live ~tok/s estimate (chars/4, legacy heuristic).
  liveStartedAt?: number;
  modelRecord?: Model;
  retryModels: string[];
  onRetry?: (model?: string) => void;
}) {
  let liveTps: number | null = null;
  if (streaming && liveStartedAt !== undefined && message.content) {
    const seconds = (performance.now() - liveStartedAt) / 1000;
    if (seconds > 0.5) {
      liveTps = Math.round(message.content.length / 4 / seconds);
    }
  }
  const reasoningActive = streaming && !message.content && !message.error;

  return (
    <div className="group text-base text-text-1">
      {(message.model || liveTps !== null) && (
        <p className="mb-1 flex items-center gap-2 font-mono text-xs text-text-4">
          {message.model && <span>{message.model}</span>}
          {liveTps !== null && <span>~{liveTps} tok/s</span>}
        </p>
      )}
      {message.reasoning && (
        <ReasoningFold
          reasoning={message.reasoning}
          active={reasoningActive}
          reasoningMs={message.reasoningMs}
        />
      )}
      {message.error ? (
        <p className="text-sm text-error">{message.content || "Request failed."}</p>
      ) : message.content ? (
        <Markdown text={message.content} streaming={streaming} />
      ) : !message.reasoning ? (
        <p className="text-text-3">Thinking…</p>
      ) : null}
      {!message.error && <MetadataLine message={message} model={modelRecord} />}
      {!streaming && (
        <div className="mt-1 flex items-center gap-0.5 opacity-0 transition-opacity duration-150 group-focus-within:opacity-100 group-hover:opacity-100">
          {message.content && !message.error && <CopyAction text={message.content} />}
          {onRetry && <RetryMenu models={retryModels} onRetry={onRetry} />}
        </div>
      )}
    </div>
  );
}

export function UserMessage({
  message,
  onEdit,
}: {
  message: ChatMessage;
  // Present when editing is allowed; submits the revised text, which
  // truncates the conversation at this turn and resends.
  onEdit?: (text: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(message.content);

  const submit = () => {
    const clean = text.trim();
    if (!clean) return;
    setEditing(false);
    onEdit?.(clean);
  };

  if (editing) {
    return (
      <div className="flex justify-end">
        <div className="w-full max-w-[85%]">
          <textarea
            autoFocus
            value={text}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                submit();
              }
              if (event.key === "Escape") {
                event.preventDefault();
                setEditing(false);
              }
            }}
            rows={Math.min(8, Math.max(2, text.split("\n").length))}
            className="w-full resize-y rounded-xl border border-border-2 bg-bg-raised px-4 py-2.5 text-base text-text-1 placeholder:text-text-4"
          />
          <div className="mt-1 flex justify-end gap-2">
            <button
              type="button"
              onClick={() => setEditing(false)}
              className="rounded-sm px-2.5 py-1 text-sm text-text-3 hover:bg-bg-hover hover:text-text-1"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={submit}
              disabled={!text.trim()}
              className="rounded-sm bg-accent px-2.5 py-1 text-sm font-medium text-accent-ink hover:bg-accent-hover disabled:opacity-50"
            >
              Send
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="group flex flex-col items-end">
      <div className="max-w-[85%] whitespace-pre-wrap rounded-xl bg-bg-raised px-4 py-2.5 text-base text-text-1">
        {message.content}
      </div>
      <div className="mt-1 flex items-center gap-0.5 opacity-0 transition-opacity duration-150 group-focus-within:opacity-100 group-hover:opacity-100">
        <CopyAction text={message.content} />
        {onEdit && (
          <ActionIcon
            label="Edit message"
            onClick={() => {
              setText(message.content);
              setEditing(true);
            }}
          >
            <Pencil className="size-3.5" />
          </ActionIcon>
        )}
      </div>
    </div>
  );
}
