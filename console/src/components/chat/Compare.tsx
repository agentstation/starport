// Comparison mode (CM12): attach two to four models as chips beside the
// model picker, fan one prompt out to all of them, and race the answers
// in side-by-side columns. Comparisons are ephemeral — they stay on the
// page and are never written to localStorage. Each column gets its own
// stop, retry, and stats line, plus a "continue with this model" action
// that collapses the run into a normal saved conversation.

import { ArrowRight, RefreshCcw, Square } from "lucide-react";
import { useRef, useState } from "react";

import {
  Markdown,
  MetadataLine,
  ReasoningFold,
} from "@/components/chat/Messages";
import { ApiError, streamChat, type Model } from "@/lib/api";
import {
  providerPreferences,
  statsFromUsage,
  type ChatMessage,
  type ChatParams,
} from "@/lib/chatStore";

export const MIN_COMPARE = 2;
export const MAX_COMPARE = 4;

export type CompareColumn = {
  // The model id the user attached; the routed model lands on
  // message.model when the stream resolves it.
  model: string;
  message: ChatMessage;
  streaming: boolean;
  // performance.now() when the first content token arrived; drives the
  // live ~tok/s estimate while streaming.
  contentStartAt?: number;
};

export type CompareState = {
  active: boolean;
  models: string[];
  columns: CompareColumn[] | null;
  prompt: string;
  streaming: boolean;
  toggle: (seedModel?: string) => void;
  exit: () => void;
  add: (id: string) => void;
  remove: (id: string) => void;
  send: (text: string, params: ChatParams) => void;
  stop: (index: number) => void;
  stopAll: () => void;
  retry: (index: number) => void;
  continueWith: (index: number) => void;
};

function compareErrorText(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  return error instanceof Error ? error.message : "Request failed.";
}

