import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { Plus, SlidersHorizontal, Trash2 } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";

import { ModelPicker } from "@/components/models/ModelPicker";
import {
  Field,
  GhostButton,
  INPUT_CLASS,
  PrimaryButton,
  RowAction,
  TEXTAREA_CLASS,
} from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { Modal } from "@/components/ui/Modal";
import {
  accessMessage,
  ApiError,
  createPreset,
  deletePreset,
  listPresets,
  updatePreset,
  type Preset,
  type PresetConfig,
  type PresetProviderPreferences,
} from "@/lib/api";
import { formatRelativeTime } from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

export const Route = createFileRoute("/presets")({
  component: PresetsPage,
});

// Provider sort orders mirror the OpenRouter wire values the preset
// domain accepts; empty keeps the server default.
const SORTS = ["", "price", "latency", "throughput", "spread"] as const;

function utcTooltip(iso: string | undefined): string | undefined {
  if (!iso) return undefined;
  const date = new Date(iso);
  return Number.isFinite(date.getTime()) ? date.toISOString() : undefined;
}

// RoutingPills compresses the stored provider policy into scannable
// neutral chips; a preset without routing shows a quiet dash.
function RoutingPills({
  provider,
}: {
  provider: PresetProviderPreferences | undefined;
}) {
  const parts: string[] = [];
  if (provider) {
    if (provider.sort) parts.push(`sort ${provider.sort}`);
    if (provider.order?.length) parts.push(`order ${provider.order.join(" → ")}`);
    if (provider.only?.length) parts.push(`only ${provider.only.join(", ")}`);
    if (provider.ignore?.length)
      parts.push(`ignore ${provider.ignore.join(", ")}`);
    if (provider.max_prompt_price_per_1m)
      parts.push(`≤$${provider.max_prompt_price_per_1m}/M in`);
    if (provider.max_completion_price_per_1m)
      parts.push(`≤$${provider.max_completion_price_per_1m}/M out`);
    if (provider.allow_fallbacks === false) parts.push("no fallbacks");
  }
  if (parts.length === 0) return <span className="text-text-4">—</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {parts.map((part) => (
        <span
          key={part}
          className="inline-flex h-5 items-center whitespace-nowrap rounded-xs bg-bg-raised px-1.5 text-xs text-text-3"
        >
          {part}
        </span>
      ))}
    </div>
  );
}

// SamplingSummary lists the stored overrides so the table answers
// "what does this preset change" without opening the editor.
function samplingSummary(config: PresetConfig): string {
  const parts: string[] = [];
  if (config.temperature !== undefined) parts.push(`temp ${config.temperature}`);
  if (config.top_p !== undefined) parts.push(`top-p ${config.top_p}`);
  if (config.max_tokens !== undefined) parts.push(`max ${config.max_tokens}`);
  if (config.seed !== undefined) parts.push(`seed ${config.seed}`);
  if (config.system) parts.push("system prompt");
  if (config.stop?.length) parts.push(`${config.stop.length} stop`);
  return parts.join(" · ");
}

// --- Editor modal ---

type EditorDraft = {
  name: string;
  description: string;
  model: string;
  models: string;
  system: string;
  temperature: string;
  topP: string;
  maxTokens: string;
  seed: string;
  presencePenalty: string;
  frequencyPenalty: string;
  stop: string;
  order: string;
  only: string;
  ignore: string;
  sort: string;
  maxPromptPrice: string;
  maxCompletionPrice: string;
  allowFallbacks: boolean;
};

function draftFromPreset(preset: Preset | null): EditorDraft {
  const config = preset?.config ?? {};
  const provider = config.provider ?? {};
  const str = (value: number | undefined) =>
    value === undefined ? "" : String(value);
  return {
    name: preset?.name ?? "",
    description: preset?.description ?? "",
    model: config.model ?? "",
    models: (config.models ?? []).join(", "),
    system: config.system ?? "",
    temperature: str(config.temperature),
    topP: str(config.top_p),
    maxTokens: str(config.max_tokens),
    seed: str(config.seed),
    presencePenalty: str(config.presence_penalty),
    frequencyPenalty: str(config.frequency_penalty),
    stop: (config.stop ?? []).join(", "),
    order: (provider.order ?? []).join(", "),
    only: (provider.only ?? []).join(", "),
    ignore: (provider.ignore ?? []).join(", "),
    sort: SORTS.includes(provider.sort as (typeof SORTS)[number])
      ? (provider.sort ?? "")
      : "",
    maxPromptPrice: provider.max_prompt_price_per_1m
      ? String(provider.max_prompt_price_per_1m)
      : "",
    maxCompletionPrice: provider.max_completion_price_per_1m
      ? String(provider.max_completion_price_per_1m)
      : "",
    allowFallbacks: provider.allow_fallbacks !== false,
  };
}

