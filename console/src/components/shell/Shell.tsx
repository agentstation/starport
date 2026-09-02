import { useQuery } from "@tanstack/react-query";
import { Link, useRouterState } from "@tanstack/react-router";
import {
  BarChart3,
  BookOpen,
  Building2,
  ExternalLink as NewTabIcon,
  FileText,
  Film,
  Key,
  LayoutDashboard,
  MessageSquare,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  ScrollText,
  Search as SearchIcon,
  Server,
  Settings,
  ShieldAlert,
  ScanText,
  SlidersHorizontal,
  Sparkles,
  Sun,
  UserRound,
  Users,
  UsersRound,
} from "lucide-react";
import { useEffect, useState, useSyncExternalStore, type ReactNode } from "react";

import { CommandPalette, openCommandPalette } from "@/components/palette/CommandPalette";
import { GitHubMark } from "@/components/ui/icons";
import { Toaster } from "@/components/ui/sonner";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { queries } from "@/lib/queries";
import { appliedTheme, onThemeChange, setTheme } from "@/lib/theme";

// Nav is grouped by who reaches for it (DESIGN.md information
// architecture): the catalog everyone browses, the account surface a
// caller owns, and the gateway surface an operator runs. A local
// developer is all three at once, so nothing hides — the labels only
// say which hat a page belongs to.
const NAV_SECTIONS: ReadonlyArray<{
  label: string | null;
  items: ReadonlyArray<{ to: string; label: string; icon: typeof LayoutDashboard }>;
}> = [
  {
    label: null,
    items: [
      { to: "/", label: "Overview", icon: LayoutDashboard },
      { to: "/chat", label: "Chat", icon: MessageSquare },
    ],
  },
  {
    label: "Catalog",
    items: [
      { to: "/models", label: "Models", icon: Sparkles },
      { to: "/providers", label: "Providers", icon: Server },
      { to: "/authors", label: "Authors", icon: Users },
    ],
  },
  {
    label: "Account",
    items: [
      { to: "/keys", label: "API Keys", icon: Key },
      { to: "/files", label: "Files", icon: FileText },
      { to: "/jobs", label: "Jobs", icon: Film },
      { to: "/documents", label: "Documents", icon: ScanText },
      { to: "/presets", label: "Presets", icon: SlidersHorizontal },
      { to: "/usage", label: "Usage", icon: BarChart3 },
    ],
  },
  {
    label: "Gateway",
    items: [
      { to: "/accounts", label: "Accounts", icon: Building2 },
      { to: "/members", label: "Members", icon: UserRound },
      { to: "/teams", label: "Teams", icon: UsersRound },
      { to: "/audit", label: "Audit Log", icon: ScrollText },
      { to: "/settings", label: "Settings", icon: Settings },
    ],
  },
  {
    label: null,
    items: [{ to: "/docs", label: "Docs", icon: BookOpen }],
  },
];

const COLLAPSE_KEY = "starport.sidebar.collapsed";

function useGatewayHealth() {
  return useQuery(queries.health());
}

function StarMark() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className="size-5 text-accent">
      <path
        fill="currentColor"
        d="M12 1l2.4 7.2L22 10l-7.6 1.8L12 19l-2.4-7.2L2 10l7.6-1.8z"
      />
    </svg>
  );
}

// FOOTER_ITEM matches the nav-item geometry exactly (h-9, px-3,
// gap-2.5, 16px icons), so the collapsed rail reads as one aligned
// icon column from top to bottom.
const FOOTER_ITEM =
  "flex h-9 items-center gap-2.5 rounded-sm px-3 text-base text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-2";

// The toggle names the state the console is in — the icon and label
// both read as "this is the current theme" — and clicking it flips to
// the other one. Showing the target instead reads as a wrong status.
function ThemeToggle({ collapsed }: { collapsed: boolean }) {
  const theme = useSyncExternalStore(onThemeChange, appliedTheme);
  const flip = () => {
    setTheme(theme === "dark" ? "light" : "dark");
  };
  const Icon = theme === "dark" ? Moon : Sun;
  const label = theme === "dark" ? "Dark theme" : "Light theme";
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <button
            type="button"
            onClick={flip}
            aria-label={theme === "dark" ? "Dark theme — switch to light" : "Light theme — switch to dark"}
            className={`${FOOTER_ITEM} ${collapsed ? "justify-center px-0" : ""}`}
          />
        }
      >
        <Icon className="size-4 shrink-0" />
        {!collapsed && <span>{label}</span>}
      </TooltipTrigger>
      <TooltipContent side="right">
        {theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
      </TooltipContent>
    </Tooltip>
  );
}

