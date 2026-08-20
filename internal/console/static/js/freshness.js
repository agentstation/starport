// Catalog freshness — shared rendering for snapshot metadata and the
// generation-to-generation diff. Used by the models page bar and the overview
// card. Staleness and degradation are rendered loudly, never hidden.

import { catalogChanges } from "./api.js";
import { el, icon, sidePanel, formatRelativeTime, formatPricePerM } from "./ui.js";

// A catalog older than a week is worth flagging: an embedded bootstrap
// snapshot ships with the binary and can predate the install by releases.
const STALE_AFTER_SECONDS = 7 * 24 * 3600;

export function shortGenerationID(id) {
    if (!id) return "—";
    return id.length > 22 ? `${id.slice(0, 20)}…` : id;
}

// freshnessBadges returns the loud part of the surface: completeness when the
// generation is not complete, degradation with its reasons, and staleness.
export function freshnessBadges(metadata) {
    const badges = [];
    if (metadata.completeness && metadata.completeness !== "complete") {
        badges.push(el("span", { class: "badge badge-warn", title: "This generation does not cover every source." }, metadata.completeness));
    }
    if (metadata.degraded) {
        const reasons = (metadata.degradation_reasons || []).join("; ") || "no reason recorded";
        badges.push(el("span", { class: "badge badge-err", title: reasons }, icon("alert"), "degraded"));
    }
    if ((metadata.age_seconds ?? 0) > STALE_AFTER_SECONDS) {
        badges.push(el("span", { class: "badge badge-warn", title: "Refresh the catalog to pick up a newer generation." }, "stale"));
    }
    if (!metadata.manifest_available) {
        badges.push(el("span", { class: "badge", title: metadata.manifest_unavailable_reason || "" }, "no manifest"));
    }
    return badges;
}

// counterText names the two catalog counters distinctly. The sequence counts
// accepted generations; the availability revision counts provider
// availability flips. They were conflated in one "rev" display before.
export function counterText(metadata) {
    return `catalog sequence ${metadata.catalog_sequence ?? "—"} · availability revision ${metadata.availability_revision ?? "—"}`;
}

export function openChangesPanel() {
    const body = el("div", { class: "drawer-body" },
        el("div", { class: "loading-row" }, el("span", { class: "spinner" }), "Loading catalog changes…"));
    sidePanel("Catalog changes", body);
    catalogChanges().then((diff) => {
        body.replaceChildren(...changesContent(diff));
    }).catch((error) => {
        const message = error.unauthorized || error.forbidden
            ? "Catalog changes need a key with the models:read scope."
            : `Failed to load catalog changes: ${error.message}`;
        body.replaceChildren(el("p", { class: "muted" }, message));
    });
}

function changesContent(diff) {
    if (!diff?.available) {
        return [el("p", { class: "muted" }, diff?.reason || "No generation history to compare yet. Refresh the catalog twice to build one.")];
    }
    const header = el("div", { class: "kv-inline mono muted" },
        `${shortGenerationID(diff.from_generation_id)} → ${shortGenerationID(diff.to_generation_id)}`,
        diff.to_generated_at ? ` · ${formatRelativeTime(diff.to_generated_at)}` : "",
    );
    if (diff.semantically_equal) {
        return [header, el("p", {},
            "The last two generations are semantically equal: no models, offerings, or prices changed. Only acquisition metadata differs.")];
    }
    const sections = [header];
    const modelList = (title, ids, cls) => {
        if (!ids?.length) return;
        sections.push(el("div", { class: "card-title" }, `${title} (${ids.length})`));
        sections.push(el("ul", { class: `change-list ${cls}` }, ids.map((id) => el("li", { class: "mono" }, id))));
    };
    modelList("Models added", diff.models_added, "is-add");
    modelList("Models removed", diff.models_removed, "is-remove");

    const byProvider = groupOfferingsByProvider(diff);
    if (byProvider.size) {
        sections.push(el("div", { class: "card-title" }, "Offerings by provider"));
        for (const [provider, entry] of byProvider) {
            const parts = [];
            if (entry.added.length) parts.push(`+${entry.added.length} added`);
            if (entry.removed.length) parts.push(`−${entry.removed.length} removed`);
            sections.push(el("div", { class: "offer" },
                el("span", { class: "mono" }, provider),
                el("span", { class: "muted" }, parts.join(" · ")),
            ));
            for (const change of entry.added) sections.push(offeringRow(change, "is-add", "+"));
            for (const change of entry.removed) sections.push(offeringRow(change, "is-remove", "−"));
        }
    }

    if (diff.price_changes?.length) {
        sections.push(el("div", { class: "card-title" }, `Price changes (${diff.price_changes.length})`));
        const tbody = el("tbody", {});
        for (const change of diff.price_changes) {
            tbody.append(el("tr", {},
                el("td", {}, el("div", { class: "cell-id mono" }, `${change.provider}/${change.provider_model_id}`)),
                el("td", {}, change.field),
                el("td", { class: "num" }, `${formatPerM(change.previous_per_1m)} → ${formatPerM(change.current_per_1m)}`),
            ));
        }
        sections.push(el("div", { class: "table-wrap" },
            el("table", { class: "table" },
                el("thead", {}, el("tr", {}, el("th", {}, "offering"), el("th", {}, "field"), el("th", { class: "num" }, "per 1M"))),
                tbody,
            )));
    }
    if (sections.length === 1) {
        sections.push(el("p", { class: "muted" }, "No model, offering, or price differences."));
    }
    return sections;
}

function offeringRow(change, cls, sign) {
    return el("div", { class: `change-offering ${cls} mono` }, `${sign} ${change.provider_model_id}`,
        el("span", { class: "muted" }, ` (${change.definition_id})`));
}

function groupOfferingsByProvider(diff) {
    const byProvider = new Map();
    const entry = (provider) => {
        if (!byProvider.has(provider)) byProvider.set(provider, { added: [], removed: [] });
        return byProvider.get(provider);
    };
    for (const change of diff.offerings_added || []) entry(change.provider).added.push(change);
    for (const change of diff.offerings_removed || []) entry(change.provider).removed.push(change);
    return new Map([...byProvider.entries()].sort((a, b) => a[0].localeCompare(b[0])));
}

// Diff prices arrive as per-1M numbers already; formatPricePerM expects a
// per-token string, so convert through it only for its formatting rules.
function formatPerM(perM) {
    const formatted = formatPricePerM(String((perM ?? 0) / 1_000_000));
    return formatted ?? "$0";
}
