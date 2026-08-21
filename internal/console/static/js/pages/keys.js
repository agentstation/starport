// Keys — gateway API key management (admin) and per-key BYOK provider
// credentials. Secrets show once at creation and are never re-displayed.

import {
    getApiKey, listKeys, getKey, createKey, updateKey, deleteKey,
    listProviderKeys, createProviderKey, deleteProviderKey, validateProviderKey,
    listProviders,
} from "../api.js";
import { el, icon, toast, modal, confirmDialog, copyButton, formatRelativeTime } from "../ui.js";
import { navigate } from "../router.js";

export const title = "Keys";

export async function render(container) {
    const page = el("div", { class: "page" });
    container.append(page);

    if (!getApiKey()) {
        page.append(connectPrompt());
        return;
    }

    const newBtn = el("button", { class: "btn btn-primary btn-sm", type: "button" }, icon("plus"), "new key");
    const listHost = el("div", {});
    page.append(
        el("div", { class: "page-head page-head-row" },
            el("div", {},
                el("h1", {}, "API Keys"),
                el("p", { class: "sub" }, "Gateway keys for your apps, plus per-key provider credentials (BYOK)."),
            ),
            newBtn,
        ),
        listHost,
    );
    listHost.append(el("div", { class: "loading-row" }, el("span", { class: "spinner" }), "Loading keys…"));

    let disposed = false;
    newBtn.addEventListener("click", () => openCreateModal(load));
    await load();

    async function load() {
        try {
            const body = await listKeys();
            if (disposed) return;
            draw(body?.data ?? body?.keys ?? []);
        } catch (error) {
            if (disposed) return;
            if (error.unauthorized || error.forbidden) {
                newBtn.hidden = true;
                listHost.replaceChildren(el("div", { class: "empty" }, icon("keys"),
                    el("p", {}, "Key management needs an admin-scoped key. Set one in Settings.")));
            } else {
                listHost.replaceChildren(el("div", { class: "empty" }, icon("alert"),
                    el("p", {}, `Failed to load keys: ${error.message}`)));
            }
        }
    }

    function draw(keys) {
        if (!keys.length) {
            listHost.replaceChildren(el("div", { class: "empty" }, icon("keys"),
                el("p", {}, "No API keys yet. Create one to hand to your apps.")));
            return;
        }
        const tbody = el("tbody", {});
        for (const key of keys) tbody.append(keyRow(key));
        listHost.replaceChildren(
            el("div", { class: "table-wrap" },
                el("table", { class: "table" },
                    el("thead", {}, el("tr", {},
                        el("th", {}, "name"),
                        el("th", {}, "key"),
                        el("th", {}, "scopes"),
                        el("th", {}, "limits"),
                        el("th", {}, "status"),
                        el("th", {}, "created"),
                        el("th", { class: "num" }, ""),
                    )),
                    tbody,
                ),
            ),
        );
    }

    function keyRow(key) {
        const id = key.id || key.key_id;
        const scopes = key.scopes || [];
        const active = key.active !== false && !key.disabled;
        const expired = key.expires_at && new Date(key.expires_at) < new Date();
        const byokBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button", title: "Provider keys (BYOK)" }, icon("providers"), "byok");
        const editBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, "edit");
        const toggleBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, active ? "disable" : "enable");
        const delBtn = el("button", { class: "icon-btn danger", type: "button", "aria-label": `Delete ${key.name || id}` }, icon("trash"));

        byokBtn.addEventListener("click", () => openByokModal(key));
        editBtn.addEventListener("click", () => openEditModal(key, load));
        toggleBtn.addEventListener("click", async () => {
            toggleBtn.disabled = true;
            try {
                await updateKey(id, { active: !active });
                toast(active ? "Key disabled" : "Key enabled", "ok");
                await load();
            } catch (error) {
                toast(`Update failed: ${error.message}`, "err");
                toggleBtn.disabled = false;
            }
        });
        delBtn.addEventListener("click", async () => {
            const ok = await confirmDialog({
                title: "Delete key",
                message: `Delete "${key.name || id}"? Apps using it will lose access immediately.`,
                confirmLabel: "Delete",
                danger: true,
            });
            if (!ok) return;
            try {
                await deleteKey(id);
                toast("Key deleted", "ok");
                await load();
            } catch (error) {
                toast(`Delete failed: ${error.message}`, "err");
            }
        });

        const statusBadge = !active
            ? el("span", { class: "badge badge-err" }, "disabled")
            : expired
                ? el("span", { class: "badge badge-err" }, "expired")
                : el("span", { class: "badge badge-ok" }, "active");

        return el("tr", {},
            el("td", {}, el("div", { class: "cell-name" }, key.name || "unnamed")),
            el("td", {}, el("code", { class: "mono" }, maskKey(key))),
            el("td", {}, scopes.length
                ? scopes.map((s) => el("span", { class: "badge" }, s))
                : el("span", { class: "muted" }, "—")),
            el("td", {}, limitsCell(key)),
            el("td", {}, statusBadge),
            el("td", {}, el("span", { class: "muted" }, key.created_at ? formatRelativeTime(key.created_at) : "—")),
            el("td", { class: "num row-actions" }, byokBtn, editBtn, toggleBtn, delBtn),
        );
    }
}