function GatewayStatus({ collapsed }: { collapsed: boolean }) {
  const health = useGatewayHealth();
  const ok = health.data?.status === "ok";
  const label = health.isPending ? "Connecting" : ok ? "Gateway healthy" : "Gateway unreachable";
  return (
    <div
      className={`flex h-9 items-center gap-2.5 px-3 ${collapsed ? "justify-center px-0" : ""}`}
      title={label}
    >
      {/* The dot sits inside an icon-sized box so it lines up with the
          16px icons above and below it. */}
      <span className="flex size-4 shrink-0 items-center justify-center">
        <span
          aria-hidden="true"
          className={`size-2 rounded-full ${
            health.isPending ? "bg-text-4" : ok ? "bg-success" : "bg-error"
          }`}
        />
      </span>
      {!collapsed && (
        <span className="truncate text-base text-text-3">
          {label}
          {ok && health.data?.version ? (
            <span className="text-text-4">
              {" · "}
              {health.data.version === "dev" ? "dev build" : `v${health.data.version}`}
            </span>
          ) : null}
        </span>
      )}
    </div>
  );
}

// OpenGatewayBanner states the one gateway setting a person cannot infer from
// the screen in front of them. With authentication off, every page looks
// exactly as it does with a key — which is how an operator leaves a gateway
// open and never notices. It rides above the content on every route rather
// than living only in Settings, because the mistake is made everywhere else.
function useOpenGateway(): boolean {
  const { data } = useQuery({
    ...queries.authMode(),
  });
  return data?.mode === "disabled";
}

// BANNER_HEIGHT is stated once and consumed twice: the strip is exactly this
// tall, and the chat route sizes its full-height column against it. A route
// that assumes the whole viewport would otherwise overflow by the strip.
const BANNER_HEIGHT = "2.25rem";

function OpenGatewayBanner() {
  return (
    <div
      role="status"
      data-testid="auth-mode-banner"
      className="flex h-9 items-center gap-2 border-b border-border-1 bg-warning-tint px-8 text-sm text-text-1"
    >
      <ShieldAlert aria-hidden="true" className="size-4 shrink-0 text-warning" />
      <span>
        Authentication is off. This gateway answers every request that reaches
        it, with no API key.
      </span>
      <Link
        to="/settings"
        className="ml-auto shrink-0 text-accent-link transition-colors duration-150 ease-standard hover:underline"
      >
        Require a key
      </Link>
    </div>
  );
}

