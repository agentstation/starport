import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { Download, Eye, EyeOff, Monitor, Moon, Sun, Trash2 } from "lucide-react";
import { useState, useSyncExternalStore, type ReactNode } from "react";

import { AuthModeControl } from "@/components/settings/AuthModeControl";
import { GhostButton, INPUT_CLASS, PrimaryButton } from "@/components/ui/Form";
import { Modal } from "@/components/ui/Modal";
import {
  getApiKey,
  listModels,
  onKeyChange,
  setApiKey,
  systemInfo,
} from "@/lib/api";
import { onThemeChange, savedTheme, setTheme, type ThemeChoice } from "@/lib/theme";

export const Route = createFileRoute("/settings")({
  component: SettingsPage,
});

// Chat conversations live in this browser's localStorage only. Both keys
// stay readable so history from an earlier console survives an upgrade.
const CHAT_STORAGE = "starport.chats";
const LEGACY_CHAT_STORAGE = "starport_chats";

// Calm density (DESIGN.md): sequential content uses flat sections with
// hairline dividers, not cards; 48px between sections.
function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section className="border-t border-border-1 py-6 first:border-t-0 first:pt-0">
      <h2 className="text-sm font-medium text-text-1">{title}</h2>
      {description && <p className="mt-1 text-sm text-text-3">{description}</p>}
      <div className="mt-4">{children}</div>
    </section>
  );
}

function maskKey(key: string): string {
  if (key.length <= 16) return key;
  return `${key.slice(0, 12)}…${key.slice(-4)}`;
}

type KeyStatus =
  | { kind: "idle" }
  | { kind: "testing" }
  | { kind: "valid"; models: number }
  | { kind: "rejected"; message: string };

function ConnectionSection() {
  const queryClient = useQueryClient();
  const storedKey = useSyncExternalStore(onKeyChange, getApiKey);
  const [draft, setDraft] = useState("");
  const [reveal, setReveal] = useState(false);
  const [status, setStatus] = useState<KeyStatus>({ kind: "idle" });

  const saveAndTest = async () => {
    const value = draft.trim();
    if (!value) return;
    setStatus({ kind: "testing" });
    const previous = getApiKey();
    setApiKey(value);
    try {
      const models = await listModels();
      queryClient.invalidateQueries();
      setStatus({ kind: "valid", models: models.length });
      setDraft("");
      setReveal(false);
    } catch (error) {
      setApiKey(previous);
      setStatus({
        kind: "rejected",
        message:
          error instanceof Error && error.message
            ? error.message
            : "The gateway rejected that key.",
      });
    }
  };

  const clear = () => {
    setApiKey("");
    queryClient.invalidateQueries();
    setDraft("");
    setStatus({ kind: "idle" });
  };

  return (
    <Section
      title="Connection"
      description="The console talks to the gateway that served this page. The key is stored in this browser's localStorage only."
    >
      <form
        className="flex max-w-xl gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          void saveAndTest();
        }}
      >
        <div className="relative min-w-0 flex-1">
          <input
            type={reveal ? "text" : "password"}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder={storedKey ? "Replace the stored key…" : "STARPORT_…"}
            aria-label="Gateway API key"
            autoComplete="off"
            spellCheck={false}
            className={`${INPUT_CLASS} w-full pr-9 font-mono`}
          />
          <button
            type="button"
            onClick={() => setReveal((value) => !value)}
            aria-label={reveal ? "Hide key" : "Show key"}
            className="absolute inset-y-0 right-0 flex w-9 items-center justify-center text-text-4 transition-colors duration-150 ease-standard hover:text-text-2"
          >
            {reveal ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
          </button>
        </div>
        <PrimaryButton
          type="submit"
          disabled={!draft.trim() || status.kind === "testing"}
        >
          {status.kind === "testing" ? "Testing…" : "Save & test"}
        </PrimaryButton>
        {storedKey && <GhostButton onClick={clear}>Clear</GhostButton>}
      </form>
      <p className="mt-3 text-sm text-text-3" aria-live="polite">
        {status.kind === "valid" ? (
          <span className="text-success">
            Key valid · {status.models} models visible
          </span>
        ) : status.kind === "rejected" ? (
          <span className="text-error">Key rejected: {status.message}</span>
        ) : storedKey ? (
          <>
            Current key{" "}
            <code className="rounded-xs bg-bg-raised px-1.5 py-0.5 font-mono text-xs text-text-2">
              {maskKey(storedKey)}
            </code>
          </>
        ) : (
          "No key set. Locked pages stay locked until one is saved."
        )}
      </p>
    </Section>
  );
}

// Authentication is a gateway setting and not a browser one, which is why it
// sits directly under Connection: the key above is what this browser presents,
// and the switch below is whether the gateway asks anyone for one.
function AuthenticationSection() {
  return (
    <Section
      title="Authentication"
      description="Whether this gateway requires an API key. It applies to every client of this deployment, and it can only be changed from the machine running the gateway."
    >
      <AuthModeControl />
    </Section>
  );
}

const THEME_CHOICES: { value: ThemeChoice; label: string; icon: typeof Sun }[] = [
  { value: "dark", label: "Dark", icon: Moon },
  { value: "light", label: "Light", icon: Sun },
  { value: "system", label: "System", icon: Monitor },
];

