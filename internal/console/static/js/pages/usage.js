// Usage — the gateway request log: every request with model, provider,
// status, tokens, latency, and cost, plus summary aggregates for the
// filtered window. Admin keys see traffic across every key; other keys
// see their own requests via /api/v1/activity.

import { getApiKey, listActivity, listAdminActivity } from "../api.js";
import {
    el, icon, toast, sidePanel, debounce,
    formatCount, formatMs, formatNanoUSD, formatRelativeTime,
} from "../ui.js";
import { navigate } from "../router.js";

export const title = "Usage";

const PAGE_LIMIT = 200;
// Pages fetched eagerly so the summary strip covers the window, not just
// the first screen. Beyond this the strip reports itself as partial.
const AUTO_PAGES = 5;
const RANGE_SECONDS = { "1h": 3600, "24h": 86400, "7d": 604800, "30d": 2592000 };
// Recorded cost-unavailability reasons (invariant 3: absent cost always
// carries its reason, never a silent zero).
const COST_REASONS = { no_pricing: "no pricing", no_route: "no route", no_usage: "no usage" };

export async function render(container) {
    const page = el("div", { class: "page" });
    container.append(page);

    if (!getApiKey()) {
        page.append(connectPrompt());
        return;
    }

    const state = {
        records: [],
        cursor: "",
        admin: null, // resolved on first load: admin activity vs own-key activity
        filters: { model: "", provider: "", status: "", range: "24h" },
        generation: 0,
        disposed: false,
    };

    // --- Toolbar ---
    const modelInput = el("input", { class: "input", type: "search", placeholder: "Filter by model…", "aria-label": "Filter by model" });
    const providerInput = el("input", { class: "input", type: "search", placeholder: "provider", "aria-label": "Filter by provider" });
    const statusSelect = el("select", { class: "select", "aria-label": "Filter by status" },
        el("option", { value: "" }, "any status"),
        el("option", { value: "ok" }, "ok"),
        el("option", { value: "error" }, "error"),
        el("option", { value: "cancelled" }, "cancelled"),
    );
    const rangeSelect = el("select", { class: "select", "aria-label": "Time range" },
        el("option", { value: "1h" }, "last hour"),
        el("option", { value: "24h", selected: true }, "last 24 hours"),
        el("option", { value: "7d" }, "last 7 days"),
        el("option", { value: "30d" }, "last 30 days"),
        el("option", { value: "" }, "all time"),
    );
    const scopeLabel = el("span", { class: "muted" }, "");
    const toolbar = el("div", { class: "usage-toolbar" },
        el("div", { class: "search-wrap" }, icon("search"), modelInput),
        providerInput,
        statusSelect,
        rangeSelect,
        scopeLabel,
    );

    const summaryHost = el("div", {});
    const tableHost = el("div", {});
    const moreHost = el("div", { class: "load-more" });

    page.append(
        el("div", { class: "page-head" },
            el("h1", {}, "Usage"),
            el("p", { class: "sub" }, "Every request through the gateway: models, providers, tokens, latency, and cost."),
        ),
        toolbar,
        summaryHost,
        tableHost,
        moreHost,
    );

    const reload = debounce(() => load(), 250);
    modelInput.addEventListener("input", () => { state.filters.model = modelInput.value.trim(); reload(); });
    providerInput.addEventListener("input", () => { state.filters.provider = providerInput.value.trim(); reload(); });
    statusSelect.addEventListener("change", () => { state.filters.status = statusSelect.value; load(); });
    rangeSelect.addEventListener("change", () => { state.filters.range = rangeSelect.value; load(); });

    await load();
    return () => { state.disposed = true; };

    function queryFor(cursor) {
        const query = { limit: PAGE_LIMIT };
        if (cursor) query.cursor = cursor;
        if (state.filters.model) query.model = state.filters.model;
        if (state.filters.provider) query.provider = state.filters.provider;
        if (state.filters.status) query.status = state.filters.status;
        const seconds = RANGE_SECONDS[state.filters.range];
        if (seconds) query.since = new Date(Date.now() - seconds * 1000).toISOString();
        return query;
    }

    // fetchPage resolves the scope on first use: admin keys read the
    // cross-key admin listing, everything else falls back to own-key
    // activity.
    async function fetchPage(cursor) {
        const query = queryFor(cursor);
        if (state.admin === null) {
            try {
                const body = await listAdminActivity(query);
                state.admin = true;
                return body;
            } catch (error) {
                if (!error.unauthorized && !error.forbidden) throw error;
                state.admin = false;
            }
        }
        return state.admin ? listAdminActivity(query) : listActivity(query);
    }

    async function load() {
        const generation = ++state.generation;
        state.records = [];
        state.cursor = "";
        tableHost.replaceChildren(el("div", { class: "loading-row" }, el("span", { class: "spinner" }), "Loading requests…"));
        summaryHost.replaceChildren();
        moreHost.replaceChildren();
        try {
            let cursor = "";
            for (let fetched = 0; fetched < AUTO_PAGES; fetched++) {
                const body = await fetchPage(cursor);
                if (state.disposed || generation !== state.generation) return;
                state.records.push(...(body?.data ?? []));
                cursor = body?.next_cursor ?? "";
                state.cursor = cursor;
                if (!cursor) break;
            }
            scopeLabel.textContent = state.admin ? "all keys" : "your key";
            draw();
        } catch (error) {
            if (state.disposed || generation !== state.generation) return;
            if (error.unauthorized || error.forbidden) {
                tableHost.replaceChildren(el("div", { class: "empty" }, icon("keys"),
                    el("p", {}, "Usage needs a key with the activity scope. Update it in Settings.")));
            } else if (error.status === 503) {
                tableHost.replaceChildren(el("div", { class: "empty" }, icon("alert"),
                    el("p", {}, "Usage accounting is not configured on this gateway.")));
            } else {
                tableHost.replaceChildren(el("div", { class: "empty" }, icon("alert"),
                    el("p", {}, `Failed to load usage: ${error.message}`)));
            }
        }
    }

    async function loadMore() {
        const generation = state.generation;
        const moreBtn = moreHost.querySelector("button");
        if (moreBtn) moreBtn.disabled = true;
        try {
            const body = await fetchPage(state.cursor);
            if (state.disposed || generation !== state.generation) return;
            state.records.push(...(body?.data ?? []));
            state.cursor = body?.next_cursor ?? "";
            draw();
        } catch (error) {
            if (state.disposed || generation !== state.generation) return;
            toast(`Failed to load more: ${error.message}`, "err");
            if (moreBtn) moreBtn.disabled = false;
        }
    }

    function draw() {
        summaryHost.replaceChildren(summaryStrip());
        if (!state.records.length) {
            const hasFilters = state.filters.model || state.filters.provider || state.filters.status;
            tableHost.replaceChildren(el("div", { class: "empty" }, icon("usage"),
                el("p", {}, hasFilters
                    ? "No requests match these filters."
                    : "No traffic in this window yet. Send a request through the gateway or try it in Chat."),
                hasFilters ? null : chatButton()));
            moreHost.replaceChildren();
            return;
        }
        const tbody = el("tbody", {});
        for (const record of state.records) tbody.append(recordRow(record));
        tableHost.replaceChildren(
            el("div", { class: "table-wrap" },
                el("table", { class: "table" },
                    el("thead", {}, el("tr", {},
                        el("th", {}, "time"),
                        el("th", {}, "model"),
                        state.admin ? el("th", {}, "key") : null,
                        el("th", {}, "provider"),
                        el("th", {}, "status"),
                        el("th", { class: "num" }, "tokens"),
                        el("th", { class: "num" }, "latency"),
                        el("th", { class: "num" }, "cost"),
                        el("th", {}, "cache"),
                    )),
                    tbody,
                ),
            ),
        );
        moreHost.replaceChildren();
        if (state.cursor) {
            const moreBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, icon("chevron-d"), "load older requests");
            moreBtn.addEventListener("click", loadMore);
            moreHost.append(moreBtn);
        }
    }

    // summaryStrip aggregates the loaded (filtered) records. When more
    // pages remain on the server the counts are labeled as partial —
    // never presented as window totals.
    function summaryStrip() {
        let tokens = 0;
        let spendNano = 0;
        let priced = 0;
        let withoutCost = 0;
        let errors = 0;
        for (const record of state.records) {
            tokens += record.tokens?.total ?? 0;
            if (record.cost) { spendNano += record.cost.nano_usd ?? 0; priced++; }
            else withoutCost++;
            if (record.status === "error") errors++;
        }
        const partial = Boolean(state.cursor);
        const suffix = partial ? "+" : "";
        const stat = (k, v, d) => el("div", { class: "stat" },
            el("div", { class: "k" }, k),
            el("div", { class: "v" }, v),
            d ? el("div", { class: "d" }, d) : null,
        );
        return el("div", { class: "grid cols-4 usage-summary" },
            stat("requests", `${formatCount(state.records.length)}${suffix}`, partial ? "loaded so far" : null),
            stat("errors", `${formatCount(errors)}${suffix}`),
            stat("tokens", `${formatCount(tokens)}${suffix}`),
            stat("spend", priced ? `${formatNanoUSD(spendNano)}${suffix}` : "—",
                withoutCost ? `${formatCount(withoutCost)} without cost` : null),
        );
    }

    function recordRow(record) {
        const row = el("tr", { class: "is-clickable", tabindex: "0", role: "button" },
            el("td", {}, el("span", { class: "muted", title: record.timestamp }, formatRelativeTime(record.timestamp))),
            el("td", {},
                el("div", { class: "cell-name" }, record.model_requested || "—"),
                record.model_used && record.model_used !== record.model_requested
                    ? el("div", { class: "cell-id mono" }, record.model_used) : null,
            ),
            state.admin ? el("td", {}, el("code", { class: "mono" }, shortKey(record.key_id))) : null,
            el("td", {}, record.provider || el("span", { class: "muted" }, "—")),
            el("td", {}, statusBadge(record)),
            el("td", { class: "num" }, record.tokens?.total ? formatCount(record.tokens.total) : "—"),
            el("td", { class: "num" }, formatMs(record.latency_ms)),
            el("td", { class: "num" }, costCell(record)),
            el("td", {}, cacheBadge(record.cache_status)),
        );
        const open = () => openDetail(record);
        row.addEventListener("click", open);
        row.addEventListener("keydown", (event) => {
            if (event.key === "Enter" || event.key === " ") { event.preventDefault(); open(); }
        });
        return row;
    }

    function openDetail(record) {
        const tokens = record.tokens || {};
        const kv = [];
        const add = (label, value) => {
            if (value === undefined || value === null || value === "") return;
            kv.push(el("dt", {}, label), el("dd", { class: "wrap" }, value));
        };
        add("request", el("code", { class: "mono" }, record.request_id || "—"));
        if (state.admin) add("key", el("code", { class: "mono" }, record.key_id));
        add("time", record.timestamp ? new Date(record.timestamp).toLocaleString() : "—");
        add("protocol", record.protocol);
        add("operation", record.operation + (record.streaming ? " (streaming)" : ""));
        add("model requested", record.model_requested);
        add("model used", record.model_used);
        add("provider", record.provider || "unrouted");
        add("status", `${record.status}${record.status_code ? ` (${record.status_code})` : ""}`);
        add("error class", record.error_class);
        add("attempts", record.attempts ? String(record.attempts) : null);
        add("routing", Number.isFinite(record.routing_ms) ? formatMs(record.routing_ms) : null);
        add("latency", formatMs(record.latency_ms));
        add("cache", record.cache_status);
        add("cost", record.cost
            ? `${formatNanoUSD(record.cost.nano_usd)} ${record.cost.currency || "USD"}`
            : `unavailable — ${COST_REASONS[record.cost_unavailable_reason] || record.cost_unavailable_reason || "unknown"}`);
        const tokenParts = [
            ["input", tokens.input], ["output", tokens.output], ["total", tokens.total],
            ["reasoning", tokens.reasoning], ["cache read", tokens.cache_read], ["cache write", tokens.cache_write],
        ].filter(([, count]) => count);
        add("tokens", tokenParts.length
            ? tokenParts.map(([name, count]) => `${name} ${formatCount(count)}`).join(" · ")
            : "—");
        const body = el("div", { class: "drawer-body" }, el("dl", { class: "kv" }, kv));
        sidePanel(record.model_requested || "request", body);
    }
}