export function Shell({ children }: { children: ReactNode }) {
  const [collapsed, setCollapsed] = useState(
    () => localStorage.getItem(COLLAPSE_KEY) === "1",
  );
  useEffect(() => {
    localStorage.setItem(COLLAPSE_KEY, collapsed ? "1" : "0");
  }, [collapsed]);

  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const openGateway = useOpenGateway();

  return (
    <TooltipProvider>
    <div className="flex min-h-screen bg-bg-canvas text-text-1">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:rounded-sm focus:border focus:border-border-2 focus:bg-bg-raised focus:px-3 focus:py-2 focus:text-sm focus:text-text-1"
      >
        Skip to content
      </a>
      <aside
        onClick={(event) => {
          // Clicking sidebar whitespace toggles the rail. Anything
          // interactive — a nav link, the search button, the footer
          // controls — owns its own click and is excluded.
          const target = event.target as HTMLElement | null;
          if (target?.closest("a,button,input,[role=dialog]")) return;
          setCollapsed((value) => !value);
        }}
        className={`fixed inset-y-0 left-0 flex flex-col border-r border-border-1 bg-bg-panel transition-[width] duration-150 ease-standard ${
          collapsed ? "w-16" : "w-60"
        }`}
      >
        <div className={`flex h-14 items-center gap-2.5 ${collapsed ? "justify-center px-0" : "px-4"}`}>
          <StarMark />
          {!collapsed && (
            <span className="text-sm font-semibold tracking-[0.08em]">STARPORT</span>
          )}
        </div>

        <div className="px-2 pt-2">
          <button
            type="button"
            onClick={openCommandPalette}
            className={`flex h-9 w-full items-center gap-2.5 rounded-sm border border-border-1 bg-bg-raised px-3 text-text-4 transition-colors duration-150 ease-standard hover:border-border-2 hover:text-text-2 ${collapsed ? "justify-center px-0" : ""}`}
          >
            <SearchIcon className="size-4 shrink-0" />
            {!collapsed && (
              <>
                <span className="text-sm">Search</span>
                <kbd className="ml-auto rounded-xs border border-border-1 px-1.5 py-0.5 font-sans text-[10px] tracking-[0.08em]">
                  ⌘ K
                </kbd>
              </>
            )}
          </button>
        </div>

        <nav aria-label="Console" className="flex flex-1 flex-col px-2 pt-2">
          {NAV_SECTIONS.map((section, sectionIndex) => (
            <div key={section.label ?? sectionIndex} className="flex flex-col gap-0.5">
              {sectionIndex > 0 &&
                (collapsed ? (
                  <div aria-hidden="true" className="mx-2 my-2 border-t border-border-1" />
                ) : section.label ? (
                  <p className="px-3 pb-1 pt-4 text-[10px] font-medium uppercase tracking-[0.08em] text-text-4">
                    {section.label}
                  </p>
                ) : (
                  <div aria-hidden="true" className="mx-1 my-2 border-t border-border-1" />
                ))}
              {section.items.map((item) => {
                const Icon = item.icon;
                // The link owns its active state. Every page except the
                // overview matches by prefix, so a detail route keeps its
                // list highlighted.
                return (
                  <Link
                    key={item.to}
                    to={item.to}
                    activeOptions={{ exact: item.to === "/" }}
                    activeProps={{ "aria-current": "page" }}
                    className={`relative flex h-9 items-center gap-2.5 rounded-sm px-3 text-base font-medium transition-colors duration-150 ease-standard ${collapsed ? "justify-center px-0" : ""}`}
                    inactiveProps={{
                      className: "text-text-3 hover:bg-bg-hover hover:text-text-2",
                    }}
                  >
                    {({ isActive }) => (
                      <>
                        {isActive && (
                          <span
                            aria-hidden="true"
                            className="absolute inset-y-1.5 left-0 w-0.5 rounded-full bg-accent"
                          />
                        )}
                        <Icon className="size-4 shrink-0" />
                        {!collapsed && <span>{item.label}</span>}
                      </>
                    )}
                  </Link>
                );
              })}
            </div>
          ))}
        </nav>

        <div className="flex flex-col gap-0.5 border-t border-border-1 p-2">
          <GatewayStatus collapsed={collapsed} />
          <ThemeToggle collapsed={collapsed} />
          <a
            href="https://github.com/agentstation/starport"
            target="_blank"
            rel="noreferrer"
            className={`${FOOTER_ITEM} ${collapsed ? "justify-center px-0" : ""}`}
          >
            <GitHubMark className="size-4 shrink-0" />
            {!collapsed && (
              <>
                <span>GitHub</span>
                <NewTabIcon aria-hidden="true" className="size-3 shrink-0" />
              </>
            )}
          </a>
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                  type="button"
                  onClick={() => setCollapsed((value) => !value)}
                  aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
                  className={`${FOOTER_ITEM} ${collapsed ? "justify-center px-0" : ""}`}
                />
              }
            >
            {collapsed ? (
              <PanelLeftOpen className="size-4 shrink-0" />
            ) : (
              <>
                <PanelLeftClose className="size-4 shrink-0" />
                <span>Collapse</span>
              </>
            )}
            </TooltipTrigger>
            <TooltipContent side="right">
              {collapsed ? "Expand sidebar" : "Collapse sidebar"}
            </TooltipContent>
          </Tooltip>
        </div>
      </aside>

      <main
        id="main"
        tabIndex={-1}
        style={
          {
            "--app-banner": openGateway ? BANNER_HEIGHT : "0px",
          } as React.CSSProperties
        }
        className={`min-w-0 flex-1 transition-[margin] duration-150 ease-standard ${
          collapsed ? "ml-16" : "ml-60"
        }`}
      >
        {openGateway && <OpenGatewayBanner />}
        {pathname === "/chat" ? (
          // Chat owns its full-height layout (thread sidebar + column).
          children
        ) : (
          <div className="mx-auto max-w-[1280px] px-8 py-8">{children}</div>
        )}
      </main>
      <CommandPalette />
      <Toaster />
    </div>
    </TooltipProvider>
  );
}
