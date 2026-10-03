// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { CatalogPanel } from "@/components/shell/CatalogPanel";
import type { CatalogAdminRead, CatalogSummaryRead } from "@/components/shell/CatalogSummary";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { EffectiveReport, SchemaSetting } from "@/lib/configuration";
import { openConsole, resetGateway, stubGateway } from "@/test/console";

import tokens from "./tokens.css?raw";

// Every instruction in Settings and in the catalog panel meets WCAG AA in
// both themes. The test reads the color tokens from tokens.css, resolves
// the text-* class and the nearest bg-* ground of each instructional
// element, and computes the contrast ratio. A page with no bg-* ancestor
// sits on the canvas. A sheet sets its own raised ground.

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return { ...actual, catalogChanges: async () => ({ available: false }) };
});

afterEach(resetGateway);

type Theme = "dark" | "light";
type RGBA = [number, number, number, number];

// --- Tokens ---

function block(selector: string): string {
  const start = tokens.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`tokens.css has no ${selector} block`);
  return tokens.slice(start, tokens.indexOf("\n}", start));
}

function declarations(source: string): Map<string, string> {
  const found = new Map<string, string>();
  for (const match of source.matchAll(/--([\w-]+):\s*([^;]+);/g)) {
    found.set(match[1] ?? "", (match[2] ?? "").trim());
  }
  return found;
}

const ROLE: Record<Theme, Map<string, string>> = {
  dark: declarations(block(":root")),
  light: declarations(block(':root[data-theme="light"]')),
};
const THEME = declarations(block("@theme inline"));

function parseColor(value: string): RGBA | null {
  const hex = /^#([0-9a-f]{6})$/i.exec(value);
  if (hex) {
    const number = Number.parseInt(hex[1] ?? "", 16);
    return [(number >> 16) & 255, (number >> 8) & 255, number & 255, 1];
  }
  const rgba = /^rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)$/.exec(value);
  if (rgba) return [Number(rgba[1]), Number(rgba[2]), Number(rgba[3]), rgba[4] === undefined ? 1 : Number(rgba[4])];
  return null;
}

// color resolves a utility name, such as text-2 in text-text-2, through
// the theme bridge to the role token of one theme.
function color(name: string, theme: Theme): RGBA | null {
  const bridged = THEME.get(`color-${name.replace(/\/\d+$/, "")}`);
  if (!bridged) return null;
  const role = /^var\(--([\w-]+)\)$/.exec(bridged);
  const value = role ? ROLE[theme].get(role[1] ?? "") : bridged;
  const parsed = value ? parseColor(value) : null;
  const opacity = /\/(\d+)$/.exec(name);
  if (parsed && opacity) parsed[3] *= Number(opacity[1]) / 100;
  return parsed;
}

function size(name: string): number | null {
  const value = THEME.get(`text-${name}`);
  const px = value ? /^(\d+(?:\.\d+)?)px$/.exec(value) : null;
  return px ? Number(px[1]) : null;
}

// --- WCAG ---

function over(top: RGBA, below: RGBA): RGBA {
  const alpha = top[3];
  return [0, 1, 2].map((index) => (top[index] ?? 0) * alpha + (below[index] ?? 0) * (1 - alpha)).concat(1) as RGBA;
}

