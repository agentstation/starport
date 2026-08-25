// Gateway API client. All requests go to the gateway that served this
// page (same origin), so the console works on any host the gateway binds.

const KEY_STORAGE = "starport.apiKey";

// SESSION_MARKER is set beside the console session cookie by /launch. The
// session itself is HttpOnly and unreadable here, which is the point: the
// credential belongs to the gateway and the browser, and nothing in this file
// can read it, forward it, or store it. The marker carries no secret and
// authenticates nothing — it exists so the console can tell a signed-in shell
// from a signed-out one without a request whose only purpose is to ask.
const SESSION_MARKER = "starport_session_present";

export function getApiKey(): string {
  return localStorage.getItem(KEY_STORAGE) ?? "";
}

export function setApiKey(key: string): void {
  if (key) {
    localStorage.setItem(KEY_STORAGE, key);
  } else {
    localStorage.removeItem(KEY_STORAGE);
  }
  credentialRejected = false;
  for (const listener of credentialListeners) listener();
}

// hasSession reports that this browser holds a console session, opened by
// `starport ui` on the machine running the gateway.
export function hasSession(): boolean {
  return document.cookie
    .split(";")
    .some((entry) => entry.trim().startsWith(`${SESSION_MARKER}=`));
}

// Credential is what this browser will present on the next request. The two
// kinds are deliberately unalike: a session belongs to a person at this
// machine and expires; a gateway API key belongs to a tenant and does not.
export type Credential =
  | { kind: "session" }
  | { kind: "key"; value: string }
  | { kind: "none" };

// credential prefers a session over a stored key. A browser holding both got
// the session more recently and from the machine itself, and sending a bearer
// key beside it would meter the request against a tenant when the operator
// signed in as the operator.
export function credential(): Credential {
  if (hasSession()) return { kind: "session" };
  const key = getApiKey();
  if (key) return { kind: "key", value: key };
  return { kind: "none" };
}

export function hasCredential(): boolean {
  return credential().kind !== "none";
}

// credentialRejected records that the gateway refused what this browser
// presented. Both kinds go stale on their own: a session ends when it expires
// or when `starport auth rotate` runs, and a key minted by one gateway process
// means nothing to the next. Tracking the rejection lets the console offer a
// way back in instead of reporting the failure as a missing permission.
let credentialRejected = false;

export function isCredentialRejected(): boolean {
  return credentialRejected;
}

function recordCredentialOutcome(rejected: boolean): void {
  if (credentialRejected === rejected) return;
  credentialRejected = rejected;
  for (const listener of credentialListeners) listener();
}

const credentialListeners = new Set<() => void>();

export function onCredentialChange(listener: () => void): () => void {
  credentialListeners.add(listener);
  return () => credentialListeners.delete(listener);
}

// authorization names the credential to the gateway. A session sends no header
// at all: the browser attaches its cookie, and this code cannot read it.
function authorization(held: Credential): Record<string, string> {
  return held.kind === "key" ? { Authorization: `Bearer ${held.value}` } : {};
}

// ApiError carries the HTTP status and the gateway's error message.
export class ApiError extends Error {
  status: number;
  body: unknown;

  constructor(status: number, message: string, body: unknown) {
    super(message);
    this.status = status;
    this.body = body;
  }

  get unauthorized(): boolean {
    return this.status === 401;
  }

  get forbidden(): boolean {
    return this.status === 403;
  }

  // needsKey reports that the request was denied for a credential reason, so
  // the caller should render a locked state instead of a failure. It does not
  // say which reason; use `unauthorized` and `forbidden` for that, or
  // accessMessage to phrase it.
  get needsKey(): boolean {
    return this.status === 401 || this.status === 403;
  }
}

// accessMessage phrases a denied request from its actual cause. A 401 means
// the gateway did not accept the key at all, which a stale key from a previous
// gateway process produces; a 403 means the key authenticated but lacks the
// scope the route requires. Reporting the first as the second sends the reader
// looking for a permission they already have.
export function accessMessage(error: ApiError, scope: string): string {
  if (error.unauthorized) {
    return "Your API key was not accepted. Set a current key in Settings → Connection.";
  }
  return `Your API key lacks the ${scope} scope.`;
}

