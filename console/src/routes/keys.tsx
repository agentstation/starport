import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { KeyRound, Plus, Trash2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { ConnectCard } from "@/components/overview/ConnectCard";
import { CopyButton } from "@/components/ui/CopyButton";
import {
  Field,
  GhostButton,
  INPUT_CLASS,
  PrimaryButton,
  RowAction,
  SELECT_CLASS,
} from "@/components/ui/Form";
import { Modal } from "@/components/ui/Modal";
import {
  accessMessage,
  ApiError,
  createKey,
  putBYOKCredential,
  deleteKey,
  DEFAULT_TENANT_ID,
  deleteBYOKCredential,
  getKeyDetail,
  listKeys,
  listProviderCatalog,
  listBYOKCredentials,
  updateKey,
  validateBYOKCredential,
  type BudgetUsage,
  type GatewayKey,
  type KeyLimits,
  type ProviderCatalogEntry,
} from "@/lib/api";
import { formatCount, formatNanoUSD, formatRelativeTime } from "@/lib/format";
import { useApiKeyUsable } from "@/lib/useApiKey";

export const Route = createFileRoute("/keys")({
  component: KeysPage,
});

// Key IDs render head-and-tail (DESIGN.md): enough prefix to recognize
// the credential family, enough tail to tell records apart.
function truncateKeyId(id: string): string {
  return id.length > 20 ? `${id.slice(0, 13)}…${id.slice(-4)}` : id;
}

function formatWindow(seconds: number): string {
  if (seconds === 60) return "min";
  if (seconds === 3600) return "hr";
  if (seconds === 86400) return "day";
  return `${seconds}s`;
}

function utcTooltip(iso: string | undefined | null): string | undefined {
  if (!iso) return undefined;
  const date = new Date(iso);
  return Number.isFinite(date.getTime()) ? date.toISOString() : undefined;
}

// The lifecycle pill is one tint per state: active (success), disabled
// (neutral — a deliberate operator choice, not a fault), expired (error).
function StatusPill({ apiKey }: { apiKey: GatewayKey }) {
  const active = apiKey.active !== false;
  const expired =
    !!apiKey.expires_at && new Date(apiKey.expires_at).getTime() < Date.now();
  const [label, tone] = !active
    ? ["disabled", "bg-bg-raised text-text-3"]
    : expired
      ? ["expired", "bg-error-tint text-error"]
      : ["active", "bg-success-tint text-success"];
  return (
    <span
      className={`inline-flex h-5 items-center rounded-xs px-1.5 text-xs font-medium ${tone}`}
    >
      {label}
    </span>
  );
}

function ScopePills({ scopes }: { scopes: string[] }) {
  if (scopes.length === 0) return <span className="text-text-4">—</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {scopes.map((scope) => (
        <span
          key={scope}
          className={`inline-flex h-5 items-center rounded-xs px-1.5 font-mono text-xs ${
            scope === "admin"
              ? "bg-accent-tint text-accent-link"
              : "bg-bg-raised text-text-3"
          }`}
        >
          {scope}
        </span>
      ))}
    </div>
  );
}

// BudgetLine shows one budget's remaining allowance with a thin consumption
// bar; an exhausted window reads in error red.
function BudgetLine({
  usage,
  render,
  unit,
}: {
  usage: BudgetUsage;
  render: (value: number) => string;
  unit: string;
}) {
  const limit = usage.limit ?? 0;
  const used = usage.used ?? 0;
  const remaining = usage.remaining ?? Math.max(0, limit - used);
  const exhausted = limit > 0 && remaining === 0;
  const fraction = limit > 0 ? Math.min(1, used / limit) : 0;
  return (
    <div className="flex items-center gap-2">
      <span
        aria-hidden="true"
        className="h-1 w-12 shrink-0 overflow-hidden rounded-full bg-bg-raised"
      >
        <span
          className={`block h-full ${exhausted ? "bg-error" : fraction > 0.8 ? "bg-warning" : "bg-success"}`}
          style={{ width: `${Math.round(fraction * 100)}%` }}
        />
      </span>
      <span
        className={`text-xs tabular-nums ${exhausted ? "text-error" : "text-text-3"}`}
      >
        {exhausted ? `${unit} exhausted` : `${render(remaining)} left`}
      </span>
    </div>
  );
}

