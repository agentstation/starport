import { Pencil, Pin, PinOff, Plus, Search, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";

import { IconButton } from "@/components/ui/IconButton";
import { DestructiveButton, GhostButton } from "@/components/ui/Form";
import { Dialog, DialogBody, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import type { Conversation } from "@/lib/chatStore";

// ThreadList is the chat sidebar: search, then conversations grouped
// by pinned state and recency (legacy grouping carried over).

type Group = { label: string; conversations: Conversation[] };

function groupConversations(conversations: Conversation[]): Group[] {
  const startOfDay = (daysAgo: number): number => {
    const date = new Date();
    date.setHours(0, 0, 0, 0);
    date.setDate(date.getDate() - daysAgo);
    return date.getTime();
  };
  const today = startOfDay(0);
  const yesterday = startOfDay(1);
  const week = startOfDay(7);
  const month = startOfDay(30);

  const pinned: Group = { label: "Pinned", conversations: [] };
  const todayGroup: Group = { label: "Today", conversations: [] };
  const yesterdayGroup: Group = { label: "Yesterday", conversations: [] };
  const weekGroup: Group = { label: "Previous 7 days", conversations: [] };
  const monthGroup: Group = { label: "Previous 30 days", conversations: [] };
  const older: Group = { label: "Older", conversations: [] };

  const sorted = [...conversations].sort((a, b) => b.updatedAt - a.updatedAt);
  for (const conversation of sorted) {
    if (conversation.pinned) {
      pinned.conversations.push(conversation);
    } else if (conversation.updatedAt >= today) {
      todayGroup.conversations.push(conversation);
    } else if (conversation.updatedAt >= yesterday) {
      yesterdayGroup.conversations.push(conversation);
    } else if (conversation.updatedAt >= week) {
      weekGroup.conversations.push(conversation);
    } else if (conversation.updatedAt >= month) {
      monthGroup.conversations.push(conversation);
    } else {
      older.conversations.push(conversation);
    }
  }
  return [pinned, todayGroup, yesterdayGroup, weekGroup, monthGroup, older].filter(
    (group) => group.conversations.length > 0,
  );
}

function ThreadRow({
  conversation,
  active,
  onOpen,
  onTogglePin,
  onRename,
  onDelete,
}: {
  conversation: Conversation;
  active: boolean;
  onOpen: () => void;
  onTogglePin: () => void;
  onRename: (title: string) => void;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(conversation.title);

  const commitRename = () => {
    setEditing(false);
    const title = draft.trim();
    if (title && title !== conversation.title) onRename(title);
    else setDraft(conversation.title);
  };

  if (editing) {
    return (
      <input
        type="text"
        value={draft}
        autoFocus
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commitRename}
        onKeyDown={(event) => {
          if (event.key === "Enter") commitRename();
          if (event.key === "Escape") {
            setDraft(conversation.title);
            setEditing(false);
          }
        }}
        aria-label="Conversation title"
        className="h-8 w-full rounded-sm border border-border-2 bg-bg-canvas px-2 text-sm text-text-1 outline-none"
      />
    );
  }

  return (
    <div
      className={`group relative flex h-8 items-center rounded-sm transition-colors duration-150 ease-standard ${
        active ? "bg-bg-hover" : "hover:bg-bg-hover"
      }`}
    >
      <button
        type="button"
        onClick={onOpen}
        aria-current={active ? "true" : undefined}
        className={`h-full min-w-0 flex-1 truncate px-2 text-left text-sm ${
          active ? "text-text-1" : "text-text-2"
        }`}
      >
        {conversation.title || "Untitled"}
      </button>
      <div className="hidden shrink-0 items-center gap-0.5 pr-1 group-focus-within:flex group-hover:flex">
        <IconButton
          label={conversation.pinned ? "Unpin conversation" : "Pin conversation"}
          onClick={onTogglePin}
          className="rounded-xs p-1 text-text-4 hover:text-text-2"
        >
          {conversation.pinned ? (
            <PinOff aria-hidden="true" className="size-3.5" />
          ) : (
            <Pin aria-hidden="true" className="size-3.5" />
          )}
        </IconButton>
        <IconButton
          label="Rename conversation"
          onClick={() => {
            setDraft(conversation.title);
            setEditing(true);
          }}
          className="rounded-xs p-1 text-text-4 hover:text-text-2"
        >
          <Pencil aria-hidden="true" className="size-3.5" />
        </IconButton>
        <IconButton
          label="Delete conversation"
          onClick={onDelete}
          className="rounded-xs p-1 text-text-4 hover:text-error"
        >
          <Trash2 aria-hidden="true" className="size-3.5" />
        </IconButton>
      </div>
    </div>
  );
}

export function ThreadList({
  conversations,
  activeId,
  onOpen,
  onNew,
  onTogglePin,
  onRename,
  onDelete,
}: {
  conversations: Conversation[];
  activeId: string | null;
  onOpen: (id: string) => void;
  onNew: () => void;
  onTogglePin: (id: string) => void;
  onRename: (id: string, title: string) => void;
  onDelete: (id: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [deleting, setDeleting] = useState<Conversation | null>(null);

  const groups = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const visible = needle
      ? conversations.filter((conversation) =>
          conversation.title.toLowerCase().includes(needle),
        )
      : conversations;
    return groupConversations(visible);
  }, [conversations, query]);

  return (
    <div className="flex h-full w-64 shrink-0 flex-col border-r border-border-1 bg-bg-panel">
      <div className="flex flex-col gap-2 p-3">
        <button
          type="button"
          onClick={onNew}
          className="flex h-9 items-center gap-2 rounded-sm border border-border-2 px-3 text-sm font-medium text-text-1 transition-colors duration-150 ease-standard hover:bg-bg-hover"
        >
          <Plus aria-hidden="true" className="size-4" />
          New chat
        </button>
        <div className="relative">
          <Search aria-hidden="true" className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-text-4" />
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search chats…"
            aria-label="Search conversations"
            className="h-8 w-full rounded-sm bg-bg-canvas pl-8 pr-2 text-sm text-text-1 outline-none placeholder:text-text-4"
          />
        </div>
      </div>
      <nav aria-label="Conversations" className="flex-1 overflow-y-auto px-3 pb-3">
        {groups.length === 0 && (
          <p className="px-2 pt-2 text-sm text-text-4">
            {query ? "No matches." : "No conversations yet."}
          </p>
        )}
        {groups.map((group) => (
          <div key={group.label} className="mb-2">
            <p className="px-2 pb-1 pt-2 text-xs font-medium text-text-4">
              {group.label}
            </p>
            <div className="flex flex-col gap-0.5">
              {group.conversations.map((conversation) => (
                <ThreadRow
                  key={conversation.id}
                  conversation={conversation}
                  active={conversation.id === activeId}
                  onOpen={() => onOpen(conversation.id)}
                  onTogglePin={() => onTogglePin(conversation.id)}
                  onRename={(title) => onRename(conversation.id, title)}
                  onDelete={() => setDeleting(conversation)}
                />
              ))}
            </div>
          </div>
        ))}
      </nav>
      {deleting && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open) setDeleting(null);
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Delete conversation</DialogTitle>
            </DialogHeader>
            <DialogBody>
              <p className="text-sm text-text-2">
                This removes{" "}
                <strong className="font-semibold text-text-1">
                  {deleting.title || "Untitled"}
                </strong>{" "}
                from this browser. There is no undo.
              </p>
            </DialogBody>
            <DialogFooter>
              <GhostButton onClick={() => setDeleting(null)}>Cancel</GhostButton>
              <DestructiveButton
                onClick={() => {
                  onDelete(deleting.id);
                  setDeleting(null);
                }}
              >
                Delete
              </DestructiveButton>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </div>
  );
}