async function parseError(response: Response): Promise<ApiError> {
  let message = `${response.status} ${response.statusText}`;
  let body: unknown = null;
  try {
    body = await response.json();
    const parsed = body as { error?: { message?: string }; message?: string };
    message = parsed?.error?.message ?? parsed?.message ?? message;
  } catch {
    // non-JSON error body
  }
  return new ApiError(response.status, message, body);
}

export async function request<T>(
  path: string,
  {
    method = "GET",
    body,
    signal,
  }: { method?: string; body?: unknown; signal?: AbortSignal } = {},
): Promise<T> {
  const held = credential();
  const response = await fetch(path, {
    method,
    signal,
    headers: {
      ...authorization(held),
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
    },
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
  });
  if (!response.ok) {
    const error = await parseError(response);
    // Only an outright rejection invalidates the credential. A 403 proves it
    // authenticated, so it must not send the console back to the prompt.
    if (held.kind !== "none" && error.unauthorized) recordCredentialOutcome(true);
    throw error;
  }
  if (held.kind !== "none") recordCredentialOutcome(false);
  if (response.status === 204) return null as T;
  return response.json() as Promise<T>;
}

// --- Response shapes the console consumes ---

export type Health = { status?: string; version?: string };

export type SystemInfo = {
  version?: string;
  uptime?: string;
  storage?: { type?: string };
};

export type SystemMetrics = {
  requests?: { total?: number; errors?: number; rate_1min?: number };
  latency?: { p50?: number; p95?: number; p99?: number };
  // Gateway-added latency only: total handling minus upstream waits.
  overhead?: { p50?: number; p95?: number; p99?: number };
  tokens?: { total?: number };
  spend?: { nano_usd?: number; requests_without_cost?: number };
};

export type ProviderOfferingStatus = {
  provider_model_id: string;
  state?: string;
  reason?: string;
};

export type ProviderRuntimeStatus = {
  provider_id: string;
  adapter?: { state?: string; reason?: string };
  operator_credential?: {
    state?: string;
    reason?: string;
    detail?: string;
    usable?: boolean;
    updated_at?: string;
  };
  offerings?: ProviderOfferingStatus[];
};

// ProviderStatus is the safe provider-state snapshot from
// /api/v1/admin/providers: one revision over adapter, operator
// credential, and offering projections.
export type ProviderStatus = {
  revision?: number;
  catalog_generation_id?: string;
  providers?: ProviderRuntimeStatus[];
};

// CredentialField is a catalog-declared inference credential field a
// caller supplies for BYOK. It never carries secret values.
export type CredentialField = {
  id: string;
  kind?: string;
  required?: boolean;
  default?: string;
  description?: string;
};

export type ProviderPolicies = {
  privacy_policy_url?: string;
  terms_of_service_url?: string;
  retains_data?: boolean;
  trains_on_data?: boolean;
  retention?: string;
  moderated?: boolean;
};

export type ProviderCatalogEntry = {
  id: string;
  name?: string;
  description?: string;
  docs_url?: string;
  url?: string;
  models?: string[];
  credential_fields?: CredentialField[];
  headquarters?: string;
  policies?: ProviderPolicies;
};

export type ProviderRefreshReport = {
  changed?: boolean;
  failure_count?: number;
  provider_state_revision?: number;
};

export type CatalogMetadata = {
  generation_id?: string;
  generated_at?: string;
  catalog_sequence?: number;
  availability_revision?: number;
  completeness?: string;
  degraded?: boolean;
  degradation_reasons?: string[];
  age_seconds?: number;
  manifest_available?: boolean;
  manifest_unavailable_reason?: string;
};

export type ModelAuthor = { id: string; name?: string };

// CatalogAuthor is the full author record from /api/v1/authors. The
// list endpoint leaves `models` empty; only the detail endpoint
// populates it with canonical model ids.
export type CatalogAuthor = {
  id: string;
  name?: string;
  description?: string;
  headquarters?: string;
  icon_url?: string;
  website?: string;
  github?: string;
  huggingface?: string;
  twitter?: string;
  models?: string[];
};