// LimitsCell summarizes a key's restrictions; keys with budgets also load
// the current-window consumption from the key detail endpoint.
function LimitsCell({ apiKey }: { apiKey: GatewayKey }) {
  const limits = apiKey.limits ?? {};
  const hasBudget = !!limits.spend || !!limits.tokens;
  const detail = useQuery({
    queryKey: ["key-detail", apiKey.id],
    queryFn: () => getKeyDetail(apiKey.id),
    enabled: hasBudget,
    retry: false,
  });

  const chips: string[] = [];
  if (apiKey.allowed_models?.length) {
    const count = apiKey.allowed_models.length;
    chips.push(`${count} model${count === 1 ? "" : "s"}`);
  }
  if (limits.requests) {
    chips.push(
      `${formatCount(limits.requests.limit)} req/${formatWindow(limits.requests.window_seconds)}`,
    );
  }
  if (limits.spend) {
    chips.push(`${formatNanoUSD(limits.spend.limit)}/${limits.spend.interval}`);
  }
  if (limits.tokens) {
    chips.push(
      `${formatCount(limits.tokens.limit)} tok/${limits.tokens.interval}`,
    );
  }
  if (apiKey.expires_at) {
    chips.push(`expires ${formatRelativeTime(apiKey.expires_at)}`);
  }
  if (chips.length === 0) return <span className="text-text-4">—</span>;

  const budgets = detail.data?.usage?.budgets;
  return (
    <div className="flex flex-col gap-1">
      <div className="flex flex-wrap gap-1">
        {chips.map((chip) => (
          <span
            key={chip}
            title={
              chip.startsWith("expires")
                ? utcTooltip(apiKey.expires_at)
                : chip.endsWith("models") || chip.endsWith("model")
                  ? apiKey.allowed_models?.join(", ")
                  : undefined
            }
            className="inline-flex h-5 items-center rounded-xs bg-bg-raised px-1.5 text-xs tabular-nums text-text-3"
          >
            {chip}
          </span>
        ))}
      </div>
      {hasBudget && budgets?.spend && (
        <BudgetLine usage={budgets.spend} render={formatNanoUSD} unit="spend" />
      )}
      {hasBudget && budgets?.tokens && (
        <BudgetLine
          usage={budgets.tokens}
          render={(value) => `${formatCount(value)} tok`}
          unit="tokens"
        />
      )}
    </div>
  );
}

// --- Limits form (shared by create and edit) ---

type LimitsDraft = {
  models: string;
  expiry: string;
  reqLimit: string;
  reqWindow: string;
  spend: string;
  spendInterval: string;
  tokens: string;
  tokensInterval: string;
};

function draftFromKey(apiKey: GatewayKey | null): LimitsDraft {
  const limits = apiKey?.limits ?? {};
  return {
    models: (apiKey?.allowed_models ?? []).join(", "),
    expiry: apiKey?.expires_at ? apiKey.expires_at.slice(0, 10) : "",
    reqLimit: limits.requests ? String(limits.requests.limit) : "",
    reqWindow: limits.requests ? String(limits.requests.window_seconds) : "60",
    spend: limits.spend ? String(limits.spend.limit / 1e9) : "",
    spendInterval: limits.spend?.interval ?? "day",
    tokens: limits.tokens ? String(limits.tokens.limit) : "",
    tokensInterval: limits.tokens?.interval ?? "day",
  };
}

