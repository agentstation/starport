import { useQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { ArrowDown, PanelLeftClose, PanelLeftOpen, X } from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { CompareView, useCompare } from "@/components/chat/Compare";
import { Composer } from "@/components/chat/Composer";
import { AssistantMessage, UserMessage } from "@/components/chat/Messages";
import { supportsReasoning } from "@/components/chat/ModelPicker";
import { ThreadList } from "@/components/chat/ThreadList";
import {
  ApiError,
  completeChat,
  listModels,
  streamChat,
} from "@/lib/api";
import {
  DEFAULT_PARAMS,
  lastModel,
  loadConversations,
  loadFavorites,
  newConversation,
  providerPreferences,
  rememberModel,
  saveConversations,
  saveFavorites,
  saveSidebarClosed,
  sidebarClosed,
  statsFromUsage,
  type ChatMessage,
  type ChatParams,
  type Conversation,
} from "@/lib/chatStore";

export const Route = createFileRoute("/chat")({
  // /chat?model=… seeds the next conversation's model (Models page link).
  validateSearch: (search: Record<string, unknown>) => ({
    model: typeof search.model === "string" ? search.model : undefined,
  }),
  component: ChatPage,
});

const TITLE_LIMIT = 44;
const STARTERS_KEY = "starport.chatStarters";

const STARTER_PROMPTS = [
  "Compare your routing options: what providers can serve this model?",
  "Summarize the trade-offs between streaming and non-streaming APIs.",
  "Write a curl request for an OpenRouter-compatible chat completion.",
  "Explain what a token is and how context windows work.",
];

function truncateTitle(text: string): string {
  const clean = text.replace(/\s+/g, " ").trim();
  return clean.length > TITLE_LIMIT ? `${clean.slice(0, TITLE_LIMIT)}…` : clean;
}

function errorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.unauthorized) {
      // A 401 can mean the console key is bad or the routed provider
      // has no credentials — show the gateway's own message when it
      // carries one.
      const generic = !error.message || /^401\b/.test(error.message);
      return generic
        ? "The gateway rejected your API key. Update it in Settings."
        : error.message;
    }
    if (error.status === 402) {
      return `${error.message} — check budgets on the Keys page.`;
    }
    if (error.status === 429) {
      return `${error.message} — wait a moment and try again.`;
    }
    return error.message;
  }
  return error instanceof Error ? error.message : "Request failed.";
}

// requestMessages builds the upstream message list: optional system
// prompt plus every prior non-error turn (legacy behavior).
function requestMessages(
  conversation: Conversation,
): { role: string; content: string }[] {
  const messages: { role: string; content: string }[] = [];
  const system = conversation.params.system.trim();
  if (system) messages.push({ role: "system", content: system });
  for (const message of conversation.messages) {
    if (message.error) continue;
    if (!message.content) continue;
    messages.push({ role: message.role, content: message.content });
  }
  return messages;
}