export type ModelLineage = { family?: string; root?: string; parent?: string };

export type OfferingPricing = {
  prompt?: string;
  completion?: string;
  reasoning?: string;
  cache_read?: string;
  cache_write?: string;
  currency?: string;
};

// ModelOffering is one provider's routable offering of a model.
export type ModelOffering = {
  provider: string;
  provider_name?: string;
  provider_model_id: string;
  context_length?: number;
  max_completion_tokens?: number;
  availability?: string;
  lifecycle?: string;
  pricing?: OfferingPricing;
};

export type Model = {
  id: string;
  name?: string;
  description?: string;
  context_length?: number;
  pricing?: { prompt?: string; completion?: string };
  architecture?: {
    input_modalities?: string[];
    output_modalities?: string[];
  };
  top_provider?: { max_completion_tokens?: number };
  supported_parameters?: string[];
  authors?: ModelAuthor[];
  tags?: string[];
  lineage?: ModelLineage;
  knowledge_cutoff?: string;
  open_weights?: boolean;
  offerings?: ModelOffering[];
};

export type OfferingChange = {
  provider: string;
  provider_model_id: string;
  definition_id?: string;
};

export type PriceChange = {
  provider: string;
  provider_model_id: string;
  field: string;
  previous_per_1m?: number;
  current_per_1m?: number;
};

export type CatalogChanges = {
  available?: boolean;
  reason?: string;
  semantically_equal?: boolean;
  from_generation_id?: string;
  to_generation_id?: string;
  to_generated_at?: string;
  models_added?: string[];
  models_removed?: string[];
  offerings_added?: OfferingChange[];
  offerings_removed?: OfferingChange[];
  price_changes?: PriceChange[];
};

export type CatalogRefreshReport = {
  changed?: boolean;
  generation_id?: string;
};

// --- Gateway API keys (admin) ---

export type KeyRequestLimit = { limit: number; window_seconds: number };

export type KeyBudget = { limit: number; interval: string };

export type KeyLimits = {
  requests?: KeyRequestLimit | null;
  spend?: KeyBudget | null;
  tokens?: KeyBudget | null;
};

export type GatewayKey = {
  id: string;
  name?: string;
  // tenant_id names the account the key belongs to. Anything the account
  // owns — its BYOK credentials, its limits — is addressed by this and never
  // by the key ID.
  tenant_id?: string;
  scopes?: string[];
  allowed_models?: string[];
  limits?: KeyLimits | null;
  active?: boolean;
  created_at?: string;
  expires_at?: string | null;
};

export type BudgetUsage = {
  limit?: number;
  interval?: string;
  used?: number;
  remaining?: number;
};

// KeyDetail adds current-window consumption; budgets appear only for
// keys that configure them.
export type KeyDetail = GatewayKey & {
  usage?: { budgets?: { spend?: BudgetUsage; tokens?: BudgetUsage } };
};

export type CreateKeyRequest = {
  name: string;
  scopes: string[];
  allowed_models?: string[];
  limits?: KeyLimits;
  expires_at?: string;
};

export type UpdateKeyRequest = {
  name?: string;
  allowed_models?: string[];
  limits?: KeyLimits;
  expires_at?: string;
  active?: boolean;
};

// CreatedKey nests the record under "key"; the plaintext secret at
// key.key exists only in this response.
export type CreatedKey = {
  key?: GatewayKey & { key?: string };
};

// ProviderCredentialSummary reports that a credential is stored, never what
// it is. The gateway holds only the encrypted value, so no read surface can
// return one.
export type ProviderCredentialSummary = {
  provider: string;
  has_credentials?: boolean;
  config?: Record<string, unknown> | null;
  created_at?: string;
  last_used?: string;
  usage_count?: number;
};

export type ActivityTokens = {
  input?: number;
  output?: number;
  total?: number;
  reasoning?: number;
  cache_read?: number;
  cache_write?: number;
};

