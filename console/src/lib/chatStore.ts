// Local chat persistence. Conversations live in this browser's
// localStorage only — the gateway never stores console state. The keys
// and record shapes are fixed by the carry-over contract: a browser that
// used an earlier console keeps its history, so they must not change.

import type { ChatUsage } from "@/lib/api";
import { attachmentPart } from "@/lib/attachments";
import type { Attachment, ContentPart } from "@/lib/attachments";

const STORAGE_KEY = "starport.chats";
const LEGACY_STORAGE_KEY = "starport_chats";
const FAVORITES_KEY = "starport.favModels";
const LAST_MODEL_KEY = "starport.lastModel";
const SIDEBAR_KEY = "starport.chatSidebar";

// Oldest unpinned conversations beyond this cap are dropped on save.
export const MAX_CONVERSATIONS = 100;

export type ChatParams = {
  system: string;
  temperature: number | null;
  maxTokens: number | null;
  // Provider routing preferences, comma-separated (legacy shape).
  order: string;
  only: string;
  ignore: string;
  sort: string;
  // Reasoning effort ("" = provider default). New in CM10; absent on
  // legacy records, so readers use `params.effort ?? ""`.
  effort?: string;
};

export const DEFAULT_PARAMS: ChatParams = {
  system: "",
  temperature: null,
  maxTokens: null,
  order: "",
  only: "",
  ignore: "",
  sort: "",
  effort: "",
};

export type ChatStats = {
  ttftMs?: number;
  latencyMs?: number;
  tps?: number;
  promptTokens?: number;
  completionTokens?: number;
  reasoningTokens?: number;
  tokens?: number;
  cache?: string;
  cacheAge?: string;
  unenforced?: string;
};

export type ChatMessage = {
  role: "user" | "assistant";
  content: string;
  // Attached media on a user turn. Absent on a text-only turn.
  attachments?: Attachment[];
  // Attached images on a user turn, as data URLs. This is the shape the
  // console wrote before an attachment carried a kind. A stored
  // conversation still holds it, so the read path keeps it.
  images?: string[];
  reasoning?: string;
  // The routed model that produced an assistant turn.
  model?: string;
  stats?: ChatStats;
  error?: boolean;
  stopped?: boolean;
  reasoningMs?: number;
};

// turnAttachments reads the attachments of one turn from either record
// shape. A conversation stored before audio existed holds images alone,
// and those turns must still render and still replay on a retry.
export function turnAttachments(
  message: Pick<ChatMessage, "images" | "attachments">,
): Attachment[] {
  if (message.attachments?.length) return message.attachments;
  return (message.images ?? []).map((url, index) => ({
    kind: "image" as const,
    url,
    name: `Attachment ${index + 1}`,
  }));
}

// messageContent builds the request content for one turn: the plain
// string without attachments, content parts with them. The string stays
// for a text-only turn because that is the request every model accepts,
// including one the catalog records no modalities for.
export function messageContent(
  message: Pick<ChatMessage, "content" | "images" | "attachments">,
): string | ContentPart[] {
  const attachments = turnAttachments(message);
  if (!attachments.length) return message.content;
  return [
    { type: "text" as const, text: message.content },
    ...attachments.map(attachmentPart),
  ];
}

export type Conversation = {
  id: string;
  title: string;
  model: string;
  pinned?: boolean;
  params: ChatParams;
  messages: ChatMessage[];
  updatedAt: number;
};

export function newConversation(model: string, title: string): Conversation {
  return {
    id: crypto.randomUUID(),
    title,
    model,
    params: { ...DEFAULT_PARAMS },
    messages: [],
    updatedAt: Date.now(),
  };
}

export function loadConversations(): Conversation[] {
  try {
    const raw =
      localStorage.getItem(STORAGE_KEY) ??
      localStorage.getItem(LEGACY_STORAGE_KEY);
    const parsed: unknown = JSON.parse(raw ?? "[]");
    if (!Array.isArray(parsed)) return [];
    return (parsed as Conversation[]).filter(
      (conversation) =>
        typeof conversation?.id === "string" &&
        Array.isArray(conversation?.messages),
    );
  } catch {
    return [];
  }
}

let saveTimer: number | undefined;

// saveConversations debounces writes (300ms) and enforces the cap.
export function saveConversations(conversations: Conversation[]): void {
  window.clearTimeout(saveTimer);
  saveTimer = window.setTimeout(() => {
    try {
      localStorage.setItem(
        STORAGE_KEY,
        JSON.stringify(conversations.slice(0, MAX_CONVERSATIONS)),
      );
      localStorage.removeItem(LEGACY_STORAGE_KEY);
    } catch {
      // Storage full or unavailable; the in-memory state still works.
    }
  }, 300);
}

export function loadFavorites(): Set<string> {
  try {
    const parsed: unknown = JSON.parse(
      localStorage.getItem(FAVORITES_KEY) ?? "[]",
    );
    return new Set(Array.isArray(parsed) ? (parsed as string[]) : []);
  } catch {
    return new Set();
  }
}

export function saveFavorites(favorites: Set<string>): void {
  localStorage.setItem(FAVORITES_KEY, JSON.stringify([...favorites]));
}

export function lastModel(): string {
  return localStorage.getItem(LAST_MODEL_KEY) ?? "";
}

export function rememberModel(model: string): void {
  if (model) localStorage.setItem(LAST_MODEL_KEY, model);
}

export function sidebarClosed(): boolean {
  return localStorage.getItem(SIDEBAR_KEY) === "closed";
}

export function saveSidebarClosed(closed: boolean): void {
  if (closed) {
    localStorage.setItem(SIDEBAR_KEY, "closed");
  } else {
    localStorage.removeItem(SIDEBAR_KEY);
  }
}

// statsFromUsage folds the gateway usage payload and stream timing into
// the persisted per-turn stats record.
export function statsFromUsage(
  usage: ChatUsage | null,
  timing: { ttftMs?: number; latencyMs: number },
  meta: { cache?: string; cacheAge?: string; unenforced?: string },
): ChatStats {
  const completionTokens = usage?.completion_tokens;
  const stats: ChatStats = {
    ttftMs: timing.ttftMs,
    latencyMs: timing.latencyMs,
    promptTokens: usage?.prompt_tokens,
    completionTokens,
    reasoningTokens: usage?.completion_tokens_details?.reasoning_tokens,
    tokens: usage?.total_tokens,
    cache: meta.cache || undefined,
    cacheAge: meta.cacheAge || undefined,
    unenforced: meta.unenforced || undefined,
  };
  const generationMs = timing.latencyMs - (timing.ttftMs ?? 0);
  if (completionTokens && generationMs > 0) {
    stats.tps = completionTokens / (generationMs / 1000);
  }
  return stats;
}

// providerPreferences builds the request "provider" object from the
// comma-separated params; null when nothing is set.
export function providerPreferences(
  params: ChatParams,
): Record<string, unknown> | null {
  const split = (value: string): string[] =>
    value
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean);
  const preferences: Record<string, unknown> = {};
  const order = split(params.order);
  const only = split(params.only);
  const ignore = split(params.ignore);
  if (order.length) preferences.order = order;
  if (only.length) preferences.only = only;
  if (ignore.length) preferences.ignore = ignore;
  if (params.sort.trim()) preferences.sort = params.sort.trim();
  return Object.keys(preferences).length ? preferences : null;
}
