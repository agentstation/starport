// Starport Console — shared UI utilities: DOM building, toasts, modals,
// confirmation dialogs, clipboard, and formatters.

// --- DOM ---

export function el(tag, attrs = {}, ...children) {
    const node = document.createElement(tag);
    for (const [name, value] of Object.entries(attrs)) {
        if (value === undefined || value === null || value === false) continue;
        if (name === "class") node.className = value;
        else if (name === "dataset") Object.assign(node.dataset, value);
        else if (name.startsWith("on") && typeof value === "function") {
            node.addEventListener(name.slice(2), value);
        } else if (value === true) node.setAttribute(name, "");
        else node.setAttribute(name, value);
    }
    append(node, children);
    return node;
}

function append(node, children) {
    for (const child of children) {
        if (child === undefined || child === null || child === false) continue;
        if (Array.isArray(child)) append(node, child);
        else if (typeof child === "string" || typeof child === "number") {
            node.append(String(child));
        } else node.append(child);
    }
}

// icon returns an <svg><use> reference into the shell's sprite.
export function icon(name, cls) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    if (cls) svg.setAttribute("class", cls);
    const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    use.setAttribute("href", `#i-${name}`);
    svg.append(use);
    return svg;
}

export function clear(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
}

// --- Toasts ---

export function toast(message, kind = "info", { timeout = 4200 } = {}) {
    const host = document.getElementById("toasts");
    const iconName = kind === "ok" ? "check" : kind === "err" ? "alert" : "refresh";
    const node = el("div", { class: `toast toast-${kind}` }, icon(iconName), el("div", {}, message));
    host.append(node);
    const remove = () => {
        node.classList.add("is-leaving");
        setTimeout(() => node.remove(), 240);
    };
    node.addEventListener("click", remove);
    if (timeout) setTimeout(remove, timeout);
    return node;
}

// --- Modals ---

// modal opens a dialog. parts: {title, body, foot, wide}. Returns close().
export function modal({ title, body, foot, wide = false, onClose }) {
    const closeBtn = el("button", { class: "icon-btn", type: "button", "aria-label": "Close" }, icon("close"));
    const box = el("div", { class: `modal${wide ? " modal-wide" : ""}`, role: "dialog", "aria-modal": "true" },
        el("div", { class: "modal-head" }, el("h2", {}, title), closeBtn),
        el("div", { class: "modal-body" }, body),
        foot ? el("div", { class: "modal-foot" }, foot) : null,
    );
    const scrim = el("div", { class: "modal-scrim" }, box);
    const close = () => {
        scrim.remove();
        document.removeEventListener("keydown", onKey);
        if (onClose) onClose();
    };
    const onKey = (event) => { if (event.key === "Escape") close(); };
    scrim.addEventListener("mousedown", (event) => { if (event.target === scrim) close(); });
    closeBtn.addEventListener("click", close);
    document.addEventListener("keydown", onKey);
    document.body.append(scrim);
    const focusable = box.querySelector("input, textarea, select, button:not(.icon-btn)");
    if (focusable) focusable.focus();
    return close;
}

