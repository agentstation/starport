import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { KeyRound, Plus, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";

import { ModelMultiPicker } from "@/components/models/ModelPicker";
import { BudgetLine } from "@/components/ui/BudgetLine";
import { DateField } from "@/components/ui/DateField";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { CopyButton } from "@/components/ui/CopyButton";
import { DataTable, dataColumns } from "@/components/ui/DataTable";
import type { RowData, TableFeatures } from "@tanstack/react-table";
import { DestructiveButton, Field, GhostButton, INPUT_CLASS, PrimaryButton, RowAction } from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { Dialog, DialogBody, DialogContent, DialogError, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Pill, type PillTone } from "@/components/ui/Pill";
import { TableSkeleton } from "@/components/ui/skeleton";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { TitleActions } from "@/components/shell/TitleActions";
import {
  accessMessage,
  ApiError,
  createKey,
  DEFAULT_ACCOUNT_ID,
  deleteKey,
  updateKey,
  type GatewayKey,
  type KeyDetail,
  type KeyLimits,
} from "@/lib/api";
import { queries, settle } from "@/lib/queries";
import { optionalString } from "@/lib/search";
import {
  formatCount,
  formatNanoUSD,
  formatRelativeTime,
  formatWindow,
  utcTooltip,
} from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";
import { announce, report } from "@/lib/mutations";

// The key under edit lives in the address, so a reload or a shared link
// opens the same panel. The create, secret, and delete dialogs stay local:
// a secret must never reach an address, and the other two are one-shot.
type KeysSearch = { selected?: string };

export const Route = createFileRoute("/keys")({
  component: KeysPage,
  loader: ({ context }) => settle(context.queryClient.ensureQueryData(queries.keys())),
  validateSearch: (search: Record<string, unknown>): KeysSearch => ({
    selected: optionalString(search.selected),
  }),
});

// Key IDs render head-and-tail (DESIGN.md): enough prefix to recognize
// the credential family, enough tail to tell records apart.
function truncateKeyId(id: string): string {
  return id.length > 20 ? `${id.slice(0, 13)}…${id.slice(-4)}` : id;
}

// The lifecycle pill is one tint per state: active (success), disabled
// (neutral — a deliberate operator choice, not a fault), expired (error).
function StatusPill({ apiKey }: { apiKey: GatewayKey }) {
  const active = apiKey.active !== false;
  const expired =
    !!apiKey.expires_at && new Date(apiKey.expires_at).getTime() < Date.now();
  const [label, tone]: [string, PillTone] = !active
    ? ["disabled", "neutral"]
    : expired
      ? ["expired", "error"]
      : ["active", "success"];
  return <Pill tone={tone}>{label}</Pill>;
}

// VISIBLE_SCOPES is how many scope pills a row shows before the rest
// collapse into a count, so a full inference key stays one line tall.
const VISIBLE_SCOPES = 4;

function ScopePill({ scope }: { scope: string }) {
  return (
    <span className="inline-flex h-5 items-center rounded-xs bg-bg-raised px-1.5 font-mono text-xs text-text-3">
      {scope}
    </span>
  );
}

// ScopePills reads a key's scopes. The admin scope and the wildcard the
// local operator key carries each stand for every scope, so they read as
// that in words. Past four scopes, the rest fold into a count whose
// tooltip lists them.
export function ScopePills({ scopes }: { scopes: string[] }) {
  if (scopes.length === 0) return <span className="text-text-4">—</span>;
  const everyScope = scopes.find((scope) => scope === "admin" || scope === "*");
  if (everyScope) {
    return (
      <span
        title={everyScope}
        className="inline-flex h-5 items-center rounded-xs bg-accent-tint px-1.5 text-xs text-accent-link"
      >
        all scopes
      </span>
    );
  }
  const shown = scopes.slice(0, VISIBLE_SCOPES);
  const rest = scopes.slice(VISIBLE_SCOPES);
  return (
    <div className="flex flex-wrap gap-1">
      {shown.map((scope) => (
        <ScopePill key={scope} scope={scope} />
      ))}
      {rest.length > 0 && (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger
              aria-label={`${rest.length} more scopes: ${rest.join(", ")}`}
              className="inline-flex h-5 items-center rounded-xs bg-bg-raised px-1.5 font-mono text-xs text-text-2"
            >
              +{rest.length}
            </TooltipTrigger>
            <TooltipContent className="max-w-sm font-mono">{rest.join(", ")}</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      )}
    </div>
  );
}

// hasBudget reports whether a key carries a spend or token budget, the two
// limits whose current-window consumption only the key detail endpoint reports.
function hasBudget(apiKey: GatewayKey): boolean {
  const limits = apiKey.limits ?? {};
  return !!limits.spend || !!limits.tokens;
}

type Budgets = NonNullable<KeyDetail["usage"]>["budgets"];

// useBudgets reads the key detail for every budgeted key in one hook at the
// page level, so the table renders no per-row query and the row count never
// changes the number of hooks.
function useBudgets(keys: GatewayKey[], enabled: boolean): Map<string, Budgets> {
  const budgeted = keys.filter(hasBudget);
  return useQueries({
    queries: budgeted.map((apiKey) => ({ ...queries.keyDetail(apiKey.id), enabled })),
    combine: (results) =>
      new Map(budgeted.map((apiKey, index) => [apiKey.id, results[index]?.data?.usage?.budgets])),
  });
}

// LimitsCell summarizes a key's restrictions and, for a budgeted key, the
// current-window consumption the page read for it.
function LimitsCell({ apiKey, budgets }: { apiKey: GatewayKey; budgets?: Budgets }) {
  const limits = apiKey.limits ?? {};

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

  const budgeted = hasBudget(apiKey);
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
      {budgeted && budgets?.spend && (
        <BudgetLine usage={budgets.spend} render={formatNanoUSD} unit="spend" />
      )}
      {budgeted && budgets?.tokens && (
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
  models: string[];
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
    models: apiKey?.allowed_models ?? [],
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
  const allowedModels = draft.models;
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
      <Field label="Allowed models" hint="empty allows every model">
        <ModelMultiPicker
          values={draft.models}
          onChange={(models) => set({ models })}
          placeholder="search the catalog"
        />
      </Field>
      <Field
        label="Expires"
        hint={expiryLocked ? "Expiry cannot be removed once set." : "at the end of the chosen day"}
      >
        <DateField
          value={draft.expiry}
          onChange={(expiry) => set({ expiry })}
          placeholder="Never"
          clearable={!expiryLocked}
        />
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
          <Select
            value={draft.reqWindow}
            onChange={(event) => set({ reqWindow: event.target.value })}
            aria-label="Request window"
          >
            <option value="60">per minute</option>
            <option value="3600">per hour</option>
            <option value="86400">per day</option>
            {!["60", "3600", "86400"].includes(draft.reqWindow) && (
              <option value={draft.reqWindow}>per {draft.reqWindow}s</option>
            )}
          </Select>
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
          <Select
            value={draft.spendInterval}
            onChange={(event) => set({ spendInterval: event.target.value })}
            aria-label="Spend budget interval"
          >
            {intervalOptions}
          </Select>
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
          <Select
            value={draft.tokensInterval}
            onChange={(event) => set({ tokensInterval: event.target.value })}
            aria-label="Token budget interval"
          >
            {intervalOptions}
          </Select>
        </div>
      </Field>
    </>
  );
}

// --- Create / edit / secret / delete modals ---

function CreateKeyModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (secret: string) => void;
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
      setFormError(
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
    // set: chat, embeddings, images, audio, reranking, model listing,
    // activity, and the two file scopes. A key that can send a document
    // inline can store one and name it later, so withholding the file scopes
    // would not withhold the capability, only the cheaper way to use it.
    // Reranking reads the caller's own documents rather than a stored one, so
    // it carries its own scope beside chat rather than riding on it, and
    // moderation classifies the caller's own text for the same reason.
    create.mutate({
      name: name.trim(),
      scopes: admin
        ? ["admin"]
        : [
            "chat:write",
            "embeddings:write",
            "images:write",
            "audio:write",
            "rerank:write",
            "moderations:write",
            "models:read",
            "activity:read",
            "files:read",
            "files:write",
            "batches:write",
          ],
      ...(fields.allowedModels.length > 0
        ? { allowed_models: fields.allowedModels }
        : {}),
      ...(fields.expiresAt ? { expires_at: fields.expiresAt } : {}),
      ...(fields.limits ? { limits: fields.limits } : {}),
    });
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New API key</DialogTitle>
        </DialogHeader>
        <DialogBody>
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
          </div>
        </DialogBody>
        <DialogError>{formError}</DialogError>
        <DialogFooter>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <PrimaryButton onClick={submit} disabled={create.isPending}>
            Create key
          </PrimaryButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function EditKeyModal({
  apiKey,
  onClose,
  onSaved,
}: {
  apiKey: GatewayKey;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(apiKey.name ?? "");
  const [draft, setDraft] = useState(() => draftFromKey(apiKey));
  const [formError, setFormError] = useState("");

  const save = useMutation({
    mutationFn: (body: Parameters<typeof updateKey>[1]) =>
      updateKey(apiKey.id, body),
    onSuccess: onSaved,
    onError: (error) =>
      setFormError(
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
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{`Edit key · ${apiKey.name || truncateKeyId(apiKey.id)}`}</DialogTitle>
        </DialogHeader>
        <DialogBody>
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
          </div>
        </DialogBody>
        <DialogError>{formError}</DialogError>
        <DialogFooter>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <PrimaryButton onClick={submit} disabled={save.isPending}>
            Save
          </PrimaryButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// SecretModal shows the freshly minted secret — the only time it exists
// in plaintext (DESIGN.md: full value exactly once, with a copy button).
function SecretModal({ secret, onClose }: { secret: string; onClose: () => void }) {
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Copy your key now</DialogTitle>
        </DialogHeader>
        <DialogBody>
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
        </DialogBody>
        <DialogFooter>
          <PrimaryButton onClick={onClose}>Done</PrimaryButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// DeleteKeyModal restates the object name before the irreversible action
// (DESIGN.md destructive-modal contract).
function DeleteKeyModal({
  apiKey,
  onClose,
  onDeleted,
}: {
  apiKey: GatewayKey;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [error, setError] = useState("");
  const remove = useMutation({
    mutationFn: () => deleteKey(apiKey.id),
    onSuccess: onDeleted,
    onError: (problem) =>
      setError(
        `Delete failed: ${problem instanceof Error ? problem.message : problem}`,
      ),
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete key</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <p className="text-sm text-text-2">
            Delete{" "}
            <strong className="text-text-1">
              {apiKey.name || truncateKeyId(apiKey.id)}
            </strong>
            ? Apps using it lose access immediately. This cannot be undone.
          </p>
        </DialogBody>
        <DialogError>{error}</DialogError>
        <DialogFooter>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <DestructiveButton
            onClick={() => remove.mutate()}
            disabled={remove.isPending}
          >
            Delete key
          </DestructiveButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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

// KeysTableMeta is what a cell needs from the page: the budget lookups and
// the row actions. It travels through table.options.meta so the columns
// stay module-level and a control inside a cell keeps its focus.
type KeysTableMeta = {
  budgets: Map<string, Budgets>;
  select: (keyId: string) => void;
  toggle: (apiKey: GatewayKey) => void;
  toggling: boolean;
  remove: (apiKey: GatewayKey) => void;
};

declare module "@tanstack/react-table" {
  interface TableMeta<in out TFeatures extends TableFeatures, in out TData extends RowData> {
    keys?: KeysTableMeta;
  }
}

function keysMeta(table: { options: { meta?: { keys?: KeysTableMeta } } }): KeysTableMeta {
  const meta = table.options.meta?.keys;
  if (!meta) throw new Error("keys table rendered without its meta");
  return meta;
}

const keyColumns = dataColumns<GatewayKey>();
// Default widths sum to 1,120px so the table fits a 1,440px viewport beside
// the sidebar; the name column takes the slack and every column resizes.
const KEY_COLUMNS = keyColumns.columns([
  keyColumns.accessor((apiKey) => apiKey.name || "unnamed", {
    id: "name",
    header: "Name",
    sortFn: "alphanumeric",
    size: 140,
    minSize: 120,
    meta: { flex: true, className: "whitespace-nowrap font-medium text-text-1" },
  }),
  keyColumns.display({
    id: "key",
    header: "Key",
    size: 190,
    minSize: 140,
    cell: ({ row }) => (
      <span className="inline-flex items-center gap-1">
        <code
          title={row.original.id}
          className="whitespace-nowrap rounded-xs bg-bg-raised px-1.5 py-0.5 font-mono text-xs text-text-2"
        >
          {truncateKeyId(row.original.id)}
        </code>
        <CopyButton text={row.original.id} label="" />
      </span>
    ),
  }),
  keyColumns.accessor((apiKey) => apiKey.account_id || DEFAULT_ACCOUNT_ID, {
    id: "account",
    header: "Account",
    sortFn: "alphanumeric",
    size: 110,
    minSize: 90,
    cell: ({ getValue }) => (
      <Link
        to="/accounts"
        className="font-mono text-xs text-accent-link transition-colors duration-150 ease-standard hover:underline"
      >
        {getValue()}
      </Link>
    ),
  }),
  keyColumns.display({
    id: "scopes",
    header: "Scopes",
    size: 150,
    minSize: 120,
    cell: ({ row }) => <ScopePills scopes={row.original.scopes ?? []} />,
  }),
  keyColumns.display({
    id: "limits",
    header: "Limits",
    size: 140,
    minSize: 120,
    cell: ({ row, table }) => (
      <LimitsCell apiKey={row.original} budgets={keysMeta(table).budgets.get(row.original.id)} />
    ),
  }),
  keyColumns.accessor((apiKey) => (apiKey.active === false ? "disabled" : "active"), {
    id: "status",
    header: "Status",
    sortFn: "alphanumeric",
    size: 90,
    minSize: 80,
    cell: ({ row }) => <StatusPill apiKey={row.original} />,
  }),
  keyColumns.accessor((apiKey) => apiKey.created_at ?? "", {
    id: "created",
    header: "Created",
    sortFn: "alphanumeric",
    size: 100,
    minSize: 90,
    cell: ({ row }) => (
      <RelativeTime iso={row.original.created_at} className="text-xs text-text-3" />
    ),
  }),
  keyColumns.display({
    id: "actions",
    size: 200,
    minSize: 200,
    cell: ({ row, table }) => {
      const apiKey = row.original;
      return (
        <div className="flex items-center justify-end gap-1">
          <RowAction onClick={() => keysMeta(table).select(apiKey.id)}>edit</RowAction>
          <RowAction
            onClick={() => keysMeta(table).toggle(apiKey)}
            disabled={keysMeta(table).toggling}
          >
            {apiKey.active === false ? "enable" : "disable"}
          </RowAction>
          <button
            type="button"
            onClick={() => keysMeta(table).remove(apiKey)}
            aria-label={`Delete ${apiKey.name || apiKey.id}`}
            className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error"
          >
            <Trash2 className="size-3.5" />
          </button>
        </div>
      );
    },
  }),
]);

type ModalState =
  | { kind: "create" }
  | { kind: "secret"; secret: string }
  | { kind: "delete"; apiKey: GatewayKey }
  | null;

function KeysPage() {
  const keyUsable = useGatewayAccess();
  const queryClient = useQueryClient();
  const [modal, setModal] = useState<ModalState>(null);
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const select = (keyId?: string) =>
    void navigate({ search: { selected: keyId }, replace: true });

  const keys = useQuery({
    ...queries.keys(),
    enabled: keyUsable,
  });
  const budgets = useBudgets(keys.data ?? [], keyUsable);
  const editing = search.selected
    ? (keys.data ?? []).find((apiKey) => apiKey.id === search.selected)
    : undefined;


  const reload = () => queryClient.invalidateQueries({ queryKey: queries.keys().queryKey });

  const toggle = useMutation({
    mutationFn: (apiKey: GatewayKey) =>
      updateKey(apiKey.id, { active: apiKey.active === false }),
    onSuccess: async (_result, apiKey) => {
      announce(apiKey.active === false ? "Key enabled" : "Key disabled");
      await reload();
    },
    onError: (error) =>
      report(`Update failed: ${error instanceof Error ? error.message : error}`),
  });

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
    body = <TableSkeleton columns={6} />;
  } else if ((keys.data ?? []).length === 0) {
    body = <EmptyState onCreate={() => setModal({ kind: "create" })} />;
  } else {
    body = (
      <DataTable
        aria-label="Gateway API keys"
        columns={KEY_COLUMNS}
        data={keys.data ?? []}
        meta={{
          keys: {
            budgets,
            select,
            toggle: (apiKey) => toggle.mutate(apiKey),
            toggling: toggle.isPending,
            remove: (apiKey) => setModal({ kind: "delete", apiKey }),
          },
        }}
        getRowId={(apiKey) => apiKey.id}
        initialSorting={[{ id: "created", desc: true }]}
      />
    );
  }

  const canCreate =
    !keys.error && !keys.isPending && (keys.data ?? []).length > 0;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <Header />
        <TitleActions>
          {canCreate && (
            <PrimaryButton onClick={() => setModal({ kind: "create" })}>
              <Plus className="size-3.5" />
              New key
            </PrimaryButton>
          )}
        </TitleActions>
      </div>
      {body}
      {modal?.kind === "create" && (
        <CreateKeyModal
          onClose={() => setModal(null)}
          onCreated={async (secret) => {
            setModal({ kind: "secret", secret });
            announce("Key created");
            await reload();
          }}
        />
      )}
      {modal?.kind === "secret" && (
        <SecretModal secret={modal.secret} onClose={() => setModal(null)} />
      )}
      {editing && (
        <EditKeyModal
          apiKey={editing}
          onClose={() => select()}
          onSaved={async () => {
            select();
            announce("Key updated");
            await reload();
          }}
        />
      )}
      {modal?.kind === "delete" && (
        <DeleteKeyModal
          apiKey={modal.apiKey}
          onClose={() => setModal(null)}
          onDeleted={async () => {
            setModal(null);
            announce("Key deleted");
            await reload();
          }}
        />
      )}
    </div>
  );
}

function Header() {
  return (
    <div>
      <h1 className="text-xl font-semibold tracking-[-0.01em]">API keys</h1>
      <p className="mt-1 text-sm text-text-3">
        Gateway keys for your apps. A key authenticates a caller and carries
        its scopes and limits; it never holds a provider credential.
      </p>
    </div>
  );
}