// limitsCell summarizes a key's restrictions and, for keys with budgets,
// loads the current-window consumption to show the remaining allowance.
function limitsCell(key) {
    const id = key.id || key.key_id;
    const limits = key.limits || {};
    const badges = [];
    if (key.allowed_models?.length) {
        badges.push(el("span", { class: "badge", title: key.allowed_models.join(", ") },
            `${key.allowed_models.length} model${key.allowed_models.length === 1 ? "" : "s"}`));
    }
    if (key.expires_at) {
        badges.push(el("span", { class: "badge", title: new Date(key.expires_at).toISOString() },
            `expires ${formatRelativeTime(key.expires_at)}`));
    }
    if (limits.requests) {
        badges.push(el("span", { class: "badge" },
            `${limits.requests.limit} req / ${formatWindow(limits.requests.window_seconds)}`));
    }
    if (limits.spend) {
        badges.push(el("span", { class: "badge" },
            `${formatNanoUSD(limits.spend.limit)} / ${limits.spend.interval}`));
    }
    if (limits.tokens) {
        badges.push(el("span", { class: "badge" },
            `${formatCount(limits.tokens.limit)} tok / ${limits.tokens.interval}`));
    }
    if (!badges.length) return el("span", { class: "muted" }, "—");

    const cell = el("div", { class: "cell-limits" }, badges);
    if (limits.spend || limits.tokens) {
        const usageLine = el("div", { class: "muted budget-usage" }, "…");
        cell.append(usageLine);
        getKey(id).then((detail) => {
            usageLine.replaceChildren(...budgetUsageParts(detail?.usage?.budgets));
        }).catch(() => {
            usageLine.replaceChildren(el("span", { class: "muted" }, "usage unavailable"));
        });
    }
    return cell;
}

// budgetUsageParts renders each budget's remaining allowance, with an
// exhausted badge once the current fixed window is spent.
function budgetUsageParts(budgets) {
    if (!budgets) return [el("span", { class: "muted" }, "usage unavailable")];
    const parts = [];
    if (budgets.spend) {
        parts.push(budgets.spend.remaining === 0
            ? el("span", { class: "badge badge-err" }, "spend exhausted")
            : el("span", {}, `${formatNanoUSD(budgets.spend.remaining)} left`));
    }
    if (budgets.tokens) {
        parts.push(budgets.tokens.remaining === 0
            ? el("span", { class: "badge badge-err" }, "tokens exhausted")
            : el("span", {}, `${formatCount(budgets.tokens.remaining)} tok left`));
    }
    return parts.length ? parts : [el("span", { class: "muted" }, "—")];
}

function formatWindow(seconds) {
    if (seconds === 60) return "min";
    if (seconds === 3600) return "hr";
    if (seconds === 86400) return "day";
    return `${seconds}s`;
}

// formatNanoUSD renders an integer nano-USD amount as dollars.
function formatNanoUSD(nano) {
    const usd = nano / 1e9;
    return usd >= 100 ? `$${Math.round(usd)}` : `$${usd.toFixed(2)}`;
}

function formatCount(n) {
    if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
    if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
    if (n >= 1e3) return `${(n / 1e3).toFixed(1)}k`;
    return String(n);
}

function maskKey(key) {
    const prefix = key.key_prefix || key.prefix;
    if (prefix) return `${prefix}…`;
    const id = key.id || key.key_id || "";
    return `${String(id).slice(0, 12)}…`;
}