// readDraft validates the draft and shapes the request fields, or throws
// an Error naming the invalid field.
function readDraft(draft: LimitsDraft): {
  allowedModels: string[];
  expiresAt: string | null;
  limits: KeyLimits | null;
} {
  const allowedModels = draft.models
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean);
  // The chosen day is inclusive: the key expires at the end of it.
  const expiresAt = draft.expiry ? `${draft.expiry}T23:59:59Z` : null;
  const limits: KeyLimits = {};
  if (draft.reqLimit) {
    const limit = Number(draft.reqLimit);
    if (!Number.isInteger(limit) || limit <= 0) {
      throw new Error("Request limit must be a positive whole number");
    }
    limits.requests = { limit, window_seconds: Number(draft.reqWindow) };
  }
  if (draft.spend) {
    const usd = Number(draft.spend);
    if (!Number.isFinite(usd) || usd <= 0) {
      throw new Error("Spend budget must be a positive amount");
    }
    limits.spend = { limit: Math.round(usd * 1e9), interval: draft.spendInterval };
  }
  if (draft.tokens) {
    const tokens = Number(draft.tokens);
    if (!Number.isInteger(tokens) || tokens <= 0) {
      throw new Error("Token budget must be a positive whole number");
    }
    limits.tokens = { limit: tokens, interval: draft.tokensInterval };
  }
  return {
    allowedModels,
    expiresAt,
    limits: Object.keys(limits).length > 0 ? limits : null,
  };
}

function LimitsFields({
  draft,
  onChange,
  expiryLocked,
}: {
  draft: LimitsDraft;
  onChange: (draft: LimitsDraft) => void;
  expiryLocked: boolean;
}) {
  const set = (patch: Partial<LimitsDraft>) => onChange({ ...draft, ...patch });
  const intervalOptions = (
    <>
      <option value="day">per day</option>
      <option value="week">per week</option>
      <option value="month">per month</option>
    </>
  );
  return (
    <>
      <Field label="Allowed models">
        <input
          type="text"
          value={draft.models}
          onChange={(event) => set({ models: event.target.value })}
          placeholder="empty = all models (comma-separated IDs)"
          autoComplete="off"
          className={`${INPUT_CLASS} font-mono`}
        />
      </Field>
      <Field label="Expires">
        <input
          type="date"
          value={draft.expiry}
          onChange={(event) => set({ expiry: event.target.value })}
          className={INPUT_CLASS}
        />
        {expiryLocked && (
          <span className="text-xs text-text-4">
            Expiry cannot be removed once set.
          </span>
        )}
      </Field>
      <Field label="Request limit">
        <div className="flex gap-2">
          <input
            type="number"
            min="1"
            value={draft.reqLimit}
            onChange={(event) => set({ reqLimit: event.target.value })}
            placeholder="unlimited"
            className={`${INPUT_CLASS} w-32`}
          />
          <select
            value={draft.reqWindow}
            onChange={(event) => set({ reqWindow: event.target.value })}
            aria-label="Request window"
            className={SELECT_CLASS}
          >
            <option value="60">per minute</option>
            <option value="3600">per hour</option>
            <option value="86400">per day</option>
            {!["60", "3600", "86400"].includes(draft.reqWindow) && (
              <option value={draft.reqWindow}>per {draft.reqWindow}s</option>
            )}
          </select>
        </div>
      </Field>
      <Field label="Spend budget (USD)">
        <div className="flex gap-2">
          <input
            type="number"
            min="0"
            step="0.01"
            value={draft.spend}
            onChange={(event) => set({ spend: event.target.value })}
            placeholder="unlimited"
            className={`${INPUT_CLASS} w-32`}
          />
          <select
            value={draft.spendInterval}
            onChange={(event) => set({ spendInterval: event.target.value })}
            aria-label="Spend budget interval"
            className={SELECT_CLASS}
          >
            {intervalOptions}
          </select>
        </div>
      </Field>
      <Field label="Token budget">
        <div className="flex gap-2">
          <input
            type="number"
            min="1"
            value={draft.tokens}
            onChange={(event) => set({ tokens: event.target.value })}
            placeholder="unlimited"
            className={`${INPUT_CLASS} w-32`}
          />
          <select
            value={draft.tokensInterval}
            onChange={(event) => set({ tokensInterval: event.target.value })}
            aria-label="Token budget interval"
            className={SELECT_CLASS}
          >
            {intervalOptions}
          </select>
        </div>
      </Field>
    </>
  );
}

// --- Create / edit / secret / delete modals ---