// ActivityRecord is one completed inference request from the usage log.
// An absent cost always carries cost_unavailable_reason — never a
// silent zero.
export type ActivityRecord = {
  request_id?: string;
  key_id?: string;
  timestamp: string;
  protocol?: string;
  operation?: string;
  model_requested?: string;
  model_used?: string;
  provider?: string;
  streaming?: boolean;
  status?: string;
  status_code?: number;
  error_class?: string;
  tokens?: ActivityTokens;
  tokens_estimated?: boolean;
  latency_ms?: number;
  routing_ms?: number;
  // Gateway-added latency: total handling minus upstream provider waits.
  overhead_ms?: number;
  // Time to first stream event; streamed requests only.
  ttft_ms?: number;
  attempts?: number;
  cache_status?: string;
  cost?: { nano_usd?: number; currency?: string };
  cost_unavailable_reason?: string;
};

export type ActivityPage = {
  data?: ActivityRecord[];
  next_cursor?: string;
};

export type ActivityFilters = {
  model?: string;
  provider?: string;
  status?: string;
  key_id?: string;
  since?: string;
  limit?: number;
  cursor?: string;
};

// --- Endpoints ---

export function healthReady(): Promise<Health> {
  return request<Health>("/health/ready");
}

export async function listModels(): Promise<Model[]> {
  const body = await request<{ data?: Model[] }>("/api/v1/models");
  return body?.data ?? [];
}

export function systemInfo(): Promise<SystemInfo> {
  return request<SystemInfo>("/api/v1/admin/info");
}

export function systemMetrics(): Promise<SystemMetrics> {
  return request<SystemMetrics>("/api/v1/admin/metrics");
}

export async function listAuthors(): Promise<CatalogAuthor[]> {
  const body = await request<{ authors?: CatalogAuthor[] }>("/api/v1/authors");
  return body?.authors ?? [];
}

export function getAuthor(id: string): Promise<CatalogAuthor> {
  return request<CatalogAuthor>(`/api/v1/authors/${encodeURIComponent(id)}`);
}

export function providerStatus(): Promise<ProviderStatus> {
  return request<ProviderStatus>("/api/v1/admin/providers");
}

export async function listProviderCatalog(): Promise<ProviderCatalogEntry[]> {
  const body = await request<{ providers?: ProviderCatalogEntry[] }>(
    "/api/v1/providers",
  );
  return body?.providers ?? [];
}

export function refreshProviders(): Promise<ProviderRefreshReport> {
  return request<ProviderRefreshReport>("/api/v1/admin/providers/refresh", {
    method: "POST",
  });
}

export function catalogMetadata(): Promise<CatalogMetadata> {
  return request<CatalogMetadata>("/api/v1/catalog");
}

export function catalogChanges(): Promise<CatalogChanges> {
  return request<CatalogChanges>("/api/v1/catalog/changes");
}

export function refreshCatalog(): Promise<CatalogRefreshReport> {
  return request<CatalogRefreshReport>("/api/v1/admin/catalog/refresh", {
    method: "POST",
  });
}

// AuthMode is what the gateway does with a missing key: "required" refuses the
// request, "disabled" serves it as the anonymous identity. It is a gateway
// setting, not a console preference — every client of this deployment sees it.
export type AuthMode = {
  mode: "required" | "disabled";
  // source names what set the running mode: default, config, flag, or console.
  // A mode set by config or a flag is fixed for the process, and the console
  // says which one to edit rather than offering a control that would fail.
  source: "default" | "config" | "flag" | "console";
  can_change: boolean;
  reason?: string;
};

// readAuthMode is deliberately unauthenticated. A console with no key needs to
// know whether it has to go get one, and asking that question with a key it
// does not have would answer 401.
export function readAuthMode(): Promise<AuthMode> {
  return request<AuthMode>("/api/v1/auth/mode");
}

export function setAuthMode(mode: AuthMode["mode"]): Promise<AuthMode> {
  return request<AuthMode>("/api/v1/admin/auth/mode", {
    method: "PUT",
    body: { mode },
  });
}

export async function listKeys(): Promise<GatewayKey[]> {
  const body = await request<{ keys?: GatewayKey[] }>("/api/v1/admin/keys");
  return body?.keys ?? [];
}