// keyLimitsForm builds the shared restriction fields (allowed models,
// expiry, request limit, spend and token budgets), optionally prefilled
// from an existing key. read() returns {allowedModels, expiresAt, limits}
// or throws an Error naming the invalid field.
function keyLimitsForm(key) {
    const limits = key?.limits || {};
    const modelsInput = el("input", {
        class: "input mono", type: "text", autocomplete: "off",
        placeholder: "empty = all models (comma-separated IDs)",
        value: (key?.allowed_models || []).join(", "),
    });
    const expiryInput = el("input", { class: "input", type: "date" });
    if (key?.expires_at) expiryInput.value = key.expires_at.slice(0, 10);
    const reqLimitInput = el("input", { class: "input", type: "number", min: "1", placeholder: "unlimited", value: limits.requests?.limit ?? "" });
    const reqWindowSelect = el("select", { class: "select", "aria-label": "Request window" },
        el("option", { value: "60" }, "per minute"),
        el("option", { value: "3600" }, "per hour"),
        el("option", { value: "86400" }, "per day"),
    );
    const reqWindow = limits.requests?.window_seconds;
    if (reqWindow && ![60, 3600, 86400].includes(reqWindow)) {
        reqWindowSelect.append(el("option", { value: String(reqWindow) }, `per ${reqWindow}s`));
    }
    if (reqWindow) reqWindowSelect.value = String(reqWindow);

    const intervalSelect = (label, value) => {
        const select = el("select", { class: "select", "aria-label": label },
            el("option", { value: "day" }, "per day"),
            el("option", { value: "week" }, "per week"),
            el("option", { value: "month" }, "per month"),
        );
        if (value) select.value = value;
        return select;
    };
    const spendInput = el("input", {
        class: "input", type: "number", min: "0", step: "0.01", placeholder: "unlimited",
        value: limits.spend ? String(limits.spend.limit / 1e9) : "",
    });
    const spendInterval = intervalSelect("Spend budget interval", limits.spend?.interval);
    const tokensInput = el("input", { class: "input", type: "number", min: "1", placeholder: "unlimited", value: limits.tokens?.limit ?? "" });
    const tokensInterval = intervalSelect("Token budget interval", limits.tokens?.interval);

    const body = el("div", {},
        el("div", { class: "field" }, el("label", {}, "Allowed models"), modelsInput),
        el("div", { class: "field" }, el("label", {}, "Expires"), expiryInput,
            key?.expires_at ? el("span", { class: "muted" }, "Expiry cannot be removed once set.") : null),
        el("div", { class: "field" }, el("label", {}, "Request limit"),
            el("div", { class: "field-row" }, reqLimitInput, reqWindowSelect)),
        el("div", { class: "field" }, el("label", {}, "Spend budget (USD)"),
            el("div", { class: "field-row" }, spendInput, spendInterval)),
        el("div", { class: "field" }, el("label", {}, "Token budget"),
            el("div", { class: "field-row" }, tokensInput, tokensInterval)),
    );

    function read() {
        const allowedModels = modelsInput.value.split(",").map((s) => s.trim()).filter(Boolean);
        let expiresAt = null;
        if (expiryInput.value) {
            // The chosen day is inclusive: the key expires at the end of it.
            expiresAt = `${expiryInput.value}T23:59:59Z`;
        }
        const out = {};
        if (reqLimitInput.value) {
            const limit = Number(reqLimitInput.value);
            if (!Number.isInteger(limit) || limit <= 0) throw new Error("Request limit must be a positive whole number");
            out.requests = { limit, window_seconds: Number(reqWindowSelect.value) };
        }
        if (spendInput.value) {
            const usd = Number(spendInput.value);
            if (!Number.isFinite(usd) || usd <= 0) throw new Error("Spend budget must be a positive amount");
            out.spend = { limit: Math.round(usd * 1e9), interval: spendInterval.value };
        }
        if (tokensInput.value) {
            const tokens = Number(tokensInput.value);
            if (!Number.isInteger(tokens) || tokens <= 0) throw new Error("Token budget must be a positive whole number");
            out.tokens = { limit: tokens, interval: tokensInterval.value };
        }
        return { allowedModels, expiresAt, limits: Object.keys(out).length ? out : null };
    }

    return { body, read };
}