// useCompare owns the whole comparison lifecycle. onContinue receives
// the chosen column's exchange so the chat page can seed a saved
// conversation from it; the hook exits compare mode first.
export function useCompare({
  onContinue,
}: {
  onContinue: (model: string, prompt: string, message: ChatMessage) => void;
}): CompareState {
  const [active, setActive] = useState(false);
  const [models, setModels] = useState<string[]>([]);
  const [columns, setColumns] = useState<CompareColumn[] | null>(null);
  const [prompt, setPrompt] = useState("");

  const abortsRef = useRef<(AbortController | null)[]>([]);
  // The request body shared by every column (everything but "model"),
  // frozen at send time so a per-column retry replays the same request.
  const baseBodyRef = useRef<Record<string, unknown>>({});

  const streaming = columns?.some((column) => column.streaming) ?? false;

  const patch = (
    index: number,
    mutate: (column: CompareColumn) => CompareColumn,
  ) => {
    setColumns((previous) =>
      previous
        ? previous.map((column, i) => (i === index ? mutate(column) : column))
        : previous,
    );
  };

  const patchMessage = (
    index: number,
    mutate: (message: ChatMessage) => ChatMessage,
  ) => {
    patch(index, (column) => ({ ...column, message: mutate(column.message) }));
  };

  // runColumn streams one model's answer into column `index`. Mirrors
  // the single-chat stream lifecycle: abort keeps partials marked
  // stopped, errors mark the column failed, success records stats.
  const runColumn = (
    index: number,
    body: Record<string, unknown>,
    controller: AbortController,
  ) => {
    const startedAt = performance.now();
    let firstTokenAt: number | undefined;
    let reasoningStartAt: number | undefined;
    let sawContent = false;

    streamChat(body, {
      signal: controller.signal,
      onDelta: (delta) => {
        const now = performance.now();
        if (firstTokenAt === undefined) firstTokenAt = now;
        if (!sawContent) {
          sawContent = true;
          patch(index, (column) => ({ ...column, contentStartAt: now }));
          if (reasoningStartAt !== undefined) {
            const reasoningMs = now - reasoningStartAt;
            patchMessage(index, (message) => ({ ...message, reasoningMs }));
          }
        }
        patchMessage(index, (message) => ({
          ...message,
          content: message.content + delta,
        }));
      },
      onReasoning: (delta) => {
        const now = performance.now();
        if (firstTokenAt === undefined) firstTokenAt = now;
        if (reasoningStartAt === undefined) reasoningStartAt = now;
        patchMessage(index, (message) => ({
          ...message,
          reasoning: (message.reasoning ?? "") + delta,
        }));
      },
    })
      .then((meta) => {
        const latencyMs = performance.now() - startedAt;
        const stats = statsFromUsage(
          meta.usage,
          {
            ttftMs:
              firstTokenAt === undefined ? undefined : firstTokenAt - startedAt,
            latencyMs,
          },
          meta,
        );
        const reasoningMs =
          reasoningStartAt !== undefined && !sawContent
            ? performance.now() - reasoningStartAt
            : undefined;
        patchMessage(index, (message) => ({
          ...message,
          model: meta.model || undefined,
          stats,
          ...(reasoningMs !== undefined ? { reasoningMs } : {}),
        }));
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) {
          const reasoningMs =
            reasoningStartAt !== undefined && !sawContent
              ? performance.now() - reasoningStartAt
              : undefined;
          patchMessage(index, (message) => ({
            ...message,
            stopped: true,
            ...(reasoningMs !== undefined ? { reasoningMs } : {}),
          }));
          return;
        }
        patchMessage(index, (message) => ({
          ...message,
          content: compareErrorText(error),
          error: true,
        }));
      })
      .finally(() => {
        if (abortsRef.current[index] === controller) {
          abortsRef.current[index] = null;
        }
        patch(index, (column) => ({ ...column, streaming: false }));
      });
  };

  const stopAll = () => {
    for (const controller of abortsRef.current) controller?.abort();
  };

  const exit = () => {
    stopAll();
    abortsRef.current = [];
    setActive(false);
    setModels([]);
    setColumns(null);
    setPrompt("");
  };

  const toggle = (seedModel?: string) => {
    if (active) {
      exit();
      return;
    }
    setActive(true);
    setModels(seedModel ? [seedModel] : []);
  };

  const add = (id: string) => {
    setModels((previous) => {
      if (previous.includes(id)) return previous;
      if (previous.length >= MAX_COMPARE) return previous;
      return [...previous, id];
    });
  };

  const remove = (id: string) => {
    if (streaming) return;
    setModels((previous) => previous.filter((model) => model !== id));
  };

  const send = (text: string, params: ChatParams) => {
    if (streaming || models.length < MIN_COMPARE) return;

    const messages: { role: string; content: string }[] = [];
    const system = params.system.trim();
    if (system) messages.push({ role: "system", content: system });
    messages.push({ role: "user", content: text });

    const base: Record<string, unknown> = { messages };
    if (params.temperature !== null) base.temperature = params.temperature;
    if (params.maxTokens !== null) base.max_tokens = params.maxTokens;
    const provider = providerPreferences(params);
    if (provider) base.provider = provider;
    baseBodyRef.current = base;

    setPrompt(text);
    setColumns(
      models.map((model) => ({
        model,
        message: { role: "assistant", content: "" },
        streaming: true,
      })),
    );
    abortsRef.current = models.map(() => new AbortController());
    models.forEach((model, index) => {
      runColumn(
        index,
        { ...base, model },
        abortsRef.current[index] as AbortController,
      );
    });
  };

  const stop = (index: number) => {
    abortsRef.current[index]?.abort();
  };

  const retry = (index: number) => {
    const column = columns?.[index];
    if (!column || column.streaming) return;
    const controller = new AbortController();
    abortsRef.current[index] = controller;
    patch(index, (current) => ({
      model: current.model,
      message: { role: "assistant", content: "" },
      streaming: true,
    }));
    runColumn(index, { ...baseBodyRef.current, model: column.model }, controller);
  };

  const continueWith = (index: number) => {
    const column = columns?.[index];
    if (!column || column.streaming || column.message.error) return;
    if (!column.message.content) return;
    const chosenPrompt = prompt;
    const { model, message } = column;
    exit();
    onContinue(model, chosenPrompt, message);
  };

  return {
    active,
    models,
    columns,
    prompt,
    streaming,
    toggle,
    exit,
    add,
    remove,
    send,
    stop,
    stopAll,
    retry,
    continueWith,
  };
}

