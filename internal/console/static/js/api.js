// Starport Console — API client.
// All requests go to the gateway that served this page (same origin), so the
// console works on localhost, 127.0.0.1, or any host the gateway binds.

const KEY_STORAGE = "starport.apiKey";

export function getApiKey() {
    return localStorage.getItem(KEY_STORAGE) || "";
}

export function setApiKey(key) {
    if (key) {
        localStorage.setItem(KEY_STORAGE, key);
    } else {
        localStorage.removeItem(KEY_STORAGE);
    }
    notifyKeyChange();
}

const keyListeners = new Set();
export function onKeyChange(fn) {
    keyListeners.add(fn);
    return () => keyListeners.delete(fn);
}
function notifyKeyChange() {
    for (const fn of keyListeners) fn();
}

// ApiError carries the HTTP status and the gateway's error message.
export class ApiError extends Error {
    constructor(status, message, body) {
        super(message);
        this.status = status;
        this.body = body;
    }
    get unauthorized() { return this.status === 401; }
    get forbidden() { return this.status === 403; }
}

async function parseError(response) {
    let message = `${response.status} ${response.statusText}`;
    let body = null;
    try {
        body = await response.json();
        message = body?.error?.message || body?.message || message;
    } catch { /* non-JSON error body */ }
    return new ApiError(response.status, message, body);
}

export async function request(path, { method = "GET", body, signal, headers } = {}) {
    const key = getApiKey();
    const init = {
        method,
        signal,
        headers: {
            ...(key ? { Authorization: `Bearer ${key}` } : {}),
            ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
            ...headers,
        },
    };
    if (body !== undefined) init.body = JSON.stringify(body);
    const response = await fetch(path, init);
    if (!response.ok) throw await parseError(response);
    if (response.status === 204) return null;
    return response.json();
}

// --- Catalog ---

let modelsCache = null;
let modelsPromise = null;

export async function listModels({ fresh = false } = {}) {
    if (modelsCache && !fresh) return modelsCache;
    if (!modelsPromise || fresh) {
        modelsPromise = request("/api/v1/models").then((data) => {
            modelsCache = data?.data || [];
            return modelsCache;
        }).finally(() => { modelsPromise = null; });
    }
    return modelsPromise;
}

export function invalidateModels() { modelsCache = null; }

export function getModelEndpoints(id) {
    return request(`/api/v1/models/${encodeURIComponent(id)}/endpoints`);
}

export function listProviders() {
    return request("/api/v1/providers");
}

// --- Admin ---

export function systemInfo() { return request("/api/v1/admin/info"); }
export function systemMetrics() { return request("/api/v1/admin/metrics"); }
export function providerStatus() { return request("/api/v1/admin/providers"); }
export function refreshProviders() { return request("/api/v1/admin/providers/refresh", { method: "POST" }); }

export function listKeys() { return request("/api/v1/admin/keys"); }
export function createKey(body) { return request("/api/v1/admin/keys", { method: "POST", body }); }
export function updateKey(id, body) { return request(`/api/v1/admin/keys/${encodeURIComponent(id)}`, { method: "PUT", body }); }
export function deleteKey(id) { return request(`/api/v1/admin/keys/${encodeURIComponent(id)}`, { method: "DELETE" }); }

// --- Activity ---

function activityQuery(filters = {}) {
    const params = new URLSearchParams();
    for (const name of ["key_id", "model", "provider", "status", "since", "until", "limit", "cursor"]) {
        if (filters[name] !== undefined && filters[name] !== null && filters[name] !== "") {
            params.set(name, filters[name]);
        }
    }
    const query = params.toString();
    return query ? `?${query}` : "";
}

export function listActivity(filters = {}) {
    return request(`/api/v1/activity${activityQuery(filters)}`);
}

export function listAdminActivity(filters = {}) {
    return request(`/api/v1/admin/activity${activityQuery(filters)}`);
}

// --- Provider keys (BYOK) ---

export function listProviderKeys(keyID) {
    return request(`/api/v1/keys/${encodeURIComponent(keyID)}/provider-keys/`);
}
export function createProviderKey(keyID, body) {
    return request(`/api/v1/keys/${encodeURIComponent(keyID)}/provider-keys/`, { method: "POST", body });
}
export function deleteProviderKey(keyID, provider) {
    return request(`/api/v1/keys/${encodeURIComponent(keyID)}/provider-keys/${encodeURIComponent(provider)}`, { method: "DELETE" });
}
export function validateProviderKey(keyID, provider) {
    return request(`/api/v1/keys/${encodeURIComponent(keyID)}/provider-keys/${encodeURIComponent(provider)}/validate`, { method: "POST" });
}
export function providerKeyUsage(keyID) {
    return request(`/api/v1/keys/${encodeURIComponent(keyID)}/usage/provider-keys`);
}

// --- Health (no auth) ---

export async function healthReady() {
    const response = await fetch("/health/ready");
    return response.ok;
}

// --- Chat completions (SSE streaming) ---

// streamChat posts to /api/v1/chat/completions with stream=true and invokes
// callbacks per delta. Returns response headers plus the final usage payload.
export async function streamChat(body, { signal, onDelta, onReasoning }) {
    const key = getApiKey();
    const response = await fetch("/api/v1/chat/completions", {
        method: "POST",
        signal,
        headers: {
            Authorization: `Bearer ${key}`,
            "Content-Type": "application/json",
        },
        body: JSON.stringify({ ...body, stream: true }),
    });
    if (!response.ok) throw await parseError(response);

    const meta = {
        cache: response.headers.get("X-Cache") || "",
        cacheAge: response.headers.get("X-Cache-Age") || "",
        usage: null,
        model: "",
    };
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop();
        for (const line of lines) {
            const trimmed = line.trim();
            if (!trimmed.startsWith("data:")) continue;
            const payload = trimmed.slice(5).trim();
            if (payload === "[DONE]") continue;
            let event;
            try { event = JSON.parse(payload); } catch { continue; }
            if (event.error) {
                throw new ApiError(event.error.code || 500, event.error.message || "stream error", event);
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

// completeChat is the non-streaming variant.
export async function completeChat(body, { signal } = {}) {
    return request("/api/v1/chat/completions", { method: "POST", body, signal });
}
