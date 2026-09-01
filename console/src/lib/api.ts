// Gateway API client. All requests go to the gateway that served this
// page (same origin), so the console works on any host the gateway binds.

import type { GeneratedMedia } from "@/lib/attachments";

const KEY_STORAGE = "starport.apiKey";

// SESSION_MARKER is set beside the console session cookie by /launch. The
// session itself is HttpOnly and unreadable here, which is the point: the
// credential belongs to the gateway and the browser, and nothing in this file
// can read it, forward it, or store it. The marker carries no secret and
// authenticates nothing — it exists so the console can tell a shell with a
// session from first contact without a request whose only purpose is to ask.
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
// machine and expires; a gateway API key belongs to an account and does not.
export type Credential =
  | { kind: "session" }
  | { kind: "key"; value: string }
  | { kind: "none" };

// credential prefers a session over a stored key. A browser holding both got
// the session more recently and from the machine itself, and sending a bearer
// key beside it would meter the request against an account when the reader is
// the operator of the machine.
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

// SESSION_PATH exchanges a local admin token for a console session. It is the
// only gateway route this file addresses by a literal path rather than through
// `request`, and deliberately: `request` attaches whatever credential this
// browser already holds, and the whole point of this call is that it holds
// none.
const SESSION_PATH = "/console/session";

// openSession hands the gateway a token an operator pasted and, if it is the
// one this gateway printed, comes back with a session.
//
// Nothing is stored here. The credential arrives as an HttpOnly cookie the
// browser attaches on its own and this code cannot read, so a token that opens
// a session never reaches localStorage, and closing the tab leaves nothing
// behind but the cookie the gateway set.
export async function openSession(token: string): Promise<void> {
  const response = await fetch(SESSION_PATH, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }),
  });
  if (!response.ok) throw await parseError(response);
  credentialRejected = false;
  for (const listener of credentialListeners) listener();
}

// IDENTITY_PATH is the acquisition surface an operator can configure: the
// provider list the first-contact page reads before drawing anything, and the
// per-provider path a browser navigates to when the reader picks one. Like
// SESSION_PATH, it is addressed literally rather than through `request`,
// because the reader holds no credential yet.
const IDENTITY_PATH = "/console/identity";

// identityProviders reports which identity providers this deployment
// configured. An unconfigured deployment answers with an empty list, and so
// does any failure here: first contact renders the same page either way, just
// without the provider choices.
export async function identityProviders(): Promise<string[]> {
  try {
    const response = await fetch(`${IDENTITY_PATH}/providers`);
    if (!response.ok) return [];
    const parsed = (await response.json()) as { providers?: unknown };
    if (!Array.isArray(parsed.providers)) return [];
    return parsed.providers.filter((name): name is string => typeof name === "string");
  } catch {
    return [];
  }
}

// identityBeginPath is where a browser goes to authenticate through a named
// provider. It is a full navigation, not a fetch: the gateway answers with a
// redirect to the provider's consent page, and the browser must follow it.
export function identityBeginPath(provider: string): string {
  return `${IDENTITY_PATH}/${encodeURIComponent(provider)}`;
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
  // Stored file bytes do not live in the record store, so the backend that
  // holds them is a separate fact.
  files?: { backend?: string };
};

export type SystemMetrics = {
  requests?: { total?: number; errors?: number; rate_1min?: number };
  latency?: { p50?: number; p95?: number; p99?: number };
  // Gateway-added latency only: total handling minus upstream waits.
  overhead?: { p50?: number; p95?: number; p99?: number };
  tokens?: { total?: number };
  spend?: { nano_usd?: number; requests_without_cost?: number };
};

// ProviderOfferingStatus answers two separate questions about one offering.
// `state` is health. `routing` is whether a request can reach it at all: an
// offering the catalog advertises but the route planner drops is healthy and
// unusable, and `routing.reason` names the filter that dropped it.
export type ProviderOfferingStatus = {
  provider_model_id: string;
  state?: string;
  reason?: string;
  routing?: { state?: string; reason?: string };
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
  // The provider's own status-page verdict, present only while the
  // provider reports an incident about itself.
  incident?: { indicator?: string; description?: string; checked_at?: string };
};

