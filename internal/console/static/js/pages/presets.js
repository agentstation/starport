// Presets — reusable inference configurations. A preset stores model
// selection, sampling defaults, and provider routing policy; chat requests
// select one with "@preset/<name>" or a "preset" body field.

import { getApiKey, listPresets, createPreset, updatePreset, deletePreset } from "../api.js";
import { el, icon, toast, modal, confirmDialog, formatRelativeTime } from "../ui.js";
import { navigate } from "../router.js";

export const title = "Presets";

const SORTS = ["", "price", "latency", "throughput"];

export async function render(container) {
    const page = el("div", { class: "page" });
    container.append(page);

    if (!getApiKey()) {
        page.append(connectPrompt());
        return;
    }

    const newBtn = el("button", { class: "btn btn-primary btn-sm", type: "button" }, icon("plus"), "new preset");
    const listHost = el("div", {});
    page.append(
        el("div", { class: "page-head page-head-row" },
            el("div", {},
                el("h1", {}, "Presets"),
                el("p", { class: "sub" }, "Reusable request configurations. Reference one from any request with ",
                    el("code", { class: "mono" }, "@preset/name"), "."),
            ),
            newBtn,
        ),
        listHost,
    );
    listHost.append(el("div", { class: "loading-row" }, el("span", { class: "spinner" }), "Loading presets…"));

    let disposed = false;
    newBtn.addEventListener("click", () => openEditor(null, load));
    await load();

    return () => { disposed = true; };

    async function load() {
        try {
            const body = await listPresets();
            if (disposed) return;
            draw(body?.data ?? []);
        } catch (error) {
            if (disposed) return;
            if (error.unauthorized || error.forbidden) {
                listHost.replaceChildren(el("div", { class: "empty" }, icon("presets"),
                    el("p", {}, "Preset management needs a key with the presets:write scope. Set one in Settings.")));
            } else if (error.status === 503) {
                newBtn.hidden = true;
                listHost.replaceChildren(el("div", { class: "empty" }, icon("alert"),
                    el("p", {}, "Preset storage is not configured on this gateway.")));
            } else {
                listHost.replaceChildren(el("div", { class: "empty" }, icon("alert"),
                    el("p", {}, `Failed to load presets: ${error.message}`)));
            }
        }
    }

    function draw(records) {
        if (!records.length) {
            listHost.replaceChildren(el("div", { class: "empty" }, icon("presets"),
                el("p", {}, "No presets yet. Create one to reuse a model, prompt, and routing policy across requests.")));
            return;
        }
        const tbody = el("tbody", {});
        for (const record of records) tbody.append(presetRow(record));
        listHost.replaceChildren(
            el("div", { class: "table-wrap" },
                el("table", { class: "table" },
                    el("thead", {}, el("tr", {},
                        el("th", {}, "name"),
                        el("th", {}, "model"),
                        el("th", {}, "routing"),
                        el("th", {}, "updated"),
                        el("th", { class: "num" }, ""),
                    )),
                    tbody,
                ),
            ),
        );
    }

    function presetRow(record) {
        const editBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, icon("edit"), "edit");
        const delBtn = el("button", { class: "icon-btn danger", type: "button", "aria-label": `Delete ${record.name}` }, icon("trash"));

        editBtn.addEventListener("click", () => openEditor(record, load));
        delBtn.addEventListener("click", async () => {
            const ok = await confirmDialog({
                title: "Delete preset",
                message: `Delete "@preset/${record.name}"? Requests referencing it will start failing.`,
                confirmLabel: "Delete",
                danger: true,
            });
            if (!ok) return;
            try {
                await deletePreset(record.name);
                toast("Preset deleted", "ok");
                await load();
            } catch (error) {
                toast(`Delete failed: ${error.message}`, "err");
            }
        });

        return el("tr", {},
            el("td", {}, el("div", { class: "cell-name" },
                el("code", { class: "mono" }, `@preset/${record.name}`),
                record.description ? el("span", { class: "muted" }, record.description) : null,
            )),
            el("td", {}, el("span", { class: "mono" }, record.config?.model || record.config?.models?.[0] || "—")),
            el("td", {}, routingSummary(record.config?.provider)),
            el("td", {}, el("span", { class: "muted" }, record.updated_at ? formatRelativeTime(record.updated_at) : "—")),
            el("td", { class: "num row-actions" }, editBtn, delBtn),
        );
    }
}

