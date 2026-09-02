import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  BarChart3,
  BookOpen,
  Building2,
  Clock,
  FileText,
  Film,
  Key,
  LayoutDashboard,
  MessageSquare,
  Moon,
  ScanText,
  Server,
  Settings,
  SlidersHorizontal,
  Sparkles,
  SquarePen,
  Sun,
  UserRound,
  Users,
  UsersRound,
} from "lucide-react";
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";

import { EntityLogo } from "@/components/catalog/EntityLogo";
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { queries } from "@/lib/queries";
import { appliedTheme, onThemeChange, setTheme } from "@/lib/theme";
import { useGatewayAccess } from "@/lib/useGatewayAccess";
import {
  KIND_LABELS,
  KIND_ORDER,
  searchPalette,
  type PaletteItem,
  type PaletteItemKind,
} from "./paletteIndex";
import { readRecents, rememberRecent } from "./paletteRecents";

// The shell's Search button and the ⌘K shortcut share one entry point:
// a window event, so no state needs lifting into the shell.
export const PALETTE_EVENT = "starport:palette";

export function openCommandPalette(): void {
  window.dispatchEvent(new Event(PALETTE_EVENT));
}

const PAGES: { path: string; label: string; icon: typeof LayoutDashboard }[] = [
  { path: "/", label: "Overview", icon: LayoutDashboard },
  { path: "/chat", label: "Chat", icon: MessageSquare },
  { path: "/models", label: "Models", icon: Sparkles },
  { path: "/providers", label: "Providers", icon: Server },
  { path: "/authors", label: "Authors", icon: Users },
  { path: "/keys", label: "API Keys", icon: Key },
  { path: "/files", label: "Files", icon: FileText },
  { path: "/jobs", label: "Jobs", icon: Film },
  { path: "/documents", label: "Documents", icon: ScanText },
  { path: "/accounts", label: "Accounts", icon: Building2 },
  { path: "/members", label: "Members", icon: UserRound },
  { path: "/teams", label: "Teams", icon: UsersRound },
  { path: "/usage", label: "Usage", icon: BarChart3 },
  { path: "/presets", label: "Presets", icon: SlidersHorizontal },
  { path: "/settings", label: "Settings", icon: Settings },
  { path: "/docs", label: "Docs", icon: BookOpen },
];

const PAGE_ICONS = new Map(PAGES.map((page) => [page.path, page.icon]));

function Kbd({ children }: { children: string }) {
  return (
    <kbd className="rounded-xs border border-border-1 bg-bg-panel px-1.5 py-0.5 font-sans text-[10px] text-text-4">
      {children}
    </kbd>
  );
}

type Group = { key: string; heading: string; items: PaletteItem[] };

// groupResults keeps the index's kind order and puts recents first when
// the query is empty, so the browsable surface opens on the reader's
// own trail.
function groupResults(query: string, results: PaletteItem[], recents: PaletteItem[]): Group[] {
  const groups: Group[] = [];
  if (!query.trim() && recents.length > 0) {
    groups.push({ key: "recent", heading: "Recent", items: recents });
  }
  const byKind = new Map<PaletteItemKind, PaletteItem[]>();
  for (const item of results) {
    const bucket = byKind.get(item.kind);
    if (bucket) {
      bucket.push(item);
    } else {
      byKind.set(item.kind, [item]);
    }
  }
  for (const kind of KIND_ORDER) {
    const items = byKind.get(kind);
    if (items?.length) groups.push({ key: kind, heading: KIND_LABELS[kind], items });
  }
  return groups;
}