function CreateKeyModal({
  onClose,
  onCreated,
  onError,
}: {
  onClose: () => void;
  onCreated: (secret: string) => void;
  onError: (message: string) => void;
}) {
  const [name, setName] = useState("");
  const [admin, setAdmin] = useState(false);
  const [draft, setDraft] = useState(() => draftFromKey(null));
  const [formError, setFormError] = useState("");

  const create = useMutation({
    mutationFn: createKey,
    onSuccess: (created) => {
      const record = created?.key;
      onCreated(record?.key ?? "");
    },
    onError: (error) =>
      onError(
        `Create failed: ${error instanceof Error ? error.message : error}`,
      ),
  });

  const submit = () => {
    if (!name.trim()) {
      setFormError("Name is required");
      return;
    }
    let fields;
    try {
      fields = readDraft(draft);
    } catch (error) {
      setFormError(error instanceof Error ? error.message : String(error));
      return;
    }
    setFormError("");
    // A key needs at least one scope. Non-admin keys get the inference
    // set: chat, embeddings, model listing, activity.
    create.mutate({
      name: name.trim(),
      scopes: admin
        ? ["admin"]
        : ["chat:write", "embeddings:write", "models:read", "activity:read"],
      ...(fields.allowedModels.length > 0
        ? { allowed_models: fields.allowedModels }
        : {}),
      ...(fields.expiresAt ? { expires_at: fields.expiresAt } : {}),
      ...(fields.limits ? { limits: fields.limits } : {}),
    });
  };

  return (
    <Modal
      title="New API key"
      onClose={onClose}
      footer={
        <>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <PrimaryButton onClick={submit} disabled={create.isPending}>
            Create key
          </PrimaryButton>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field label="Name">
          <input
            type="text"
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="e.g. local-dev"
            autoComplete="off"
            className={INPUT_CLASS}
          />
        </Field>
        <label className="flex items-start gap-2 text-sm text-text-2">
          <input
            type="checkbox"
            checked={admin}
            onChange={(event) => setAdmin(event.target.checked)}
            className="mt-0.5 accent-(--accent)"
          />
          <span>
            Admin scope — can manage keys, providers, and the catalog
          </span>
        </label>
        <LimitsFields draft={draft} onChange={setDraft} expiryLocked={false} />
        {formError && <p className="text-xs text-error">{formError}</p>}
      </div>
    </Modal>
  );
}

function EditKeyModal({
  apiKey,
  onClose,
  onSaved,
  onError,
}: {
  apiKey: GatewayKey;
  onClose: () => void;
  onSaved: () => void;
  onError: (message: string) => void;
}) {
  const [name, setName] = useState(apiKey.name ?? "");
  const [draft, setDraft] = useState(() => draftFromKey(apiKey));
  const [formError, setFormError] = useState("");

  const save = useMutation({
    mutationFn: (body: Parameters<typeof updateKey>[1]) =>
      updateKey(apiKey.id, body),
    onSuccess: onSaved,
    onError: (error) =>
      onError(
        `Update failed: ${error instanceof Error ? error.message : error}`,
      ),
  });

  const submit = () => {
    if (!name.trim()) {
      setFormError("Name is required");
      return;
    }
    let fields;
    try {
      fields = readDraft(draft);
    } catch (error) {
      setFormError(error instanceof Error ? error.message : String(error));
      return;
    }
    setFormError("");
    // An empty model list and an empty limits object clear the
    // restriction; expiry is only sent when the field has a value.
    save.mutate({
      name: name.trim(),
      allowed_models: fields.allowedModels,
      limits: fields.limits ?? {},
      ...(fields.expiresAt ? { expires_at: fields.expiresAt } : {}),
    });
  };

  return (
    <Modal
      title={`Edit key · ${apiKey.name || truncateKeyId(apiKey.id)}`}
      onClose={onClose}
      footer={
        <>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <PrimaryButton onClick={submit} disabled={save.isPending}>
            Save
          </PrimaryButton>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field label="Name">
          <input
            type="text"
            value={name}
            onChange={(event) => setName(event.target.value)}
            autoComplete="off"
            className={INPUT_CLASS}
          />
        </Field>
        <LimitsFields
          draft={draft}
          onChange={setDraft}
          expiryLocked={!!apiKey.expires_at}
        />
        {formError && <p className="text-xs text-error">{formError}</p>}
      </div>
    </Modal>
  );
}

// SecretModal shows the freshly minted secret — the only time it exists
// in plaintext (DESIGN.md: full value exactly once, with a copy button).
function SecretModal({ secret, onClose }: { secret: string; onClose: () => void }) {
  return (
    <Modal
      title="Copy your key now"
      onClose={onClose}
      footer={<PrimaryButton onClick={onClose}>Done</PrimaryButton>}
    >
      <div className="flex flex-col gap-3">
        <p className="text-sm text-text-2">
          This key is shown once. Store it somewhere safe — the gateway keeps
          only a hash.
        </p>
        <div className="flex items-center gap-2 rounded-sm border border-border-1 bg-bg-canvas p-3">
          <code className="min-w-0 flex-1 break-all font-mono text-xs text-text-1">
            {secret || "(secret unavailable)"}
          </code>
          {secret && <CopyButton text={secret} label="key" />}
        </div>
      </div>
    </Modal>
  );
}

// DeleteKeyModal restates the object name before the irreversible action
// (DESIGN.md destructive-modal contract).
function DeleteKeyModal({
  apiKey,
  onClose,
  onDeleted,
  onError,
}: {
  apiKey: GatewayKey;
  onClose: () => void;
  onDeleted: () => void;
  onError: (message: string) => void;
}) {
  const remove = useMutation({
    mutationFn: () => deleteKey(apiKey.id),
    onSuccess: onDeleted,
    onError: (error) =>
      onError(
        `Delete failed: ${error instanceof Error ? error.message : error}`,
      ),
  });
  return (
    <Modal
      title="Delete key"
      onClose={onClose}
      footer={
        <>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <button
            type="button"
            onClick={() => remove.mutate()}
            disabled={remove.isPending}
            className="flex h-9 items-center rounded-sm bg-error px-4 text-sm font-medium text-white transition-colors duration-150 ease-standard hover:opacity-90 disabled:opacity-50"
          >
            Delete key
          </button>
        </>
      }
    >
      <p className="text-sm text-text-2">
        Delete{" "}
        <strong className="text-text-1">
          {apiKey.name || truncateKeyId(apiKey.id)}
        </strong>
        ? Apps using it lose access immediately. This cannot be undone.
      </p>
    </Modal>
  );
}

// --- BYOK: the provider credentials a tenant brings ---

function ByokModal({
  apiKey,
  onClose,
}: {
  apiKey: GatewayKey;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [provider, setProvider] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );

  // BYOK belongs to the account, not to this key. Every key in the account
  // reaches the same credentials, and rotating this one leaves them in place.
  const tenantId = apiKey.tenant_id || DEFAULT_TENANT_ID;
  const byok = useQuery({
    queryKey: ["byok", tenantId],
    queryFn: () => listBYOKCredentials(tenantId),
    retry: false,
  });
  const catalog = useQuery({
    queryKey: ["provider-catalog"],
    queryFn: listProviderCatalog,
    retry: false,
  });

  const providers = useMemo(
    () =>
      [...(catalog.data ?? [])].sort((a, b) =>
        (a.name ?? a.id).localeCompare(b.name ?? b.id),
      ),
    [catalog.data],
  );
  const selected: ProviderCatalogEntry | undefined = providers.find(
    (entry) => entry.id === provider,
  );
  // The credential form is catalog-driven: each provider declares its
  // inference credential fields, so the console never assumes an "api key".
  const fields = selected?.credential_fields ?? [];

  const say = (text: string, error = false) => setNotice({ text, error });
  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: ["byok", tenantId] });

  const attach = useMutation({
    mutationFn: () => {
      const credentials: Record<string, string> = {};
      const config: Record<string, string> = {};
      for (const field of fields) {
        const value = values[field.id]?.trim();
        if (!value) continue;
        if (field.kind === "secret") credentials[field.id] = value;
        else config[field.id] = value;
      }
      return putBYOKCredential(tenantId, provider, {
        credentials,
        ...(Object.keys(config).length > 0 ? { config } : {}),
      });
    },
    onSuccess: async () => {
      setValues({});
      say(`Applied ${provider} credential`);
      await refresh();
    },
    onError: (error) =>
      say(
        `Apply failed: ${error instanceof Error ? error.message : error}`,
        true,
      ),
  });

  const validate = useMutation({
    mutationFn: (target: string) => validateBYOKCredential(tenantId, target),
    onSuccess: (result, target) => {
      const valid = result?.valid !== false;
      say(
        valid
          ? `${target} credential is valid`
          : `${target} credential is invalid`,
        !valid,
      );
    },
    onError: (error) =>
      say(
        `Validation failed: ${error instanceof Error ? error.message : error}`,
        true,
      ),
  });

  const detach = useMutation({
    mutationFn: (target: string) => deleteBYOKCredential(tenantId, target),
    onSuccess: async (_result, target) => {
      say(`Removed ${target} credential`);
      await refresh();
    },
    onError: (error) =>
      say(
        `Remove failed: ${error instanceof Error ? error.message : error}`,
        true,
      ),
  });

  const hasSecret = fields.some(
    (field) => field.kind === "secret" && values[field.id]?.trim(),
  );

  return (
    <Modal
      title={`Provider keys · ${apiKey.name || truncateKeyId(apiKey.id)}`}
      onClose={onClose}
      wide
      footer={<GhostButton onClick={onClose}>Close</GhostButton>}
    >
      <div className="flex flex-col gap-4">
        <p className="text-sm text-text-3">
          Credentials this account brings for itself. They belong to the{" "}
          <span className="font-mono text-text-2">{tenantId}</span> account, not
          to this key, so every key in the account uses them and rotating a key
          leaves them in place.
        </p>
        {notice && (
          <p className={`text-xs ${notice.error ? "text-error" : "text-success"}`}>
            {notice.text}
          </p>
        )}
        {byok.isPending ? (
          <p className="text-sm text-text-3">Loading credentials…</p>
        ) : byok.error ? (
          <p className="text-sm text-text-3">
            {byok.error instanceof ApiError && byok.error.forbidden
              ? `Only a key in the ${tenantId} account, or an operator, can view or change its credentials.`
              : byok.error instanceof ApiError && byok.error.unauthorized
                ? accessMessage(byok.error, "provider_keys:read")
                : `Failed to load credentials: ${byok.error.message}`}
          </p>
        ) : (byok.data ?? []).length === 0 ? (
          <p className="text-sm text-text-3">
            This account brings no provider credentials.
          </p>
        ) : (
          <div className="flex flex-col divide-y divide-border-1 rounded-sm border border-border-1">
            {(byok.data ?? []).map((row) => (
              <div
                key={row.provider}
                className="flex h-10 items-center gap-3 px-3"
              >
                <span className="flex-1 font-mono text-sm text-text-1">
                  {row.provider}
                </span>
                {row.created_at && (
                  <span
                    title={utcTooltip(row.created_at)}
                    className="text-xs text-text-4"
                  >
                    {formatRelativeTime(row.created_at)}
                  </span>
                )}
                <button
                  type="button"
                  onClick={() => validate.mutate(row.provider)}
                  disabled={validate.isPending}
                  className="flex h-7 items-center rounded-xs px-2 text-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-2 disabled:opacity-50"
                >
                  validate
                </button>
                <button
                  type="button"
                  onClick={() => detach.mutate(row.provider)}
                  disabled={detach.isPending}
                  aria-label={`Remove ${row.provider} credential`}
                  className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error disabled:opacity-50"
                >
                  <Trash2 className="size-3.5" />
                </button>
              </div>
            ))}
          </div>
        )}
        {!(byok.error instanceof ApiError && byok.error.needsKey) && (
        <div className="flex flex-col gap-2 border-t border-border-1 pt-4">
          <span className="text-xs font-medium text-text-2">
            Attach a provider credential
          </span>
          <select
            value={provider}
            onChange={(event) => {
              setProvider(event.target.value);
              setValues({});
            }}
            aria-label="Provider"
            className={SELECT_CLASS}
          >
            <option value="">select provider…</option>
            {providers.map((entry) => (
              <option key={entry.id} value={entry.id}>
                {entry.name ?? entry.id}
              </option>
            ))}
          </select>
          {fields.map((field) => (
            <input
              key={field.id}
              type={field.kind === "secret" ? "password" : "text"}
              value={values[field.id] ?? ""}
              onChange={(event) =>
                setValues((prev) => ({ ...prev, [field.id]: event.target.value }))
              }
              placeholder={
                field.default ? `${field.id} (${field.default})` : field.id
              }
              autoComplete="off"
              aria-label={field.id}
              className={`${INPUT_CLASS} font-mono`}
            />
          ))}
          {provider && fields.length === 0 && (
            <span className="text-xs text-text-4">
              This provider declares no credential contract.
            </span>
          )}
          <PrimaryButton
            onClick={() => attach.mutate()}
            disabled={!hasSecret || attach.isPending}
          >
            <Plus className="size-3.5" />
            Attach
          </PrimaryButton>
        </div>
        )}
      </div>
    </Modal>
  );
}