function luminance([red, green, blue]: RGBA): number {
  const channel = (value: number) => {
    const scaled = value / 255;
    return scaled <= 0.03928 ? scaled / 12.92 : ((scaled + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(red) + 0.7152 * channel(green) + 0.0722 * channel(blue);
}

function ratio(first: RGBA, second: RGBA): number {
  const [light, dark] = [luminance(first), luminance(second)].sort((a, b) => b - a);
  return ((light ?? 0) + 0.05) / ((dark ?? 0) + 0.05);
}

// --- Elements ---

const INSTRUCTION = "p, dt, dd, li, label, [data-instruction]";

// classes returns the unconditional utilities of one element. A variant
// such as hover: or data-[...]: applies only in a state.
function classes(element: Element): string[] {
  return (element.getAttribute("class") ?? "").split(/\s+/).filter((name) => name && !name.includes(":"));
}

function lineage(element: Element): Element[] {
  const chain: Element[] = [];
  for (let node: Element | null = element; node; node = node.parentElement) chain.push(node);
  return chain;
}

function textColor(element: Element, theme: Theme): RGBA {
  for (const node of lineage(element)) {
    for (const name of classes(node)) {
      const found = name.startsWith("text-") ? color(name.slice(5), theme) : null;
      if (found) return found;
    }
  }
  return color("foreground", theme) as RGBA;
}

function ground(element: Element, theme: Theme): RGBA {
  const layers: RGBA[] = [];
  for (const node of lineage(element)) {
    for (const name of classes(node)) {
      const found = name.startsWith("bg-") ? color(name.slice(3), theme) : null;
      if (found) layers.push(found);
    }
    if (layers.at(-1)?.[3] === 1) break;
  }
  let below = color("bg-canvas", theme) as RGBA;
  for (const layer of layers.reverse()) below = over(layer, below);
  return below;
}

function fontSize(element: Element): number {
  for (const node of lineage(element)) {
    for (const name of classes(node)) {
      const found = name.startsWith("text-") ? size(name.slice(5)) : null;
      if (found) return found;
    }
  }
  return 16;
}

function mono(element: Element): boolean {
  return lineage(element).some((node) => classes(node).includes("font-mono"));
}

function bold(element: Element): boolean {
  return lineage(element).some((node) => classes(node).some((name) => name === "font-semibold" || name === "font-bold"));
}

function shown(element: Element): boolean {
  return Boolean(element.textContent?.trim()) && !lineage(element).some((node) => classes(node).includes("sr-only"));
}

function instructions(root: ParentNode): Element[] {
  return [...root.querySelectorAll(INSTRUCTION)].filter(shown);
}

// failures lists each instruction under AA: 4.5:1, or 3:1 for large text
// (24 px, or 18.66 px bold).
function failures(root: ParentNode): string[] {
  const found: string[] = [];
  for (const element of instructions(root)) {
    const px = fontSize(element);
    const minimum = px >= 24 || (px >= 18.66 && bold(element)) ? 3 : 4.5;
    for (const theme of ["dark", "light"] as const) {
      const measured = ratio(over(textColor(element, theme), ground(element, theme)), ground(element, theme));
      if (measured < minimum) {
        found.push(`${theme} ${measured.toFixed(2)}:1 <${element.tagName.toLowerCase()}> ${element.textContent?.trim().slice(0, 60)}`);
      }
    }
  }
  return found;
}

// --- Fixtures ---

function schemaSetting(key: string, extra: Partial<SchemaSetting> = {}): SchemaSetting {
  return {
    id: key.replaceAll("_", "."),
    key,
    environment: `STARMAP_${key.toUpperCase()}`,
    type: "string",
    scope: "deployment",
    mutability: "runtime-replacement",
    sensitive: false,
    applicability: ["all"],
    ...extra,
  };
}

const SCHEMA = [
  schemaSetting("catalog_source"),
  schemaSetting("catalog_source_api_key", { sensitive: true, applicability: ["starmap"] }),
  schemaSetting("catalog_source_token", { sensitive: true, applicability: ["public", "github"] }),
  schemaSetting("network_mode"),
  schemaSetting("catalog_refresh_timeout", { type: "duration" }),
  schemaSetting("state_dir", { scope: "node", mutability: "restart" }),
];

const EFFECTIVE: EffectiveReport = {
  management: "local",
  revision: { authority: "local", checksum: "a".repeat(64), file_checksum: "a".repeat(64), retained: false },
  target: { kind: "local-file", path: "/etc/starport/starport.env" },
  paths: {
    relative_path_base: "/etc/starport",
    relative_path_base_origin: "config-file",
    config_dir: "/etc/starport",
    config_file: "/etc/starport/starport.env",
    data_dir: "/var/lib/starport",
    state_dir: "/var/lib/starport/state",
    cache_dir: "/var/cache/starport",
    runtime_dir: "/run/starport",
    baseline_dir: "/var/lib/starport/baseline",
    badger_dir: "/var/lib/starport/badger",
    sqlite_file: "/var/lib/starport/starport.db",
    files_dir: "/var/lib/starport/files",
    local_token_file: "/var/lib/starport/token",
    welcome_stamp_file: "/var/lib/starport/welcome",
    instance_id: "instance-1",
    deployment_id: "deployment-1",
    origins: { state: { path: "/var/lib/starport/state", origin: "platform-default" } },
  },
  storage: [{ id: "kv", selection: "badger", lifetime: "local", location: "/var/lib/starport/badger" }],
  settings: [
    { id: "catalog.source", name: "STARMAP_CATALOG_SOURCE", value: "starmap", scope: "deployment", authority: "local", origin: "file:/etc/starport/starport.env" },
    { id: "catalog.source.api.key", name: "STARMAP_CATALOG_SOURCE_API_KEY", value: "<redacted>", scope: "deployment", authority: "local", origin: "env" },
    { id: "catalog.source.token", name: "STARMAP_CATALOG_SOURCE_TOKEN", value: "", scope: "deployment", authority: "local", origin: "default" },
    { id: "network.mode", name: "STARMAP_NETWORK_MODE", value: "online", scope: "deployment", authority: "local", origin: "default" },
    { id: "catalog.refresh.timeout", name: "STARMAP_CATALOG_REFRESH_TIMEOUT", value: "5m", scope: "deployment", authority: "local", origin: "default" },
    { id: "state.dir", name: "STARPORT_STATE_DIR", value: "/var/lib/starport/state", scope: "node", authority: "local", origin: "platform-default" },
  ],
};

const INFO = {
  service: "starport",
  version: "1.2.0",
  commit: "abc1234",
  started_at: "2026-10-03T00:00:00Z",
  uptime: "1h0m0s",
  storage: { type: "badger", relational: "sqlite", status: "connected" },
  files: { backend: "local" },
  telemetry: { metrics: "admin" },
  retention: { audit_seconds: 172800, files_seconds: 0, job_assets_seconds: 86400 },
};

async function openSettings() {
  stubGateway({
    "/api/v1/admin/info": INFO,
    "/api/v1/admin/config/schema": { settings: SCHEMA },
    "/api/v1/admin/config/effective": EFFECTIVE,
  });
  openConsole("/settings");
  fireEvent.click(await screen.findByRole("button", { name: "Advanced details" }));
  return document.querySelector("main") ?? document.body;
}

const SUMMARY: CatalogSummaryRead = {
  summary: {
    generation_id: "01J9ABCDEFGHJKMNPQRSTVWXYZ",
    generated_at: "2026-10-02T00:00:00Z",
    age_seconds: 7200,
    usable: true,
    freshness: "current",
    providers: 12,
    models: 511,
    next_update_at: "2026-10-03T06:00:00Z",
  },
  error: null,
  pending: false,
  refused: false,
  session: "session",
};

function openCatalogPanel() {
  stubGateway({});
  const admin: CatalogAdminRead = {
    status: { runtime: { source_kind: "starmap", fallback: false } },
    admin: true,
    working: { id: "0f6c2a9e-1111-4222-8333-444455556666", state: "running", joined: true },
  };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <TooltipProvider>
        <CatalogPanel read={SUMMARY} admin={admin} open onOpenChange={() => {}} />
      </TooltipProvider>
    </QueryClientProvider>,
  );
  return document.querySelector('[data-slot="sheet-content"]') ?? document.body;
}

describe("instruction contrast", () => {
  it("settings instructions meet WCAG AA in both themes", async () => {
    const root = await openSettings();
    await screen.findByText("Saves go to");

    expect(instructions(root).length).toBeGreaterThan(20);
    expect(failures(root)).toEqual([]);
    // The U08 pair failed before the fix: disabled text on the canvas.
    expect(ratio(color("text-4", "light") as RGBA, color("bg-canvas", "light") as RGBA)).toBeLessThan(4.5);
  });

  it("catalog panel instructions meet WCAG AA in both themes", async () => {
    const root = openCatalogPanel();
    await screen.findByText("Refresh sources");

    expect(instructions(root).length).toBeGreaterThan(5);
    expect(failures(root)).toEqual([]);
  });

  // The prose of each settings section. The page header keeps the subtitle
  // size that every console page shares.
  it("settings prose is at least 16 px", async () => {
    const root = await openSettings();
    await screen.findByText("Saves go to");

    const sections = [...root.querySelectorAll("section")];
    expect(sections.length).toBeGreaterThan(5);
    const small = sections
      .flatMap(instructions)
      .filter((element) => element.matches("p, dt, dd, li, label"))
      .map((element) => ({ element, px: fontSize(element), floor: mono(element) ? 14 : 16 }))
      .filter(({ px, floor }) => px < floor)
      .map(({ element, px }) => `${px} px <${element.tagName.toLowerCase()}> ${element.textContent?.trim().slice(0, 60)}`);
    expect(small).toEqual([]);
  });
});
