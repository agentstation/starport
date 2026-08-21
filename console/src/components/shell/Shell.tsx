import { useQuery } from "@tanstack/react-query";
import { Link, useRouterState } from "@tanstack/react-router";
import {
  BarChart3,
  Key,
  LayoutDashboard,
  MessageSquare,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Server,
  Settings,
  SlidersHorizontal,
  Sparkles,
  Sun,
} from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";

import { appliedTheme, setTheme } from "@/lib/theme";

// Nav entries flip to implemented as their page tasks land (CM3–CM10).
const NAV = [
  { to: "/", label: "Overview", icon: LayoutDashboard, implemented: true },
  { to: "/chat", label: "Chat", icon: MessageSquare, implemented: false },
  { to: "/models", label: "Models", icon: Sparkles, implemented: true },
  { to: "/providers", label: "Providers", icon: Server, implemented: true },
  { to: "/keys", label: "Keys", icon: Key, implemented: true },
  { to: "/usage", label: "Usage", icon: BarChart3, implemented: false },
  { to: "/presets", label: "Presets", icon: SlidersHorizontal, implemented: false },
  { to: "/settings", label: "Settings", icon: Settings, implemented: false },
] as const;

const COLLAPSE_KEY = "starport.sidebar.collapsed";

type Health = { status?: string; version?: string };

function useGatewayHealth() {
  return useQuery<Health>({
    queryKey: ["health"],
    queryFn: async () => {
      const response = await fetch("/health/ready");
      if (!response.ok) {
        throw new Error(`health ${response.status}`);
      }
      return response.json() as Promise<Health>;
    },
    refetchInterval: 30_000,
    retry: false,
  });
}

// Lucide 1.x removed brand icons; GitHub's mark is inlined (octicon path).
function GitHubMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true" className={className}>
      <path
        fill="currentColor"
        d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"
      />
    </svg>
  );
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

function ThemeToggle({ collapsed }: { collapsed: boolean }) {
  const [theme, setThemeState] = useState<"dark" | "light">(() => appliedTheme());
  const flip = () => {
    const next = theme === "dark" ? "light" : "dark";
    setTheme(next);
    setThemeState(next);
  };
  const Icon = theme === "dark" ? Sun : Moon;
  return (
    <button
      type="button"
      onClick={flip}
      aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
      className="flex h-8 items-center gap-2 rounded-sm px-2 text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-2"
    >
      <Icon className="size-4 shrink-0" />
      {!collapsed && (
        <span className="text-xs">{theme === "dark" ? "Light theme" : "Dark theme"}</span>
      )}
    </button>
  );
}

function GatewayStatus({ collapsed }: { collapsed: boolean }) {
  const health = useGatewayHealth();
  const ok = health.data?.status === "ok";
  const label = health.isPending ? "Connecting" : ok ? "Gateway healthy" : "Gateway unreachable";
  return (
    <div className="flex h-8 items-center gap-2 px-2" title={label}>
      <span
        aria-hidden="true"
        className={`size-2 shrink-0 rounded-full ${
          health.isPending ? "bg-text-4" : ok ? "bg-success" : "bg-error"
        }`}
      />
      {!collapsed && (
        <span className="truncate text-xs text-text-3">
          {label}
          {ok && health.data?.version ? ` · v${health.data.version}` : ""}
        </span>
      )}
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

  return (
    <div className="flex min-h-screen bg-bg-canvas text-text-1">
      <aside
        className={`fixed inset-y-0 left-0 flex flex-col border-r border-border-1 bg-bg-panel transition-[width] duration-150 ease-standard ${
          collapsed ? "w-16" : "w-60"
        }`}
      >
        <div className={`flex h-14 items-center gap-2.5 ${collapsed ? "justify-center px-0" : "px-4"}`}>
          <StarMark />
          {!collapsed && (
            <span className="text-base font-semibold tracking-[-0.01em]">Starport</span>
          )}
        </div>

        <nav aria-label="Console" className="flex flex-1 flex-col gap-0.5 px-2 pt-2">
          {NAV.map((item) => {
            const active = pathname === item.to;
            const Icon = item.icon;
            if (!item.implemented) {
              return (
                <span
                  key={item.to}
                  aria-disabled="true"
                  title="Not built yet"
                  className={`relative flex h-9 items-center gap-2.5 rounded-sm px-3 text-base text-text-4 ${collapsed ? "justify-center px-0" : ""}`}
                >
                  <Icon className="size-4 shrink-0" />
                  {!collapsed && <span>{item.label}</span>}
                </span>
              );
            }
            return (
              <Link
                key={item.to}
                to={item.to}
                aria-current={active ? "page" : undefined}
                className={`relative flex h-9 items-center gap-2.5 rounded-sm px-3 text-base font-medium transition-colors duration-150 ease-standard ${collapsed ? "justify-center px-0" : ""} ${
                  active
                    ? "bg-bg-hover text-text-1"
                    : "text-text-3 hover:bg-bg-hover hover:text-text-2"
                }`}
              >
                {active && (
                  <span
                    aria-hidden="true"
                    className="absolute inset-y-1.5 left-0 w-0.5 rounded-full bg-accent"
                  />
                )}
                <Icon className="size-4 shrink-0" />
                {!collapsed && <span>{item.label}</span>}
              </Link>
            );
          })}
        </nav>

        <div className="flex flex-col gap-0.5 border-t border-border-1 p-2">
          <GatewayStatus collapsed={collapsed} />
          <ThemeToggle collapsed={collapsed} />
          <a
            href="https://github.com/agentstation/starport"
            target="_blank"
            rel="noreferrer"
            className="flex h-8 items-center gap-2 rounded-sm px-2 text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-2"
          >
            <GitHubMark className="size-4 shrink-0" />
            {!collapsed && <span className="text-xs">GitHub</span>}
          </a>
          <button
            type="button"
            onClick={() => setCollapsed((value) => !value)}
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            className="flex h-8 items-center gap-2 rounded-sm px-2 text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-2"
          >
            {collapsed ? (
              <PanelLeftOpen className="size-4 shrink-0" />
            ) : (
              <>
                <PanelLeftClose className="size-4 shrink-0" />
                <span className="text-xs">Collapse</span>
              </>
            )}
          </button>
        </div>
      </aside>

      <main
        className={`min-w-0 flex-1 transition-[margin] duration-150 ease-standard ${
          collapsed ? "ml-16" : "ml-60"
        }`}
      >
        <div className="mx-auto max-w-[1280px] px-8 py-8">{children}</div>
      </main>
    </div>
  );
}