function ColumnCard({
  column,
  modelRecord,
  onStop,
  onRetry,
  onContinue,
}: {
  column: CompareColumn;
  modelRecord: Model | undefined;
  onStop: () => void;
  onRetry: () => void;
  onContinue: () => void;
}) {
  const { message, streaming } = column;

  let liveTps: number | null = null;
  if (streaming && column.contentStartAt !== undefined && message.content) {
    const seconds = (performance.now() - column.contentStartAt) / 1000;
    if (seconds > 0.5) {
      liveTps = Math.round(message.content.length / 4 / seconds);
    }
  }

  // "via <provider>" once routing resolves; the requested id may be a
  // preset or differ from the provider that actually served the run.
  const routed = message.model ?? "";
  const slash = routed.indexOf("/");
  const via = slash > 0 ? routed.slice(0, slash) : "";

  const reasoningActive = streaming && !message.content && !message.error;
  const done = !streaming;
  const canContinue = done && Boolean(message.content) && !message.error;

  return (
    <div className="flex min-w-0 flex-col rounded-md border border-border-1 bg-bg-panel">
      <div className="flex items-center gap-2 border-b border-border-1 px-3 py-2">
        <span className="truncate font-mono text-xs text-text-2" title={column.model}>
          {column.model}
        </span>
        {via && <span className="shrink-0 font-mono text-xs text-text-4">via {via}</span>}
        {liveTps !== null && (
          <span className="shrink-0 font-mono text-xs text-text-4">~{liveTps} tok/s</span>
        )}
        <div className="ml-auto flex shrink-0 items-center gap-0.5">
          {streaming ? (
            <button
              type="button"
              onClick={onStop}
              aria-label={`Stop ${column.model}`}
              title="Stop"
              className="flex size-6 items-center justify-center rounded-sm text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
            >
              <Square className="size-3" fill="currentColor" />
            </button>
          ) : (
            <button
              type="button"
              onClick={onRetry}
              aria-label={`Retry ${column.model}`}
              title="Retry"
              className="flex size-6 items-center justify-center rounded-sm text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
            >
              <RefreshCcw className="size-3" />
            </button>
          )}
        </div>
      </div>
      <div className="min-w-0 flex-1 px-3 py-3 text-sm text-text-1">
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
        ) : streaming && !message.reasoning ? (
          <p className="text-text-3">Thinking…</p>
        ) : !streaming && !message.reasoning ? (
          <p className="text-text-3">The model returned no content.</p>
        ) : null}
        {!message.error && <MetadataLine message={message} model={modelRecord} />}
        {message.error && (
          <p className="mt-2 font-mono text-xs text-error">failed</p>
        )}
      </div>
      {canContinue && (
        <div className="border-t border-border-1 px-3 py-2">
          <button
            type="button"
            onClick={onContinue}
            className="flex items-center gap-1.5 text-xs text-text-3 transition-colors duration-150 ease-standard hover:text-text-1"
          >
            Continue with this model
            <ArrowRight className="size-3" />
          </button>
        </div>
      )}
    </div>
  );
}

// CompareView fills the main chat area while compare mode is active:
// a welcome explainer before the first send, then the prompt echo and
// the responsive column grid.
export function CompareView({
  compare,
  models,
}: {
  compare: CompareState;
  models: Model[] | undefined;
}) {
  if (!compare.columns) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center px-4">
        <div className="max-w-md text-center">
          <h1 className="mb-3 text-2xl font-semibold tracking-[-0.01em]">
            Compare models
          </h1>
          <p className="text-sm leading-relaxed text-text-3">
            Pick two to four models, then send one prompt to race them side
            by side. Comparisons stay on this page and are not saved.
          </p>
          <p className="mt-3 font-mono text-xs text-text-4">
            {compare.models.length}/{MAX_COMPARE} models attached
            {compare.models.length < MIN_COMPARE
              ? ` — add ${MIN_COMPARE - compare.models.length} more to start`
              : ""}
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto flex max-w-[1440px] flex-col gap-4 px-4 py-6">
        <div className="ml-auto max-w-[70%] rounded-lg bg-bg-raised px-4 py-2.5 text-base text-text-1 whitespace-pre-wrap">
          {compare.prompt}
        </div>
        <div
          className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:[grid-template-columns:repeat(var(--compare-cols),minmax(0,1fr))]"
          style={{ "--compare-cols": String(compare.columns.length) } as React.CSSProperties}
        >
          {compare.columns.map((column, index) => (
            <ColumnCard
              key={`${column.model}-${index}`}
              column={column}
              modelRecord={models?.find(
                (candidate) =>
                  candidate.id === (column.message.model ?? column.model),
              )}
              onStop={() => compare.stop(index)}
              onRetry={() => compare.retry(index)}
              onContinue={() => compare.continueWith(index)}
            />
          ))}
        </div>
      </div>
    </div>
  );
}
