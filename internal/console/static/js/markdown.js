// Starport Console — markdown rendering pipeline.
// marked (vendored ESM) → DOMPurify sanitize → Prism highlight.
// DOMPurify and Prism load as classic scripts and attach to window.
// KaTeX and Mermaid are vendored too, but load lazily on first use so pages
// without math or diagrams never pay for them.

import { Marked } from "../vendor/marked.esm.js";
import { el, icon, copyText } from "./ui.js";

const marked = new Marked({
    gfm: true,
    breaks: true,
});

// --- Math tokens ---
// A marked extension claims TeX spans before markdown escaping can mangle
// them (\[ → [, a_i → emphasis). The renderer emits a placeholder element
// holding the escaped TeX source; KaTeX typesets it after sanitization.

function escapeHtml(text) {
    return text
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;");
}

marked.use({
    extensions: [
        {
            name: "blockMath",
            level: "block",
            start(src) {
                const match = src.match(/\$\$|\\\[/);
                return match ? match.index : undefined;
            },
            tokenizer(src) {
                const match = /^\$\$([\s\S]+?)\$\$(?:\n+|$)/.exec(src)
                    || /^\\\[([\s\S]+?)\\\](?:\n+|$)/.exec(src);
                if (match) return { type: "blockMath", raw: match[0], text: match[1].trim() };
                return undefined;
            },
            renderer(token) {
                return `<div class="math math-block">${escapeHtml(token.text)}</div>\n`;
            },
        },
        {
            name: "inlineMath",
            level: "inline",
            start(src) {
                const match = src.match(/\$|\\\(/);
                return match ? match.index : undefined;
            },
            tokenizer(src) {
                // \( ... \) always; $...$ only when the content hugs both
                // dollars and no digit follows, so prices stay prose.
                const match = /^\\\(([\s\S]+?)\\\)/.exec(src)
                    || /^\$((?:[^\s$][^$\n]*?)?[^\s$])\$(?!\d)/.exec(src);
                if (match) return { type: "inlineMath", raw: match[0], text: match[1].trim() };
                return undefined;
            },
            renderer(token) {
                return `<span class="math math-inline">${escapeHtml(token.text)}</span>`;
            },
        },
    ],
});

const PURIFY_CONFIG = {
    ALLOW_UNKNOWN_PROTOCOLS: false,
    FORBID_TAGS: ["style", "form", "input", "button"],
    ADD_ATTR: ["target", "rel"],
};

// renderMarkdown parses, sanitizes, and highlights markdown into `target`.
export function renderMarkdown(target, source) {
    const html = marked.parse(source ?? "");
    const clean = window.DOMPurify ? window.DOMPurify.sanitize(html, PURIFY_CONFIG) : null;
    if (clean === null) {
        // Sanitizer unavailable: never inject unsanitized HTML.
        target.textContent = source ?? "";
        return;
    }
    target.innerHTML = clean;
    for (const link of target.querySelectorAll("a[href]")) {
        link.setAttribute("target", "_blank");
        link.setAttribute("rel", "noopener noreferrer");
    }
    enhanceCodeBlocks(target);
    renderMath(target);
}

// --- Lazy vendored assets ---

function loadScript(url) {
    return new Promise((resolve, reject) => {
        const node = document.createElement("script");
        node.src = url;
        node.onload = resolve;
        node.onerror = () => { node.remove(); reject(new Error(`failed to load ${url}`)); };
        document.head.append(node);
    });
}

function loadStyle(url) {
    return new Promise((resolve, reject) => {
        const node = document.createElement("link");
        node.rel = "stylesheet";
        node.href = url;
        node.onload = resolve;
        node.onerror = () => { node.remove(); reject(new Error(`failed to load ${url}`)); };
        document.head.append(node);
    });
}

let katexPromise = null;
function ensureKatex() {
    if (!katexPromise) {
        katexPromise = Promise.all([
            loadStyle(new URL("../vendor/katex.min.css", import.meta.url)),
            loadScript(new URL("../vendor/katex.min.js", import.meta.url)),
        ]).catch((err) => { katexPromise = null; throw err; });
    }
    return katexPromise;
}

let mermaidPromise = null;
function ensureMermaid() {
    if (!mermaidPromise) {
        mermaidPromise = loadScript(new URL("../vendor/mermaid.min.js", import.meta.url))
            .then(() => window.mermaid.initialize(mermaidConfig()))
            .catch((err) => { mermaidPromise = null; throw err; });
    }
    return mermaidPromise;
}

// --- KaTeX typesetting ---

function renderMath(root) {
    const nodes = root.querySelectorAll(".math");
    if (!nodes.length) return;
    if (window.katex) {
        for (const node of nodes) renderMathNode(node);
        return;
    }
    ensureKatex().then(() => {
        // The root may have re-rendered while KaTeX loaded; query again.
        for (const node of root.querySelectorAll(".math")) renderMathNode(node);
    }).catch(() => { /* raw TeX stays visible */ });
}

function renderMathNode(node) {
    if (node.dataset.mathRendered) return;
    const source = node.textContent;
    try {
        window.katex.render(source, node, {
            displayMode: node.classList.contains("math-block"),
            throwOnError: false,
            strict: "ignore",
        });
        node.dataset.mathRendered = "1";
    } catch {
        node.textContent = source;
    }
}

// --- Mermaid diagrams ---
// A ```mermaid fence stays a plain code block until its source parses as a
// complete diagram, so partial streams degrade gracefully.

const MERMAID_PURIFY = { USE_PROFILES: { svg: true, svgFilters: true, html: true } };
const mermaidSvgCache = new Map();
let mermaidSeq = 0;

function mermaidConfig() {
    const stamped = document.documentElement.dataset.theme;
    const dark = stamped ? stamped === "dark" : window.matchMedia("(prefers-color-scheme: dark)").matches;
    // SVG text labels, not foreignObject HTML: DOMPurify strips foreignObject,
    // and plain <text> nodes survive sanitization with the labels intact.
    return {
        startOnLoad: false,
        securityLevel: "strict",
        theme: dark ? "dark" : "neutral",
        fontFamily: "inherit",
        htmlLabels: false,
        flowchart: { htmlLabels: false },
    };
}

async function upgradeMermaid(wrap, source) {
    const key = source.trim();
    if (!key || !window.DOMPurify) return;
    try {
        await ensureMermaid();
    } catch {
        return;
    }
    if (!wrap.isConnected) return;
    let svg = mermaidSvgCache.get(key);
    if (svg === undefined) {
        try {
            const valid = await window.mermaid.parse(key, { suppressErrors: true });
            if (!valid) return;
            ({ svg } = await window.mermaid.render(`console-mmd-${++mermaidSeq}`, key));
        } catch {
            return;
        }
        if (mermaidSvgCache.size > 100) mermaidSvgCache.clear();
        mermaidSvgCache.set(key, svg);
    }
    if (!wrap.isConnected) return;
    const clean = window.DOMPurify.sanitize(svg, MERMAID_PURIFY);
    const holder = el("div", { class: "md-mermaid", dataset: { source: key } });
    holder.innerHTML = clean;
    wrap.replaceWith(holder);
}

// Re-theme rendered diagrams when the user flips the theme.
function refreshMermaidTheme() {
    if (!window.mermaid) return;
    window.mermaid.initialize(mermaidConfig());
    mermaidSvgCache.clear();
    for (const holder of document.querySelectorAll(".md-mermaid")) {
        const key = holder.dataset.source || "";
        if (!key) continue;
        window.mermaid.render(`console-mmd-${++mermaidSeq}`, key).then(({ svg }) => {
            mermaidSvgCache.set(key, svg);
            if (holder.isConnected && window.DOMPurify) {
                holder.innerHTML = window.DOMPurify.sanitize(svg, MERMAID_PURIFY);
            }
        }).catch(() => { /* keep the previous rendering */ });
    }
}

new MutationObserver(refreshMermaidTheme)
    .observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (!document.documentElement.dataset.theme) refreshMermaidTheme();
});

function enhanceCodeBlocks(root) {
    for (const pre of root.querySelectorAll("pre")) {
        if (pre.parentElement?.classList.contains("md-codeblock")) continue;
        const code = pre.querySelector("code");
        const language = languageOf(code);
        const wrap = el("div", { class: "md-codeblock" });
        pre.replaceWith(wrap);
        wrap.append(pre);
        if (language === "mermaid") {
            upgradeMermaid(wrap, code ? code.textContent : "");
        }
        if (language) wrap.append(el("span", { class: "md-lang" }, language));
        const btn = el("button", { class: "copy-btn", type: "button", title: "Copy code" }, icon("copy"));
        btn.addEventListener("click", () => copyText(code ? code.textContent : pre.textContent, btn));
        wrap.append(btn);
        if (code && language && window.Prism?.languages?.[language]) {
            code.classList.add(`language-${language}`);
            window.Prism.highlightElement(code);
        }
    }
}

function languageOf(code) {
    if (!code) return "";
    const match = /language-([\w-]+)/.exec(code.className || "");
    return match ? match[1].toLowerCase() : "";
}
