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

export type ProviderStatus = {
  providers?: Array<{
    provider?: string;
    operator_credential?: { usable?: boolean; state?: string };
  }>;
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
