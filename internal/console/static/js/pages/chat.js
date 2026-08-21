// Chat — the playground. Conversations live in this browser's localStorage;
// the gateway only ever sees the completion requests.

import { getApiKey, listModels, listPresets, streamChat, completeChat } from "../api.js";
import { el, icon, toast, confirmDialog, promptDialog, modal, copyText, formatModelPrice, formatContext, formatCount, formatNanoUSD, formatMs, debounce } from "../ui.js";
import { renderMarkdown } from "../markdown.js";
import { navigate } from "../router.js";

export const title = "Chat";

const CHAT_STORAGE = "starport.chats";
const LEGACY_CHAT_STORAGE = "starport_chats";
const FAV_STORAGE = "starport.favModels";
const LAST_MODEL_STORAGE = "starport.lastModel";
const SIDEBAR_STORAGE = "starport.chatSidebar";
const MAX_CONVERSATIONS = 100;
const BASE_DOC_TITLE = "Chat · Starport Console";

// Per-conversation request parameters: sampling plus provider routing
// preferences (order/only/ignore are comma-separated provider lists).
const DEFAULT_PARAMS = {
    system: "", temperature: null, maxTokens: null,
    order: "", only: "", ignore: "", sort: "",
};

// --- Local persistence ---

function loadConversations() {
    try {
        const raw = localStorage.getItem(CHAT_STORAGE);
        if (raw) return JSON.parse(raw) || [];
        const legacy = localStorage.getItem(LEGACY_CHAT_STORAGE);
        if (legacy) {
            const parsed = JSON.parse(legacy);
            const converted = (Array.isArray(parsed) ? parsed : []).map((c) => ({
                id: c.id || crypto.randomUUID(),
                title: c.title || "Conversation",
                model: c.model || "",
                pinned: Boolean(c.pinned),
                params: {},
                messages: (c.messages || []).filter((m) => m?.role && typeof m.content === "string"),
                updatedAt: c.updatedAt || Date.now(),
            }));
            localStorage.setItem(CHAT_STORAGE, JSON.stringify(converted));
            localStorage.removeItem(LEGACY_CHAT_STORAGE);
            return converted;
        }
    } catch { /* corrupted store: start fresh */ }
    return [];
}

const saveConversations = debounce((conversations) => {
    try {
        localStorage.setItem(CHAT_STORAGE, JSON.stringify(conversations.slice(0, MAX_CONVERSATIONS)));
    } catch {
        toast("Could not save conversations — storage full?", "err");
    }
}, 300);

function loadFavorites() {
    try { return new Set(JSON.parse(localStorage.getItem(FAV_STORAGE) || "[]")); }
    catch { return new Set(); }
}

// groupLabel buckets a conversation for the sidebar, newest bucket first.
function groupLabel(convo) {
    if (convo.pinned) return "pinned";
    const now = new Date();
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
    const day = 86_400_000;
    const t = convo.updatedAt || 0;
    if (t >= today) return "today";
    if (t >= today - day) return "yesterday";
    if (t >= today - 7 * day) return "previous 7 days";
    if (t >= today - 30 * day) return "previous 30 days";
    return "older";
}
const GROUP_ORDER = ["pinned", "today", "yesterday", "previous 7 days", "previous 30 days", "older"];

// formatAge renders the X-Cache-Age header (seconds) compactly.
function formatAge(seconds) {
    const s = Number.parseInt(seconds, 10);
    if (!Number.isFinite(s)) return "";
    if (s < 60) return `${s}s`;
    if (s < 3600) return `${Math.round(s / 60)}m`;
    if (s < 86_400) return `${Math.round(s / 3600)}h`;
    return `${Math.round(s / 86_400)}d`;
}

// --- Page ---

