// Keys — gateway API key management (admin) and per-key BYOK provider
// credentials. Secrets show once at creation and are never re-displayed.

import {
    getApiKey, listKeys, createKey, updateKey, deleteKey,
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
        const byokBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button", title: "Provider keys (BYOK)" }, icon("providers"), "byok");
        const toggleBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, active ? "disable" : "enable");
        const delBtn = el("button", { class: "icon-btn danger", type: "button", "aria-label": `Delete ${key.name || id}` }, icon("trash"));

        byokBtn.addEventListener("click", () => openByokModal(key));
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

        return el("tr", {},
            el("td", {}, el("div", { class: "cell-name" }, key.name || "unnamed")),
            el("td", {}, el("code", { class: "mono" }, maskKey(key))),
            el("td", {}, scopes.length
                ? scopes.map((s) => el("span", { class: "badge" }, s))
                : el("span", { class: "muted" }, "—")),
            el("td", {}, el("span", { class: `badge ${active ? "badge-ok" : "badge-err"}` }, active ? "active" : "disabled")),
            el("td", {}, el("span", { class: "muted" }, key.created_at ? formatRelativeTime(key.created_at) : "—")),
            el("td", { class: "num row-actions" }, byokBtn, toggleBtn, delBtn),
        );
    }
}

function maskKey(key) {
    const prefix = key.key_prefix || key.prefix;
    if (prefix) return `${prefix}…`;
    const id = key.id || key.key_id || "";
    return `${String(id).slice(0, 12)}…`;
}

function openCreateModal(reload) {
    const nameInput = el("input", { class: "input", id: "key-name", type: "text", placeholder: "e.g. local-dev", autocomplete: "off" });
    const adminCheck = el("input", { type: "checkbox", id: "scope-admin" });
    const createBtn = el("button", { class: "btn btn-primary", type: "button" }, "Create key");
    const cancelBtn = el("button", { class: "btn btn-ghost", type: "button" }, "Cancel");
    const close = modal({
        title: "New API key",
        body: el("div", {},
            el("div", { class: "field" }, el("label", { for: "key-name" }, "Name"), nameInput),
            el("label", { class: "check" }, adminCheck,
                el("span", {}, "Admin scope — can manage keys, providers, and the catalog")),
        ),
        foot: [cancelBtn, createBtn],
    });
    cancelBtn.addEventListener("click", close);
    createBtn.addEventListener("click", async () => {
        const name = nameInput.value.trim();
        if (!name) { nameInput.focus(); return; }
        createBtn.disabled = true;
        try {
            const body = { name };
            if (adminCheck.checked) body.scopes = ["admin"];
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

// showSecretOnce displays the freshly minted secret — the only time it exists
// in plaintext.
function showSecretOnce(created) {
    const secret = created?.key || created?.secret || created?.api_key || "";
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