// configFromDraft builds the typed config, dropping empty fields so
// the stored preset carries only deliberate settings. Returns null
// when nothing is set — the backend rejects an empty config.
function configFromDraft(draft: EditorDraft): PresetConfig | null {
  const list = (value: string) =>
    value
      .split(",")
      .map((part) => part.trim())
      .filter(Boolean);
  const num = (value: string, integer = false) => {
    if (!value.trim()) return undefined;
    const parsed = integer ? Number.parseInt(value, 10) : Number.parseFloat(value);
    return Number.isFinite(parsed) ? parsed : undefined;
  };

  const config: PresetConfig = {};
  if (draft.model.trim()) config.model = draft.model.trim();
  const models = list(draft.models);
  if (models.length) config.models = models;
  if (draft.system.trim()) config.system = draft.system.trim();
  const sampling: Partial<PresetConfig> = {
    temperature: num(draft.temperature),
    top_p: num(draft.topP),
    max_tokens: num(draft.maxTokens, true),
    seed: num(draft.seed, true),
    presence_penalty: num(draft.presencePenalty),
    frequency_penalty: num(draft.frequencyPenalty),
  };
  for (const [key, value] of Object.entries(sampling)) {
    if (value !== undefined) {
      (config as Record<string, unknown>)[key] = value;
    }
  }
  const stop = list(draft.stop);
  if (stop.length) config.stop = stop;

  const routing: PresetProviderPreferences = {};
  const order = list(draft.order);
  const only = list(draft.only);
  const ignore = list(draft.ignore);
  if (order.length) routing.order = order;
  if (only.length) routing.only = only;
  if (ignore.length) routing.ignore = ignore;
  if (draft.sort) routing.sort = draft.sort;
  const maxPrompt = num(draft.maxPromptPrice);
  const maxCompletion = num(draft.maxCompletionPrice);
  if (maxPrompt) routing.max_prompt_price_per_1m = maxPrompt;
  if (maxCompletion) routing.max_completion_price_per_1m = maxCompletion;
  if (!draft.allowFallbacks) routing.allow_fallbacks = false;
  if (Object.keys(routing).length) config.provider = routing;

  return Object.keys(config).length ? config : null;
}

function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <h3 className="mt-1 border-t border-border-1 pt-4 text-xs font-semibold uppercase tracking-[0.05em] text-text-3">
      {children}
    </h3>
  );
}

function numberInput(
  value: string,
  onChange: (next: string) => void,
  attrs: React.InputHTMLAttributes<HTMLInputElement> = {},
) {
  return (
    <input
      type="number"
      value={value}
      onChange={(event) => onChange(event.target.value)}
      placeholder="default"
      className={INPUT_CLASS}
      {...attrs}
    />
  );
}

