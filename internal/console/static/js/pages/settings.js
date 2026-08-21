// Settings — connection (API key), appearance, chat data, and about.

import { getApiKey, setApiKey, listModels, invalidateModels, systemInfo } from "../api.js";
import { el, icon, toast, confirmDialog, downloadFile } from "../ui.js";

export const title = "Settings";

const CHAT_STORAGE = "starport.chats";
const LEGACY_CHAT_STORAGE = "starport_chats";

export async function render(container) {
    const page = el("div", { class: "page" });
    container.append(page);
    page.append(
        el("div", { class: "page-head" },
            el("h1", {}, "Settings"),
            el("p", { class: "sub" }, "Everything lives in this browser — the gateway never stores console state."),
        ),
        connectionCard(),
        el("div", { class: "grid cols-2" }, appearanceCard(), chatDataCard()),
        aboutCard(),
    );
}

function connectionCard() {
    const current = getApiKey();
    const input = el("input", {
        class: "input mono",
        type: "password",
        placeholder: current ? "•".repeat(24) : "STARPORT_…",
        autocomplete: "off",
        "aria-label": "Gateway API key",
    });
    const revealBtn = el("button", { class: "icon-btn", type: "button", "aria-label": "Show key" }, icon("eye"));
    revealBtn.addEventListener("click", () => {
        const showing = input.type === "text";
        input.type = showing ? "password" : "text";
        revealBtn.replaceChildren(icon(showing ? "eye" : "eye-off"));
        if (!input.value && !showing) input.value = getApiKey();
    });

    const status = el("span", { class: "muted" }, current ? "key set" : "no key set");
    const saveBtn = el("button", { class: "btn btn-primary btn-sm", type: "button" }, "Save & test");
    const clearBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, "Clear");

    saveBtn.addEventListener("click", async () => {
        const value = input.value.trim();
        if (!value) { input.focus(); return; }
        saveBtn.disabled = true;
        const previous = getApiKey();
        setApiKey(value);
        invalidateModels();
        try {
            const models = await listModels({ fresh: true });
            status.textContent = `key valid · ${models.length} models visible`;
            toast("API key saved", "ok");
            input.value = "";
            input.type = "password";
            input.placeholder = "•".repeat(24);
        } catch (error) {
            setApiKey(previous);
            invalidateModels();
            status.textContent = "key rejected";
            toast(error.unauthorized ? "The gateway rejected that key" : `Test failed: ${error.message}`, "err");
        } finally {
            saveBtn.disabled = false;
        }
    });
    clearBtn.addEventListener("click", () => {
        setApiKey("");
        invalidateModels();
        input.value = "";
        input.placeholder = "STARPORT_…";
        status.textContent = "no key set";
        toast("API key cleared", "ok");
    });
    input.addEventListener("keydown", (event) => { if (event.key === "Enter") saveBtn.click(); });

    return el("div", { class: "card" },
        el("div", { class: "card-title" }, "Connection"),
        el("p", { class: "muted" },
            "The console talks to the gateway that served this page. The key is stored in this browser's localStorage only."),
        el("div", { class: "key-row" },
            el("div", { class: "key-input" }, input, revealBtn),
            saveBtn, clearBtn, status,
        ),
    );
}

function appearanceCard() {
    const themeButtons = ["dark", "light"].map((theme) => {
        const btn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, icon(theme === "dark" ? "moon" : "sun"), theme);
        const sync = () => btn.classList.toggle("is-active", (document.documentElement.dataset.theme || "dark") === theme);
        sync();
        btn.addEventListener("click", () => {
            document.documentElement.dataset.theme = theme;
            try { localStorage.setItem("starport.theme", theme); } catch { /* private mode */ }
            for (const other of themeButtons) other.dispatchEvent(new Event("theme-sync"));
        });
        btn.addEventListener("theme-sync", sync);
        return btn;
    });
    return el("div", { class: "card" },
        el("div", { class: "card-title" }, "Appearance"),
        el("div", { class: "btn-row" }, themeButtons),
    );
}

function chatDataCard() {
    const count = () => {
        try { return (JSON.parse(localStorage.getItem(CHAT_STORAGE) || "[]") || []).length; }
        catch { return 0; }
    };
    const label = el("span", { class: "muted" }, `${count()} conversations stored locally`);
    const exportBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, icon("download"), "export json");
    const clearBtn = el("button", { class: "btn btn-danger btn-sm", type: "button" }, icon("trash"), "delete all");
    exportBtn.addEventListener("click", () => {
        const raw = localStorage.getItem(CHAT_STORAGE) || "[]";
        downloadFile("starport-chats.json", raw, "application/json");
    });
    clearBtn.addEventListener("click", async () => {
        const ok = await confirmDialog({
            title: "Delete all conversations",
            message: "This removes every conversation stored in this browser. There is no undo.",
            confirmLabel: "Delete all",
            danger: true,
        });
        if (!ok) return;
        localStorage.removeItem(CHAT_STORAGE);
        localStorage.removeItem(LEGACY_CHAT_STORAGE);
        label.textContent = "0 conversations stored locally";
        toast("Conversations deleted", "ok");
    });
    return el("div", { class: "card" },
        el("div", { class: "card-title" }, "Chat data"),
        label,
        el("div", { class: "btn-row" }, exportBtn, clearBtn),
    );
}

function aboutCard() {
    const kv = el("dl", { class: "kv" },
        el("dt", {}, "gateway"),
        el("dd", {}, location.origin),
    );
    systemInfo().then((info) => {
        if (info?.version) kv.append(el("dt", {}, "version"), el("dd", {}, info.version));
        if (info?.storage?.type) kv.append(el("dt", {}, "storage"), el("dd", {}, info.storage.type));
    }).catch(() => {});
    const gh = el("a", { class: "btn btn-ghost btn-sm", href: "https://github.com/agentstation/starport", target: "_blank", rel: "noopener noreferrer" },
        icon("github"), "agentstation/starport", icon("external"));
    return el("div", { class: "card" },
        el("div", { class: "card-title" }, "About"),
        el("p", { class: "muted" }, "Starport is a local, open-source LLM gateway — an OpenRouter-compatible drop-in that routes against the Starmap catalog."),
        kv,
        el("div", { class: "btn-row" }, gh),
    );
}