// ProviderStatus is the safe provider-state snapshot from
// /api/v1/admin/providers: one revision over adapter, operator
// credential, and offering projections.
export type ProviderStatus = {
  revision?: number;
  catalog_generation_id?: string;
  providers?: ProviderRuntimeStatus[];
};

// ProviderIncident is one entry in the provider's own published incident
// log, normalized by the gateway. Every fact in it is the provider's
// assertion about itself; absent fields mean the wire left them unstated.
export type ProviderIncident = {
  title: string;
  indicator?: string;
  status?: string;
  started_at?: string;
  resolved_at?: string;
  url?: string;
  update?: string;
  components?: string[];
};

// ObservedIncidentTransition is one indicator change this gateway itself
// recorded from the provider's status feed — the deployment's own memory,
// on its own clock, distinct from what the provider chooses to publish.
export type ObservedIncidentTransition = {
  provider_id: string;
  indicator: string;
  description?: string;
  observed_at: string;
};

// ProviderIncidentLog carries both provenances for one provider: `log` is
// the provider's published history (with an availability verdict so an
// empty list is honest), `observed` is what this gateway recorded.
export type ProviderIncidentLog = {
  provider_id: string;
  log: {
    availability: "available" | "unpublished" | "unreachable";
    incidents?: ProviderIncident[];
    fetched_at?: string;
  };
  observed?: ObservedIncidentTransition[];
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
  status_page_url?: string;
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
  // Audio tokens bill at their own rate wherever a provider meters them.
  audio_input?: string;
  audio_output?: string;
  // A page is the unit document recognition bills in. No token price converts
  // into it, so an offering that reads documents and publishes no page price
  // is one this console cannot price at all.
  page_input?: string;
  // A search unit is what reranking bills in on the providers that meter it:
  // one query against a bounded document count. Such an offering may publish
  // no token price at all, so a reader looking only at prompt and completion
  // would take a priced rerank for a free one.
  search_unit?: string;
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
  // max_documents is the longest document list this offering ranks in one
  // rerank request. A caller that sends more gets a refusal.
  max_documents?: number;
  operations?: string[];
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
  // account_id names the account the key belongs to. Anything the account
  // owns — its BYOK credentials, its limits — is addressed by this and never
  // by the key ID.
  account_id?: string;
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

// SharedCredentialSummary is one entry of a provider's shared plane. A
// provider can hold several, so each carries the id that addresses it and
// the access rule that says which accounts may resolve it: "open" serves
// every account, "granted" only the accounts in grants. Like every credential
// summary, it never carries the secret.
export type SharedCredentialSummary = ProviderCredentialSummary & {
  id: string;
  label?: string;
  access?: "open" | "granted";
  grants?: string[];
};

export type ActivityTokens = {
  input?: number;
  output?: number;
  total?: number;
  reasoning?: number;
  cache_read?: number;
  cache_write?: number;
  // The audio shares are already inside input and output, the way cache_read
  // is. Adding them to a displayed total would count the same audio twice.
  audio_input?: number;
  audio_output?: number;
};

// ActivityMedia counts the units of an answer that no token total describes.
// A provider meters a generated image per image and reports no tokens for it,
// so a record without this object is a text turn, not a free one.
export type ActivityMedia = {
  generated_images?: number;
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
  // Which credential plane paid: environment, gateway, byok, or anonymous
  // for a provider that accepted the call without one. A record written
  // before the gateway recorded planes carries none.
  credential_source?: string;
  streaming?: boolean;
  status?: string;
  status_code?: number;
  error_class?: string;
  tokens?: ActivityTokens;
  media?: ActivityMedia;
  tokens_estimated?: boolean;
  latency_ms?: number;
  routing_ms?: number;
  // Gateway-added latency: total handling minus upstream provider waits.
  overhead_ms?: number;
  // Time to first stream event; streamed requests only.
  ttft_ms?: number;
  attempts?: number;
  cache_status?: string;
  // Which engine read the documents this turn attached: "native" for the
  // in-process reader, "recognition" for a catalogued model. Absent on a turn
  // that attached none, which is every ordinary chat turn.
  parser_engine?: string;
  document_pages?: number;
  // The pages a recognition model was paid to read this turn. Zero on a cached
  // read: an earlier turn paid for those pages.
  recognized_pages?: number;
  native_pages?: number;
  // Every attachment came back from the extraction cache. A cached read and a
  // native read both cost nothing, and only this separates them.
  extraction_cached?: boolean;
  extraction_millis?: number;
  // The recognized share of `cost`, reported on its own so a reader can tell
  // what reading the document cost from what answering about it cost.
  extraction_cost?: { nano_usd?: number; currency?: string };
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

export function providerIncidentLog(
  providerId: string,
): Promise<ProviderIncidentLog> {
  return request<ProviderIncidentLog>(
    `/api/v1/admin/providers/${encodeURIComponent(providerId)}/incidents`,
  );
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

// --- Shared credentials: the provider credentials the operator shares ---
//
// These serve the deployment's accounts and belong to nobody. A provider can
// hold several — each addressed by the id the gateway assigned at creation —
// and each one names its own access rule: open to every account, or granted
// to a listed few. They are not BYOK, and no screen that shows them says BYOK.

function sharedCredentialPath(provider: string, suffix = ""): string {
  return `/api/v1/providers/${encodeURIComponent(provider)}/credentials${suffix}`;
}

function sharedCredentialItemPath(
  provider: string,
  credentialId: string,
  suffix = "",
): string {
  return sharedCredentialPath(
    provider,
    `/${encodeURIComponent(credentialId)}${suffix}`,
  );
}

// listSharedCredentials reads the provider's whole shared plane. An empty
// plane is an empty list, not an error: "no shared credential yet" is a state
// to render.
export async function listSharedCredentials(
  provider: string,
): Promise<SharedCredentialSummary[]> {
  const body = await request<{ credentials?: SharedCredentialSummary[] }>(
    sharedCredentialPath(provider),
  );
  return body?.credentials ?? [];
}

// createSharedCredential stores a new shared credential and returns its
// summary — the id in it addresses every later read, rotation, and removal.
// The access rule is chosen here, at creation; omitting it means open.
export function createSharedCredential(
  provider: string,
  body: {
    credentials: Record<string, string>;
    config?: Record<string, string>;
    label?: string;
    access?: "open" | "granted";
    grants?: string[];
  },
): Promise<SharedCredentialSummary> {
  return request<SharedCredentialSummary>(sharedCredentialPath(provider), {
    method: "POST",
    body,
  });
}

// updateSharedCredential rotates or reconfigures one credential in place, so
// its identity, grants, and usage history survive the new value. Absent
// fields stay as they are.
export function updateSharedCredential(
  provider: string,
  credentialId: string,
  body: {
    credentials?: Record<string, string>;
    config?: Record<string, string>;
    label?: string;
    access?: "open" | "granted";
    grants?: string[];
  },
): Promise<SharedCredentialSummary> {
  return request<SharedCredentialSummary>(
    sharedCredentialItemPath(provider, credentialId),
    { method: "PUT", body },
  );
}

export function deleteSharedCredential(
  provider: string,
  credentialId: string,
): Promise<unknown> {
  return request<unknown>(sharedCredentialItemPath(provider, credentialId), {
    method: "DELETE",
  });
}

export function validateSharedCredential(
  provider: string,
  credentialId: string,
): Promise<{ valid?: boolean }> {
  return request<{ valid?: boolean }>(
    sharedCredentialItemPath(provider, credentialId, "/validate"),
    { method: "POST" },
  );
}

// --- BYOK: the provider credentials one account brings for itself ---
//
// These are addressed by account, never by gateway API key. An account's
// credentials outlive any key it rotates, and a second key in the same account
// reaches the same set. The deployment-wide credential an operator applies is
// a separate plane on the provider itself and is not BYOK.

// DEFAULT_ACCOUNT_ID is the canonical account every deployment has from first
// boot. A key that names no account runs under it.
export const DEFAULT_ACCOUNT_ID = "default";

function byokPath(accountId: string, provider?: string): string {
  const base = `/api/v1/accounts/${encodeURIComponent(accountId)}/byok`;
  return provider ? `${base}/${encodeURIComponent(provider)}` : base;
}

export async function listBYOKCredentials(
  accountId: string,
): Promise<ProviderCredentialSummary[]> {
  const body = await request<{ credentials?: ProviderCredentialSummary[] }>(
    byokPath(accountId),
  );
  return body?.credentials ?? [];
}

// putBYOKCredential applies or rotates one provider's credential. PUT is an
// upsert, so the caller states what the credential should be without first
// asking whether one is already stored.
export function putBYOKCredential(
  accountId: string,
  provider: string,
  body: {
    credentials: Record<string, string>;
    config?: Record<string, string>;
  },
): Promise<unknown> {
  return request<unknown>(byokPath(accountId, provider), {
    method: "PUT",
    body,
  });
}

export function deleteBYOKCredential(
  accountId: string,
  provider: string,
): Promise<unknown> {
  return request<unknown>(byokPath(accountId, provider), { method: "DELETE" });
}

export function validateBYOKCredential(
  accountId: string,
  provider: string,
): Promise<{ valid?: boolean }> {
  return request<{ valid?: boolean }>(
    `${byokPath(accountId, provider)}/validate`,
    { method: "POST" },
  );
}

// --- Accounts: the accounts an operator governs ---

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

// AccountLimits meters the sum over every key the account holds. It is not a
// per-key ceiling: a key limit bounds one key, and a request satisfies both.
export type AccountLimits = {
  requests?: { limit?: number; window_seconds?: number } | null;
  spend?: { limit?: number; interval?: string } | null;
  tokens?: { limit?: number; interval?: string } | null;
  // stored_bytes is a level, not a rate. Nothing resets it at an interval
  // boundary: an upload raises it and a delete lowers it. That is why it
  // carries no window and no interval.
  stored_bytes?: number | null;
};

// AccountBYOKPolicy is the operator's answer to whether this account may
// bring its own provider credentials. No policy stored means yes, for every
// provider; "selected" narrows that to the listed providers; "none" refuses
// BYOK outright. Sending `{"mode":"all"}` with no providers clears the
// stored policy back to the default.
export type AccountBYOKPolicy = {
  mode: "all" | "selected" | "none";
  providers?: string[];
};

// AccountProviderAccess grants the account one provider, optionally narrowed
// to specific models. An empty or absent models list grants every model the
// provider serves — model granularity is an opt-in, not the default.
export type AccountProviderAccess = {
  provider: string;
  models?: string[];
};

export type Account = {
  id: string;
  name?: string;
  limits?: AccountLimits | null;
  credential_strategy?: CredentialStrategy;
  byok_policy?: AccountBYOKPolicy | null;
  // access names the providers this account may reach. Absent means every
  // provider and every model; sending `[]` clears a stored list back to that.
  access?: AccountProviderAccess[] | null;
  metadata?: Record<string, unknown> | null;
  active?: boolean;
  created_at?: string;
  updated_at?: string;
};

export async function listAccounts(): Promise<Account[]> {
  const body = await request<{ accounts?: Account[] }>("/api/v1/admin/accounts");
  return body?.accounts ?? [];
}

export function getAccount(accountId: string): Promise<Account> {
  return request<Account>(
    `/api/v1/admin/accounts/${encodeURIComponent(accountId)}`,
  );
}

export function createAccount(body: {
  id: string;
  name?: string;
  credential_strategy?: CredentialStrategy;
  // template names an account template whose defaults stamp the new account.
  // It is read only at creation, and any explicit field in this same body
  // wins over what the template would have stamped.
  template?: string;
}): Promise<Account> {
  return request<Account>("/api/v1/admin/accounts", { method: "POST", body });
}

export function updateAccount(
  accountId: string,
  body: {
    name?: string;
    credential_strategy?: CredentialStrategy;
    limits?: AccountLimits | null;
    byok_policy?: AccountBYOKPolicy;
    access?: AccountProviderAccess[];
    active?: boolean;
  },
): Promise<Account> {
  return request<Account>(
    `/api/v1/admin/accounts/${encodeURIComponent(accountId)}`,
    { method: "PUT", body },
  );
}

export function deleteAccount(accountId: string): Promise<unknown> {
  return request<unknown>(
    `/api/v1/admin/accounts/${encodeURIComponent(accountId)}`,
    { method: "DELETE" },
  );
}

// --- Account templates ---

// AccountTemplate names creation defaults once: an account template holds
// the limits, credential strategy, BYOK policy, and provider access that a
// new account starts with. The stamp is a copy — editing a template later
// never rewrites an account it already created.
export type AccountTemplate = {
  id: string;
  name?: string;
  limits?: AccountLimits | null;
  credential_strategy?: CredentialStrategy;
  byok_policy?: AccountBYOKPolicy | null;
  access?: AccountProviderAccess[] | null;
  created_at?: string;
  updated_at?: string;
};

// AccountTemplateBody is what a create or update may set. The clearing
// sentinels are the account's: `{"mode":"all"}` erases a stored BYOK policy
// and `[]` erases stored access grants.
export type AccountTemplateBody = {
  name?: string;
  credential_strategy?: CredentialStrategy;
  limits?: AccountLimits | null;
  byok_policy?: AccountBYOKPolicy;
  access?: AccountProviderAccess[];
};

export async function listAccountTemplates(): Promise<AccountTemplate[]> {
  const body = await request<{ templates?: AccountTemplate[] }>(
    "/api/v1/admin/account-templates",
  );
  return body?.templates ?? [];
}

export function createAccountTemplate(
  body: AccountTemplateBody & { id: string },
): Promise<AccountTemplate> {
  return request<AccountTemplate>("/api/v1/admin/account-templates", {
    method: "POST",
    body,
  });
}

export function updateAccountTemplate(
  templateId: string,
  body: AccountTemplateBody,
): Promise<AccountTemplate> {
  return request<AccountTemplate>(
    `/api/v1/admin/account-templates/${encodeURIComponent(templateId)}`,
    { method: "PUT", body },
  );
}

export function deleteAccountTemplate(templateId: string): Promise<unknown> {
  return request<unknown>(
    `/api/v1/admin/account-templates/${encodeURIComponent(templateId)}`,
    { method: "DELETE" },
  );
}

// --- Members and teams: the people plane an identity provider fills ---

// Member is one user this gateway knows: a row the identity provider
// resolved. The gateway never creates one from the console — membership in
// the deployment arrives through the identity grant, so this surface reads
// and governs, it does not invent people.
export type Member = {
  id: string;
  subject: string;
  email?: string;
  display_name?: string;
  created_at?: string;
  updated_at?: string;
};

// TeamBudget bounds the team's nano-USD spend inside one fixed UTC
// interval, summed over every key attributed to the team across every
// account the team reaches. An absent budget leaves the team unmetered.
export type TeamBudget = {
  limit: number;
  interval: string;
};

// Team is an operator-formed group of members. Granting an account to a
// team grants it to everyone on the roster, now and later.
export type Team = {
  id: string;
  name: string;
  budget?: TeamBudget | null;
  created_at?: string;
  updated_at?: string;
};

export type TeamMembership = {
  user_id: string;
  team_id: string;
  created_at?: string;
};

// AccountGrant gives one account to exactly one grantee: a user or a team,
// never both. The row's identity is the whole triple, which is why removal
// names every part of it.
export type AccountGrant = {
  account_id: string;
  user_id?: string;
  team_id?: string;
  created_at?: string;
};

export async function listMembers(): Promise<Member[]> {
  const body = await request<{ users?: Member[] }>("/api/v1/admin/users");
  return body?.users ?? [];
}

export async function listTeams(): Promise<Team[]> {
  const body = await request<{ teams?: Team[] }>("/api/v1/admin/teams");
  return body?.teams ?? [];
}

export function createTeam(name: string): Promise<Team> {
  return request<Team>("/api/v1/admin/teams", {
    method: "POST",
    body: { name },
  });
}

// updateTeam states the team's whole mutable surface: the name and the
// budget. Omitting the budget clears it — the PUT is the team as it should
// now be, not a delta.
export function updateTeam(
  teamId: string,
  body: { name: string; budget?: TeamBudget },
): Promise<Team> {
  return request<Team>(`/api/v1/admin/teams/${encodeURIComponent(teamId)}`, {
    method: "PUT",
    body,
  });
}

export function deleteTeam(teamId: string): Promise<unknown> {
  return request<unknown>(
    `/api/v1/admin/teams/${encodeURIComponent(teamId)}`,
    { method: "DELETE" },
  );
}

export async function listTeamMembers(
  teamId: string,
): Promise<TeamMembership[]> {
  const body = await request<{ members?: TeamMembership[] }>(
    `/api/v1/admin/teams/${encodeURIComponent(teamId)}/members`,
  );
  return body?.members ?? [];
}

export function addTeamMember(
  teamId: string,
  userId: string,
): Promise<TeamMembership> {
  return request<TeamMembership>(
    `/api/v1/admin/teams/${encodeURIComponent(teamId)}/members/${encodeURIComponent(userId)}`,
    { method: "PUT" },
  );
}

export function removeTeamMember(
  teamId: string,
  userId: string,
): Promise<unknown> {
  return request<unknown>(
    `/api/v1/admin/teams/${encodeURIComponent(teamId)}/members/${encodeURIComponent(userId)}`,
    { method: "DELETE" },
  );
}

export async function listMemberGrants(
  userId: string,
): Promise<AccountGrant[]> {
  const body = await request<{ grants?: AccountGrant[] }>(
    `/api/v1/admin/users/${encodeURIComponent(userId)}/grants`,
  );
  return body?.grants ?? [];
}

export async function listTeamGrants(teamId: string): Promise<AccountGrant[]> {
  const body = await request<{ grants?: AccountGrant[] }>(
    `/api/v1/admin/teams/${encodeURIComponent(teamId)}/grants`,
  );
  return body?.grants ?? [];
}

// listReachableAccounts folds a member's direct grants with the grants of
// every team the member is on — the same answer the gateway resolves when
// that member's session asks for an account.
export async function listReachableAccounts(
  userId: string,
): Promise<string[]> {
  const body = await request<{ accounts?: string[] }>(
    `/api/v1/admin/users/${encodeURIComponent(userId)}/accounts`,
  );
  return body?.accounts ?? [];
}

export function createAccountGrant(body: {
  account_id: string;
  user_id?: string;
  team_id?: string;
}): Promise<AccountGrant> {
  return request<AccountGrant>("/api/v1/admin/account-grants", {
    method: "POST",
    body,
  });
}

export function deleteAccountGrant(grant: {
  account_id: string;
  user_id?: string;
  team_id?: string;
}): Promise<unknown> {
  const query = new URLSearchParams({ account_id: grant.account_id });
  if (grant.user_id) query.set("user_id", grant.user_id);
  if (grant.team_id) query.set("team_id", grant.team_id);
  return request<unknown>(`/api/v1/admin/account-grants?${query.toString()}`, {
    method: "DELETE",
  });
}

// --- Stored files ---

// StoredFile is one file this gateway holds for the calling account. The shape
// is the file object the /v1/files routes serve.
//
// `bytes` is the stored length. `created_at` and `expires_at` are Unix seconds.
// `status` is the write state: a file reads `processing` while its bytes are
// still landing, and `processed` once they have.
export type StoredFile = {
  id: string;
  object?: string;
  bytes: number;
  created_at: number;
  filename: string;
  purpose: string;
  expires_at?: number;
  status: string;
};

// FileList is one page of stored files and whether more follow it.
//
// `hasMore` matters to the stored total below. A total summed from a partial
// page is a floor, not the amount the account holds, and the view says which
// of the two it is showing.
export type FileList = {
  files: StoredFile[];
  hasMore: boolean;
};

// FILE_PAGE_LIMIT is the largest page the routes serve.
const FILE_PAGE_LIMIT = 1000;

// listFiles reads the files the credential's own account stores. It takes no
// account argument: the routes scope every answer to the caller, so one account
// never learns what another holds.
export async function listFiles(): Promise<FileList> {
  const body = await request<{ data?: StoredFile[]; has_more?: boolean }>(
    `/v1/files?limit=${FILE_PAGE_LIMIT}`,
  );
  return { files: body?.data ?? [], hasMore: body?.has_more === true };
}

// uploadFile sends one file as multipart form data.
//
// It does not go through `request`, which sets a JSON content type and encodes
// its body. A multipart body carries its own boundary, and the browser writes
// that header itself from the FormData.
export async function uploadFile(
  file: File,
  purpose: string,
): Promise<StoredFile> {
  const form = new FormData();
  form.append("file", file);
  form.append("purpose", purpose);
  const held = credential();
  const response = await fetch("/v1/files", {
    method: "POST",
    headers: authorization(held),
    body: form,
  });
  if (!response.ok) {
    const error = await parseError(response);
    if (held.kind !== "none" && error.unauthorized) recordCredentialOutcome(true);
    throw error;
  }
  if (held.kind !== "none") recordCredentialOutcome(false);
  return (await response.json()) as StoredFile;
}

export function deleteFile(fileID: string): Promise<unknown> {
  return request<unknown>(`/v1/files/${encodeURIComponent(fileID)}`, {
    method: "DELETE",
  });
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

// listPresetHistory answers the stored revisions of one preset, newest
// first. Pin one from a request with @preset/name@N.
export async function listPresetHistory(name: string): Promise<Preset[]> {
  const body = await request<{ data?: Preset[] }>(
    `/api/v1/presets/${encodeURIComponent(name)}/history`,
  );
  return body?.data ?? [];
}

// rollbackPreset saves a new head revision copying toRevision; revision
// names the head the caller read (409 on mismatch).
export function rollbackPreset(
  name: string,
  toRevision: number,
  revision: number,
): Promise<Preset> {
  return request<Preset>(
    `/api/v1/presets/${encodeURIComponent(name)}/rollback`,
    { method: "POST", body: { to_revision: toRevision, revision } },
  );
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
  // media holds what the turn produced beside its text. It is empty for a
  // text answer, which is most of them.
  media: GeneratedMedia[];
};

// decodeBase64 returns the raw bytes of one base64 string, or an empty
// string when the payload is unreadable. A malformed audio chunk drops that
// chunk rather than failing the whole answer, because the text of the same
// turn is already on screen.
function decodeBase64(payload: string): string {
  try {
    return atob(payload);
  } catch {
    return "";
  }
}

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
    media: [],
  };

  // A picture arrives whole in one delta. A spoken answer arrives in
  // pieces, and each piece is base64 on its own, so the bytes are joined
  // before they are re-encoded rather than the base64 strings.
  let audioBytes = "";
  let audioFormat = "";
  let audioTranscript = "";

  type StreamEvent = {
    error?: { code?: number; message?: string };
    usage?: ChatUsage;
    model?: string;
    choices?: {
      delta?: {
        content?: string;
        reasoning?: string;
        reasoning_content?: string;
        images?: { image_url?: { url?: string } }[];
        audio?: { data?: string; format?: string; transcript?: string };
      };
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
      for (const image of delta.images ?? []) {
        const url = image.image_url?.url;
        if (url) meta.media.push({ kind: "image", url });
      }
      if (delta.audio?.data) audioBytes += decodeBase64(delta.audio.data);
      if (delta.audio?.format) audioFormat = delta.audio.format;
      if (delta.audio?.transcript) audioTranscript += delta.audio.transcript;
      if (delta.content) onDelta(delta.content);
    }
  }
  if (audioBytes !== "") {
    meta.media.push({
      kind: "audio",
      url: `data:audio/${audioFormat || "mp3"};base64,${btoa(audioBytes)}`,
      transcript: audioTranscript || undefined,
    });
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

// --- Video jobs ---

// A video job outlives the request that started it. The gateway answers with a
// Starport identifier and never a provider link, so a reader polls this
// gateway and fetches the bytes from it.

export const VIDEO_OPERATION = "videos-generations";

// RECOGNITION_OPERATION is the catalog's own name for reading the text off a
// document. A model that serves it is a document reader, and a chat request
// naming one is a routing refusal waiting to happen.
export const RECOGNITION_OPERATION = "documents-recognition";

// RERANK_OPERATION is the catalog's own name for scoring a document list
// against one query. A rerank model answers no chat turn, so a chat request
// naming one is a routing refusal waiting to happen.
export const RERANK_OPERATION = "rerank";

export type VideoJob = {
  id: string;
  object?: string;
  model: string;
  provider?: string;
  status: string;
  created_at: number;
  completed_at?: number;
  // expires_at is the end of the retention window on the stored bytes. It is
  // absent whenever this gateway holds none, which covers every unfinished job
  // and every finished one whose bytes already went.
  expires_at?: number;
  error?: { message?: string };
};

// TERMINAL_JOB_STATES are the states a job never leaves. A poll of one of these
// reads the same answer forever, so a page holding only these stops polling.
export const TERMINAL_JOB_STATES = ["completed", "failed", "cancelled"];

// A listing has no cursor, so the cap is what the gateway serves rather than a
// page the reader can walk. Reaching it is reported as a floor the way a capped
// file listing is.
const VIDEO_PAGE_LIMIT = 100;

export type VideoJobList = { jobs: VideoJob[]; capped: boolean };

export async function listJobs(): Promise<VideoJobList> {
  const body = await request<{ data?: VideoJob[] }>(
    `/v1/videos?limit=${VIDEO_PAGE_LIMIT}`,
  );
  const jobs = body?.data ?? [];
  return { jobs, capped: jobs.length >= VIDEO_PAGE_LIMIT };
}

export function submitJob(model: string, prompt: string): Promise<VideoJob> {
  return request<VideoJob>("/v1/videos", { method: "POST", body: { model, prompt } });
}

export function cancelJob(jobID: string): Promise<VideoJob> {
  return request<VideoJob>(`/v1/videos/${encodeURIComponent(jobID)}/cancel`, {
    method: "POST",
  });
}

// fetchJobAsset reads the stored bytes of one finished job.
//
// A player element cannot fetch these on its own. It sends no Authorization
// header, and a reader holding a pasted gateway key rather than a console
// session would get a refusal from a `src` pointing at the route. So the bytes
// come through the same credential every other call uses, and the caller hands
// the player an object URL over what this returns.
//
// This is why nothing calls it for a whole listing: a page that fetched every
// asset would hold every video in memory. A reader asks for one at a time.
export async function fetchJobAsset(jobID: string): Promise<Blob> {
  const held = credential();
  const response = await fetch(
    `/v1/videos/${encodeURIComponent(jobID)}/content`,
    { headers: authorization(held) },
  );
  if (!response.ok) {
    const error = await parseError(response);
    if (held.kind !== "none" && error.unauthorized) recordCredentialOutcome(true);
    throw error;
  }
  if (held.kind !== "none") recordCredentialOutcome(false);
  return response.blob();
}

// listActivity reads the authenticated key's own request log; key_id is
// ignored there — only the admin listing can widen the scope.
export function listActivity(filters: ActivityFilters): Promise<ActivityPage> {
  return request<ActivityPage>(`/api/v1/activity${activityQuery(filters)}`);
}

export function listAdminActivity(filters: ActivityFilters): Promise<ActivityPage> {
  return request<ActivityPage>(`/api/v1/admin/activity${activityQuery(filters)}`);
}

// --- Audit log ---

// AuditRecord is one admin mutation on the durable trail: who did what to
// which subject, and whether the store accepted it. It never holds a
// credential value.
export type AuditRecord = {
  id?: number;
  time?: string;
  actor?: string;
  action?: string;
  subject?: string;
  outcome?: string;
};

export type AuditPage = {
  data?: AuditRecord[];
  next_cursor?: string;
};

export type AuditFilters = {
  action?: string;
  actor?: string;
  since?: string;
  limit?: number;
  cursor?: string;
};

export function listAuditLog(filters: AuditFilters): Promise<AuditPage> {
  const params = new URLSearchParams();
  if (filters.action) params.set("action", filters.action);
  if (filters.actor) params.set("actor", filters.actor);
  if (filters.since) params.set("since", filters.since);
  if (filters.limit) params.set("limit", String(filters.limit));
  if (filters.cursor) params.set("cursor", filters.cursor);
  const query = params.toString();
  return request<AuditPage>(`/api/v1/admin/audit${query ? `?${query}` : ""}`);
}
