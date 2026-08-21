// Models — the Starmap catalog: searchable model table, snapshot identity,
// catalog refresh, and a per-model detail drawer with endpoint health.

import { getApiKey, listModels, invalidateModels, getModelEndpoints, providerStatus, refreshProviders } from "../api.js";
import { el, icon, toast, copyButton, formatModelPrice, formatPricePerM, formatContext, debounce } from "../ui.js";
import { navigate } from "../router.js";

export const title = "Models";

const MODALITY_ICONS = { image: "image", audio: "audio", file: "eye" };

export async function render(container) {
    const page = el("div", { class: "page" });
    container.append(page);

    if (!getApiKey()) {
        page.append(connectPrompt());
        return;
    }

    const state = {
        models: [],
        query: "",
        provider: "",
        modality: "",
        capability: "",
        sort: "id",
        disposed: false,
    };

    // --- Toolbar ---
    const searchInput = el("input", { class: "input", type: "search", placeholder: "Search models…", "aria-label": "Search models" });
    const providerSelect = el("select", { class: "select", "aria-label": "Filter by provider" }, el("option", { value: "" }, "all providers"));
    const modalitySelect = el("select", { class: "select", "aria-label": "Filter by input modality" },
        el("option", { value: "" }, "any input"),
        el("option", { value: "text" }, "text"),
        el("option", { value: "image" }, "image"),
        el("option", { value: "audio" }, "audio"),
        el("option", { value: "file" }, "file"),
    );
    const capabilitySelect = el("select", { class: "select", "aria-label": "Filter by capability" },
        el("option", { value: "" }, "any capability"),
        el("option", { value: "tools" }, "tool calls"),
        el("option", { value: "reasoning" }, "reasoning"),
        el("option", { value: "structured_outputs" }, "structured output"),
    );
    const countLabel = el("span", { class: "muted" }, "…");
    const toolbar = el("div", { class: "models-toolbar" },
        el("div", { class: "search-wrap" }, icon("search"), searchInput),
        providerSelect,
        modalitySelect,
        capabilitySelect,
        countLabel,
    );

    // --- Snapshot bar ---
    const snapshotText = el("span", { class: "snapshot-id" }, "snapshot —");
    const refreshBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, icon("refresh"), "refresh catalog");
    const snapshotBar = el("div", { class: "snapshot-bar" },
        el("span", { class: "snapshot-left" }, icon("star"), snapshotText),
        refreshBtn,
    );
    refreshBtn.addEventListener("click", async () => {
        refreshBtn.disabled = true;
        refreshBtn.classList.add("is-busy");
        try {
            await refreshProviders();
            invalidateModels();
            toast("Catalog refresh triggered", "ok");
            await load({ fresh: true });
        } catch (error) {
            if (error.unauthorized || error.forbidden) toast("Catalog refresh needs an admin-scoped key", "err");
            else toast(`Refresh failed: ${error.message}`, "err");
        } finally {
            refreshBtn.disabled = false;
            refreshBtn.classList.remove("is-busy");
        }
    });

    const tableHost = el("div", {});
    page.append(
        el("div", { class: "page-head" },
            el("h1", {}, "Models"),
            el("p", { class: "sub" }, "The Starmap catalog this gateway routes against."),
        ),
        snapshotBar, toolbar, tableHost,
    );
    tableHost.append(loadingRows());

    searchInput.addEventListener("input", debounce(() => { state.query = searchInput.value.trim().toLowerCase(); draw(); }, 120));
    providerSelect.addEventListener("change", () => { state.provider = providerSelect.value; draw(); });
    modalitySelect.addEventListener("change", () => { state.modality = modalitySelect.value; draw(); });
    capabilitySelect.addEventListener("change", () => { state.capability = capabilitySelect.value; draw(); });

    await load();

    async function load({ fresh = false } = {}) {
        try {
            const models = await listModels({ fresh });
            if (state.disposed) return;
            state.models = models;
            fillProviderFilter(models);
            draw();
        } catch (error) {
            if (state.disposed) return;
            tableHost.replaceChildren(errorCard(error));
        }
        providerStatus().then((status) => {
            if (state.disposed || !status?.catalog_generation_id) return;
            snapshotText.textContent = `snapshot ${status.catalog_generation_id} · rev ${status.revision ?? "—"}`;
        }).catch(() => {
            snapshotText.textContent = "snapshot — (admin key required)";
            refreshBtn.hidden = true;
        });
    }

    function fillProviderFilter(models) {
        const prefixes = new Map();
        for (const model of models) {
            const slash = model.id.indexOf("/");
            if (slash <= 0) continue;
            const prefix = model.id.slice(0, slash);
            prefixes.set(prefix, (prefixes.get(prefix) || 0) + 1);
        }
        const current = providerSelect.value;
        providerSelect.replaceChildren(el("option", { value: "" }, "all providers"));
        for (const [prefix, count] of [...prefixes.entries()].sort((a, b) => a[0].localeCompare(b[0]))) {
            providerSelect.append(el("option", { value: prefix }, `${prefix} (${count})`));
        }
        providerSelect.value = current;
    }

    function matches(model) {
        if (state.provider && !model.id.startsWith(`${state.provider}/`)) return false;
        if (state.modality) {
            const inputs = model.architecture?.input_modalities || [];
            if (!inputs.includes(state.modality)) return false;
        }
        if (state.capability) {
            const params = model.supported_parameters || [];
            if (state.capability === "reasoning") {
                if (!params.includes("reasoning") && !params.includes("include_reasoning")) return false;
            } else if (!params.includes(state.capability)) return false;
        }
        if (state.query) {
            const haystack = `${model.id} ${model.name || ""}`.toLowerCase();
            if (!haystack.includes(state.query)) return false;
        }
        return true;
    }

    function draw() {
        const rows = state.models.filter(matches);
        countLabel.textContent = `${rows.length} of ${state.models.length}`;
        if (!rows.length) {
            tableHost.replaceChildren(el("div", { class: "empty" }, icon("search"), el("p", {}, "No models match these filters.")));
            return;
        }
        const tbody = el("tbody", {});
        for (const model of rows) tbody.append(modelRow(model));
        tableHost.replaceChildren(
            el("div", { class: "table-wrap" },
                el("table", { class: "table" },
                    el("thead", {}, el("tr", {},
                        el("th", {}, "model"),
                        el("th", {}, "modality"),
                        el("th", { class: "num" }, "context"),
                        el("th", { class: "num" }, "price / 1M"),
                    )),
                    tbody,
                ),
            ),
        );
    }

    function modelRow(model) {
        const inputs = model.architecture?.input_modalities || [];
        const params = model.supported_parameters || [];
        const modIcons = el("span", { class: "mod-icons" });
        for (const modality of inputs) {
            if (MODALITY_ICONS[modality]) modIcons.append(icon(MODALITY_ICONS[modality], "", ));
        }
        if (params.includes("tools")) modIcons.append(icon("tool"));
        if (params.includes("reasoning") || params.includes("include_reasoning")) modIcons.append(icon("reasoning"));
        const prompt = formatPricePerM(model.pricing?.prompt);
        const completion = formatPricePerM(model.pricing?.completion);
        const price = prompt !== null || completion !== null ? `${prompt ?? "—"} / ${completion ?? "—"}` : "—";
        const row = el("tr", { class: "is-clickable", tabindex: "0", role: "button" },
            el("td", {},
                el("div", { class: "cell-name" }, model.name || model.id),
                el("div", { class: "cell-id mono" }, model.id),
            ),
            el("td", {}, modIcons),
            el("td", { class: "num" }, formatContext(model.context_length)),
            el("td", { class: "num" }, price),
        );
        const open = () => openDrawer(model);
        row.addEventListener("click", open);
        row.addEventListener("keydown", (event) => {
            if (event.key === "Enter" || event.key === " ") { event.preventDefault(); open(); }
        });
        return row;
    }

    function openDrawer(model) {
        const params = model.supported_parameters || [];
        const arch = model.architecture || {};
        const endpointsHost = el("div", {}, el("div", { class: "loading-row" }, el("span", { class: "spinner" }), "Checking endpoints…"));
        const body = el("div", { class: "drawer-body" },
            el("div", { class: "endpoint" },
                el("code", {}, model.id),
                copyButton(model.id),
            ),
            model.description ? el("p", { class: "drawer-desc" }, truncate(model.description, 420)) : null,
            el("dl", { class: "kv" },
                el("dt", {}, "context"),
                el("dd", {}, `${formatContext(model.context_length)} tokens`),
                el("dt", {}, "max output"),
                el("dd", {}, model.top_provider?.max_completion_tokens ? `${formatContext(model.top_provider.max_completion_tokens)} tokens` : "—"),
                el("dt", {}, "price"),
                el("dd", {}, formatModelPrice(model.pricing) || "—"),
                el("dt", {}, "input"),
                el("dd", {}, (arch.input_modalities || []).join(", ") || "—"),
                el("dt", {}, "output"),
                el("dd", {}, (arch.output_modalities || []).join(", ") || "—"),
                el("dt", {}, "parameters"),
                el("dd", { class: "wrap" }, params.join(", ") || "—"),
            ),
            el("div", { class: "card-title" }, "Endpoints"),
            endpointsHost,
        );
        const tryBtn = el("button", { class: "btn btn-primary btn-sm", type: "button" }, icon("chat"), "open in chat");
        tryBtn.addEventListener("click", () => {
            close();
            navigate(`/chat?model=${encodeURIComponent(model.id)}`);
        });
        const close = openSidePanel(model.name || model.id, body, tryBtn);

        getModelEndpoints(model.id).then((data) => {
            const endpoints = data?.endpoints || data?.data?.endpoints || [];
            if (!endpoints.length) {
                endpointsHost.replaceChildren(el("p", { class: "muted" }, "No endpoint detail for this model."));
                return;
            }
            const list = el("div", { class: "endpoint-list" });
            for (const ep of endpoints) {
                const price = formatModelPrice({ prompt: ep.cost_prompt, completion: ep.cost_output });
                list.append(el("div", { class: "offer" },
                    el("span", { class: `offer-dot ${ep.available === false ? "bad" : "ok"}` }),
                    el("span", { class: "mono" }, ep.provider || ep.provider_name || "endpoint"),
                    el("span", { class: "muted" }, price || ""),
                ));
            }
            endpointsHost.replaceChildren(list);
        }).catch(() => {
            endpointsHost.replaceChildren(el("p", { class: "muted" }, "Endpoint detail unavailable."));
        });
    }

    return () => { state.disposed = true; };
}