function EditorModal({
  preset,
  onClose,
  onSaved,
}: {
  preset: Preset | null;
  onClose: () => void;
  onSaved: (name: string, created: boolean) => void;
}) {
  const editing = preset !== null;
  const [draft, setDraft] = useState(() => draftFromPreset(preset));
  const [formError, setFormError] = useState("");
  const patch = (part: Partial<EditorDraft>) =>
    setDraft((prev) => ({ ...prev, ...part }));

  const save = useMutation({
    mutationFn: (body: Parameters<typeof createPreset>[0]) =>
      editing ? updatePreset(preset.name, body) : createPreset(body),
    onSuccess: () => onSaved(draft.name.trim(), !editing),
    onError: (error) =>
      setFormError(
        error instanceof ApiError && error.status === 409
          ? "Preset changed elsewhere — reload and retry."
          : `Save failed: ${error instanceof Error ? error.message : error}`,
      ),
  });

  const submit = () => {
    const name = draft.name.trim();
    if (!name) {
      setFormError("Name is required");
      return;
    }
    const config = configFromDraft(draft);
    if (!config) {
      setFormError("A preset needs at least one setting");
      return;
    }
    setFormError("");
    save.mutate({
      name,
      description: draft.description.trim(),
      config,
      ...(editing ? { revision: preset.revision } : {}),
    });
  };

  return (
    <Modal
      title={editing ? `Edit @preset/${preset.name}` : "New preset"}
      onClose={onClose}
      wide
      footer={
        <>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <PrimaryButton onClick={submit} disabled={save.isPending}>
            {editing ? "Save preset" : "Create preset"}
          </PrimaryButton>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-4">
          <Field
            label="Name"
            hint={editing ? "names are immutable" : "letters, digits, - and _"}
          >
            <input
              type="text"
              value={draft.name}
              onChange={(event) => patch({ name: event.target.value })}
              placeholder="e.g. fast-cheap"
              autoComplete="off"
              disabled={editing}
              className={`${INPUT_CLASS} font-mono disabled:opacity-50`}
            />
          </Field>
          <Field label="Description">
            <input
              type="text"
              value={draft.description}
              onChange={(event) => patch({ description: event.target.value })}
              placeholder="what this preset is for"
              autoComplete="off"
              className={INPUT_CLASS}
            />
          </Field>
        </div>

        <SectionTitle>Model</SectionTitle>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Model">
            <ModelPicker
              value={draft.model}
              onChange={(model) => patch({ model })}
              placeholder="search the catalog"
            />
          </Field>
          <Field label="Fallback models" hint="comma-separated, tried in order">
            <input
              type="text"
              value={draft.models}
              onChange={(event) => patch({ models: event.target.value })}
              placeholder="fallbacks, comma-separated"
              autoComplete="off"
              className={`${INPUT_CLASS} font-mono`}
            />
          </Field>
        </div>
        <Field label="System prompt">
          <textarea
            rows={3}
            value={draft.system}
            onChange={(event) => patch({ system: event.target.value })}
            placeholder="You are a helpful assistant…"
            className={TEXTAREA_CLASS}
          />
        </Field>

        <SectionTitle>Sampling</SectionTitle>
        <div className="grid grid-cols-3 gap-4">
          <Field label="Temperature">
            {numberInput(draft.temperature, (temperature) => patch({ temperature }), {
              step: 0.1,
              min: 0,
              max: 2,
            })}
          </Field>
          <Field label="Top-p">
            {numberInput(draft.topP, (topP) => patch({ topP }), {
              step: 0.05,
              min: 0,
              max: 1,
            })}
          </Field>
          <Field label="Max tokens">
            {numberInput(draft.maxTokens, (maxTokens) => patch({ maxTokens }), {
              min: 1,
            })}
          </Field>
          <Field label="Seed">
            {numberInput(draft.seed, (seed) => patch({ seed }))}
          </Field>
          <Field label="Presence penalty">
            {numberInput(
              draft.presencePenalty,
              (presencePenalty) => patch({ presencePenalty }),
              { step: 0.1, min: -2, max: 2 },
            )}
          </Field>
          <Field label="Frequency penalty">
            {numberInput(
              draft.frequencyPenalty,
              (frequencyPenalty) => patch({ frequencyPenalty }),
              { step: 0.1, min: -2, max: 2 },
            )}
          </Field>
        </div>
        <Field label="Stop sequences" hint="comma-separated">
          <input
            type="text"
            value={draft.stop}
            onChange={(event) => patch({ stop: event.target.value })}
            autoComplete="off"
            className={`${INPUT_CLASS} font-mono`}
          />
        </Field>

        <SectionTitle>Provider routing</SectionTitle>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Order" hint="try providers in this order">
            <input
              type="text"
              value={draft.order}
              onChange={(event) => patch({ order: event.target.value })}
              placeholder="e.g. groq, openai"
              autoComplete="off"
              className={`${INPUT_CLASS} font-mono`}
            />
          </Field>
          <Field label="Sort" hint="price, latency, throughput, or spread">
            <Select
              value={draft.sort}
              onChange={(event) => patch({ sort: event.target.value })}
            >
              {SORTS.map((sort) => (
                <option key={sort} value={sort}>
                  {sort || "server default"}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Only" hint="allowlist, comma-separated">
            <input
              type="text"
              value={draft.only}
              onChange={(event) => patch({ only: event.target.value })}
              autoComplete="off"
              className={`${INPUT_CLASS} font-mono`}
            />
          </Field>
          <Field label="Ignore" hint="denylist, comma-separated">
            <input
              type="text"
              value={draft.ignore}
              onChange={(event) => patch({ ignore: event.target.value })}
              autoComplete="off"
              className={`${INPUT_CLASS} font-mono`}
            />
          </Field>
          <Field label="Max prompt price" hint="USD per million input tokens">
            {numberInput(
              draft.maxPromptPrice,
              (maxPromptPrice) => patch({ maxPromptPrice }),
              { step: 0.01, min: 0 },
            )}
          </Field>
          <Field
            label="Max completion price"
            hint="USD per million output tokens"
          >
            {numberInput(
              draft.maxCompletionPrice,
              (maxCompletionPrice) => patch({ maxCompletionPrice }),
              { step: 0.01, min: 0 },
            )}
          </Field>
        </div>
        <label className="flex items-start gap-2 text-sm text-text-2">
          <input
            type="checkbox"
            checked={draft.allowFallbacks}
            onChange={(event) => patch({ allowFallbacks: event.target.checked })}
            className="mt-0.5 accent-(--accent)"
          />
          <span>Allow fallbacks beyond the ordered providers</span>
        </label>
        {formError && <p className="text-xs text-error">{formError}</p>}
      </div>
    </Modal>
  );
}

// --- Delete modal ---

function DeleteModal({
  preset,
  onClose,
  onDeleted,
  onError,
}: {
  preset: Preset;
  onClose: () => void;
  onDeleted: () => void;
  onError: (message: string) => void;
}) {
  const remove = useMutation({
    mutationFn: () => deletePreset(preset.name),
    onSuccess: onDeleted,
    onError: (error) =>
      onError(
        `Delete failed: ${error instanceof Error ? error.message : error}`,
      ),
  });
  return (
    <Modal
      title="Delete preset"
      onClose={onClose}
      footer={
        <>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <button
            type="button"
            onClick={() => remove.mutate()}
            disabled={remove.isPending}
            className="flex h-9 items-center rounded-sm bg-error px-4 text-sm font-medium text-white transition-opacity duration-150 ease-standard hover:opacity-90 disabled:opacity-50"
          >
            Delete preset
          </button>
        </>
      }
    >
      <p className="text-sm text-text-2">
        Delete{" "}
        <strong className="font-mono font-semibold text-text-1">
          @preset/{preset.name}
        </strong>
        ? Requests referencing it will start failing immediately.
      </p>
    </Modal>
  );
}

// --- Empty and page states ---

function EmptyState({ onCreate }: { onCreate: () => void }) {
  const curl = [
    `curl -X POST ${window.location.origin}/api/v1/chat/completions \\`,
    `  -H "Authorization: Bearer $STARPORT_API_KEY" \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -d '{"model":"@preset/fast-cheap","messages":[{"role":"user","content":"Hello"}]}'`,
  ].join("\n");
  return (
    <div className="flex flex-col items-center gap-4 rounded-md border border-border-1 bg-bg-panel px-6 py-12">
      <SlidersHorizontal aria-hidden="true" className="size-6 text-text-4" />
      <p className="text-sm text-text-2">
        No presets yet. Create one to reuse a model, prompt, and routing
        policy across requests.
      </p>
      <PrimaryButton onClick={onCreate}>
        <Plus className="size-3.5" />
        New preset
      </PrimaryButton>
      <div className="w-full max-w-xl overflow-x-auto rounded-sm border border-border-1 bg-bg-canvas p-3">
        <pre className="font-mono text-xs leading-relaxed text-text-2">
          {curl}
        </pre>
      </div>
    </div>
  );
}

function Header() {
  return (
    <div>
      <h1 className="text-xl font-semibold tracking-[-0.01em]">Presets</h1>
      <p className="mt-1 text-sm text-text-3">
        Reusable request configurations. Reference one from any request with{" "}
        <code className="rounded-xs bg-bg-raised px-1 py-0.5 font-mono text-xs text-text-2">
          @preset/name
        </code>
        .
      </p>
    </div>
  );
}

type ModalState =
  | { kind: "create" }
  | { kind: "edit"; preset: Preset }
  | { kind: "delete"; preset: Preset }
  | null;

function PresetsPage() {
  const keyUsable = useGatewayAccess();
  const queryClient = useQueryClient();
  const [modal, setModal] = useState<ModalState>(null);
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );
  const noticeTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(noticeTimer.current), []);

  const presets = useQuery({
    queryKey: ["presets"],
    queryFn: listPresets,
    enabled: keyUsable,
    retry: false,
  });

  const say = (text: string, error = false) => {
    setNotice({ text, error });
    clearTimeout(noticeTimer.current);
    noticeTimer.current = setTimeout(() => setNotice(null), 6000);
  };

  const reload = () =>
    queryClient.invalidateQueries({ queryKey: ["presets"] });


  let body: ReactNode;
  let canCreate = false;
  if (presets.error) {
    if (presets.error instanceof ApiError && presets.error.needsKey) {
      body = (
        <p className="text-base text-text-3">
          {accessMessage(presets.error, "presets:write")}
        </p>
      );
    } else if (
      presets.error instanceof ApiError &&
      presets.error.status === 503
    ) {
      body = (
        <p className="text-base text-text-3">
          Preset storage is not configured on this gateway.
        </p>
      );
    } else {
      body = (
        <p className="text-base text-text-3">
          Failed to load presets: {presets.error.message}
        </p>
      );
    }
  } else if (presets.isPending) {
    body = <p className="text-base text-text-3">Loading presets…</p>;
  } else if ((presets.data ?? []).length === 0) {
    body = <EmptyState onCreate={() => setModal({ kind: "create" })} />;
  } else {
    canCreate = true;
    body = (
      <div className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
              <th className="px-4 py-2.5">Preset</th>
              <th className="px-4 py-2.5">Model</th>
              <th className="px-4 py-2.5">Routing</th>
              <th className="px-4 py-2.5">Overrides</th>
              <th className="px-4 py-2.5">Updated</th>
              <th className="px-4 py-2.5" />
            </tr>
          </thead>
          <tbody>
            {(presets.data ?? []).map((preset) => (
              <tr
                key={preset.name}
                className="h-10 border-b border-border-1 transition-colors duration-150 ease-standard last:border-b-0 hover:bg-bg-hover"
              >
                <td className="px-4 py-2">
                  <div className="flex flex-col gap-0.5">
                    <code className="w-fit whitespace-nowrap rounded-xs bg-bg-raised px-1.5 py-0.5 font-mono text-xs text-text-1">
                      @preset/{preset.name}
                    </code>
                    {preset.description && (
                      <span className="text-xs text-text-4">
                        {preset.description}
                      </span>
                    )}
                  </div>
                </td>
                <td className="whitespace-nowrap px-4 py-2 font-mono text-xs text-text-2">
                  {preset.config.model ?? preset.config.models?.[0] ?? (
                    <span className="text-text-4">—</span>
                  )}
                </td>
                <td className="px-4 py-2">
                  <RoutingPills provider={preset.config.provider} />
                </td>
                <td className="px-4 py-2 text-xs text-text-3">
                  {samplingSummary(preset.config) || (
                    <span className="text-text-4">—</span>
                  )}
                </td>
                <td
                  className="whitespace-nowrap px-4 py-2 text-xs text-text-3"
                  title={utcTooltip(preset.updated_at)}
                >
                  {preset.updated_at
                    ? formatRelativeTime(preset.updated_at)
                    : "—"}
                </td>
                <td className="px-4 py-2">
                  <div className="flex items-center justify-end gap-1">
                    <RowAction
                      onClick={() => setModal({ kind: "edit", preset })}
                    >
                      edit
                    </RowAction>
                    <button
                      type="button"
                      onClick={() => setModal({ kind: "delete", preset })}
                      aria-label={`Delete ${preset.name}`}
                      className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error"
                    >
                      <Trash2 className="size-3.5" />
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <Header />
        <div className="flex items-center gap-3">
          {notice && (
            <span
              className={`text-xs ${notice.error ? "text-error" : "text-success"}`}
            >
              {notice.text}
            </span>
          )}
          {canCreate && (
            <PrimaryButton onClick={() => setModal({ kind: "create" })}>
              <Plus className="size-3.5" />
              New preset
            </PrimaryButton>
          )}
        </div>
      </div>
      {body}
      {(modal?.kind === "create" || modal?.kind === "edit") && (
        <EditorModal
          preset={modal.kind === "edit" ? modal.preset : null}
          onClose={() => setModal(null)}
          onSaved={async (name, created) => {
            setModal(null);
            say(created ? `Created @preset/${name}` : "Preset saved");
            await reload();
          }}
        />
      )}
      {modal?.kind === "delete" && (
        <DeleteModal
          preset={modal.preset}
          onClose={() => setModal(null)}
          onDeleted={async () => {
            setModal(null);
            say("Preset deleted");
            await reload();
          }}
          onError={(message) => say(message, true)}
        />
      )}
    </div>
  );
}
