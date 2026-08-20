// Starport Console — application shell: routing, theme, navigation, and the
// gateway status indicator. Pages live in ./pages/*.js and export
// { title, render(container) => cleanup? }.

import { healthReady } from "./api.js";
import { onNavigate } from "./router.js";
import * as overview from "./pages/overview.js";
import * as chat from "./pages/chat.js";
import * as models from "./pages/models.js";
import * as providers from "./pages/providers.js";
import * as keys from "./pages/keys.js";
import * as usage from "./pages/usage.js";
import * as settings from "./pages/settings.js";

const ROUTES = {
    "/": { page: overview, id: "overview" },
    "/chat": { page: chat, id: "chat" },
    "/models": { page: models, id: "models" },
    "/providers": { page: providers, id: "providers" },
    "/keys": { page: keys, id: "keys" },
    "/usage": { page: usage, id: "usage" },
    "/settings": { page: settings, id: "settings" },
};

const stage = document.getElementById("stage");
const baseTitle = document.body.dataset.title || "Starport Console";
let cleanup = null;

function normalizePath(pathname) {
    if (pathname.length > 1 && pathname.endsWith("/")) return pathname.slice(0, -1);
    return pathname;
}

async function renderRoute() {
    const route = ROUTES[normalizePath(location.pathname)] || ROUTES["/"];
    if (typeof cleanup === "function") {
        try { cleanup(); } catch { /* page cleanup must not block navigation */ }
    }
    cleanup = null;
    for (const link of document.querySelectorAll(".nav-links a")) {
        const active = link.dataset.page === route.id;
        if (active) link.setAttribute("aria-current", "page");
        else link.removeAttribute("aria-current");
    }
    document.title = route.page.title ? `${route.page.title} · ${baseTitle}` : baseTitle;
    stage.replaceChildren();
    stage.scrollTop = 0;
    closeMobileNav();
    cleanup = await route.page.render(stage);
}

function navigate(path) {
    if (normalizePath(location.pathname) === normalizePath(path)) return;
    history.pushState(null, "", path);
    renderRoute();
}
onNavigate(navigate);

// Intercept same-origin nav links for SPA navigation; plain loads still work.
document.addEventListener("click", (event) => {
    const link = event.target.closest("a[data-nav]");
    if (!link || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    navigate(link.getAttribute("href"));
});
window.addEventListener("popstate", renderRoute);

// --- Theme ---

const themeToggle = document.getElementById("theme-toggle");
themeToggle.addEventListener("click", () => {
    const current = document.documentElement.dataset.theme === "light" ? "light" : "dark";
    const next = current === "light" ? "dark" : "light";
    document.documentElement.dataset.theme = next;
    try { localStorage.setItem("starport.theme", next); } catch { /* private mode */ }
});

// --- Mobile navigation ---

const nav = document.getElementById("nav");
const mobileToggle = document.getElementById("mobile-nav-toggle");
mobileToggle.addEventListener("click", () => nav.classList.toggle("is-open"));
function closeMobileNav() { nav.classList.remove("is-open"); }
document.addEventListener("click", (event) => {
    if (!nav.classList.contains("is-open")) return;
    if (nav.contains(event.target) || mobileToggle.contains(event.target)) return;
    closeMobileNav();
});

// --- Gateway status LED ---

const statusLed = document.querySelector("#nav-status .led");
const statusText = document.querySelector("#nav-status .nav-status-text");

async function pollHealth() {
    try {
        const ready = await healthReady();
        statusLed.dataset.state = ready ? "ok" : "warn";
        statusText.textContent = ready ? "gateway ready" : "not ready";
    } catch {
        statusLed.dataset.state = "err";
        statusText.textContent = "unreachable";
    }
}
pollHealth();
setInterval(pollHealth, 15000);

renderRoute();
