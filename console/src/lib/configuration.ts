import { ApiError, request, type ReadOptions } from "@/lib/api";

// Configuration operations: GET and POST /api/v1/admin/config/*. The gateway
// owns every fact here. The console never computes an origin, an authority,
// or a revision, and it shows each value exactly as the gateway returns it.
// A sensitive value arrives as the REDACTED marker and never as text.

export const REDACTED = "<redacted>";

export type ManagementMode = "local" | "shared" | "external";

export type SaveTargetKind = "local-file" | "shared-revision" | "external-controller";

// SchemaSetting describes one setting. Key is the field-save key.
export type SchemaSetting = {
  id: string;
  key: string;
  environment: string;
  type: string;
  scope: "deployment" | "node" | string;
  mutability: string;
  sensitive: boolean;
  applicability: string[];
};

export type IgnoredValue = { origin: string; reason: string };

export type EffectiveSetting = {
  id: string;
  name: string;
  value: string;
  scope: string;
  authority: ManagementMode | string;
  origin: string;
  ignored?: IgnoredValue[];
  desired_revision?: number;
  applied_revision?: number;
};

export type AppliedRevision = {
  authority: string;
  namespace?: string;
  desired?: number;
  applied?: number;
  checksum?: string;
  file_checksum?: string;
  retained: boolean;
};

export type PathOrigin = { path: string; origin: string; anchor?: string };

export type EffectivePaths = {
  relative_path_base: string;
  relative_path_base_origin: string;
  config_dir: string;
  config_file: string;
  data_dir: string;
  state_dir: string;
  cache_dir: string;
  runtime_dir: string;
  baseline_dir: string;
  badger_dir: string;
  sqlite_file: string;
  files_dir: string;
  local_token_file: string;
  welcome_stamp_file: string;
  instance_id: string;
  deployment_id: string;
  origins?: Record<string, PathOrigin>;
};

export type StorageLifetime = {
  id: "kv" | "sql" | "blobs" | string;
  selection: string;
  lifetime: "process" | "local" | "service" | string;
  location?: string;
};

export type SaveTarget = { kind: SaveTargetKind; path?: string; unavailable?: string };

export type EffectiveReport = {
  management: ManagementMode;
  controller?: string;
  namespace?: string;
  revision: AppliedRevision;
  target: SaveTarget;
  paths: EffectivePaths;
  storage: StorageLifetime[];
  settings: EffectiveSetting[];
};

export type ReceiptRevision = { revision: string; sequence: number; checksum: string };

export type Refusal = {
  reason: string;
  message: string;
  setting?: string;
  expected?: string;
  current?: string;
};

export type Receipt = {
  operation_id: string;
  deployment_id: string;
  management: ManagementMode;
  status: "applied" | "saved" | "refused";
  saved: ReceiptRevision;
  applied: ReceiptRevision;
  activation_error?: string;
  audit_error?: string;
  settings?: string[];
  actor: string;
  created_at: string;
  refusal?: Refusal;
};

export type Validation = {
  valid: boolean;
  current: ReceiptRevision;
  preview: ReceiptRevision;
  settings?: string[];
  refusal?: Refusal;
};

export type ConnectionResult = { reachable: boolean; source: string; error?: string };

// FieldSave is one change. A null edit removes the setting.
export type FieldSave = {
  operation_id: string;
  deployment_id: string;
  expected_revision: string;
  edits: Record<string, string | null>;
};

const ROUTE = "/api/v1/admin/config";

export function configSchema({ signal }: ReadOptions = {}): Promise<SchemaSetting[]> {
  return request<{ settings: SchemaSetting[] }>(`${ROUTE}/schema`, { signal }).then((body) => body.settings ?? []);
}

// configEffective answers null when the gateway reports no save target, which
// a gateway older than the target field does.
export function configEffective({ signal }: ReadOptions = {}): Promise<EffectiveReport | null> {
  return request<Partial<EffectiveReport>>(`${ROUTE}/effective`, { signal }).then((body) =>
    body.target && body.paths && body.revision
      ? ({ ...body, settings: body.settings ?? [], storage: body.storage ?? [] } as EffectiveReport)
      : null,
  );
}

export function validateConfig(save: FieldSave): Promise<Validation> {
  return request<Validation>(`${ROUTE}/validate`, { method: "POST", body: save });
}

export function testConfigConnection(save: FieldSave): Promise<ConnectionResult> {
  return request<ConnectionResult>(`${ROUTE}/test-connection`, { method: "POST", body: save });
}

export function saveConfig(save: FieldSave): Promise<Receipt> {
  return request<Receipt>(`${ROUTE}/save`, { method: "POST", body: save });
}

// refusalOf reads the typed refusal from a refused operation. Any other
// failure has no refusal.
export function refusalOf(error: unknown): { refusal: Refusal; receipt?: Receipt } | null {
  if (!(error instanceof ApiError)) return null;
  const body = error.body as { refusal?: Refusal; receipt?: Receipt } | null;
  return body?.refusal ? { refusal: body.refusal, receipt: body.receipt } : null;
}

// expectedRevision is the revision a save must name: the checksum of the
// loaded file for a local deployment, and the decimal head sequence for a
// shared one. An external controller takes no save.
export function expectedRevision(report: EffectiveReport): string {
  if (report.target.kind === "shared-revision") return String(report.revision.desired ?? 0);
  if (report.target.kind === "local-file") return report.revision.file_checksum ?? "";
  return "";
}

// targetLabel states where a save writes, before the operator saves.
export function targetLabel(report: EffectiveReport, expected: string): string {
  switch (report.target.kind) {
    case "local-file":
      return report.target.path ? `local file ${report.target.path}` : "local file (unavailable)";
    case "shared-revision":
      return `shared revision ${Number(expected || 0) + 1}`;
    default:
      return "external controller (read only, shows the diff)";
  }
}

// editable reports whether a field save can change the setting. A node
// setting belongs to each process environment, and a migration-only setting
// changes only through starport config migrate.
export function editable(setting: SchemaSetting | undefined): boolean {
  return setting !== undefined && setting.scope === "deployment" && setting.mutability !== "migration-only";
}

// applies reports whether the setting matters for the selected catalog source.
export function applies(setting: SchemaSetting | undefined, source: string): boolean {
  if (!setting) return true;
  return setting.applicability.includes("all") || setting.applicability.includes(source);
}

const ACRONYMS: Record<string, string> = { api: "API", url: "URL", id: "ID", git: "Git", dev: "dev" };

// settingLabel turns a field-save key into a sentence-case label.
export function settingLabel(key: string): string {
  const words = key.replace(/^catalog_/, "").split("_").map((word) => ACRONYMS[word] ?? word);
  const [first = "", ...rest] = words;
  return [first.charAt(0).toUpperCase() + first.slice(1), ...rest].join(" ");
}

// newOperationID names one save. A retry of the same save reuses it, so the
// gateway answers with the first receipt instead of a second write.
export function newOperationID(): string {
  return crypto.randomUUID();
}