function statusBadge(record) {
    const status = record.status || "—";
    const cls = status === "ok" ? "badge-ok" : status === "error" ? "badge-err" : "badge-warn";
    // Name the two enforcement rejections instead of a bare code.
    let label = status === "error" && record.error_class ? record.error_class.replaceAll("_", " ") : status;
    if (status === "error" && record.status_code === 402) label = "budget exhausted";
    if (status === "error" && record.status_code === 429) label = "rate limited";
    return el("span", { class: `badge ${cls}`, title: record.status_code ? `HTTP ${record.status_code}` : "" }, label);
}

function costCell(record) {
    if (record.cost) return formatNanoUSD(record.cost.nano_usd);
    const reason = COST_REASONS[record.cost_unavailable_reason] || record.cost_unavailable_reason;
    return reason
        ? el("span", { class: "badge badge-warn", title: "Cost is unavailable for this request" }, reason)
        : "—";
}

function cacheBadge(cacheStatus) {
    if (cacheStatus === "HIT") return el("span", { class: "badge badge-cyan" }, "hit");
    return el("span", { class: "muted" }, cacheStatus ? cacheStatus.toLowerCase() : "—");
}

function shortKey(keyID) {
    if (!keyID) return "—";
    return keyID.length > 14 ? `${keyID.slice(0, 14)}…` : keyID;
}

function chatButton() {
    const btn = el("button", { class: "btn btn-primary btn-sm", type: "button" }, icon("chat"), "open chat");
    btn.addEventListener("click", () => navigate("/chat"));
    return btn;
}

function connectPrompt() {
    const goSettings = el("button", { class: "btn btn-primary", type: "button" }, "Set API key");
    goSettings.addEventListener("click", () => navigate("/settings"));
    return el("div", { class: "empty" },
        icon("keys"),
        el("p", {}, "Set your gateway API key to see usage."),
        goSettings,
    );
}
