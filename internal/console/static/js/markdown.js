// Starport Console — markdown rendering pipeline.
// marked (vendored ESM) → DOMPurify sanitize → Prism highlight.
// DOMPurify and Prism load as classic scripts and attach to window.

import { Marked } from "../vendor/marked.esm.js";
import { el, icon, copyText } from "./ui.js";

const marked = new Marked({
    gfm: true,
    breaks: true,
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
}

function enhanceCodeBlocks(root) {
    for (const pre of root.querySelectorAll("pre")) {
        if (pre.parentElement?.classList.contains("md-codeblock")) continue;
        const code = pre.querySelector("code");
        const language = languageOf(code);
        const wrap = el("div", { class: "md-codeblock" });
        pre.replaceWith(wrap);
        wrap.append(pre);
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