export function getKeyDetail(keyId: string): Promise<KeyDetail> {
  return request<KeyDetail>(`/api/v1/admin/keys/${encodeURIComponent(keyId)}`);
}

export function createKey(body: CreateKeyRequest): Promise<CreatedKey> {
  return request<CreatedKey>("/api/v1/admin/keys", { method: "POST", body });
}

export function updateKey(
  keyId: string,
  body: UpdateKeyRequest,
): Promise<GatewayKey> {
  return request<GatewayKey>(`/api/v1/admin/keys/${encodeURIComponent(keyId)}`, {
    method: "PUT",
    body,
  });
}

export function deleteKey(keyId: string): Promise<unknown> {
  return request<unknown>(`/api/v1/admin/keys/${encodeURIComponent(keyId)}`, {
    method: "DELETE",
  });
}

// --- Gateway credentials: the provider credentials the operator applies ---
//
// These serve the whole deployment and belong to nobody. They are addressed by
// provider alone, because there is exactly one per provider and no account
// owns it. They are not BYOK, and no screen that shows them says BYOK.

function gatewayCredentialPath(provider: string, suffix = ""): string {
  return `/api/v1/providers/${encodeURIComponent(provider)}/credentials${suffix}`;
}

export function getGatewayCredential(
  provider: string,
): Promise<ProviderCredentialSummary> {
  return request<ProviderCredentialSummary>(gatewayCredentialPath(provider));
}

// putGatewayCredential applies or rotates the deployment credential. PUT is an
// upsert, so an operator rotating one does not first have to ask whether one is
// already applied.
export function putGatewayCredential(
  provider: string,
  body: {
    credentials: Record<string, string>;
    config?: Record<string, string>;
  },
): Promise<unknown> {
  return request<unknown>(gatewayCredentialPath(provider), {
    method: "PUT",
    body,
  });
}

export function deleteGatewayCredential(provider: string): Promise<unknown> {
  return request<unknown>(gatewayCredentialPath(provider), { method: "DELETE" });
}

export function validateGatewayCredential(
  provider: string,
): Promise<{ valid?: boolean }> {
  return request<{ valid?: boolean }>(
    gatewayCredentialPath(provider, "/validate"),
    { method: "POST" },
  );
}

// --- BYOK: the provider credentials one tenant brings for itself ---
//
// These are addressed by tenant, never by gateway API key. A tenant's
// credentials outlive any key it rotates, and a second key in the same tenant
// reaches the same set. The deployment-wide credential an operator applies is
// a separate plane on the provider itself and is not BYOK.

// DEFAULT_TENANT_ID is the canonical account every deployment has from first
// boot. A key that names no account runs under it.
export const DEFAULT_TENANT_ID = "default";

function byokPath(tenantId: string, provider?: string): string {
  const base = `/api/v1/tenants/${encodeURIComponent(tenantId)}/byok`;
  return provider ? `${base}/${encodeURIComponent(provider)}` : base;
}

export async function listBYOKCredentials(
  tenantId: string,
): Promise<ProviderCredentialSummary[]> {
  const body = await request<{ credentials?: ProviderCredentialSummary[] }>(
    byokPath(tenantId),
  );
  return body?.credentials ?? [];
}

// putBYOKCredential applies or rotates one provider's credential. PUT is an
// upsert, so the caller states what the credential should be without first
// asking whether one is already stored.
export function putBYOKCredential(
  tenantId: string,
  provider: string,
  body: {
    credentials: Record<string, string>;
    config?: Record<string, string>;
  },
): Promise<unknown> {
  return request<unknown>(byokPath(tenantId, provider), {
    method: "PUT",
    body,
  });
}

export function deleteBYOKCredential(
  tenantId: string,
  provider: string,
): Promise<unknown> {
  return request<unknown>(byokPath(tenantId, provider), { method: "DELETE" });
}

export function validateBYOKCredential(
  tenantId: string,
  provider: string,
): Promise<{ valid?: boolean }> {
  return request<{ valid?: boolean }>(
    `${byokPath(tenantId, provider)}/validate`,
    { method: "POST" },
  );
}

// --- Tenants: the accounts an operator governs ---