export async function render(container) {
    if (!getApiKey()) {
        container.append(el("div", { class: "page" }, connectPrompt()));
        return;
    }

    const state = {
        conversations: loadConversations(),
        current: null,
        models: [],
        modelIndex: new Map(),
        favorites: loadFavorites(),
        model: localStorage.getItem(LAST_MODEL_STORAGE) || "",
        presets: [],
        params: { ...DEFAULT_PARAMS },
        streaming: false,
        generating: null,
        listQuery: "",
        abort: null,
        // Compare mode: one prompt fanned out to 2–4 models in parallel
        // streamed columns. Runs are ephemeral — never persisted.
        compare: { active: false, models: [], streaming: false, aborts: [] },
        disposed: false,
    };
    const disposers = [() => {
        state.disposed = true;
        if (state.abort) state.abort.abort();
        abortCompare();
        document.title = BASE_DOC_TITLE;
    }];

    // A model requested via /chat?model=… (from the Models page drawer).
    const requested = new URLSearchParams(location.search).get("model");
    if (requested) {
        state.model = requested;
        history.replaceState(null, "", "/chat");
    }

    // --- Skeleton ---
    const sideToggle = el("button", { class: "icon-btn chat-side-toggle", type: "button", "aria-label": "Toggle conversations" }, icon("menu"));
    const newBtn = el("button", { class: "btn btn-sm", type: "button" }, icon("plus"), "new chat");
    const searchInput = el("input", { class: "input chat-side-search", type: "search", placeholder: "Search chats…  ⌘K" });
    const listHost = el("div", { class: "chat-list" });
    const side = el("aside", { class: "chat-side" },
        el("div", { class: "chat-side-head" }, newBtn, searchInput),
        listHost,
    );

    const pickerBtn = el("button", { class: "btn btn-sm model-picker-btn", type: "button", "aria-haspopup": "listbox" },
        el("span", { class: "model-name" }, "select model"), icon("chevron-d"));
    const picker = el("div", { class: "model-picker" }, pickerBtn);
    const priceHint = el("span", { class: "model-price-hint" });
    const compareChips = el("span", { class: "compare-chips" });
    const compareBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button", "aria-pressed": "false" }, icon("usage"), "compare");
    const topbar = el("div", { class: "chat-topbar" }, sideToggle, picker, priceHint, compareChips, el("span", { class: "spacer" }), compareBtn);

    const thread = el("div", { class: "chat-thread" });
    const compareHost = el("div", { class: "compare-host" });
    compareHost.hidden = true;
    const scroll = el("div", { class: "chat-scroll" }, thread, compareHost);

    const textarea = el("textarea", { placeholder: "Message the gateway…  (Enter to send, Shift+Enter for newline)", rows: "1" });
    const sendBtn = el("button", { class: "composer-send", type: "button", "aria-label": "Send" }, icon("send"));
    const paramsBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, icon("settings"), "params");
    const paramsAnchor = el("span", { class: "params-anchor" }, paramsBtn);
    const routingBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, icon("providers"), "routing");
    const routingAnchor = el("span", { class: "params-anchor" }, routingBtn);
    const statsHost = el("span", { class: "stats" });
    const scrollBtn = el("button", { class: "scroll-btn", type: "button", "aria-label": "Scroll to bottom" }, icon("chevron-d"));
    const composer = el("div", { class: "composer-wrap" },
        scrollBtn,
        el("div", { class: "composer" },
            el("div", { class: "composer-box" }, textarea, sendBtn),
            el("div", { class: "composer-foot" }, paramsAnchor, routingAnchor, statsHost),
        ),
    );

    const main = el("div", { class: "chat-main" }, topbar, scroll, composer);
    const layout = el("div", { class: "chat-layout" }, side, main);
    if (localStorage.getItem(SIDEBAR_STORAGE) === "closed") layout.classList.add("side-collapsed");
    container.append(layout);

    // --- Wiring ---
    // Popover state must initialize before render returns; the statements
    // after the cleanup return never run, so a declaration there stays in
    // the temporal dead zone and the toggle handlers throw.
    let modelPop = null;
    let paramsPop = null;
    let routingPop = null;
    sideToggle.addEventListener("click", () => {
        // Mobile gets the overlay drawer; desktop collapses in place and
        // the choice persists across visits.
        if (window.matchMedia("(max-width: 860px)").matches) {
            side.classList.toggle("is-open");
            return;
        }
        const closed = layout.classList.toggle("side-collapsed");
        try { localStorage.setItem(SIDEBAR_STORAGE, closed ? "closed" : "open"); } catch { /* private mode */ }
    });
    const outsideSide = (event) => {
        if (!side.classList.contains("is-open")) return;
        if (side.contains(event.target) || sideToggle.contains(event.target)) return;
        side.classList.remove("is-open");
    };
    document.addEventListener("click", outsideSide);
    disposers.push(() => document.removeEventListener("click", outsideSide));

    // ⌘K / Ctrl+K opens full-text conversation search.
    const onGlobalKey = (event) => {
        if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
            event.preventDefault();
            openSearch();
        }
    };
    document.addEventListener("keydown", onGlobalKey);
    disposers.push(() => document.removeEventListener("keydown", onGlobalKey));

    newBtn.addEventListener("click", () => { setCompareMode(false); selectConversation(null); });
    pickerBtn.addEventListener("click", () => toggleModelPop());
    paramsBtn.addEventListener("click", () => toggleParamsPop());
    routingBtn.addEventListener("click", () => toggleRoutingPop());
    compareBtn.addEventListener("click", () => setCompareMode(!state.compare.active));

    searchInput.addEventListener("input", debounce(() => {
        state.listQuery = searchInput.value.trim().toLowerCase();
        drawList();
    }, 150));
    searchInput.addEventListener("keydown", (event) => {
        if (event.key === "Escape" && searchInput.value) {
            event.stopPropagation();
            searchInput.value = "";
            state.listQuery = "";
            drawList();
        }
    });

    textarea.addEventListener("input", autosize);
    textarea.addEventListener("keydown", (event) => {
        if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
            event.preventDefault();
            send();
        }
    });
    sendBtn.addEventListener("click", () => {
        if (state.compare.active) { state.compare.streaming ? abortCompare() : send(); return; }
        state.streaming ? state.abort?.abort() : send();
    });

    scrollBtn.addEventListener("click", () => {
        scroll.scrollTo({ top: scroll.scrollHeight, behavior: "smooth" });
        // Some engines ignore smooth scrollTo on overflow containers; make
        // sure the jump lands either way.
        setTimeout(() => {
            if (scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight > 160) {
                scroll.scrollTop = scroll.scrollHeight;
            }
            scrollBtn.classList.remove("is-visible");
        }, 350);
    });
    scroll.addEventListener("scroll", () => {
        const away = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight;
        scrollBtn.classList.toggle("is-visible", away > 160);
    });

    drawList();
    drawThread();
    updateStats();

    listModels().then((models) => {
        if (state.disposed) return;
        state.models = models;
        state.modelIndex = new Map(models.map((m) => [m.id, m]));
        if (!state.model || !state.modelIndex.has(state.model)) {
            const firstFav = [...state.favorites].find((id) => state.modelIndex.has(id));
            state.model = firstFav || models[0]?.id || "";
        }
        updatePicker();
        updateStats();
    }).catch((error) => {
        toast(`Failed to load models: ${error.message}`, "err");
    });

    listPresets().then((body) => {
        if (state.disposed) return;
        state.presets = body?.data || [];
    }).catch(() => { /* presets need storage and scope; the picker just omits them */ });

    return () => { for (const dispose of disposers) dispose(); };

    // --- Conversations ---

    function currentConversation() {
        return state.conversations.find((c) => c.id === state.current) || null;
    }

    function updateDocTitle() {
        const convo = currentConversation();
        document.title = convo ? `${convo.title} · Starport Console` : BASE_DOC_TITLE;
    }

    function selectConversation(id) {
        if (state.compare.active) setCompareMode(false);
        if (state.streaming) state.abort?.abort();
        state.current = id;
        const convo = currentConversation();
        if (convo?.model && state.modelIndex.has(convo.model)) state.model = convo.model;
        if (convo?.params) state.params = { ...DEFAULT_PARAMS, ...convo.params };
        drawList();
        drawThread();
        updatePicker();
        updateStats();
        updateDocTitle();
        side.classList.remove("is-open");
        textarea.focus();
    }

    function ensureConversation() {
        let convo = currentConversation();
        if (convo) return convo;
        convo = {
            id: crypto.randomUUID(),
            title: "New conversation",
            model: state.model,
            pinned: false,
            params: { ...state.params },
            messages: [],
            updatedAt: Date.now(),
        };
        state.conversations.unshift(convo);
        state.current = convo.id;
        return convo;
    }

    function touch(convo) {
        convo.updatedAt = Date.now();
        convo.model = state.model;
        convo.params = { ...state.params };
        state.conversations.sort((a, b) => b.updatedAt - a.updatedAt);
        saveConversations(state.conversations);
        drawList();
    }

    function drawList() {
        listHost.replaceChildren();
        const q = state.listQuery;
        const match = (c) => !q || c.title.toLowerCase().includes(q)
            || c.messages.some((m) => (m.content || "").toLowerCase().includes(q));
        const visible = state.conversations.filter(match);
        if (!visible.length) {
            listHost.append(el("div", { class: "chat-list-section" }, q ? "no matches" : "no conversations"));
            return;
        }
        const groups = new Map();
        for (const convo of visible) {
            const label = groupLabel(convo);
            if (!groups.has(label)) groups.set(label, []);
            groups.get(label).push(convo);
        }
        for (const label of GROUP_ORDER) {
            const rows = groups.get(label);
            if (!rows?.length) continue;
            listHost.append(el("div", { class: `chat-list-section${label === "pinned" ? " is-pinned" : ""}` }, label));
            for (const convo of rows) listHost.append(itemNode(convo));
        }
    }

    function itemNode(convo) {
        const pinBtn = el("button", {
            class: `icon-btn pin-btn${convo.pinned ? " is-pinned" : ""}`,
            type: "button",
            "aria-label": convo.pinned ? `Unpin ${convo.title}` : `Pin ${convo.title}`,
        }, icon("pin"));
        pinBtn.addEventListener("click", (event) => {
            event.stopPropagation();
            convo.pinned = !convo.pinned;
            saveConversations(state.conversations);
            drawList();
        });
        const renameBtn = el("button", { class: "icon-btn", type: "button", "aria-label": `Rename ${convo.title}` }, icon("edit"));
        renameBtn.addEventListener("click", async (event) => {
            event.stopPropagation();
            const name = await promptDialog({ title: "Rename conversation", value: convo.title });
            if (!name || name === convo.title) return;
            convo.title = name;
            saveConversations(state.conversations);
            drawList();
            if (state.current === convo.id) updateDocTitle();
        });
        const delBtn = el("button", { class: "icon-btn danger", type: "button", "aria-label": `Delete ${convo.title}` }, icon("trash"));
        delBtn.addEventListener("click", async (event) => {
            event.stopPropagation();
            const ok = await confirmDialog({
                title: "Delete conversation",
                message: `Delete "${convo.title}"?`,
                confirmLabel: "Delete",
                danger: true,
            });
            if (!ok) return;
            state.conversations = state.conversations.filter((c) => c.id !== convo.id);
            saveConversations(state.conversations);
            if (state.current === convo.id) selectConversation(null);
            else drawList();
        });
        const trailing = state.generating === convo.id
            ? el("span", { class: "chat-item-spin" }, icon("refresh"))
            : el("span", { class: "actions" }, pinBtn, renameBtn, delBtn);
        const item = el("div", { class: `chat-item${convo.id === state.current ? " is-active" : ""}${convo.pinned ? " is-pinned" : ""}` },
            el("span", { class: "title" }, convo.title),
            trailing,
        );
        item.addEventListener("click", () => selectConversation(convo.id));
        return item;
    }

    // --- Conversation search (⌘K) ---

    function openSearch() {
        const input = el("input", { class: "input", type: "search", placeholder: "Search all conversations…" });
        const results = el("div", { class: "search-results" });
        let hits = [];
        let hilite = -1;

        const highlightText = (text, q) => {
            const out = [];
            let rest = text;
            for (;;) {
                const at = rest.toLowerCase().indexOf(q);
                if (at === -1 || !q) break;
                out.push(rest.slice(0, at), el("mark", {}, rest.slice(at, at + q.length)));
                rest = rest.slice(at + q.length);
            }
            out.push(rest);
            return out;
        };

        const paintHilite = () => {
            [...results.children].forEach((node, i) => node.classList.toggle("is-hilite", i === hilite));
            results.children[hilite]?.scrollIntoView({ block: "nearest" });
        };

        const jump = (hit) => {
            close();
            selectConversation(hit.convo.id);
            if (hit.msgIndex < 0) return;
            requestAnimationFrame(() => {
                const node = thread.children[hit.msgIndex];
                if (!node) return;
                node.scrollIntoView({ behavior: "smooth", block: "center" });
                node.classList.add("is-flash");
                setTimeout(() => node.classList.remove("is-flash"), 2000);
            });
        };

        const runSearch = () => {
            const q = input.value.trim().toLowerCase();
            results.replaceChildren();
            hits = [];
            hilite = -1;
            if (!q) return;
            for (const convo of state.conversations) {
                const titleHit = convo.title.toLowerCase().includes(q);
                let msgIndex = -1;
                let snippet = "";
                for (let i = 0; i < convo.messages.length; i++) {
                    const content = convo.messages[i].content || "";
                    const at = content.toLowerCase().indexOf(q);
                    if (at !== -1) {
                        msgIndex = i;
                        snippet = (at > 32 ? "…" : "") + content.slice(Math.max(0, at - 32), at + q.length + 56);
                        break;
                    }
                }
                if (!titleHit && msgIndex === -1) continue;
                hits.push({ convo, msgIndex, snippet });
            }
            hits.sort((a, b) => (b.convo.updatedAt || 0) - (a.convo.updatedAt || 0));
            if (!hits.length) {
                results.append(el("div", { class: "search-empty" }, "no matches"));
                return;
            }
            for (const hit of hits) {
                const node = el("div", { class: "search-hit" },
                    el("div", { class: "t" }, highlightText(hit.convo.title, q)),
                    hit.snippet ? el("div", { class: "s" }, highlightText(hit.snippet, q)) : null,
                );
                node.addEventListener("click", () => jump(hit));
                results.append(node);
            }
        };

        input.addEventListener("input", debounce(runSearch, 200));
        input.addEventListener("keydown", (event) => {
            if (event.key === "ArrowDown") { event.preventDefault(); hilite = Math.min(hilite + 1, hits.length - 1); paintHilite(); }
            else if (event.key === "ArrowUp") { event.preventDefault(); hilite = Math.max(hilite - 1, 0); paintHilite(); }
            else if (event.key === "Enter" && hilite >= 0 && hits[hilite]) { event.preventDefault(); jump(hits[hilite]); }
        });

        const close = modal({
            title: "Search conversations",
            body: el("div", { class: "search-box" }, input, results),
        });
        input.focus();
    }

    // --- Thread rendering ---

    function drawThread() {
        thread.replaceChildren();
        const convo = currentConversation();
        if (!convo || !convo.messages.length) {
            thread.append(el("div", { class: "chat-welcome" },
                el("span", { class: "glyph" }, icon("star")),
                el("h2", {}, "STARPORT CHAT"),
                el("p", {}, "Test any model in the catalog through your local gateway. Conversations never leave this browser."),
            ));
            return;
        }
        for (const message of convo.messages) thread.append(messageNode(message));
        scrollToEnd(true);
    }

    function messageNode(message) {
        if (message.role === "user") {
            return el("div", { class: "msg msg-user" },
                el("div", { class: "msg-head" }, el("span", { class: "who" }, "you")),
                el("div", { class: "msg-body" }, message.content),
            );
        }
        const body = el("div", { class: "msg-body" });
        const md = el("div", { class: "md" });
        if (message.reasoning) body.append(reasoningFold(message.reasoning, false, message.reasoningMs));
        body.append(md);
        if (message.error) {
            body.append(el("div", { class: "msg-error" }, message.error));
        }
        if (message.content) renderMarkdown(md, message.content);
        const node = el("div", { class: "msg msg-assistant" },
            el("div", { class: "msg-head" },
                el("span", { class: "who" }, "assistant"),
                message.model ? el("span", {}, message.model) : null,
            ),
            body,
            messageFoot(message),
        );
        return node;
    }

    function reasoningFold(text, open = false, thoughtMs = null) {
        const bodyEl = el("div", { class: "reasoning-body" }, text);
        const labelEl = el("span", {}, thoughtMs ? `thought for ${(thoughtMs / 1000).toFixed(1)}s` : "reasoning");
        const spin = el("span", { class: "reasoning-spin" }, icon("refresh"));
        spin.hidden = true;
        const summary = el("summary", {}, icon("reasoning"), labelEl, spin);
        const details = el("details", { class: "reasoning" }, summary, bodyEl);
        if (open) details.setAttribute("open", "");
        // The user's own toggle wins over streaming auto-collapse.
        summary.addEventListener("click", () => { details.userToggled = true; });
        details.update = (value) => {
            bodyEl.textContent = value;
            bodyEl.scrollTop = bodyEl.scrollHeight;
        };
        details.setThinking = (on) => { spin.hidden = !on; };
        details.setLabel = (value) => { labelEl.textContent = value; };
        return details;
    }

    function messageFoot(message) {
        const meta = el("span", { class: "meta" });
        const stats = message.stats || {};
        const badge = (text, tip, cls) => meta.append(el("span", { title: tip || null, class: cls || null }, text));
        if (stats.ttftMs) badge(`ttft ${formatMs(stats.ttftMs)}`, "Time to first token");
        else if (stats.ttft) badge(`ttft ${stats.ttft}`, "Time to first token");
        if (stats.latencyMs) badge(`${formatMs(stats.latencyMs)} total`, "Total generation time");
        if (stats.tps) badge(`${stats.tps} tok/s`, "Completion tokens per second");
        if (stats.promptTokens) badge(`↓${formatCount(stats.promptTokens)}`, "Prompt tokens");
        if (stats.completionTokens) badge(`↑${formatCount(stats.completionTokens)}`, "Completion tokens");
        else if (stats.tokens) badge(`${formatCount(stats.tokens)} tokens`, "Total tokens");
        if (stats.reasoningTokens) badge(`${formatCount(stats.reasoningTokens)} reasoning`, "Reasoning tokens");
        if (stats.cache) {
            const age = stats.cacheAge ? ` · ${formatAge(stats.cacheAge)}` : "";
            badge(`cache ${stats.cache.toLowerCase()}${age}`, stats.cache === "HIT" ? "Served from the response cache" : "Response cache miss", stats.cache === "HIT" ? "cache-hit" : null);
        }
        if (message.stopped) badge("stopped", "Generation stopped by you");
        if (stats.unenforced) {
            meta.append(el("span", {
                class: "unenforced",
                title: "Provider preference fields the gateway accepted but cannot enforce yet",
            }, `unenforced: ${stats.unenforced.split(",").join(", ")}`));
        }
        const copyBtn = el("button", { class: "icon-btn", type: "button", "aria-label": "Copy response" }, icon("copy"));
        copyBtn.addEventListener("click", () => copyText(message.content, copyBtn));
        const retryBtn = el("button", { class: "icon-btn", type: "button", "aria-label": "Retry" }, icon("refresh"));
        retryBtn.addEventListener("click", retry);
        return el("div", { class: "msg-foot" }, meta, copyBtn, retryBtn);
    }

    function scrollToEnd(force = false) {
        const nearBottom = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 120;
        if (force || nearBottom) scroll.scrollTop = scroll.scrollHeight;
    }

    // --- Sending ---

    async function send() {
        if (state.compare.active) { await compareSend(); return; }
        const text = textarea.value.trim();
        if (!text || state.streaming) return;
        if (!state.model) { toast("Pick a model first", "err"); return; }

        const convo = ensureConversation();
        convo.messages.push({ role: "user", content: text });
        if (convo.messages.length === 1) {
            convo.title = text.length > 44 ? `${text.slice(0, 44).trimEnd()}…` : text;
            updateDocTitle();
        }
        textarea.value = "";
        autosize();
        touch(convo);
        drawThread();
        await stream(convo);
    }

    async function retry() {
        if (state.streaming) return;
        const convo = currentConversation();
        if (!convo) return;
        // Drop trailing assistant turns so the last user message re-runs.
        while (convo.messages.length && convo.messages[convo.messages.length - 1].role !== "user") {
            convo.messages.pop();
        }
        if (!convo.messages.length) return;
        touch(convo);
        drawThread();
        await stream(convo);
    }

    async function stream(convo) {
        const message = { role: "assistant", content: "", reasoning: "", model: state.model, stats: {} };
        convo.messages.push(message);

        // Live message scaffold.
        const md = el("div", { class: "md" });
        const cursor = el("span", { class: "stream-cursor" });
        const thinking = el("div", { class: "thinking" },
            el("span", { class: "dots" }, el("i"), el("i"), el("i")),
            "thinking");
        const body = el("div", { class: "msg-body" }, thinking, md, cursor);
        let fold = null;
        const liveTps = el("span", { class: "live-tps", title: "Live estimated generation speed" });
        const liveNode = el("div", { class: "msg msg-assistant" },
            el("div", { class: "msg-head" }, el("span", { class: "who" }, "assistant"), el("span", {}, state.model), liveTps),
            body,
        );
        if (thread.querySelector(".chat-welcome")) thread.replaceChildren();
        thread.append(liveNode);
        scrollToEnd(true);

        state.streaming = true;
        state.generating = convo.id;
        drawList();
        state.abort = new AbortController();
        sendBtn.classList.add("is-stop");
        sendBtn.setAttribute("aria-label", "Stop");
        sendBtn.replaceChildren(icon("stop"));

        const started = performance.now();
        let firstDelta = 0;
        let reasoningStart = 0;
        let reasoningMs = 0;
        let lastPaint = 0;
        const arrived = () => {
            if (!firstDelta) {
                firstDelta = performance.now() - started;
                thinking.remove();
            }
        };
        // Live tok/s runs on a chars/4 estimate; the provider's real counts
        // replace it in the message footer when the stream ends.
        let lastTps = 0;
        const paintTps = () => {
            const now = performance.now();
            if (!firstDelta || now - lastTps < 250) return;
            lastTps = now;
            const seconds = (now - started - firstDelta) / 1000;
            if (seconds < 0.4) return;
            const estimate = Math.round((message.content.length + message.reasoning.length) / 4 / seconds);
            if (estimate > 0) liveTps.textContent = `~${estimate} tok/s`;
        };
        const paint = (final = false) => {
            const now = performance.now();
            if (!final && now - lastPaint < 120) return;
            lastPaint = now;
            renderMarkdown(md, message.content);
            paintTps();
            scrollToEnd();
        };

        const body_ = {
            model: state.model,
            messages: [
                ...(state.params.system ? [{ role: "system", content: state.params.system }] : []),
                ...convo.messages.slice(0, -1)
                    .filter((m) => m.content && !m.error)
                    .map((m) => ({ role: m.role, content: m.content })),
            ],
        };
        if (state.params.temperature !== null) body_.temperature = state.params.temperature;
        if (state.params.maxTokens !== null) body_.max_tokens = state.params.maxTokens;
        const provider = providerPreferences();
        if (provider) body_.provider = provider;

        try {
            const meta = await streamChat(body_, {
                signal: state.abort.signal,
                onDelta: (delta) => {
                    arrived();
                    // Content ends the visible reasoning phase: record the
                    // duration and fold the panel unless the user opened it.
                    if (fold && !reasoningMs) {
                        reasoningMs = performance.now() - started - reasoningStart;
                        fold.setThinking(false);
                        fold.setLabel(`thought for ${(reasoningMs / 1000).toFixed(1)}s`);
                        if (!fold.userToggled) fold.removeAttribute("open");
                    }
                    message.content += delta;
                    paint();
                },
                onReasoning: (delta) => {
                    arrived();
                    message.reasoning += delta;
                    if (!fold) {
                        reasoningStart = performance.now() - started;
                        fold = reasoningFold(message.reasoning, true);
                        fold.setThinking(true);
                        body.prepend(fold);
                    } else fold.update(message.reasoning);
                    paintTps();
                    scrollToEnd();
                },
            });
            const elapsed = (performance.now() - started) / 1000;
            const completionTokens = meta.usage?.completion_tokens;
            message.stats = {
                ttftMs: firstDelta || null,
                latencyMs: elapsed > 0 ? elapsed * 1000 : null,
                tps: completionTokens && elapsed > 0 ? Number((completionTokens / elapsed).toFixed(1)) : null,
                promptTokens: meta.usage?.prompt_tokens || null,
                completionTokens: completionTokens || null,
                reasoningTokens: meta.usage?.completion_tokens_details?.reasoning_tokens || null,
                tokens: meta.usage?.total_tokens || null,
                cache: meta.cache || null,
                cacheAge: meta.cacheAge || null,
                unenforced: meta.unenforced || null,
            };
            if (meta.model) message.model = meta.model;
        } catch (error) {
            if (error.name === "AbortError") {
                message.stopped = true;
                message.stats = {};
                if (!message.content && !message.reasoning) {
                    convo.messages.pop();
                }
            } else {
                message.error = error.unauthorized
                    ? "The gateway rejected your API key. Update it in Settings."
                    : error.budgetExhausted
                        ? `Budget exhausted — ${error.message} Raise or clear this key's budget on the Keys page.`
                        : error.rateLimited
                            ? `Rate limited — ${error.message} Retry shortly, or raise this key's request limit on the Keys page.`
                            : error.message;
            }
        } finally {
            if (fold && message.reasoning && !message.reasoningMs) {
                if (!reasoningMs) reasoningMs = performance.now() - started - reasoningStart;
                message.reasoningMs = reasoningMs;
            }
            state.streaming = false;
            state.generating = null;
            state.abort = null;
            sendBtn.classList.remove("is-stop");
            sendBtn.setAttribute("aria-label", "Send");
            sendBtn.replaceChildren(icon("send"));
            if (!message.reasoning) delete message.reasoning;
            touch(convo);
            drawThread();
            updateStats();
            // First successful exchange: replace the truncated title with a
            // model-written one. Fire-and-forget; failure keeps the fallback.
            if (!message.error && !message.stopped && message.content && convo.messages.length === 2) {
                generateTitle(convo);
            }
            if (!state.disposed) textarea.focus();
        }
    }

    // generateTitle asks the conversation's own model for a short title.
    async function generateTitle(convo) {
        const userText = convo.messages.find((m) => m.role === "user")?.content;
        if (!userText) return;
        try {
            const response = await completeChat({
                model: convo.model || state.model,
                max_tokens: 24,
                temperature: 0.2,
                messages: [{
                    role: "user",
                    content: `Write a title of at most six words for a conversation that starts with:\n\n${userText.slice(0, 500)}\n\nReply with the title only — no quotes, no punctuation at the end.`,
                }],
            });
            if (state.disposed) return;
            let generated = response?.choices?.[0]?.message?.content?.trim().replace(/^["'\s]+|["'\s.]+$/g, "");
            if (!generated || generated.includes("\n")) return;
            if (generated.length > 60) generated = `${generated.slice(0, 60).trimEnd()}…`;
            convo.title = generated;
            saveConversations(state.conversations);
            drawList();
            if (state.current === convo.id) updateDocTitle();
        } catch { /* keep the truncated title */ }
    }

    // --- Model picker ---

    function toggleModelPop() {
        if (modelPop) { closeModelPop(); return; }
        const search = el("input", { class: "input", type: "search", placeholder: "Search models…" });
        const list = el("div", { class: "model-pop-list", role: "listbox" });
        modelPop = el("div", { class: "model-pop" },
            el("div", { class: "model-pop-head" }, search),
            list,
        );
        picker.append(modelPop);
        search.focus();

        const fill = () => {
            const query = search.value.trim().toLowerCase();
            list.replaceChildren();
            const match = (m) => !query || m.id.toLowerCase().includes(query) || (m.name || "").toLowerCase().includes(query);
            const presetHit = (p) => !query || `@preset/${p.name}`.toLowerCase().includes(query)
                || (p.description || "").toLowerCase().includes(query);
            const presetRows = state.presets.filter(presetHit);
            if (presetRows.length) {
                list.append(el("div", { class: "model-pop-section" }, "presets"));
                for (const p of presetRows) list.append(presetOption(p));
            }
            const favs = state.models.filter((m) => state.favorites.has(m.id) && match(m));
            const rest = state.models.filter((m) => !state.favorites.has(m.id) && match(m));
            if (favs.length) {
                list.append(el("div", { class: "model-pop-section" }, "favorites"));
                for (const m of favs) list.append(option(m));
            }
            list.append(el("div", { class: "model-pop-section" }, favs.length ? "all models" : `${rest.length} models`));
            for (const m of rest.slice(0, 200)) list.append(option(m));
            if (rest.length > 200) list.append(el("div", { class: "model-pop-section" }, `+${rest.length - 200} more — keep typing`));
        };

        const option = (m) => {
            const fav = el("button", {
                class: `icon-btn fav${state.favorites.has(m.id) ? " is-fav" : ""}`,
                type: "button",
                "aria-label": state.favorites.has(m.id) ? "Unfavorite" : "Favorite",
            }, icon("star"));
            fav.addEventListener("click", (event) => {
                event.stopPropagation();
                if (state.favorites.has(m.id)) state.favorites.delete(m.id);
                else state.favorites.add(m.id);
                try { localStorage.setItem(FAV_STORAGE, JSON.stringify([...state.favorites])); } catch { /* full */ }
                fill();
            });
            const opt = el("div", { class: "model-opt", role: "option" },
                el("span", { class: "name" }, m.name || m.id, el("small", {}, m.id)),
                m.context_length ? el("span", { class: "ctx" }, `ctx ${formatContext(m.context_length)}`) : null,
                el("span", { class: "price" }, formatModelPrice(m.pricing) || "—"),
                fav,
            );
            opt.addEventListener("click", () => {
                if (state.compare.active) { addCompareModel(m.id); return; }
                state.model = m.id;
                try { localStorage.setItem(LAST_MODEL_STORAGE, m.id); } catch { /* full */ }
                updatePicker();
                updateStats();
                closeModelPop();
            });
            return opt;
        };

        const presetOption = (p) => {
            const reference = `@preset/${p.name}`;
            const opt = el("div", { class: "model-opt", role: "option" },
                el("span", { class: "name" }, reference, el("small", {}, p.description || p.config?.model || "stored preset")),
                el("span", { class: "price" }, p.config?.provider?.sort || ""),
            );
            opt.addEventListener("click", () => {
                if (state.compare.active) { addCompareModel(reference); return; }
                state.model = reference;
                try { localStorage.setItem(LAST_MODEL_STORAGE, reference); } catch { /* full */ }
                updatePicker();
                updateStats();
                closeModelPop();
            });
            return opt;
        };

        search.addEventListener("input", debounce(fill, 100));
        fill();

        const onKey = (event) => { if (event.key === "Escape") closeModelPop(); };
        const onOutside = (event) => { if (!picker.contains(event.target)) closeModelPop(); };
        document.addEventListener("keydown", onKey);
        document.addEventListener("mousedown", onOutside);
        modelPop.dispose = () => {
            document.removeEventListener("keydown", onKey);
            document.removeEventListener("mousedown", onOutside);
        };
        disposers.push(closeModelPop);
    }

    function closeModelPop() {
        if (!modelPop) return;
        modelPop.dispose?.();
        modelPop.remove();
        modelPop = null;
    }

    function updatePicker() {
        if (state.compare.active) {
            pickerBtn.querySelector(".model-name").textContent = `add model (${state.compare.models.length}/4)`;
            priceHint.textContent = "";
            return;
        }
        const m = state.modelIndex.get(state.model);
        pickerBtn.querySelector(".model-name").textContent = m ? (m.name || m.id) : (state.model || "select model");
        priceHint.textContent = m ? (formatModelPrice(m.pricing) || "") : "";
    }

    // --- Params popover ---

    function toggleParamsPop() {
        if (paramsPop) { closeParamsPop(); return; }
        const systemInput = el("textarea", { class: "input", rows: "3", placeholder: "You are a helpful assistant…" });
        systemInput.value = state.params.system;
        const tempInput = el("input", { class: "input", type: "number", step: "0.1", min: "0", max: "2", placeholder: "default" });
        if (state.params.temperature !== null) tempInput.value = state.params.temperature;
        const maxInput = el("input", { class: "input", type: "number", min: "1", placeholder: "default" });
        if (state.params.maxTokens !== null) maxInput.value = state.params.maxTokens;

        const apply = () => {
            state.params.system = systemInput.value.trim();
            const temp = Number.parseFloat(tempInput.value);
            state.params.temperature = Number.isFinite(temp) ? temp : null;
            const max = Number.parseInt(maxInput.value, 10);
            state.params.maxTokens = Number.isFinite(max) && max > 0 ? max : null;
            const convo = currentConversation();
            if (convo) { convo.params = { ...state.params }; saveConversations(state.conversations); }
        };
        for (const input of [systemInput, tempInput, maxInput]) input.addEventListener("change", apply);

        paramsPop = el("div", { class: "params-pop" },
            el("div", { class: "field" }, el("label", {}, "System prompt"), systemInput),
            el("div", { class: "field" }, el("label", {}, "Temperature"), tempInput),
            el("div", { class: "field" }, el("label", {}, "Max tokens"), maxInput),
        );
        paramsAnchor.append(paramsPop);
        const onKey = (event) => { if (event.key === "Escape") closeParamsPop(); };
        const onOutside = (event) => { if (!paramsAnchor.contains(event.target)) closeParamsPop(); };
        document.addEventListener("keydown", onKey);
        document.addEventListener("mousedown", onOutside);
        paramsPop.dispose = () => {
            apply();
            document.removeEventListener("keydown", onKey);
            document.removeEventListener("mousedown", onOutside);
        };
        disposers.push(closeParamsPop);
    }

    function closeParamsPop() {
        if (!paramsPop) return;
        paramsPop.dispose?.();
        paramsPop.remove();
        paramsPop = null;
    }

    // --- Provider routing popover ---

    // providerPreferences builds the request's provider object from the
    // conversation's routing params; null when nothing is set.
    function providerPreferences() {
        const list = (value) => (value || "").split(",").map((s) => s.trim()).filter(Boolean);
        const prefs = {};
        const order = list(state.params.order);
        const only = list(state.params.only);
        const ignore = list(state.params.ignore);
        if (order.length) prefs.order = order;
        if (only.length) prefs.only = only;
        if (ignore.length) prefs.ignore = ignore;
        if (state.params.sort) prefs.sort = state.params.sort;
        return Object.keys(prefs).length ? prefs : null;
    }

    function toggleRoutingPop() {
        if (routingPop) { closeRoutingPop(); return; }
        const sortSelect = el("select", { class: "select" },
            el("option", { value: "" }, "server default"),
            el("option", { value: "price" }, "price — cheapest first"),
            el("option", { value: "latency" }, "latency — fastest first"),
            el("option", { value: "throughput" }, "throughput — routed by latency"),
        );
        sortSelect.value = state.params.sort || "";
        const orderInput = el("input", { class: "input mono", type: "text", placeholder: "e.g. groq, openai" });
        orderInput.value = state.params.order || "";
        const onlyInput = el("input", { class: "input mono", type: "text", placeholder: "allowlist, comma-separated" });
        onlyInput.value = state.params.only || "";
        const ignoreInput = el("input", { class: "input mono", type: "text", placeholder: "denylist, comma-separated" });
        ignoreInput.value = state.params.ignore || "";

        const apply = () => {
            state.params.sort = sortSelect.value;
            state.params.order = orderInput.value.trim();
            state.params.only = onlyInput.value.trim();
            state.params.ignore = ignoreInput.value.trim();
            const convo = currentConversation();
            if (convo) { convo.params = { ...state.params }; saveConversations(state.conversations); }
        };
        for (const input of [sortSelect, orderInput, onlyInput, ignoreInput]) input.addEventListener("change", apply);

        routingPop = el("div", { class: "params-pop" },
            el("div", { class: "field" }, el("label", {}, "Sort"), sortSelect),
            el("div", { class: "field" }, el("label", {}, "Provider order"), orderInput),
            el("div", { class: "field" }, el("label", {}, "Only providers"), onlyInput),
            el("div", { class: "field" }, el("label", {}, "Ignore providers"), ignoreInput),
        );
        routingAnchor.append(routingPop);
        const onKey = (event) => { if (event.key === "Escape") closeRoutingPop(); };
        const onOutside = (event) => { if (!routingAnchor.contains(event.target)) closeRoutingPop(); };
        document.addEventListener("keydown", onKey);
        document.addEventListener("mousedown", onOutside);
        routingPop.dispose = () => {
            apply();
            document.removeEventListener("keydown", onKey);
            document.removeEventListener("mousedown", onOutside);
        };
        disposers.push(closeRoutingPop);
    }

    function closeRoutingPop() {
        if (!routingPop) return;
        routingPop.dispose?.();
        routingPop.remove();
        routingPop = null;
    }

    // --- Compare mode ---

    // setCompareMode swaps the single-model thread for the ephemeral
    // comparison view. Single-model conversations are untouched.
    function setCompareMode(active) {
        if (state.compare.active === active) return;
        if (!active) abortCompare();
        state.compare.active = active;
        compareBtn.classList.toggle("is-active", active);
        compareBtn.setAttribute("aria-pressed", String(active));
        thread.hidden = active;
        compareHost.hidden = !active;
        if (active && !state.compare.models.length && state.model) {
            state.compare.models = [state.model];
        }
        drawCompareChips();
        if (active && !compareHost.childElementCount) {
            compareHost.append(el("div", { class: "chat-welcome" },
                el("span", { class: "glyph" }, icon("usage")),
                el("h2", {}, "COMPARE MODELS"),
                el("p", {}, "Pick two to four models, then send one prompt to race them side by side. Comparisons stay on this page and are not saved."),
            ));
        }
        updatePicker();
        textarea.focus();
    }

    function addCompareModel(id) {
        if (state.compare.models.includes(id)) { toast("Model already in the comparison", "err"); return; }
        if (state.compare.models.length >= 4) { toast("Compare holds at most four models", "err"); return; }
        state.compare.models.push(id);
        drawCompareChips();
        updatePicker();
    }

    function drawCompareChips() {
        compareChips.replaceChildren();
        if (!state.compare.active) return;
        for (const id of state.compare.models) {
            const remove = el("button", { class: "icon-btn", type: "button", "aria-label": `Remove ${id}` }, icon("close"));
            remove.addEventListener("click", () => {
                state.compare.models = state.compare.models.filter((m) => m !== id);
                drawCompareChips();
                updatePicker();
            });
            const m = state.modelIndex.get(id);
            compareChips.append(el("span", { class: "compare-chip" }, m?.name || id, remove));
        }
    }

    function abortCompare() {
        for (const controller of state.compare.aborts) controller.abort();
        state.compare.aborts = [];
    }

    // compareCost prices a run from the catalog snapshot; null means the
    // catalog has no pricing for this model, which the column must say.
    function compareCost(modelId, usage) {
        const pricing = state.modelIndex.get(modelId)?.pricing;
        const promptRate = Number.parseFloat(pricing?.prompt);
        const completionRate = Number.parseFloat(pricing?.completion);
        if (!usage || !Number.isFinite(promptRate) || !Number.isFinite(completionRate)) return null;
        return (usage.prompt_tokens || 0) * promptRate + (usage.completion_tokens || 0) * completionRate;
    }

    async function compareSend() {
        const text = textarea.value.trim();
        if (!text || state.compare.streaming) return;
        const models = [...state.compare.models];
        if (models.length < 2) { toast("Pick at least two models to compare", "err"); return; }
        textarea.value = "";
        autosize();

        compareHost.querySelector(".chat-welcome")?.remove();
        const grid = el("div", { class: "compare-grid" });
        // Set through the CSSOM: some environments drop style attributes,
        // which would silently collapse the grid to the two-column fallback.
        grid.style.setProperty("--compare-cols", String(models.length));
        compareHost.append(
            el("div", { class: "msg msg-user" },
                el("div", { class: "msg-head" }, el("span", { class: "who" }, "you")),
                el("div", { class: "msg-body" }, text),
            ),
            grid,
        );
        scroll.scrollTop = scroll.scrollHeight;

        state.compare.streaming = true;
        state.compare.aborts = models.map(() => new AbortController());
        sendBtn.classList.add("is-stop");
        sendBtn.setAttribute("aria-label", "Stop");
        sendBtn.replaceChildren(icon("stop"));

        const baseBody = {
            messages: [
                ...(state.params.system ? [{ role: "system", content: state.params.system }] : []),
                { role: "user", content: text },
            ],
        };
        if (state.params.temperature !== null) baseBody.temperature = state.params.temperature;
        if (state.params.maxTokens !== null) baseBody.max_tokens = state.params.maxTokens;
        const provider = providerPreferences();
        if (provider) baseBody.provider = provider;

        await Promise.allSettled(models.map((id, i) => compareColumn(grid, id, { ...baseBody, model: id }, state.compare.aborts[i])));

        state.compare.streaming = false;
        state.compare.aborts = [];
        sendBtn.classList.remove("is-stop");
        sendBtn.setAttribute("aria-label", "Send");
        sendBtn.replaceChildren(icon("send"));
        if (!state.disposed) textarea.focus();
    }

    async function compareColumn(grid, modelId, body, controller) {
        const md = el("div", { class: "md" });
        const cursor = el("span", { class: "stream-cursor" });
        const colBody = el("div", { class: "compare-col-body" }, md, cursor);
        const foot = el("div", { class: "compare-col-foot" });
        const liveTps = el("span", { class: "live-tps", title: "Live estimated generation speed" });
        const column = el("div", { class: "compare-col" },
            el("div", { class: "compare-col-head" }, el("span", { class: "mono" }, modelId), liveTps),
            colBody,
            foot,
        );
        grid.append(column);

        const started = performance.now();
        let firstDelta = 0;
        let content = "";
        let lastPaint = 0;
        let lastTps = 0;
        const paintTps = () => {
            const now = performance.now();
            if (!firstDelta || now - lastTps < 250) return;
            lastTps = now;
            const seconds = (now - started - firstDelta) / 1000;
            if (seconds < 0.4) return;
            const estimate = Math.round(content.length / 4 / seconds);
            if (estimate > 0) liveTps.textContent = `~${estimate} tok/s`;
        };
        const paint = (final = false) => {
            const now = performance.now();
            if (!final && now - lastPaint < 120) return;
            lastPaint = now;
            renderMarkdown(md, content);
            paintTps();
        };

        try {
            const meta = await streamChat(body, {
                signal: controller.signal,
                onDelta: (delta) => {
                    if (!firstDelta) firstDelta = performance.now() - started;
                    content += delta;
                    paint();
                },
                onReasoning: () => {
                    if (!firstDelta) firstDelta = performance.now() - started;
                },
            });
            paint(true);
            if (!content) md.append(el("em", {}, "The model returned no content."));
            const elapsed = (performance.now() - started) / 1000;
            const usedProvider = (meta.model || "").split("/")[0];
            const cost = compareCost(modelId, meta.usage);
            const parts = [];
            if (usedProvider && !modelId.startsWith("@preset/")) parts.push(`via ${usedProvider}`);
            if (firstDelta) parts.push(`ttft ${(firstDelta / 1000).toFixed(2)}s`);
            parts.push(`${elapsed.toFixed(1)}s total`);
            if (meta.usage?.completion_tokens && elapsed > 0) {
                parts.push(`${(meta.usage.completion_tokens / elapsed).toFixed(1)} tok/s`);
            }
            if (meta.usage?.total_tokens) parts.push(`${formatCount(meta.usage.total_tokens)} tokens`);
            parts.push(cost !== null ? formatNanoUSD(cost * 1_000_000_000) : "no pricing");
            foot.replaceChildren(...parts.map((part) => el("span", {}, part)));
        } catch (error) {
            paint(true);
            if (error.name !== "AbortError") {
                colBody.append(el("div", { class: "msg-error" }, error.message));
                foot.replaceChildren(el("span", {}, "failed"));
            } else {
                foot.replaceChildren(el("span", {}, "stopped"));
            }
        } finally {
            cursor.remove();
            // The foot's measured tok/s replaces the live estimate.
            liveTps.remove();
        }
    }

    // --- Composer helpers ---

    function autosize() {
        textarea.style.height = "auto";
        textarea.style.height = `${Math.min(textarea.scrollHeight, 224)}px`;
    }

    function updateStats() {
        statsHost.replaceChildren();
        const m = state.modelIndex.get(state.model);
        if (m?.context_length) statsHost.append(el("span", {}, `ctx ${formatContext(m.context_length)}`));
        const convo = currentConversation();
        if (!convo?.messages.length) return;
        statsHost.append(el("span", {}, `${convo.messages.length} msgs`));
        // Conversation totals from reported usage; "*" marks turns where the
        // provider stream reported no usage, so the totals are partial.
        let down = 0;
        let up = 0;
        let cost = 0;
        let priced = true;
        let missing = false;
        for (const msg of convo.messages) {
            if (msg.role !== "assistant" || msg.error) continue;
            const s = msg.stats || {};
            const prompt = s.promptTokens || 0;
            const completion = s.completionTokens || 0;
            if (!prompt && !completion && !s.tokens) { missing = true; continue; }
            down += prompt;
            up += completion;
            const pricing = state.modelIndex.get(msg.model)?.pricing;
            const promptRate = Number.parseFloat(pricing?.prompt);
            const completionRate = Number.parseFloat(pricing?.completion);
            if (Number.isFinite(promptRate) && Number.isFinite(completionRate)) {
                cost += prompt * promptRate + completion * completionRate;
            } else priced = false;
        }
        const note = missing ? "*" : "";
        if (down || up) {
            statsHost.append(el("span", { title: "Prompt ↓ / completion ↑ tokens this conversation" }, `↓${formatCount(down)} ↑${formatCount(up)} tok${note}`));
            statsHost.append(el("span", { title: missing ? "Partial — some responses reported no usage" : "Priced from the catalog snapshot" }, `cost ${priced ? formatNanoUSD(cost * 1_000_000_000) : "—"}${note}`));
        } else if (missing) {
            statsHost.append(el("span", { title: "The provider stream reported no usage for this conversation" }, "no usage*"));
        }
    }
}

function connectPrompt() {
    const goSettings = el("button", { class: "btn btn-primary", type: "button" }, "Set API key");
    goSettings.addEventListener("click", () => navigate("/settings"));
    return el("div", { class: "empty" },
        icon("chat"),
        el("p", {}, "Set your gateway API key to start chatting."),
        goSettings,
    );
}