function ChatPage() {
  const navigate = useNavigate();
  const { model: seedModel } = Route.useSearch();

  const [conversations, setConversations] = useState<Conversation[]>(loadConversations);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [favorites, setFavorites] = useState<Set<string>>(loadFavorites);
  const [sidebarOpen, setSidebarOpen] = useState(() => !sidebarClosed());
  const [pickerOpen, setPickerOpen] = useState(false);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [newModel, setNewModel] = useState<string>(() => seedModel ?? lastModel());
  const [newParams, setNewParams] = useState<ChatParams>({ ...DEFAULT_PARAMS });
  const [streamingId, setStreamingId] = useState<string | null>(null);
  const [startersDismissed, setStartersDismissed] = useState(
    () => localStorage.getItem(STARTERS_KEY) === "dismissed",
  );

  // Scroll lock: auto-follow the stream only while the user stays near
  // the bottom; a scroll-to-bottom pill appears once they scroll away.
  const [pinned, setPinned] = useState(true);

  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  // When the first content token of the active stream arrived; drives
  // the live ~tok/s estimate.
  const streamStartRef = useRef<number | null>(null);

  const models = useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    staleTime: 60_000,
    retry: false,
  });

  // Clear the ?model= seed from the URL once captured.
  useEffect(() => {
    if (seedModel) {
      void navigate({ to: "/chat", search: { model: undefined }, replace: true });
    }
  }, [seedModel, navigate]);

  // Fall back to the first catalog model when nothing is remembered.
  useEffect(() => {
    const first = models.data?.[0];
    if (!newModel && first) {
      setNewModel(first.id);
    }
  }, [newModel, models.data]);

  const active = useMemo(
    () => conversations.find((conversation) => conversation.id === activeId) ?? null,
    [conversations, activeId],
  );

  const persist = useCallback((next: Conversation[]) => {
    setConversations(next);
    saveConversations(next);
  }, []);

  const updateConversation = useCallback(
    (id: string, mutate: (conversation: Conversation) => Conversation) => {
      setConversations((previous) => {
        const next = previous.map((conversation) =>
          conversation.id === id ? mutate(conversation) : conversation,
        );
        saveConversations(next);
        return next;
      });
    },
    [],
  );

  // ⌘K / Ctrl+K opens the model picker (DESIGN.md).
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPickerOpen((open) => !open);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  // Opening a thread always starts at the bottom.
  useEffect(() => {
    setPinned(true);
  }, [activeId]);

  // Follow the stream while pinned near the bottom.
  const activeMessages = active?.messages;
  useEffect(() => {
    const el = scrollRef.current;
    if (el && pinned) el.scrollTop = el.scrollHeight;
  }, [activeMessages, activeId, pinned]);

  const scrollToBottom = () => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    setPinned(true);
  };

  const draftKey = activeId ?? "new";
  const draft = drafts[draftKey] ?? "";
  const setDraft = (value: string) =>
    setDrafts((previous) => ({ ...previous, [draftKey]: value }));

  const model = active ? active.model : newModel;
  const params = active ? active.params : newParams;

  const setModel = (next: string) => {
    rememberModel(next);
    if (active) {
      updateConversation(active.id, (conversation) => ({
        ...conversation,
        model: next,
      }));
    } else {
      setNewModel(next);
    }
  };

  const setParams = (next: ChatParams) => {
    if (active) {
      updateConversation(active.id, (conversation) => ({
        ...conversation,
        params: next,
      }));
    } else {
      setNewParams(next);
    }
  };

  const toggleFavorite = (id: string) => {
    setFavorites((previous) => {
      const next = new Set(previous);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      saveFavorites(next);
      return next;
    });
  };

  const stop = () => {
    abortRef.current?.abort();
  };

  // generateTitle asks the conversation's model for a short name after
  // the first exchange. Fire-and-forget; the truncated prompt stands
  // until (and unless) it answers.
  const generateTitle = (conversation: Conversation, userText: string) => {
    completeChat({
      model: conversation.model,
      messages: [
        {
          role: "user",
          content: `Reply with a title of six words or fewer for a conversation that starts with this message. Reply with the title only.\n\n${userText.slice(0, 400)}`,
        },
      ],
      max_tokens: 24,
      temperature: 0.2,
    })
      .then((completion) => {
        const title = completion.choices?.[0]?.message?.content
          ?.replace(/^["'\s]+|["'\s.]+$/g, "")
          .trim();
        if (title) {
          updateConversation(conversation.id, (current) => ({
            ...current,
            title: truncateTitle(title),
          }));
        }
      })
      .catch(() => {
        // Keep the truncated first message as the title.
      });
  };

  // Compare mode (CM12): ephemeral fan-out across 2–4 models. A chosen
  // column collapses into a normal saved conversation via onContinue.
  const compare = useCompare({
    onContinue: (chosenModel, prompt, message) => {
      const conversation = newConversation(chosenModel, truncateTitle(prompt));
      conversation.params = { ...newParams };
      conversation.messages = [{ role: "user", content: prompt }, message];
      persist([conversation, ...conversations]);
      setActiveId(conversation.id);
      rememberModel(chosenModel);
      generateTitle(conversation, prompt);
    },
  });

  const send = (text: string) => {
    if (streamingId || compare.streaming) return;
    if (compare.active) {
      compare.send(text, newParams);
      setDrafts((previous) => ({ ...previous, [draftKey]: "" }));
      return;
    }

    let conversation = active;
    if (!conversation) {
      conversation = newConversation(newModel, truncateTitle(text));
      conversation.params = { ...newParams };
      persist([conversation, ...conversations]);
      setActiveId(conversation.id);
      setNewParams({ ...DEFAULT_PARAMS });
    }
    setDrafts((previous) => ({ ...previous, [draftKey]: "" }));
    sendTo(conversation, text);
  };

  // sendTo streams one exchange into the given conversation snapshot.
  // send, retry, and edit all funnel through here; retry and edit pass
  // a snapshot whose messages were truncated at the redone turn.
  const sendTo = (conversation: Conversation, text: string) => {
    const conversationId = conversation.id;
    const isFirstExchange = conversation.messages.length === 0;
    rememberModel(conversation.model);

    const userMessage: ChatMessage = { role: "user", content: text };
    const placeholder: ChatMessage = { role: "assistant", content: "" };
    updateConversation(conversationId, (current) => ({
      ...current,
      messages: [...current.messages, userMessage, placeholder],
      updatedAt: Date.now(),
    }));
    setPinned(true);

    const body: Record<string, unknown> = {
      model: conversation.model,
      messages: [...requestMessages(conversation), { role: "user", content: text }],
    };
    if (conversation.params.temperature !== null) {
      body.temperature = conversation.params.temperature;
    }
    if (conversation.params.maxTokens !== null) {
      body.max_tokens = conversation.params.maxTokens;
    }
    // A stale effort (set while a reasoning model was selected) must not
    // reach a model that rejects the parameter.
    const effort = conversation.params.effort ?? "";
    const modelRecord = models.data?.find(
      (candidate) => candidate.id === conversation.model,
    );
    if (effort && supportsReasoning(modelRecord)) {
      body.reasoning_effort = effort;
    }
    const provider = providerPreferences(conversation.params);
    if (provider) body.provider = provider;

    const controller = new AbortController();
    abortRef.current = controller;
    setStreamingId(conversationId);

    const startedAt = performance.now();
    let firstTokenAt: number | undefined;
    let reasoningStartAt: number | undefined;
    let sawContent = false;
    streamStartRef.current = null;

    const patchLast = (mutate: (message: ChatMessage) => ChatMessage) => {
      updateConversation(conversationId, (current) => {
        const messages = [...current.messages];
        const last = messages[messages.length - 1];
        if (last?.role === "assistant") {
          messages[messages.length - 1] = mutate(last);
        }
        return { ...current, messages, updatedAt: Date.now() };
      });
    };

    streamChat(body, {
      signal: controller.signal,
      onDelta: (delta) => {
        const now = performance.now();
        if (firstTokenAt === undefined) firstTokenAt = now;
        if (!sawContent) {
          sawContent = true;
          streamStartRef.current = now;
          // The answer started: close out the reasoning stopwatch.
          if (reasoningStartAt !== undefined) {
            const reasoningMs = now - reasoningStartAt;
            patchLast((message) => ({ ...message, reasoningMs }));
          }
        }
        patchLast((message) => ({ ...message, content: message.content + delta }));
      },
      onReasoning: (delta) => {
        const now = performance.now();
        if (firstTokenAt === undefined) firstTokenAt = now;
        if (reasoningStartAt === undefined) reasoningStartAt = now;
        patchLast((message) => ({
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
              firstTokenAt === undefined
                ? undefined
                : firstTokenAt - startedAt,
            latencyMs,
          },
          meta,
        );
        // A reasoning-only completion never hit the onDelta stopwatch.
        const reasoningMs =
          reasoningStartAt !== undefined && !sawContent
            ? performance.now() - reasoningStartAt
            : undefined;
        patchLast((message) => ({
          ...message,
          model: meta.model || undefined,
          stats,
          ...(reasoningMs !== undefined ? { reasoningMs } : {}),
        }));
        if (isFirstExchange) {
          const current = { ...conversation, id: conversationId };
          generateTitle(current, text);
        }
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) {
          if (sawContent || reasoningStartAt !== undefined) {
            // Keep partial output (content or reasoning) marked stopped.
            const reasoningMs =
              reasoningStartAt !== undefined && !sawContent
                ? performance.now() - reasoningStartAt
                : undefined;
            patchLast((message) => ({
              ...message,
              stopped: true,
              ...(reasoningMs !== undefined ? { reasoningMs } : {}),
            }));
          } else {
            // Nothing arrived; drop the empty placeholder.
            updateConversation(conversationId, (current) => ({
              ...current,
              messages: current.messages.filter(
                (message, index) =>
                  !(index === current.messages.length - 1 &&
                    message.role === "assistant" &&
                    !message.content &&
                    !message.reasoning),
              ),
            }));
          }
          return;
        }
        patchLast((message) => ({
          ...message,
          content: errorText(error),
          error: true,
        }));
      })
      .finally(() => {
        if (abortRef.current === controller) abortRef.current = null;
        setStreamingId((current) => (current === conversationId ? null : current));
      });
  };

  // retry redoes the exchange that produced the assistant turn at
  // `index`, optionally on a different model. The turn (and everything
  // after it) is replaced.
  const retry = (index: number, modelOverride?: string) => {
    if (!active || streamingId) return;
    const prior = active.messages[index - 1];
    if (!prior || prior.role !== "user") return;
    const truncated = active.messages.slice(0, index - 1);
    const nextModel = modelOverride ?? active.model;
    updateConversation(active.id, (conversation) => ({
      ...conversation,
      messages: truncated,
      model: nextModel,
    }));
    sendTo({ ...active, messages: truncated, model: nextModel }, prior.content);
  };

  // editMessage resends a revised user turn; the conversation is
  // truncated at that turn.
  const editMessage = (index: number, text: string) => {
    if (!active || streamingId) return;
    const truncated = active.messages.slice(0, index);
    updateConversation(active.id, (conversation) => ({
      ...conversation,
      messages: truncated,
    }));
    sendTo({ ...active, messages: truncated }, text);
  };

  // Retry-with-model options: the thread's model first, then favorites.
  const retryOptions = useMemo(
    () => [...new Set([model, ...favorites])].filter(Boolean),
    [model, favorites],
  );

  const dismissStarters = () => {
    localStorage.setItem(STARTERS_KEY, "dismissed");
    setStartersDismissed(true);
  };

  const composer = (
    <Composer
      draft={draft}
      onDraftChange={setDraft}
      onSend={send}
      streaming={
        compare.active
          ? compare.streaming
          : streamingId !== null && streamingId === (activeId ?? streamingId)
      }
      onStop={compare.active ? compare.stopAll : stop}
      compareActive={compare.active}
      compareModels={compare.models}
      onCompareToggle={() => {
        if (streamingId) return;
        if (!compare.active) setActiveId(null);
        compare.toggle(model);
      }}
      onCompareAdd={compare.add}
      onCompareRemove={compare.remove}
      model={model}
      onModelChange={setModel}
      favorites={favorites}
      onToggleFavorite={toggleFavorite}
      params={params}
      onParamsChange={setParams}
      pickerOpen={pickerOpen}
      onPickerOpenChange={setPickerOpen}
      autoFocus
    />
  );

  return (
    <div className="flex h-screen min-w-0">
      {sidebarOpen && (
        <ThreadList
          conversations={conversations}
          activeId={activeId}
          onOpen={(id) => {
            compare.exit();
            setActiveId(id);
            setPickerOpen(false);
          }}
          onNew={() => {
            compare.exit();
            setActiveId(null);
          }}
          onTogglePin={(id) =>
            updateConversation(id, (conversation) => ({
              ...conversation,
              pinned: !conversation.pinned,
            }))
          }
          onRename={(id, title) =>
            updateConversation(id, (conversation) => ({ ...conversation, title }))
          }
          onDelete={(id) => {
            persist(conversations.filter((conversation) => conversation.id !== id));
            if (activeId === id) setActiveId(null);
          }}
        />
      )}
      <div className="flex h-full min-w-0 flex-1 flex-col">
        <div className="flex h-12 shrink-0 items-center gap-2 px-3">
          <button
            type="button"
            onClick={() => {
              setSidebarOpen((open) => {
                saveSidebarClosed(open);
                return !open;
              });
            }}
            aria-label={sidebarOpen ? "Hide conversations" : "Show conversations"}
            className="rounded-sm p-1.5 text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-2"
          >
            {sidebarOpen ? (
              <PanelLeftClose className="size-4" />
            ) : (
              <PanelLeftOpen className="size-4" />
            )}
          </button>
          {compare.active ? (
            <p className="truncate text-sm text-text-2">Compare models</p>
          ) : (
            active && (
              <p className="truncate text-sm text-text-2">{active.title}</p>
            )
          )}
        </div>

        {compare.active ? (
          <>
            <CompareView compare={compare} models={models.data} />
            <div className="relative mx-auto w-full max-w-[768px] px-4 pb-4">
              {composer}
            </div>
          </>
        ) : active ? (
          <>
            <div
              ref={scrollRef}
              onScroll={(event) => {
                const el = event.currentTarget;
                const nearBottom =
                  el.scrollHeight - el.scrollTop - el.clientHeight < 120;
                setPinned(nearBottom);
              }}
              className="min-h-0 flex-1 overflow-y-auto"
            >
              <div className="mx-auto flex max-w-[768px] flex-col gap-6 px-4 py-6">
                {active.messages.map((message, index) =>
                  message.role === "user" ? (
                    <UserMessage
                      key={index}
                      message={message}
                      onEdit={
                        streamingId
                          ? undefined
                          : (text) => editMessage(index, text)
                      }
                    />
                  ) : (
                    <AssistantMessage
                      key={index}
                      message={message}
                      streaming={
                        streamingId === active.id &&
                        index === active.messages.length - 1
                      }
                      liveStartedAt={streamStartRef.current ?? undefined}
                      modelRecord={models.data?.find(
                        (candidate) =>
                          candidate.id === (message.model ?? active.model),
                      )}
                      retryModels={retryOptions}
                      onRetry={
                        streamingId
                          ? undefined
                          : (nextModel) => retry(index, nextModel)
                      }
                    />
                  ),
                )}
              </div>
            </div>
            <div className="relative mx-auto w-full max-w-[768px] px-4 pb-4">
              {!pinned && (
                <button
                  type="button"
                  onClick={scrollToBottom}
                  aria-label="Scroll to latest message"
                  className="absolute -top-10 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1 rounded-full border border-border-2 bg-bg-panel px-3 py-1.5 text-xs text-text-2 shadow-md transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
                >
                  <ArrowDown className="size-3.5" />
                  Latest
                </button>
              )}
              {composer}
            </div>
          </>
        ) : (
          <div className="flex min-h-0 flex-1 items-center justify-center px-4">
            <div className="w-full max-w-[768px] pb-24">
              <h1 className="mb-6 text-center text-2xl font-semibold tracking-[-0.01em]">
                What can I help with?
              </h1>
              {composer}
              {!startersDismissed && (
                <div className="mt-4">
                  <div className="mb-2 flex items-center justify-between">
                    <p className="text-xs uppercase tracking-wide text-text-4">
                      Try one of these
                    </p>
                    <button
                      type="button"
                      onClick={dismissStarters}
                      aria-label="Dismiss starter prompts"
                      className="rounded-xs p-1 text-text-4 hover:text-text-2"
                    >
                      <X className="size-3.5" />
                    </button>
                  </div>
                  <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                    {STARTER_PROMPTS.map((prompt) => (
                      <button
                        key={prompt}
                        type="button"
                        onClick={() => send(prompt)}
                        disabled={!model}
                        className="rounded-md border border-border-1 px-3 py-2.5 text-left text-sm text-text-2 transition-colors duration-150 ease-standard hover:border-border-2 hover:bg-bg-hover hover:text-text-1 disabled:opacity-50"
                      >
                        {prompt}
                      </button>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