// CredentialStrategy names which credential sources serve an account, and in
// which order. It is the operator's lever for whether an account may draw on
// the deployment's own provider credentials at all.
export type CredentialStrategy =
  | "operator_first"
  | "byok_first"
  | "byok_only";

export const CREDENTIAL_STRATEGY_LABELS: Record<CredentialStrategy, string> = {
  operator_first: "Operator credentials first, then this account's own",
  byok_first: "This account's own credentials first, then the operator's",
  byok_only: "This account's own credentials only",
};

// TenantLimits meters the sum over every key the account holds. It is not a
// per-key ceiling: a key limit bounds one key, and a request satisfies both.
export type TenantLimits = {
  requests?: { limit?: number; window_seconds?: number } | null;
  spend?: { limit?: number; interval?: string } | null;
  tokens?: { limit?: number; interval?: string } | null;
};

export type Tenant = {
  id: string;
  name?: string;
  limits?: TenantLimits | null;
  credential_strategy?: CredentialStrategy;
  metadata?: Record<string, unknown> | null;
  active?: boolean;
  created_at?: string;
  updated_at?: string;
};

export async function listTenants(): Promise<Tenant[]> {
  const body = await request<{ tenants?: Tenant[] }>("/api/v1/admin/tenants");
  return body?.tenants ?? [];
}

export function getTenant(tenantId: string): Promise<Tenant> {
  return request<Tenant>(
    `/api/v1/admin/tenants/${encodeURIComponent(tenantId)}`,
  );
}

export function createTenant(body: {
  id: string;
  name?: string;
  credential_strategy?: CredentialStrategy;
}): Promise<Tenant> {
  return request<Tenant>("/api/v1/admin/tenants", { method: "POST", body });
}

export function updateTenant(
  tenantId: string,
  body: {
    name?: string;
    credential_strategy?: CredentialStrategy;
    limits?: TenantLimits | null;
    active?: boolean;
  },
): Promise<Tenant> {
  return request<Tenant>(
    `/api/v1/admin/tenants/${encodeURIComponent(tenantId)}`,
    { method: "PUT", body },
  );
}

export function deleteTenant(tenantId: string): Promise<unknown> {
  return request<unknown>(
    `/api/v1/admin/tenants/${encodeURIComponent(tenantId)}`,
    { method: "DELETE" },
  );
}

function activityQuery(filters: ActivityFilters): string {
  const params = new URLSearchParams();
  if (filters.model) params.set("model", filters.model);
  if (filters.provider) params.set("provider", filters.provider);
  if (filters.status) params.set("status", filters.status);
  if (filters.key_id) params.set("key_id", filters.key_id);
  if (filters.since) params.set("since", filters.since);
  if (filters.limit) params.set("limit", String(filters.limit));
  if (filters.cursor) params.set("cursor", filters.cursor);
  const query = params.toString();
  return query ? `?${query}` : "";
}

// --- Presets ---

// PresetProviderPreferences is the preset-owned provider routing
// policy, mirroring internal/presets.ProviderPreferences.
export type PresetProviderPreferences = {
  order?: string[];
  only?: string[];
  ignore?: string[];
  allow_fallbacks?: boolean;
  sort?: string;
  max_prompt_price_per_1m?: number;
  max_completion_price_per_1m?: number;
};

// PresetConfig is the typed request subset a preset stores; request
// fields win over preset fields at inference time.
export type PresetConfig = {
  model?: string;
  models?: string[];
  provider?: PresetProviderPreferences;
  system?: string;
  temperature?: number;
  top_p?: number;
  presence_penalty?: number;
  frequency_penalty?: number;
  max_tokens?: number;
  seed?: number;
  stop?: string[];
};

// Preset revision is server-assigned; updates and deletes send it back
// for optimistic concurrency (409 on mismatch).
export type Preset = {
  name: string;
  description?: string;
  config: PresetConfig;
  revision?: number;
  created_at?: string;
  updated_at?: string;
};

export type PresetWriteRequest = {
  name: string;
  description?: string;
  config: PresetConfig;
  revision?: number;
};

