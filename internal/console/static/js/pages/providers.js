// Providers — upstream posture: adapter state, operator credential health,
// and per-offering availability from the gateway's runtime projection.

import { getApiKey, listProviders, providerStatus, refreshProviders } from "../api.js";
import { el, icon, toast, formatCount } from "../ui.js";
import { navigate } from "../router.js";

export const title = "Providers";

export async function render(container) {
    const page = el("div", { class: "page" });
    container.append(page);

    if (!getApiKey()) {
        page.append(connectPrompt());
        return;
    }

    const refreshBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, icon("refresh"), "refresh");
    const listHost = el("div", { class: "grid cols-2" });
    page.append(
        el("div", { class: "page-head page-head-row" },
            el("div", {},
                el("h1", {}, "Providers"),
                el("p", { class: "sub" }, "Upstream services this gateway can route to, and whether it can reach them."),
            ),
            refreshBtn,
        ),
        listHost,
    );
    listHost.append(el("div", { class: "loading-row" }, el("span", { class: "spinner" }), "Loading providers…"));

    let disposed = false;

    refreshBtn.addEventListener("click", async () => {
        refreshBtn.disabled = true;
        try {
            await refreshProviders();
            toast("Provider refresh triggered", "ok");
            await load();
        } catch (error) {
            if (error.unauthorized || error.forbidden) toast("Refresh needs an admin-scoped key", "err");
            else toast(`Refresh failed: ${error.message}`, "err");
        } finally {
            refreshBtn.disabled = false;
        }
    });

    await load();

    async function load() {
        let catalog = [];
        let status = null;
        try {
            const body = await listProviders();
            catalog = body?.data?.providers ?? body?.providers ?? [];
        } catch { /* fall through; admin status may still work */ }
        try {
            status = await providerStatus();
        } catch (error) {
            if (disposed) return;
            if (error.unauthorized || error.forbidden) {
                drawCatalogOnly(catalog);
                return;
            }
            listHost.replaceChildren(el("div", { class: "empty" }, icon("alert"), el("p", {}, `Failed to load providers: ${error.message}`)));
            return;
        }
        if (disposed) return;
        drawStatus(status, catalog);
    }

    function drawStatus(status, catalog) {
        const byId = new Map((catalog || []).map((p) => [p.id, p]));
        const providers = [...(status?.providers || [])].sort((a, b) => {
            const rank = (p) => (p.operator_credential?.usable ? 0 : p.operator_credential?.state === "not_configured" ? 2 : 1);
            return rank(a) - rank(b) || a.provider_id.localeCompare(b.provider_id);
        });
        if (!providers.length) {
            listHost.replaceChildren(el("div", { class: "empty" }, icon("providers"), el("p", {}, "No providers in this catalog snapshot.")));
            return;
        }
        listHost.replaceChildren(...providers.map((p) => providerCard(p, byId.get(p.provider_id))));
    }

    function providerCard(p, catalogEntry) {
        const cred = p.operator_credential || {};
        const offerings = p.offerings || [];
        const available = offerings.filter((o) => o.state === "available").length;
        const ledState = cred.usable ? "ok" : cred.state === "not_configured" ? "off" : "err";
        const credLabel = cred.usable ? "credential ok"
            : cred.state === "not_configured" ? "no credential"
            : (cred.state || "unknown").replaceAll("_", " ");

        const track = el("div", { class: "offer-track", title: `${available} of ${offerings.length} offerings available` });
        for (const offering of offerings.slice(0, 40)) {
            track.append(el("span", {
                class: `offer-dot ${offering.state === "available" ? "ok" : ""}`,
                title: offering.provider_model_id,
            }));
        }
        if (offerings.length > 40) track.append(el("span", { class: "offer-more" }, `+${offerings.length - 40}`));

        const card = el("div", { class: "card provider-card" },
            el("div", { class: "provider-head" },
                el("span", { class: "led", "data-state": ledState }),
                el("div", {},
                    el("div", { class: "provider-name" }, catalogEntry?.name || p.provider_id),
                    el("div", { class: "provider-id mono" }, p.provider_id),
                ),
                el("span", { class: `badge ${cred.usable ? "badge-ok" : cred.state === "not_configured" ? "" : "badge-err"}` }, credLabel),
            ),
            cred.reason && !cred.usable && cred.state !== "not_configured"
                ? el("p", { class: "provider-reason" }, cred.reason)
                : null,
            el("div", { class: "provider-meta" },
                el("span", {}, `${formatCount(offerings.length)} offerings`),
                el("span", {}, `${formatCount(available)} available`),
                p.adapter?.state ? el("span", {}, `adapter ${p.adapter.state}`) : null,
            ),
            offerings.length ? track : null,
        );
        return card;
    }

    function drawCatalogOnly(catalog) {
        if (!catalog?.length) {
            listHost.replaceChildren(el("div", { class: "empty" }, icon("alert"),
                el("p", {}, "Provider status needs an admin-scoped key.")));
            return;
        }
        listHost.replaceChildren(
            el("div", { class: "locked-note card" }, icon("alert"),
                "Credential and availability detail needs an admin-scoped key. Showing the catalog view."),
            ...catalog.map((p) => el("div", { class: "card provider-card" },
                el("div", { class: "provider-head" },
                    el("span", { class: "led", "data-state": "unknown" }),
                    el("div", {},
                        el("div", { class: "provider-name" }, p.name || p.id),
                        el("div", { class: "provider-id mono" }, p.id),
                    ),
                ),
                el("div", { class: "provider-meta" },
                    el("span", {}, `${formatCount((p.models || []).length)} models`),
                ),
            )),
        );
    }

    return () => { disposed = true; };
}

function connectPrompt() {
    const goSettings = el("button", { class: "btn btn-primary", type: "button" }, "Set API key");
    goSettings.addEventListener("click", () => navigate("/settings"));
    return el("div", { class: "empty" },
        icon("providers"),
        el("p", {}, "Set your gateway API key to inspect providers."),
        goSettings,
    );
}