// sidePanel slides in a right-hand drawer with a title, body, and action.
// Returns close().
export function sidePanel(titleText, body, action) {
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

// confirmDialog replaces window.confirm with a styled, promise-based dialog.
export function confirmDialog({ title = "Confirm", message, confirmLabel = "Confirm", danger = false }) {
    return new Promise((resolve) => {
        let done = false;
        const finish = (value) => { if (!done) { done = true; close(); resolve(value); } };
        const okBtn = el("button", { class: `btn ${danger ? "btn-danger" : "btn-primary"}`, type: "button" }, confirmLabel);
        const cancelBtn = el("button", { class: "btn btn-ghost", type: "button" }, "Cancel");
        okBtn.addEventListener("click", () => finish(true));
        cancelBtn.addEventListener("click", () => finish(false));
        const close = modal({
            title,
            body: el("p", {}, message),
            foot: [cancelBtn, okBtn],
            onClose: () => { if (!done) { done = true; resolve(false); } },
        });
        okBtn.focus();
    });
}

// --- Clipboard ---

export async function copyText(text, trigger) {
    try {
        await navigator.clipboard.writeText(text);
        if (trigger) {
            trigger.classList.add("is-copied");
            setTimeout(() => trigger.classList.remove("is-copied"), 1200);
        }
        return true;
    } catch {
        toast("Copy failed — clipboard unavailable", "err");
        return false;
    }
}

export function copyButton(getText, label = "") {
    const btn = el("button", { class: "copy-btn", type: "button", title: "Copy" }, icon("copy"), label);
    btn.addEventListener("click", (event) => {
        event.stopPropagation();
        const text = typeof getText === "function" ? getText() : getText;
        copyText(text, btn);
    });
    return btn;
}

// --- Formatters ---

// formatPricePerM renders a per-token dollar string (as returned by the
// gateway, e.g. "8e-07") as dollars per million tokens.
export function formatPricePerM(perTokenString) {
    const perToken = Number.parseFloat(perTokenString);
    if (!Number.isFinite(perToken)) return null;
    const perMillion = perToken * 1_000_000;
    if (perMillion === 0) return "$0";
    if (perMillion >= 100) return `$${perMillion.toFixed(0)}`;
    if (perMillion >= 1) return `$${perMillion.toFixed(2).replace(/\.?0+$/, "")}`;
    return `$${perMillion.toPrecision(2).replace(/\.?0+$/, "")}`;
}

export function formatModelPrice(pricing) {
    if (!pricing) return null;
    const prompt = formatPricePerM(pricing.prompt);
    const completion = formatPricePerM(pricing.completion);
    if (prompt === null && completion === null) return null;
    return `${prompt ?? "—"} in / ${completion ?? "—"} out per 1M`;
}

// formatNanoUSD renders the gateway's exact nano-USD cost unit as dollars.
// Small per-request costs keep enough precision to stay meaningful.
export function formatNanoUSD(nanoUSD) {
    const dollars = Number(nanoUSD) / 1_000_000_000;
    if (!Number.isFinite(dollars)) return "—";
    if (dollars === 0) return "$0";
    if (dollars >= 1) return `$${dollars.toFixed(2)}`;
    if (dollars >= 0.001) return `$${dollars.toFixed(4).replace(/0+$/, "").replace(/\.$/, "")}`;
    return `$${dollars.toPrecision(2)}`;
}

export function formatCount(value) {
    if (value === null || value === undefined) return "—";
    const number = Number(value);
    if (!Number.isFinite(number)) return String(value);
    if (number >= 1_000_000) return `${(number / 1_000_000).toFixed(1)}M`;
    if (number >= 10_000) return `${(number / 1_000).toFixed(1)}K`;
    return number.toLocaleString("en-US");
}

export function formatContext(tokens) {
    if (!tokens) return "—";
    if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(tokens % 1_000_000 ? 1 : 0)}M`;
    if (tokens >= 1_000) return `${Math.round(tokens / 1_000)}K`;
    return String(tokens);
}

export function formatMs(ms) {
    if (!Number.isFinite(ms)) return "—";
    if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
    return `${Math.round(ms)}ms`;
}

export function formatRelativeTime(iso) {
    const then = new Date(iso).getTime();
    if (!Number.isFinite(then)) return "—";
    const seconds = Math.round((Date.now() - then) / 1000);
    if (seconds < 60) return "just now";
    const minutes = Math.round(seconds / 60);
    if (minutes < 60) return `${minutes}m ago`;
    const hours = Math.round(minutes / 60);
    if (hours < 24) return `${hours}h ago`;
    const days = Math.round(hours / 24);
    if (days < 30) return `${days}d ago`;
    return new Date(iso).toLocaleDateString();
}

export function debounce(fn, wait = 150) {
    let timer;
    return (...args) => {
        clearTimeout(timer);
        timer = setTimeout(() => fn(...args), wait);
    };
}

// downloadFile hands the browser a client-generated file.
export function downloadFile(name, content, type = "application/octet-stream") {
    const blob = new Blob([content], { type });
    const url = URL.createObjectURL(blob);
    const link = el("a", { href: url, download: name });
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
}
