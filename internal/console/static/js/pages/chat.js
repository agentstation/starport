// Chat — the playground. Conversations live in this browser's localStorage;
// the gateway only ever sees the completion requests.

import { getApiKey, listModels, streamChat } from "../api.js";
import { el, icon, toast, confirmDialog, copyText, formatModelPrice, formatContext, formatCount, debounce } from "../ui.js";
import { renderMarkdown } from "../markdown.js";
import { navigate } from "../router.js";

export const title = "Chat";

const CHAT_STORAGE = "starport.chats";
const LEGACY_CHAT_STORAGE = "starport_chats";
const FAV_STORAGE = "starport.favModels";
const LAST_MODEL_STORAGE = "starport.lastModel";
const MAX_CONVERSATIONS = 100;

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
        params: { system: "", temperature: null, maxTokens: null },
        streaming: false,
        abort: null,
        disposed: false,
    };
    const disposers = [() => { state.disposed = true; if (state.abort) state.abort.abort(); }];

    // A model requested via /chat?model=… (from the Models page drawer).
    const requested = new URLSearchParams(location.search).get("model");
    if (requested) {
        state.model = requested;
        history.replaceState(null, "", "/chat");
    }

    // --- Skeleton ---
    const sideToggle = el("button", { class: "icon-btn chat-side-toggle", type: "button", "aria-label": "Conversations" }, icon("menu"));
    const newBtn = el("button", { class: "btn btn-sm", type: "button" }, icon("plus"), "new chat");
    const listHost = el("div", { class: "chat-list" });
    const side = el("aside", { class: "chat-side" },
        el("div", { class: "chat-side-head" }, newBtn),
        listHost,
    );

    const pickerBtn = el("button", { class: "btn btn-sm model-picker-btn", type: "button", "aria-haspopup": "listbox" },
        el("span", { class: "model-name" }, "select model"), icon("chevron-d"));
    const picker = el("div", { class: "model-picker" }, pickerBtn);
    const priceHint = el("span", { class: "model-price-hint" });
    const topbar = el("div", { class: "chat-topbar" }, sideToggle, picker, priceHint, el("span", { class: "spacer" }));

    const thread = el("div", { class: "chat-thread" });
    const scroll = el("div", { class: "chat-scroll" }, thread);

    const textarea = el("textarea", { placeholder: "Message the gateway…  (Enter to send, Shift+Enter for newline)", rows: "1" });
    const sendBtn = el("button", { class: "composer-send", type: "button", "aria-label": "Send" }, icon("send"));
    const paramsBtn = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, icon("settings"), "params");
    const paramsAnchor = el("span", { class: "params-anchor" }, paramsBtn);
    const statsHost = el("span", { class: "stats" });
    const composer = el("div", { class: "composer-wrap" },
        el("div", { class: "composer" },
            el("div", { class: "composer-box" }, textarea, sendBtn),
            el("div", { class: "composer-foot" }, paramsAnchor, statsHost),
        ),
    );

    const main = el("div", { class: "chat-main" }, topbar, scroll, composer);
    container.append(el("div", { class: "chat-layout" }, side, main));

    // --- Wiring ---
    sideToggle.addEventListener("click", () => side.classList.toggle("is-open"));
    const outsideSide = (event) => {
        if (!side.classList.contains("is-open")) return;
        if (side.contains(event.target) || sideToggle.contains(event.target)) return;
        side.classList.remove("is-open");
    };
    document.addEventListener("click", outsideSide);
    disposers.push(() => document.removeEventListener("click", outsideSide));

    newBtn.addEventListener("click", () => selectConversation(null));
    pickerBtn.addEventListener("click", () => toggleModelPop());
    paramsBtn.addEventListener("click", () => toggleParamsPop());

    textarea.addEventListener("input", autosize);
    textarea.addEventListener("keydown", (event) => {
        if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
            event.preventDefault();
            send();
        }
    });
    sendBtn.addEventListener("click", () => { state.streaming ? state.abort?.abort() : send(); });

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

    return () => { for (const dispose of disposers) dispose(); };

    // --- Conversations ---

    function currentConversation() {
        return state.conversations.find((c) => c.id === state.current) || null;
    }

    function selectConversation(id) {
        if (state.streaming) state.abort?.abort();
        state.current = id;
        const convo = currentConversation();
        if (convo?.model && state.modelIndex.has(convo.model)) state.model = convo.model;
        if (convo?.params) state.params = { system: "", temperature: null, maxTokens: null, ...convo.params };
        drawList();
        drawThread();
        updatePicker();
        updateStats();
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
        if (!state.conversations.length) {
            listHost.append(el("div", { class: "chat-list-section" }, "no conversations"));
            return;
        }
        listHost.append(el("div", { class: "chat-list-section" }, "conversations"));
        for (const convo of state.conversations) {
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
            const item = el("div", { class: `chat-item${convo.id === state.current ? " is-active" : ""}` },
                el("span", { class: "title" }, convo.title),
                delBtn,
            );
            item.addEventListener("click", () => selectConversation(convo.id));
            listHost.append(item);
        }
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
        if (message.reasoning) body.append(reasoningFold(message.reasoning));
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

    function reasoningFold(text, open = false) {
        const bodyEl = el("div", { class: "reasoning-body" }, text);
        const details = el("details", { class: "reasoning" },
            el("summary", {}, icon("reasoning"), "reasoning"),
            bodyEl,
        );
        if (open) details.setAttribute("open", "");
        details.update = (value) => { bodyEl.textContent = value; };
        return details;
    }

    function messageFoot(message) {
        const meta = el("span", { class: "meta" });
        const stats = message.stats || {};
        if (stats.ttft) meta.append(el("span", {}, `ttft ${stats.ttft}`));
        if (stats.tps) meta.append(el("span", {}, `${stats.tps} tok/s`));
        if (stats.tokens) meta.append(el("span", {}, `${formatCount(stats.tokens)} tokens`));
        if (stats.cache === "HIT") meta.append(el("span", {}, "cache hit"));
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
        const text = textarea.value.trim();
        if (!text || state.streaming) return;
        if (!state.model) { toast("Pick a model first", "err"); return; }

        const convo = ensureConversation();
        convo.messages.push({ role: "user", content: text });
        if (convo.messages.length === 1) {
            convo.title = text.length > 44 ? `${text.slice(0, 44).trimEnd()}…` : text;
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
        const body = el("div", { class: "msg-body" }, md, cursor);
        let fold = null;
        const liveNode = el("div", { class: "msg msg-assistant" },
            el("div", { class: "msg-head" }, el("span", { class: "who" }, "assistant"), el("span", {}, state.model)),
            body,
        );
        if (thread.querySelector(".chat-welcome")) thread.replaceChildren();
        thread.append(liveNode);
        scrollToEnd(true);

        state.streaming = true;
        state.abort = new AbortController();
        sendBtn.classList.add("is-stop");
        sendBtn.setAttribute("aria-label", "Stop");
        sendBtn.replaceChildren(icon("stop"));

        const started = performance.now();
        let firstDelta = 0;
        let lastPaint = 0;
        const paint = (final = false) => {
            const now = performance.now();
            if (!final && now - lastPaint < 120) return;
            lastPaint = now;
            renderMarkdown(md, message.content);
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

        try {
            const meta = await streamChat(body_, {
                signal: state.abort.signal,
                onDelta: (delta) => {
                    if (!firstDelta) firstDelta = performance.now() - started;
                    message.content += delta;
                    paint();
                },
                onReasoning: (delta) => {
                    if (!firstDelta) firstDelta = performance.now() - started;
                    message.reasoning += delta;
                    if (!fold) {
                        fold = reasoningFold(message.reasoning, true);
                        body.prepend(fold);
                    } else fold.update(message.reasoning);
                    scrollToEnd();
                },
            });
            const elapsed = (performance.now() - started) / 1000;
            const completionTokens = meta.usage?.completion_tokens;
            message.stats = {
                ttft: firstDelta ? `${(firstDelta / 1000).toFixed(2)}s` : null,
                tps: completionTokens && elapsed > 0 ? (completionTokens / elapsed).toFixed(1) : null,
                tokens: meta.usage?.total_tokens || null,
                cache: meta.cache || null,
            };
            if (meta.model) message.model = meta.model;
        } catch (error) {
            if (error.name === "AbortError") {
                message.stats = { tokens: null };
                if (!message.content && !message.reasoning) {
                    convo.messages.pop();
                }
            } else {
                message.error = error.unauthorized
                    ? "The gateway rejected your API key. Update it in Settings."
                    : error.message;
            }
        } finally {
            state.streaming = false;
            state.abort = null;
            sendBtn.classList.remove("is-stop");
            sendBtn.setAttribute("aria-label", "Send");
            sendBtn.replaceChildren(icon("send"));
            if (!message.reasoning) delete message.reasoning;
            touch(convo);
            drawThread();
            updateStats();
            if (!state.disposed) textarea.focus();
        }
    }

    // --- Model picker ---

    let modelPop = null;
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
                el("span", { class: "price" }, formatModelPrice(m.pricing) || "—"),
                fav,
            );
            opt.addEventListener("click", () => {
                state.model = m.id;
                try { localStorage.setItem(LAST_MODEL_STORAGE, m.id); } catch { /* full */ }
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
        const m = state.modelIndex.get(state.model);
        pickerBtn.querySelector(".model-name").textContent = m ? (m.name || m.id) : (state.model || "select model");
        priceHint.textContent = m ? (formatModelPrice(m.pricing) || "") : "";
    }

    // --- Params popover ---

    let paramsPop = null;
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
        if (convo?.messages.length) statsHost.append(el("span", {}, `${convo.messages.length} msgs`));
        const lastStats = convo?.messages.findLast?.((msg) => msg.stats?.tokens)?.stats;
        if (lastStats?.tokens) statsHost.append(el("span", {}, `${formatCount(lastStats.tokens)} tok last`));
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