function AppearanceSection() {
  const choice = useSyncExternalStore(onThemeChange, savedTheme);
  return (
    <Section
      title="Appearance"
      description="System follows the operating-system preference."
    >
      <div
        role="radiogroup"
        aria-label="Theme"
        className="inline-flex rounded-sm border border-border-2 p-0.5"
      >
        {THEME_CHOICES.map(({ value, label, icon: Icon }) => (
          <button
            key={value}
            type="button"
            role="radio"
            aria-checked={choice === value}
            onClick={() => setTheme(value)}
            className={`flex h-8 items-center gap-1.5 rounded-xs px-3 text-sm transition-colors duration-150 ease-standard ${
              choice === value
                ? "bg-bg-hover text-text-1"
                : "text-text-3 hover:text-text-2"
            }`}
          >
            <Icon className="size-4" />
            {label}
          </button>
        ))}
      </div>
    </Section>
  );
}

function conversationCount(): number {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(CHAT_STORAGE) ?? "[]");
    return Array.isArray(parsed) ? parsed.length : 0;
  } catch {
    return 0;
  }
}

function ChatDataSection() {
  const [count, setCount] = useState(conversationCount);
  const [confirming, setConfirming] = useState(false);

  const exportJson = () => {
    const raw = localStorage.getItem(CHAT_STORAGE) ?? "[]";
    const url = URL.createObjectURL(
      new Blob([raw], { type: "application/json" }),
    );
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "starport-chats.json";
    anchor.click();
    URL.revokeObjectURL(url);
  };

  const deleteAll = () => {
    localStorage.removeItem(CHAT_STORAGE);
    localStorage.removeItem(LEGACY_CHAT_STORAGE);
    setCount(0);
    setConfirming(false);
  };

  return (
    <Section
      title="Chat data"
      description="Conversations live in this browser — the gateway never stores console state."
    >
      <p className="text-sm text-text-2">
        {count} conversation{count === 1 ? "" : "s"} stored locally
      </p>
      <div className="mt-3 flex gap-2">
        <GhostButton onClick={exportJson} disabled={count === 0}>
          <Download className="mr-1.5 size-4" />
          Export JSON
        </GhostButton>
        <button
          type="button"
          onClick={() => setConfirming(true)}
          disabled={count === 0}
          className="flex h-9 items-center rounded-sm px-3 text-sm text-text-2 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error disabled:opacity-50"
        >
          <Trash2 className="mr-1.5 size-4" />
          Delete all
        </button>
      </div>
      {confirming && (
        <Modal
          title="Delete all conversations"
          onClose={() => setConfirming(false)}
          footer={
            <>
              <GhostButton onClick={() => setConfirming(false)}>
                Cancel
              </GhostButton>
              <button
                type="button"
                onClick={deleteAll}
                className="flex h-9 items-center rounded-sm bg-error px-4 text-sm font-medium text-white transition-opacity duration-150 ease-standard hover:opacity-90"
              >
                Delete all
              </button>
            </>
          }
        >
          <p className="text-sm text-text-2">
            This removes all{" "}
            <strong className="font-semibold text-text-1">
              {count} conversation{count === 1 ? "" : "s"}
            </strong>{" "}
            stored in this browser. There is no undo.
          </p>
        </Modal>
      )}
    </Section>
  );
}

function AboutSection() {
  // Version and storage need admin scope; the section degrades to the
  // gateway origin alone when /admin/info is locked.
  const { data: info } = useQuery({
    queryKey: ["system-info"],
    queryFn: systemInfo,
    retry: false,
  });
  return (
    <Section
      title="About"
      description="Starport is a local, open-source LLM gateway — an OpenRouter-compatible drop-in that routes against the Starmap catalog."
    >
      <dl className="grid max-w-md grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-sm">
        <dt className="text-text-3">Gateway</dt>
        <dd className="font-mono text-text-2">{location.origin}</dd>
        {info?.version && (
          <>
            <dt className="text-text-3">Version</dt>
            <dd className="font-mono text-text-2">{info.version}</dd>
          </>
        )}
        {info?.storage?.type && (
          <>
            <dt className="text-text-3">Storage</dt>
            <dd className="font-mono text-text-2">{info.storage.type}</dd>
          </>
        )}
        {info?.uptime && info.uptime !== "unavailable" && (
          <>
            <dt className="text-text-3">Uptime</dt>
            <dd className="font-mono text-text-2">{info.uptime}</dd>
          </>
        )}
      </dl>
      <a
        href="https://github.com/agentstation/starport"
        target="_blank"
        rel="noreferrer"
        className="mt-4 inline-flex h-9 items-center rounded-sm px-3 text-sm text-text-2 transition-colors duration-150 ease-standard hover:bg-bg-hover"
      >
        agentstation/starport ↗
      </a>
    </Section>
  );
}

function SettingsPage() {
  return (
    <div>
      <div className="mb-8">
        <h1 className="text-xl font-semibold tracking-[-0.01em]">Settings</h1>
        <p className="mt-1 text-sm text-text-3">
          Connection, appearance, and local chat data.
        </p>
      </div>
      <div className="max-w-2xl">
        <ConnectionSection />
        <AuthenticationSection />
        <AppearanceSection />
        <ChatDataSection />
        <AboutSection />
      </div>
    </div>
  );
}