// --- Empty and locked states ---

function EmptyState({ onCreate }: { onCreate: () => void }) {
  const curl = [
    `curl -X POST ${window.location.origin}/api/v1/admin/keys \\`,
    `  -H "Authorization: Bearer $STARPORT_ADMIN_KEY" \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -d '{"name":"local-dev","scopes":["chat:write","models:read"]}'`,
  ].join("\n");
  return (
    <div className="flex flex-col items-center gap-4 rounded-md border border-border-1 bg-bg-panel px-6 py-12">
      <KeyRound aria-hidden="true" className="size-6 text-text-4" />
      <p className="text-sm text-text-2">
        No API keys yet. Create one to hand to your apps.
      </p>
      <PrimaryButton onClick={onCreate}>
        <Plus className="size-3.5" />
        New key
      </PrimaryButton>
      <div className="w-full max-w-xl overflow-x-auto rounded-sm border border-border-1 bg-bg-canvas p-3">
        <pre className="font-mono text-xs leading-relaxed text-text-2">
          {curl}
        </pre>
      </div>
    </div>
  );
}

// --- Page ---

type ModalState =
  | { kind: "create" }
  | { kind: "secret"; secret: string }
  | { kind: "edit"; apiKey: GatewayKey }
  | { kind: "delete"; apiKey: GatewayKey }
  | { kind: "byok"; apiKey: GatewayKey }
  | null;

