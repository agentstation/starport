// Overview — mission control for the local gateway: identity, endpoints,
// quickstart, live metrics, provider posture, and the Starmap snapshot.

import { getApiKey, healthReady, systemInfo, systemMetrics, providerStatus, catalogMetadata, listModels } from "../api.js";
import { el, icon, copyButton, formatCount, formatMs, formatNanoUSD, formatRelativeTime, toast } from "../ui.js";
import { shortGenerationID, freshnessBadges, openChangesPanel } from "../freshness.js";
import { navigate } from "../router.js";

export const title = "Overview";

export async function render(container) {
    const page = el("div", { class: "page" });
    container.append(page);

    const origin = location.origin;
    const hero = renderHero(origin);
    page.append(hero.node);

    page.append(
        el("div", { class: "grid cols-2" },
            renderEndpointsCard(origin),
            renderQuickstartCard(origin),
        ),
    );

    const statsHost = el("div", {});
    const lowerHost = el("div", { class: "grid cols-2" });
    page.append(statsHost, lowerHost);

    let disposed = false;
    hydrate();

    async function hydrate() {
        healthReady().then((ready) => {
            if (disposed) return;
            hero.setHealth(ready);
        }).catch(() => { if (!disposed) hero.setHealth(false); });

        if (!getApiKey()) {
            statsHost.append(connectCard());
            return;
        }

        listModels().then((models) => {
            if (!disposed) hero.setModels(models.length);
        }).catch(() => {});

        try {
            const [info, metrics] = await Promise.all([systemInfo(), systemMetrics()]);
            if (disposed) return;
            hero.setInfo(info);
            statsHost.append(renderStats(metrics));
        } catch (error) {
            if (disposed) return;
            if (error.unauthorized || error.forbidden) statsHost.append(lockedCard("Gateway metrics need an admin-scoped key."));
            else toast(`Failed to load metrics: ${error.message}`, "err");
        }

        const providersHost = el("div", {});
        const catalogHost = el("div", {});
        lowerHost.append(providersHost, catalogHost);

        providerStatus().then((status) => {
            if (disposed) return;
            providersHost.append(renderProvidersCard(status));
        }).catch((error) => {
            if (disposed) return;
            if (error.unauthorized || error.forbidden) providersHost.append(lockedCard("Provider status needs an admin-scoped key."));
        });

        // Catalog freshness needs only models:read, so it renders even when
        // the admin-scoped provider status does not.
        catalogMetadata().then((metadata) => {
            if (disposed) return;
            catalogHost.append(renderCatalogCard(metadata));
        }).catch((error) => {
            if (disposed) return;
            if (error.unauthorized || error.forbidden) catalogHost.append(lockedCard("Catalog freshness needs a key with the models:read scope."));
        });
    }

    return () => { disposed = true; };
}