function openCreateModal(reload) {
    const nameInput = el("input", { class: "input", id: "key-name", type: "text", placeholder: "e.g. local-dev", autocomplete: "off" });
    const adminCheck = el("input", { type: "checkbox", id: "scope-admin" });
    const form = keyLimitsForm(null);
    const createBtn = el("button", { class: "btn btn-primary", type: "button" }, "Create key");
    const cancelBtn = el("button", { class: "btn btn-ghost", type: "button" }, "Cancel");
    const close = modal({
        title: "New API key",
        body: el("div", {},
            el("div", { class: "field" }, el("label", { for: "key-name" }, "Name"), nameInput),
            el("label", { class: "check" }, adminCheck,
                el("span", {}, "Admin scope — can manage keys, providers, and the catalog")),
            form.body,
        ),
        foot: [cancelBtn, createBtn],
    });
    cancelBtn.addEventListener("click", close);
    createBtn.addEventListener("click", async () => {
        const name = nameInput.value.trim();
        if (!name) { nameInput.focus(); return; }
        let fields;
        try {
            fields = form.read();
        } catch (error) {
            toast(error.message, "err");
            return;
        }
        createBtn.disabled = true;
        try {
            // A key needs at least one scope. Non-admin keys get the
            // inference set: chat, embeddings, model listing, activity.
            const body = {
                name,
                scopes: adminCheck.checked
                    ? ["admin"]
                    : ["chat:write", "embeddings:write", "models:read", "activity:read"],
            };
            if (fields.allowedModels.length) body.allowed_models = fields.allowedModels;
            if (fields.expiresAt) body.expires_at = fields.expiresAt;
            if (fields.limits) body.limits = fields.limits;
            const created = await createKey(body);
            close();
            showSecretOnce(created);
            reload();
        } catch (error) {
            toast(`Create failed: ${error.message}`, "err");
            createBtn.disabled = false;
        }
    });
}

function openEditModal(key, reload) {
    const id = key.id || key.key_id;
    const nameInput = el("input", { class: "input", type: "text", autocomplete: "off", value: key.name || "" });
    const form = keyLimitsForm(key);
    const saveBtn = el("button", { class: "btn btn-primary", type: "button" }, "Save");
    const cancelBtn = el("button", { class: "btn btn-ghost", type: "button" }, "Cancel");
    const close = modal({
        title: `Edit key · ${key.name || id}`,
        body: el("div", {},
            el("div", { class: "field" }, el("label", {}, "Name"), nameInput),
            form.body,
        ),
        foot: [cancelBtn, saveBtn],
    });
    cancelBtn.addEventListener("click", close);
    saveBtn.addEventListener("click", async () => {
        const name = nameInput.value.trim();
        if (!name) { nameInput.focus(); return; }
        let fields;
        try {
            fields = form.read();
        } catch (error) {
            toast(error.message, "err");
            return;
        }
        saveBtn.disabled = true;
        try {
            // An empty model list and an empty limits object clear the
            // restriction; expiry is only sent when the field has a value.
            const body = {
                name,
                allowed_models: fields.allowedModels,
                limits: fields.limits ?? {},
            };
            if (fields.expiresAt) body.expires_at = fields.expiresAt;
            await updateKey(id, body);
            close();
            toast("Key updated", "ok");
            reload();
        } catch (error) {
            toast(`Update failed: ${error.message}`, "err");
            saveBtn.disabled = false;
        }
    });
}

// showSecretOnce displays the freshly minted secret — the only time it exists
// in plaintext.
function showSecretOnce(created) {
    // The create response nests the record under "key", with the secret at
    // key.key. Accept a plain string too, for older response shapes.
    const record = created?.key;
    const secret = (typeof record === "string" ? record : record?.key)
        || created?.secret || created?.api_key || "";
    const doneBtn = el("button", { class: "btn btn-primary", type: "button" }, "Done");
    const close = modal({
        title: "Copy your key now",
        body: el("div", {},
            el("p", {}, "This key is shown once. Store it somewhere safe — the gateway keeps only a hash."),
            el("div", { class: "secret-reveal" },
                el("code", { class: "mono" }, secret || "(secret unavailable)"),
                secret ? copyButton(secret) : null,
            ),
        ),
        foot: [doneBtn],
    });
    doneBtn.addEventListener("click", close);
}

// --- BYOK provider keys ---