function KeysPage() {
  const keyUsable = useApiKeyUsable();
  const queryClient = useQueryClient();
  const [modal, setModal] = useState<ModalState>(null);
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );
  const noticeTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(noticeTimer.current), []);

  const keys = useQuery({
    queryKey: ["keys"],
    queryFn: listKeys,
    enabled: keyUsable,
    retry: false,
  });

  const say = (text: string, error = false) => {
    setNotice({ text, error });
    clearTimeout(noticeTimer.current);
    noticeTimer.current = setTimeout(() => setNotice(null), 6000);
  };

  const reload = () => queryClient.invalidateQueries({ queryKey: ["keys"] });

  const toggle = useMutation({
    mutationFn: (apiKey: GatewayKey) =>
      updateKey(apiKey.id, { active: apiKey.active === false }),
    onSuccess: async (_result, apiKey) => {
      say(apiKey.active === false ? "Key enabled" : "Key disabled");
      await reload();
    },
    onError: (error) =>
      say(
        `Update failed: ${error instanceof Error ? error.message : error}`,
        true,
      ),
  });

  if (!keyUsable) {
    return (
      <div className="flex flex-col gap-4">
        <Header />
        <ConnectCard />
      </div>
    );
  }

  let body: ReactNode;
  if (keys.error) {
    if (keys.error instanceof ApiError && keys.error.needsKey) {
      body = (
        <p className="text-base text-text-3">
          {accessMessage(keys.error, "admin")}
        </p>
      );
    } else {
      body = (
        <p className="text-base text-text-3">
          Failed to load keys: {keys.error.message}
        </p>
      );
    }
  } else if (keys.isPending) {
    body = <p className="text-base text-text-3">Loading keys…</p>;
  } else if ((keys.data ?? []).length === 0) {
    body = <EmptyState onCreate={() => setModal({ kind: "create" })} />;
  } else {
    body = (
      <div className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
              <th className="px-4 py-2.5">Name</th>
              <th className="px-4 py-2.5">Key</th>
              <th className="px-4 py-2.5">Scopes</th>
              <th className="px-4 py-2.5">Limits</th>
              <th className="px-4 py-2.5">Status</th>
              <th className="px-4 py-2.5">Created</th>
              <th className="px-4 py-2.5" />
            </tr>
          </thead>
          <tbody>
            {(keys.data ?? []).map((apiKey) => (
              <tr
                key={apiKey.id}
                className="h-10 border-b border-border-1 transition-colors duration-150 ease-standard last:border-b-0 hover:bg-bg-hover"
              >
                <td className="whitespace-nowrap px-4 py-2 font-medium text-text-1">
                  {apiKey.name || "unnamed"}
                </td>
                <td className="px-4 py-2">
                  <span className="inline-flex items-center gap-1">
                    <code
                      title={apiKey.id}
                      className="whitespace-nowrap rounded-xs bg-bg-raised px-1.5 py-0.5 font-mono text-xs text-text-2"
                    >
                      {truncateKeyId(apiKey.id)}
                    </code>
                    <CopyButton text={apiKey.id} label="" />
                  </span>
                </td>
                <td className="px-4 py-2">
                  <ScopePills scopes={apiKey.scopes ?? []} />
                </td>
                <td className="px-4 py-2">
                  <LimitsCell apiKey={apiKey} />
                </td>
                <td className="px-4 py-2">
                  <StatusPill apiKey={apiKey} />
                </td>
                <td
                  className="px-4 py-2 text-xs text-text-3"
                  title={utcTooltip(apiKey.created_at)}
                >
                  {formatRelativeTime(apiKey.created_at)}
                </td>
                <td className="px-4 py-2">
                  <div className="flex items-center justify-end gap-1">
                    <RowAction
                      onClick={() => setModal({ kind: "byok", apiKey })}
                    >
                      byok
                    </RowAction>
                    <RowAction
                      onClick={() => setModal({ kind: "edit", apiKey })}
                    >
                      edit
                    </RowAction>
                    <RowAction
                      onClick={() => toggle.mutate(apiKey)}
                      disabled={toggle.isPending}
                    >
                      {apiKey.active === false ? "enable" : "disable"}
                    </RowAction>
                    <button
                      type="button"
                      onClick={() => setModal({ kind: "delete", apiKey })}
                      aria-label={`Delete ${apiKey.name || apiKey.id}`}
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

  const canCreate =
    !keys.error && !keys.isPending && (keys.data ?? []).length > 0;

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
              New key
            </PrimaryButton>
          )}
        </div>
      </div>
      {body}
      {modal?.kind === "create" && (
        <CreateKeyModal
          onClose={() => setModal(null)}
          onCreated={async (secret) => {
            setModal({ kind: "secret", secret });
            await reload();
          }}
          onError={(message) => say(message, true)}
        />
      )}
      {modal?.kind === "secret" && (
        <SecretModal secret={modal.secret} onClose={() => setModal(null)} />
      )}
      {modal?.kind === "edit" && (
        <EditKeyModal
          apiKey={modal.apiKey}
          onClose={() => setModal(null)}
          onSaved={async () => {
            setModal(null);
            say("Key updated");
            await reload();
          }}
          onError={(message) => say(message, true)}
        />
      )}
      {modal?.kind === "delete" && (
        <DeleteKeyModal
          apiKey={modal.apiKey}
          onClose={() => setModal(null)}
          onDeleted={async () => {
            setModal(null);
            say("Key deleted");
            await reload();
          }}
          onError={(message) => say(message, true)}
        />
      )}
      {modal?.kind === "byok" && (
        <ByokModal apiKey={modal.apiKey} onClose={() => setModal(null)} />
      )}
    </div>
  );
}

function Header() {
  return (
    <div>
      <h1 className="text-xl font-semibold tracking-[-0.01em]">API Keys</h1>
      <p className="mt-1 text-sm text-text-3">
        Gateway keys for your apps, plus per-key provider credentials (BYOK).
      </p>
    </div>
  );
}