function renderHero(origin) {
    const led = el("span", { class: "led", "data-state": "unknown" });
    const state = el("div", { class: "hero-title" }, "GATEWAY");
    const meta = el("div", { class: "hero-meta" }, el("span", {}, origin.replace(/^https?:\/\//, "")));
    const node = el("div", { class: "hero" },
        el("div", { class: "hero-id" }, led, el("div", {}, state, meta)),
    );
    return {
        node,
        setHealth(ready) {
            led.dataset.state = ready ? "ok" : "err";
            state.textContent = ready ? "GATEWAY READY" : "GATEWAY NOT READY";
        },
        setInfo(info) {
            if (info?.version) meta.append(el("span", {}, `v${info.version}`));
            if (info?.storage?.type) meta.append(el("span", {}, `${info.storage.type} storage`));
            if (info?.uptime && info.uptime !== "unavailable") meta.append(el("span", {}, `up ${info.uptime}`));
        },
        setModels(count) {
            meta.append(el("span", {}, `${count} models`));
        },
    };
}

function renderEndpointsCard(origin) {
    const endpoint = (label, url) => el("div", { class: "endpoint" },
        el("span", { class: "label" }, label),
        el("code", {}, url),
        copyButton(url),
    );
    return el("div", { class: "card" },
        el("div", { class: "card-title" }, "Endpoints"),
        el("div", { class: "endpoint-row" },
            endpoint("openai sdk", `${origin}/v1`),
            endpoint("openrouter sdk", `${origin}/api/v1`),
            endpoint("health", `${origin}/health/ready`),
        ),
    );
}

function renderQuickstartCard(origin) {
    const snippets = {
        curl: `curl ${origin}/v1/chat/completions \\
  -H "Authorization: Bearer $STARPORT_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "anthropic/claude-sonnet-5", "messages": [{"role": "user", "content": "Hello"}]}'`,
        python: `from openai import OpenAI

client = OpenAI(base_url="${origin}/v1", api_key=STARPORT_API_KEY)
reply = client.chat.completions.create(
    model="anthropic/claude-sonnet-5",
    messages=[{"role": "user", "content": "Hello"}],
)`,
        javascript: `import OpenAI from "openai";

const client = new OpenAI({ baseURL: "${origin}/v1", apiKey: STARPORT_API_KEY });
const reply = await client.chat.completions.create({
  model: "anthropic/claude-sonnet-5",
  messages: [{ role: "user", content: "Hello" }],
});`,
    };
    const pre = el("pre", {});
    const codeEl = el("code", {}, snippets.curl);
    pre.append(codeEl);
    let current = "curl";
    const tabs = el("div", { class: "tabs" },
        Object.keys(snippets).map((name) => {
            const btn = el("button", { class: name === current ? "is-active" : "", type: "button" }, name);
            btn.addEventListener("click", () => {
                current = name;
                codeEl.textContent = snippets[name];
                for (const sibling of tabs.children) sibling.classList.toggle("is-active", sibling === btn);
            });
            return btn;
        }),
    );
    return el("div", { class: "card" },
        el("div", { class: "card-title" }, "Quickstart", el("span", {}, "drop-in openai client")),
        tabs,
        el("div", { class: "codebox" }, pre, copyButton(() => snippets[current])),
    );
}

// renderStats shows the last 24 hours of recorded traffic. Spend that the
// gateway could not price is surfaced as "without cost", never as zero.
function renderStats(metrics) {
    const requests = metrics?.requests || {};
    const latency = metrics?.latency || {};
    const tokens = metrics?.tokens || {};
    const spend = metrics?.spend || {};
    const stat = (k, v, d) => el("div", { class: "stat" },
        el("div", { class: "k" }, k),
        el("div", { class: "v" }, v),
        d ? el("div", { class: "d" }, d) : null,
    );
    const openUsage = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, "open usage", icon("chevron-r"));
    openUsage.addEventListener("click", () => navigate("/usage"));
    return el("div", {},
        el("div", { class: "grid cols-3" },
            stat("requests 24h", formatCount(requests.total ?? 0), `${formatCount(requests.rate_1min ?? 0)}/min now`),
            stat("errors", formatCount(requests.errors ?? 0), requests.total ? `${((requests.errors / requests.total) * 100).toFixed(1)}% of total` : null),
            stat("tokens", formatCount(tokens.total ?? 0)),
            stat("spend", spend.nano_usd || !spend.requests_without_cost ? formatNanoUSD(spend.nano_usd ?? 0) : "—",
                spend.requests_without_cost ? `${formatCount(spend.requests_without_cost)} without cost` : null),
            stat("latency p50", formatMs(latency.p50 ?? NaN)),
            stat("latency p95", formatMs(latency.p95 ?? NaN)),
        ),
        el("div", { class: "stats-foot" }, openUsage),
    );
}

function renderProvidersCard(status) {
    const providers = status?.providers || [];
    const usable = providers.filter((p) => p.operator_credential?.usable).length;
    const configured = providers.filter((p) => p.operator_credential?.state !== "not_configured").length;
    const link = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, "open providers", icon("chevron-r"));
    link.addEventListener("click", () => navigate("/providers"));
    return el("div", { class: "card" },
        el("div", { class: "card-title" }, "Providers", link),
        el("div", { class: "grid cols-3" },
            el("div", { class: "stat" }, el("div", { class: "k" }, "known"), el("div", { class: "v" }, String(providers.length))),
            el("div", { class: "stat" }, el("div", { class: "k" }, "credentialed"), el("div", { class: "v" }, String(configured))),
            el("div", { class: "stat" }, el("div", { class: "k" }, "usable"), el("div", { class: "v" }, String(usable))),
        ),
    );
}

// renderCatalogCard shows catalog freshness with its two counters named
// distinctly: the catalog sequence counts accepted generations, and the
// availability revision counts provider availability flips.
function renderCatalogCard(metadata) {
    const link = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, "open models", icon("chevron-r"));
    link.addEventListener("click", () => navigate("/models"));
    const changesLink = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, "what changed");
    changesLink.addEventListener("click", () => openChangesPanel());
    const badges = freshnessBadges(metadata);
    return el("div", { class: "card" },
        el("div", { class: "card-title" }, "Starmap catalog", el("span", {}, changesLink, link)),
        badges.length ? el("div", { class: "freshness-badges" }, ...badges) : null,
        el("dl", { class: "kv" },
            el("dt", {}, "generation"),
            el("dd", { class: "mono", title: metadata.generation_id || "" }, shortGenerationID(metadata.generation_id)),
            el("dt", {}, "generated"),
            el("dd", { title: metadata.generated_at || "" }, metadata.generated_at ? formatRelativeTime(metadata.generated_at) : "—"),
            el("dt", {}, "catalog sequence"),
            el("dd", {}, String(metadata.catalog_sequence ?? "—")),
            el("dt", {}, "availability revision"),
            el("dd", {}, String(metadata.availability_revision ?? "—")),
        ),
    );
}

function connectCard() {
    const goSettings = el("button", { class: "btn btn-primary", type: "button" }, "Set API key");
    goSettings.addEventListener("click", () => navigate("/settings"));
    return el("div", { class: "card" },
        el("div", { class: "card-title" }, "Connect"),
        el("p", {}, "Set your gateway API key to unlock the model catalog, chat, providers, and key management."),
        el("div", {}, goSettings),
    );
}

function lockedCard(message) {
    return el("div", { class: "card" },
        el("div", { class: "locked-note" }, icon("alert"), message),
    );
}
