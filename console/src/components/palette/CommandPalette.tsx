import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  BarChart3,
  BookOpen,
  Building2,
  FileText,
  Film,
  Key,
  LayoutDashboard,
  MessageSquare,
  Moon,
  ScanText,
  Search,
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
import { queries } from "@/lib/queries";
import { appliedTheme, onThemeChange, setTheme } from "@/lib/theme";
import { useGatewayAccess } from "@/lib/useGatewayAccess";
import {
  KIND_LABELS,
  searchPalette,
  type PaletteItem,
} from "./paletteIndex";

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

// CommandPalette is the global ⌘K surface over models, providers,
// authors, pages, and actions. It stays mounted (and dormant) in the
// shell; opening it starts the catalog queries, which share their
// cache keys with the pages.
export function CommandPalette() {
  const navigate = useNavigate();
  const keyUsable = useGatewayAccess();
  const theme = useSyncExternalStore(onThemeChange, appliedTheme);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);

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

  const enabled = open && keyUsable;
  const models = useQuery({
    ...queries.models(),
    enabled,
  });
  const providers = useQuery({
    ...queries.providerCatalog(),
    enabled,
  });
  const authors = useQuery({
    ...queries.authors(),
    enabled,
  });

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
    ],
    [models.data, providers.data, authors.data, theme],
  );

  const visible = useMemo(() => searchPalette(query, items), [query, items]);

  // Query and result changes reset the cursor to the first row.
  useEffect(() => {
    setActive(0);
  }, [query, open]);
  const activeItem = visible[Math.min(active, visible.length - 1)];

  useEffect(() => {
    if (!activeItem) return;
    document
      .getElementById(`palette-${activeItem.kind}-${activeItem.id}`)
      ?.scrollIntoView?.({ block: "nearest" });
  }, [activeItem]);

  const close = () => {
    setOpen(false);
    setQuery("");
  };

  const run = (item: PaletteItem) => {
    close();
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
    } else if (item.id === "toggle-theme") {
      setTheme(appliedTheme() === "dark" ? "light" : "dark");
    } else if (item.id === "new-chat") {
      void navigate({ to: "/chat" });
    }
  };

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center p-6 pt-[14vh]">
      <button
        type="button"
        aria-label="Close command palette"
        onClick={close}
        className="absolute inset-0 cursor-default bg-black/60"
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="relative flex max-h-[58vh] w-full max-w-2xl flex-col overflow-hidden rounded-lg border border-border-2 bg-bg-raised shadow-[0_16px_48px_rgba(0,0,0,0.5)]"
      >
        <div className="flex items-center gap-3 border-b border-border-1 px-4">
          <Search className="size-5 shrink-0 text-text-4" />
          <input
            autoFocus
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "ArrowDown") {
                event.preventDefault();
                setActive((index) => Math.min(index + 1, visible.length - 1));
              } else if (event.key === "ArrowUp") {
                event.preventDefault();
                setActive((index) => Math.max(index - 1, 0));
              } else if (event.key === "Home") {
                event.preventDefault();
                setActive(0);
              } else if (event.key === "End") {
                event.preventDefault();
                setActive(visible.length - 1);
              } else if (event.key === "Enter") {
                event.preventDefault();
                if (activeItem) run(activeItem);
              } else if (event.key === "Escape") {
                close();
              }
            }}
            placeholder="Search models, providers, authors, pages…"
            aria-label="Search everything"
            aria-activedescendant={
              activeItem
                ? `palette-${activeItem.kind}-${activeItem.id}`
                : undefined
            }
            className="h-14 w-full bg-transparent text-md text-text-1 outline-none placeholder:text-text-4"
          />
        </div>
        <div role="listbox" aria-label="Results" className="overflow-y-auto p-1.5">
          {visible.length === 0 && (
            <p className="px-3 py-4 text-sm text-text-3">
              No matches for “{query.trim()}”.
            </p>
          )}
          {visible.map((item, index) => {
            const first =
              index === 0 || visible[index - 1]?.kind !== item.kind;
            const PageIcon =
              item.kind === "page"
                ? (PAGE_ICONS.get(item.id) ?? LayoutDashboard)
                : item.kind === "action"
                  ? item.id === "toggle-theme"
                    ? theme === "dark"
                      ? Sun
                      : Moon
                    : SquarePen
                  : null;
            return (
              <div key={`${item.kind}:${item.id}`}>
                {first && (
                  <p className="px-3 pb-1 pt-2.5 text-[10px] font-medium uppercase tracking-[0.08em] text-text-4">
                    {KIND_LABELS[item.kind]}
                  </p>
                )}
                <button
                  type="button"
                  id={`palette-${item.kind}-${item.id}`}
                  role="option"
                  aria-selected={item === activeItem}
                  onMouseEnter={() => setActive(index)}
                  onClick={() => run(item)}
                  className={`flex h-10 w-full items-center gap-3 rounded-sm px-3 text-left ${
                    item === activeItem ? "bg-bg-hover" : ""
                  }`}
                >
                  {PageIcon && (
                    <PageIcon className="size-4 shrink-0 text-text-3" />
                  )}
                  {(item.kind === "provider" || item.kind === "author") && (
                    <EntityLogo
                      kind={item.kind === "provider" ? "providers" : "authors"}
                      id={item.id}
                      name={item.label}
                      size={20}
                    />
                  )}
                  {item.kind === "model" && (
                    <Sparkles className="size-4 shrink-0 text-text-4" />
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
                </button>
              </div>
            );
          })}
        </div>
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
      </div>
    </div>
  );
}