async function openByokModal(key) {
    const keyID = key.id || key.key_id;
    const listBody = el("div", {}, el("div", { class: "loading-row" }, el("span", { class: "spinner" }), "Loading…"));
    const providerSelect = el("select", { class: "select", "aria-label": "Provider" }, el("option", { value: "" }, "select provider…"));
    const fieldsHost = el("div", { class: "byok-fields" });
    const addBtn = el("button", { class: "btn btn-primary btn-sm", type: "button", disabled: "" }, icon("plus"), "attach");
    const closeBtn = el("button", { class: "btn btn-ghost", type: "button" }, "Close");
    const providersByID = new Map();

    const close = modal({
        title: `Provider keys · ${key.name || keyID}`,
        wide: true,
        body: el("div", {},
            el("p", { class: "muted" },
                "Requests authenticated with this gateway key use these provider credentials instead of the operator's."),
            listBody,
            el("div", { class: "byok-add" },
                providerSelect,
                fieldsHost,
                addBtn,
            ),
        ),
        foot: [closeBtn],
    });
    closeBtn.addEventListener("click", close);

    listProviders().then((body) => {
        const providers = body?.data?.providers ?? body?.providers ?? [];
        for (const p of [...providers].sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id))) {
            providersByID.set(p.id, p);
            providerSelect.append(el("option", { value: p.id }, p.name || p.id));
        }
    }).catch(() => {});

    // The credential form is catalog-driven: each provider declares its
    // inference credential fields, so the console never assumes an "api key".
    providerSelect.addEventListener("change", () => {
        fieldsHost.replaceChildren();
        const fields = providersByID.get(providerSelect.value)?.credential_fields || [];
        for (const field of fields) {
            fieldsHost.append(el("input", {
                class: "input mono",
                type: field.kind === "secret" ? "password" : "text",
                placeholder: field.default ? `${field.id} (${field.default})` : field.id,
                autocomplete: "off",
                "aria-label": field.id,
                "data-field-id": field.id,
                "data-field-kind": field.kind,
            }));
        }
        if (!fields.length && providerSelect.value) {
            fieldsHost.append(el("span", { class: "muted" }, "no credential contract declared"));
        }
        addBtn.disabled = !fields.length;
    });

    addBtn.addEventListener("click", async () => {
        const provider = providerSelect.value;
        if (!provider) return;
        const credentials = {};
        const config = {};
        for (const input of fieldsHost.querySelectorAll("input[data-field-id]")) {
            const value = input.value.trim();
            if (!value) continue;
            if (input.dataset.fieldKind === "secret") credentials[input.dataset.fieldId] = value;
            else config[input.dataset.fieldId] = value;
        }
        if (!Object.keys(credentials).length) return;
        addBtn.disabled = true;
        try {
            const body = { provider, credentials };
            if (Object.keys(config).length) body.config = config;
            await createProviderKey(keyID, body);
            for (const input of fieldsHost.querySelectorAll("input")) input.value = "";
            toast(`Attached ${provider} key`, "ok");
            await refresh();
        } catch (error) {
            toast(`Attach failed: ${error.message}`, "err");
        } finally {
            addBtn.disabled = false;
        }
    });

    await refresh();

    async function refresh() {
        try {
            const body = await listProviderKeys(keyID);
            const rows = body?.data ?? body?.provider_keys ?? [];
            if (!rows.length) {
                listBody.replaceChildren(el("p", { class: "muted" }, "No provider keys attached."));
                return;
            }
            const list = el("div", { class: "byok-list" });
            for (const row of rows) list.append(byokRow(row));
            listBody.replaceChildren(list);
        } catch (error) {
            listBody.replaceChildren(el("p", { class: "muted" }, `Failed to load: ${error.message}`));
        }
    }

    function byokRow(row) {
        const provider = row.provider || row.provider_id;
        const validateBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, "validate");
        const removeBtn = el("button", { class: "icon-btn danger", type: "button", "aria-label": `Remove ${provider} key` }, icon("trash"));
        validateBtn.addEventListener("click", async () => {
            validateBtn.disabled = true;
            try {
                const result = await validateProviderKey(keyID, provider);
                const valid = result?.valid ?? result?.data?.valid;
                toast(valid === false ? `${provider} key is invalid` : `${provider} key is valid`, valid === false ? "err" : "ok");
            } catch (error) {
                toast(`Validation failed: ${error.message}`, "err");
            } finally {
                validateBtn.disabled = false;
            }
        });
        removeBtn.addEventListener("click", async () => {
            const ok = await confirmDialog({
                title: "Remove provider key",
                message: `Remove the ${provider} credential from this key?`,
                confirmLabel: "Remove",
                danger: true,
            });
            if (!ok) return;
            try {
                await deleteProviderKey(keyID, provider);
                toast("Provider key removed", "ok");
                await refresh();
            } catch (error) {
                toast(`Remove failed: ${error.message}`, "err");
            }
        });
        return el("div", { class: "byok-row" },
            el("span", { class: "mono" }, provider),
            row.created_at ? el("span", { class: "muted" }, formatRelativeTime(row.created_at)) : el("span", {}),
            el("span", { class: "row-actions" }, validateBtn, removeBtn),
        );
    }
}

function connectPrompt() {
    const goSettings = el("button", { class: "btn btn-primary", type: "button" }, "Set API key");
    goSettings.addEventListener("click", () => navigate("/settings"));
    return el("div", { class: "empty" },
        icon("keys"),
        el("p", {}, "Set your gateway API key to manage keys."),
        goSettings,
    );
}