function routingSummary(provider) {
    if (!provider) return el("span", { class: "muted" }, "—");
    const parts = [];
    if (provider.sort) parts.push(`sort ${provider.sort}`);
    if (provider.order?.length) parts.push(`order ${provider.order.join(" → ")}`);
    if (provider.only?.length) parts.push(`only ${provider.only.join(", ")}`);
    if (provider.ignore?.length) parts.push(`ignore ${provider.ignore.join(", ")}`);
    if (provider.max_prompt_price_per_1m) parts.push(`≤$${provider.max_prompt_price_per_1m}/M in`);
    if (provider.max_completion_price_per_1m) parts.push(`≤$${provider.max_completion_price_per_1m}/M out`);
    if (provider.allow_fallbacks === false) parts.push("no fallbacks");
    if (!parts.length) return el("span", { class: "muted" }, "—");
    return el("span", {}, parts.map((p) => el("span", { class: "badge" }, p)));
}

// openEditor shows the typed preset config form. Passing a record edits it
// (revision-checked); passing null creates a new preset.
function openEditor(record, reload) {
    const editing = record !== null;
    const config = record?.config || {};
    const provider = config.provider || {};

    const field = (labelText, input, hint) => el("div", { class: "field" },
        el("label", {}, labelText), input,
        hint ? el("span", { class: "muted" }, hint) : null);
    const text = (value, placeholder, mono = false) =>
        el("input", { class: `input${mono ? " mono" : ""}`, type: "text", value: value ?? "", placeholder, autocomplete: "off" });
    const number = (value, attrs = {}) =>
        el("input", { class: "input", type: "number", value: value ?? "", placeholder: "default", ...attrs });

    const nameInput = text(record?.name, "e.g. fast-cheap", true);
    if (editing) nameInput.disabled = true;
    const descInput = text(record?.description, "what this preset is for");
    const modelInput = text(config.model, "e.g. openai/gpt-4o-mini", true);
    const modelsInput = text((config.models || []).join(", "), "fallbacks, comma-separated", true);
    const systemInput = el("textarea", { class: "input", rows: "3", placeholder: "You are a helpful assistant…" });
    systemInput.value = config.system || "";
    const tempInput = number(config.temperature, { step: "0.1", min: "0", max: "2" });
    const topPInput = number(config.top_p, { step: "0.05", min: "0", max: "1" });
    const maxTokInput = number(config.max_tokens, { min: "1" });
    const seedInput = number(config.seed, {});
    const presenceInput = number(config.presence_penalty, { step: "0.1", min: "-2", max: "2" });
    const frequencyInput = number(config.frequency_penalty, { step: "0.1", min: "-2", max: "2" });
    const stopInput = text((config.stop || []).join(", "), "stop sequences, comma-separated", true);

    const orderInput = text((provider.order || []).join(", "), "e.g. groq, openai", true);
    const onlyInput = text((provider.only || []).join(", "), "allowlist, comma-separated", true);
    const ignoreInput = text((provider.ignore || []).join(", "), "denylist, comma-separated", true);
    const sortSelect = el("select", { class: "select" },
        ...SORTS.map((s) => el("option", { value: s }, s || "server default")));
    sortSelect.value = SORTS.includes(provider.sort) ? provider.sort || "" : "";
    const maxPromptInput = number(provider.max_prompt_price_per_1m, { step: "0.01", min: "0" });
    const maxCompletionInput = number(provider.max_completion_price_per_1m, { step: "0.01", min: "0" });
    const fallbacksCheck = el("input", { type: "checkbox" });
    fallbacksCheck.checked = provider.allow_fallbacks !== false;

    const saveBtn = el("button", { class: "btn btn-primary", type: "button" }, editing ? "Save preset" : "Create preset");
    const cancelBtn = el("button", { class: "btn btn-ghost", type: "button" }, "Cancel");
    const close = modal({
        title: editing ? `Edit @preset/${record.name}` : "New preset",
        wide: true,
        body: el("div", {},
            el("div", { class: "form-grid" },
                field("Name", nameInput, editing ? "names are immutable" : "letters, digits, - and _"),
                field("Description", descInput),
            ),
            el("h3", { class: "form-section" }, "Model"),
            el("div", { class: "form-grid" },
                field("Model", modelInput),
                field("Fallback models", modelsInput),
            ),
            field("System prompt", systemInput),
            el("h3", { class: "form-section" }, "Sampling"),
            el("div", { class: "form-grid" },
                field("Temperature", tempInput),
                field("Top-p", topPInput),
                field("Max tokens", maxTokInput),
                field("Seed", seedInput),
                field("Presence penalty", presenceInput),
                field("Frequency penalty", frequencyInput),
            ),
            field("Stop sequences", stopInput),
            el("h3", { class: "form-section" }, "Provider routing"),
            el("div", { class: "form-grid" },
                field("Order", orderInput, "try providers in this order"),
                field("Only", onlyInput),
                field("Ignore", ignoreInput),
                field("Sort", sortSelect, "price, latency, or throughput"),
                field("Max prompt price", maxPromptInput, "USD per million input tokens"),
                field("Max completion price", maxCompletionInput, "USD per million output tokens"),
            ),
            el("label", { class: "check" }, fallbacksCheck,
                el("span", {}, "Allow fallbacks beyond the ordered providers")),
        ),
        foot: [cancelBtn, saveBtn],
    });
    cancelBtn.addEventListener("click", close);

    saveBtn.addEventListener("click", async () => {
        const name = nameInput.value.trim();
        if (!name) { nameInput.focus(); return; }
        const built = buildConfig();
        if (!built) { toast("A preset needs at least one setting", "err"); return; }
        saveBtn.disabled = true;
        const body = { name, description: descInput.value.trim(), config: built };
        try {
            if (editing) {
                body.revision = record.revision;
                await updatePreset(record.name, body);
                toast("Preset saved", "ok");
            } else {
                await createPreset(body);
                toast(`Created @preset/${name}`, "ok");
            }
            close();
            reload();
        } catch (error) {
            toast(error.status === 409
                ? "Preset changed elsewhere — reload and retry"
                : `Save failed: ${error.message}`, "err");
            saveBtn.disabled = false;
        }
    });

    function buildConfig() {
        const list = (input) => input.value.split(",").map((s) => s.trim()).filter(Boolean);
        const num = (input, parse = Number.parseFloat) => {
            const value = parse(input.value);
            return Number.isFinite(value) ? value : undefined;
        };
        const built = {};
        if (modelInput.value.trim()) built.model = modelInput.value.trim();
        const models = list(modelsInput);
        if (models.length) built.models = models;
        if (systemInput.value.trim()) built.system = systemInput.value.trim();
        const sampling = {
            temperature: num(tempInput),
            top_p: num(topPInput),
            max_tokens: num(maxTokInput, (v) => Number.parseInt(v, 10)),
            seed: num(seedInput, (v) => Number.parseInt(v, 10)),
            presence_penalty: num(presenceInput),
            frequency_penalty: num(frequencyInput),
        };
        for (const [key, value] of Object.entries(sampling)) {
            if (value !== undefined) built[key] = value;
        }
        const stop = list(stopInput);
        if (stop.length) built.stop = stop;

        const routing = {};
        const order = list(orderInput);
        const only = list(onlyInput);
        const ignore = list(ignoreInput);
        if (order.length) routing.order = order;
        if (only.length) routing.only = only;
        if (ignore.length) routing.ignore = ignore;
        if (sortSelect.value) routing.sort = sortSelect.value;
        const maxPrompt = num(maxPromptInput);
        const maxCompletion = num(maxCompletionInput);
        if (maxPrompt) routing.max_prompt_price_per_1m = maxPrompt;
        if (maxCompletion) routing.max_completion_price_per_1m = maxCompletion;
        if (!fallbacksCheck.checked) routing.allow_fallbacks = false;
        if (Object.keys(routing).length) built.provider = routing;

        return Object.keys(built).length ? built : null;
    }
}

function connectPrompt() {
    const goSettings = el("button", { class: "btn btn-primary", type: "button" }, "Set API key");
    goSettings.addEventListener("click", () => navigate("/settings"));
    return el("div", { class: "empty" },
        icon("presets"),
        el("p", {}, "Set your gateway API key to manage presets."),
        goSettings,
    );
}