// openSidePanel slides in a right-hand drawer with a title, body, and action.
function openSidePanel(titleText, body, action) {
    const closeBtn = el("button", { class: "icon-btn", type: "button", "aria-label": "Close" }, icon("close"));
    const panel = el("aside", { class: "drawer", role: "dialog", "aria-modal": "true" },
        el("div", { class: "drawer-head" }, el("h2", {}, titleText), closeBtn),
        body,
        action ? el("div", { class: "drawer-foot" }, action) : null,
    );
    const scrim = el("div", { class: "drawer-scrim" }, panel);
    const close = () => {
        scrim.remove();
        document.removeEventListener("keydown", onKey);
    };
    const onKey = (event) => { if (event.key === "Escape") close(); };
    scrim.addEventListener("mousedown", (event) => { if (event.target === scrim) close(); });
    closeBtn.addEventListener("click", close);
    document.addEventListener("keydown", onKey);
    document.body.append(scrim);
    return close;
}

function truncate(text, max) {
    if (!text || text.length <= max) return text;
    return `${text.slice(0, max).trimEnd()}…`;
}

function loadingRows() {
    return el("div", { class: "loading-row" }, el("span", { class: "spinner" }), "Loading catalog…");
}

function errorCard(error) {
    const message = error.unauthorized
        ? "Your API key was rejected. Update it in Settings."
        : `Failed to load the catalog: ${error.message}`;
    return el("div", { class: "empty" }, icon("alert"), el("p", {}, message));
}

function connectPrompt() {
    const goSettings = el("button", { class: "btn btn-primary", type: "button" }, "Set API key");
    goSettings.addEventListener("click", () => navigate("/settings"));
    return el("div", { class: "empty" },
        icon("keys"),
        el("p", {}, "Set your gateway API key to browse the catalog."),
        goSettings,
    );
}