export async function listPresets(): Promise<Preset[]> {
  const body = await request<{ data?: Preset[] }>("/api/v1/presets");
  return body?.data ?? [];
}

export function createPreset(body: PresetWriteRequest): Promise<Preset> {
  return request<Preset>("/api/v1/presets", { method: "POST", body });
}

export function updatePreset(
  name: string,
  body: PresetWriteRequest,
): Promise<Preset> {
  return request<Preset>(`/api/v1/presets/${encodeURIComponent(name)}`, {
    method: "PUT",
    body,
  });
}

export function deletePreset(name: string): Promise<unknown> {
  return request<unknown>(`/api/v1/presets/${encodeURIComponent(name)}`, {
    method: "DELETE",
  });
}

// --- Chat completions ---

export type ChatUsage = {
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  completion_tokens_details?: { reasoning_tokens?: number };
};

// ChatStreamMeta carries the response facts the chat page records per
// assistant turn: the routed model, final usage, cache headers, and any
// provider-preference fields the gateway accepted but cannot enforce.
export type ChatStreamMeta = {
  model: string;
  usage: ChatUsage | null;
  cache: string;
  cacheAge: string;
  unenforced: string;
};

// streamChat posts to /api/v1/chat/completions with stream=true and
// invokes callbacks per SSE delta. Resolves with the stream metadata.
export async function streamChat(
  body: Record<string, unknown>,
  {
    signal,
    onDelta,
    onReasoning,
  }: {
    signal?: AbortSignal;
    onDelta: (text: string) => void;
    onReasoning?: (text: string) => void;
  },
): Promise<ChatStreamMeta> {
  const response = await fetch("/api/v1/chat/completions", {
    method: "POST",
    signal,
    headers: {
      ...authorization(credential()),
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      ...body,
      stream: true,
      stream_options: { include_usage: true },
    }),
  });
  if (!response.ok || !response.body) throw await parseError(response);

  const meta: ChatStreamMeta = {
    model: "",
    usage: null,
    cache: response.headers.get("X-Cache") ?? "",
    cacheAge: response.headers.get("X-Cache-Age") ?? "",
    unenforced:
      response.headers.get("X-Starport-Unenforced-Provider-Fields") ?? "",
  };

  type StreamEvent = {
    error?: { code?: number; message?: string };
    usage?: ChatUsage;
    model?: string;
    choices?: {
      delta?: { content?: string; reasoning?: string; reasoning_content?: string };
    }[];
  };

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";
    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed.startsWith("data:")) continue;
      const payload = trimmed.slice(5).trim();
      if (payload === "[DONE]") continue;
      let event: StreamEvent;
      try {
        event = JSON.parse(payload) as StreamEvent;
      } catch {
        continue;
      }
      if (event.error) {
        throw new ApiError(
          event.error.code ?? 500,
          event.error.message ?? "stream error",
          event,
        );
      }
      if (event.usage) meta.usage = event.usage;
      if (event.model) meta.model = event.model;
      const delta = event.choices?.[0]?.delta;
      if (!delta) continue;
      const reasoning = delta.reasoning_content ?? delta.reasoning;
      if (reasoning && onReasoning) onReasoning(reasoning);
      if (delta.content) onDelta(delta.content);
    }
  }
  return meta;
}

export type ChatCompletion = {
  model?: string;
  choices?: { message?: { content?: string } }[];
  usage?: ChatUsage;
};

// completeChat is the non-streaming variant (thread title generation).
export function completeChat(
  body: Record<string, unknown>,
  { signal }: { signal?: AbortSignal } = {},
): Promise<ChatCompletion> {
  return request<ChatCompletion>("/api/v1/chat/completions", {
    method: "POST",
    body,
    signal,
  });
}

// listActivity reads the authenticated key's own request log; key_id is
// ignored there — only the admin listing can widen the scope.
export function listActivity(filters: ActivityFilters): Promise<ActivityPage> {
  return request<ActivityPage>(`/api/v1/activity${activityQuery(filters)}`);
}

export function listAdminActivity(filters: ActivityFilters): Promise<ActivityPage> {
  return request<ActivityPage>(`/api/v1/admin/activity${activityQuery(filters)}`);
}
