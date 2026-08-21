// Gateway API client. All requests go to the gateway that served this
// page (same origin), so the console works on any host the gateway binds.

const KEY_STORAGE = "starport.apiKey";

export function getApiKey(): string {
  return localStorage.getItem(KEY_STORAGE) ?? "";
}

export function setApiKey(key: string): void {
  if (key) {
    localStorage.setItem(KEY_STORAGE, key);
  } else {
    localStorage.removeItem(KEY_STORAGE);
  }
  for (const listener of keyListeners) listener();
}

const keyListeners = new Set<() => void>();

export function onKeyChange(listener: () => void): () => void {
  keyListeners.add(listener);
  return () => keyListeners.delete(listener);
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

  get needsKey(): boolean {
    return this.status === 401 || this.status === 403;
  }
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
  const key = getApiKey();
  const response = await fetch(path, {
    method,
    signal,
    headers: {
      ...(key ? { Authorization: `Bearer ${key}` } : {}),
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
    },
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
  });
  if (!response.ok) throw await parseError(response);
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
  latency?: { p50?: number; p95?: number };
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

export type ProviderCatalogEntry = {
  id: string;
  name?: string;
  url?: string;
  models?: string[];
  credential_fields?: CredentialField[];
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

export type ProviderKeySummary = {
  provider: string;
  created_at?: string;
  last_used?: string;
  usage_count?: number;
};

export type ActivityRecord = {
  timestamp: string;
  status?: string;
  tokens?: { total?: number };
};

export type ActivityPage = {
  data?: ActivityRecord[];
  next_cursor?: string;
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

// --- Per-key BYOK provider credentials ---

export async function listProviderKeys(
  keyId: string,
): Promise<ProviderKeySummary[]> {
  const body = await request<{ provider_keys?: ProviderKeySummary[] }>(
    `/api/v1/keys/${encodeURIComponent(keyId)}/provider-keys`,
  );
  return body?.provider_keys ?? [];
}

export function createProviderKey(
  keyId: string,
  body: {
    provider: string;
    credentials: Record<string, string>;
    config?: Record<string, string>;
  },
): Promise<unknown> {
  return request<unknown>(
    `/api/v1/keys/${encodeURIComponent(keyId)}/provider-keys`,
    { method: "POST", body },
  );
}

export function deleteProviderKey(
  keyId: string,
  provider: string,
): Promise<unknown> {
  return request<unknown>(
    `/api/v1/keys/${encodeURIComponent(keyId)}/provider-keys/${encodeURIComponent(provider)}`,
    { method: "DELETE" },
  );
}

export function validateProviderKey(
  keyId: string,
  provider: string,
): Promise<{ valid?: boolean }> {
  return request<{ valid?: boolean }>(
    `/api/v1/keys/${encodeURIComponent(keyId)}/provider-keys/${encodeURIComponent(provider)}/validate`,
    { method: "POST" },
  );
}

export function listAdminActivity(filters: {
  since?: string;
  limit?: number;
}): Promise<ActivityPage> {
  const params = new URLSearchParams();
  if (filters.since) params.set("since", filters.since);
  if (filters.limit) params.set("limit", String(filters.limit));
  const query = params.toString();
  return request<ActivityPage>(`/api/v1/admin/activity${query ? `?${query}` : ""}`);
}
