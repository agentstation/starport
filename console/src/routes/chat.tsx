import { useQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { PanelLeftClose, PanelLeftOpen, X } from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { Composer } from "@/components/chat/Composer";
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

function MessageView({ message }: { message: ChatMessage }) {
  if (message.role === "user") {
    return (
      <div className="flex justify-end">
        <div className="max-w-[85%] whitespace-pre-wrap rounded-xl bg-bg-raised px-4 py-2.5 text-base text-text-1">
          {message.content}
        </div>
      </div>
    );
  }
  return (
    <div className="text-base text-text-1">
      {message.model && (
        <p className="mb-1 font-mono text-xs text-text-4">{message.model}</p>
      )}
      {message.error ? (
        <p className="text-sm text-error">{message.content || "Request failed."}</p>
      ) : message.content ? (
        <div className="whitespace-pre-wrap leading-relaxed">
          {message.content}
        </div>
      ) : (
        <p className="text-text-3">Thinking…</p>
      )}
      {message.stopped && (
        <p className="mt-1 text-sm text-text-4">Stopped.</p>
      )}
    </div>
  );
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

  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

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

  // Follow the stream: keep the scroll pinned to the bottom.
  const activeMessages = active?.messages;
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [activeMessages, activeId]);

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

  const send = (text: string) => {
    if (streamingId) return;

    let conversation = active;
    if (!conversation) {
      conversation = newConversation(newModel, truncateTitle(text));
      conversation.params = { ...newParams };
      persist([conversation, ...conversations]);
      setActiveId(conversation.id);
      setNewParams({ ...DEFAULT_PARAMS });
    }
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
    setDrafts((previous) => ({ ...previous, [draftKey]: "" }));

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
    let sawContent = false;

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
        if (firstTokenAt === undefined) firstTokenAt = performance.now();
        sawContent = true;
        patchLast((message) => ({ ...message, content: message.content + delta }));
      },
      onReasoning: (delta) => {
        if (firstTokenAt === undefined) firstTokenAt = performance.now();
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
        patchLast((message) => ({
          ...message,
          model: meta.model || undefined,
          stats,
        }));
        if (isFirstExchange) {
          const current = { ...conversation, id: conversationId };
          generateTitle(current, text);
        }
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) {
          if (sawContent) {
            patchLast((message) => ({ ...message, stopped: true }));
          } else {
            // Nothing arrived; drop the empty placeholder.
            updateConversation(conversationId, (current) => ({
              ...current,
              messages: current.messages.filter(
                (message, index) =>
                  !(index === current.messages.length - 1 &&
                    message.role === "assistant" &&
                    !message.content),
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

  const dismissStarters = () => {
    localStorage.setItem(STARTERS_KEY, "dismissed");
    setStartersDismissed(true);
  };

  const composer = (
    <Composer
      draft={draft}
      onDraftChange={setDraft}
      onSend={send}
      streaming={streamingId !== null && streamingId === (activeId ?? streamingId)}
      onStop={stop}
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
            setActiveId(id);
            setPickerOpen(false);
          }}
          onNew={() => setActiveId(null)}
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
          {active && (
            <p className="truncate text-sm text-text-2">{active.title}</p>
          )}
        </div>

        {active ? (
          <>
            <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto">
              <div className="mx-auto flex max-w-[768px] flex-col gap-6 px-4 py-6">
                {active.messages.map((message, index) => (
                  <MessageView key={index} message={message} />
                ))}
              </div>
            </div>
            <div className="mx-auto w-full max-w-[768px] px-4 pb-4">
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