// CommandPalette is the global ⌘K surface over pages, actions, models,
// providers, authors, and keys, plus the last five destinations. It
// stays mounted (and dormant) in the shell; opening it starts the
// catalog queries, which share their cache keys with the pages. cmdk
// owns the combobox roles, the highlight, and the arrow keys; the
// palette index owns matching and ranking, so the fuzzy contract the
// pages use stays in one place.
export function CommandPalette() {
  const navigate = useNavigate();
  const keyUsable = useGatewayAccess();
  const theme = useSyncExternalStore(onThemeChange, appliedTheme);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [recents, setRecents] = useState<PaletteItem[]>([]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOpen((current) => !current);
      }
    };
    const onOpen = () => setOpen(true);
    document.addEventListener("keydown", onKey);
    window.addEventListener(PALETTE_EVENT, onOpen);
    return () => {
      document.removeEventListener("keydown", onKey);
      window.removeEventListener(PALETTE_EVENT, onOpen);
    };
  }, []);

  useEffect(() => {
    if (open) setRecents(readRecents());
  }, [open]);

  const enabled = open && keyUsable;
  const models = useQuery({ ...queries.models(), enabled });
  const providers = useQuery({ ...queries.providerCatalog(), enabled });
  const authors = useQuery({ ...queries.authors(), enabled });
  const keys = useQuery({ ...queries.keys(), enabled });

  const items = useMemo<PaletteItem[]>(
    () => [
      ...PAGES.map((page) => ({
        kind: "page" as const,
        id: page.path,
        label: page.label,
        hint: page.path,
      })),
      {
        kind: "action" as const,
        id: "toggle-theme",
        label: theme === "dark" ? "Switch to light theme" : "Switch to dark theme",
        keywords: ["dark", "light", "theme", "appearance", "toggle"],
      },
      {
        kind: "action" as const,
        id: "new-chat",
        label: "New chat",
        hint: "/chat",
        keywords: ["conversation", "compose"],
      },
      ...(models.data ?? []).map((model) => ({
        kind: "model" as const,
        id: model.id,
        label: model.id,
        hint: model.name !== model.id ? model.name : undefined,
      })),
      ...(providers.data ?? []).map((provider) => ({
        kind: "provider" as const,
        id: provider.id,
        label: provider.name || provider.id,
        hint: provider.id,
      })),
      ...(authors.data ?? []).map((author) => ({
        kind: "author" as const,
        id: author.id,
        label: author.name || author.id,
        hint: author.id,
      })),
      ...(keys.data ?? []).map((key) => ({
        kind: "key" as const,
        id: key.id,
        label: key.name || key.id,
        hint: key.id,
      })),
    ],
    [models.data, providers.data, authors.data, keys.data, theme],
  );

  const visible = useMemo(() => searchPalette(query, items), [query, items]);
  const groups = useMemo(
    () => groupResults(query, visible, recents),
    [query, visible, recents],
  );

  const close = () => {
    setOpen(false);
    setQuery("");
  };

  const run = (item: PaletteItem) => {
    close();
    setRecents(rememberRecent(item));
    if (item.kind === "page") {
      void navigate({ to: item.id });
    } else if (item.kind === "model") {
      void navigate({ to: "/models/$modelId", params: { modelId: item.id } });
    } else if (item.kind === "provider") {
      void navigate({
        to: "/providers/$providerId",
        params: { providerId: item.id },
      });
    } else if (item.kind === "author") {
      void navigate({ to: "/authors/$authorId", params: { authorId: item.id } });
    } else if (item.kind === "key") {
      void navigate({ to: "/keys", search: { selected: item.id } });
    } else if (item.id === "toggle-theme") {
      setTheme(appliedTheme() === "dark" ? "light" : "dark");
    } else if (item.id === "new-chat") {
      void navigate({ to: "/chat" });
    }
  };

  const iconFor = (item: PaletteItem) => {
    if (item.kind === "page") {
      const PageIcon = PAGE_ICONS.get(item.id) ?? LayoutDashboard;
      return <PageIcon className="size-4 shrink-0 text-text-3" />;
    }
    if (item.kind === "action") {
      const ActionIcon =
        item.id === "toggle-theme" ? (theme === "dark" ? Sun : Moon) : SquarePen;
      return <ActionIcon className="size-4 shrink-0 text-text-3" />;
    }
    if (item.kind === "provider" || item.kind === "author") {
      return (
        <EntityLogo
          kind={item.kind === "provider" ? "providers" : "authors"}
          id={item.id}
          name={item.label}
          size={20}
        />
      );
    }
    if (item.kind === "key") return <Key className="size-4 shrink-0 text-text-4" />;
    return <Sparkles className="size-4 shrink-0 text-text-4" />;
  };

  return (
    <CommandDialog
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setOpen(true);
        } else {
          close();
        }
      }}
      title="Command palette"
      description="Search models, providers, authors, keys, pages, and actions."
    >
      <Command shouldFilter={false} loop label="Search everything">
        <CommandInput
          value={query}
          onValueChange={setQuery}
          placeholder="Search models, providers, authors, keys, pages…"
        />
        <CommandList aria-label="Results">
          <CommandEmpty>No matches for “{query.trim()}”.</CommandEmpty>
          {groups.map((group) => (
            <CommandGroup key={group.key} heading={group.heading}>
              {group.items.map((item) => (
                <CommandItem
                  key={`${group.key}:${item.kind}:${item.id}`}
                  value={`${group.key}:${item.kind}:${item.id}`}
                  onSelect={() => run(item)}
                >
                  {group.key === "recent" ? (
                    <Clock className="size-4 shrink-0 text-text-4" />
                  ) : (
                    iconFor(item)
                  )}
                  <span
                    className={`min-w-0 truncate text-text-1 ${
                      item.kind === "model" ? "font-mono text-sm" : "text-base"
                    }`}
                  >
                    {item.label}
                  </span>
                  {item.hint && (
                    <span className="ml-auto min-w-0 shrink truncate pl-3 text-xs text-text-4">
                      {item.hint}
                    </span>
                  )}
                </CommandItem>
              ))}
            </CommandGroup>
          ))}
        </CommandList>
        <div className="flex items-center gap-4 border-t border-border-1 px-4 py-2 text-[11px] text-text-4">
          <span className="flex items-center gap-1.5">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd>
            navigate
          </span>
          <span className="flex items-center gap-1.5">
            <Kbd>↵</Kbd>
            open
          </span>
          <span className="flex items-center gap-1.5">
            <Kbd>esc</Kbd>
            close
          </span>
        </div>
      </Command>
    </CommandDialog>
  );
}
