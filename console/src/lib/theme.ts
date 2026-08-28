// Theme state. Dark is the design-first default on :root; the .light
// class applies the light palette (DESIGN.md, implementation mapping).
// The saved choice wins; "system" follows prefers-color-scheme.

export type ThemeChoice = "dark" | "light" | "system";

const STORAGE_KEY = "starport.theme";
const lightQuery = "(prefers-color-scheme: light)";

// Storage access stays defensive: a test DOM or a locked-down browser
// context may not provide localStorage, and "system" is a safe default.
export function savedTheme(): ThemeChoice {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(STORAGE_KEY);
  } catch {
    // Fall through to the default.
  }
  return raw === "dark" || raw === "light" || raw === "system" ? raw : "system";
}

function resolve(choice: ThemeChoice): "dark" | "light" {
  if (choice === "system") {
    // A DOM without matchMedia (test environments) gets the design-first
    // dark default.
    if (typeof window.matchMedia !== "function") return "dark";
    return window.matchMedia(lightQuery).matches ? "light" : "dark";
  }
  return choice;
}

export function appliedTheme(): "dark" | "light" {
  return resolve(savedTheme());
}

export function applyTheme(choice: ThemeChoice): void {
  document.documentElement.classList.toggle("light", resolve(choice) === "light");
}

export function setTheme(choice: ThemeChoice): void {
  try {
    localStorage.setItem(STORAGE_KEY, choice);
  } catch {
    // The choice still applies for this page via applyTheme below.
  }
  applyTheme(choice);
  for (const listener of themeListeners) listener();
}

// Subscribers (the shell toggle, the settings page) re-render when the
// choice changes from anywhere.
const themeListeners = new Set<() => void>();

export function onThemeChange(listener: () => void): () => void {
  themeListeners.add(listener);
  return () => themeListeners.delete(listener);
}

// Bootstrap: apply before first paint (imported from main.tsx, which
// runs before render) and track system changes while on "system".
export function initTheme(): void {
  applyTheme(savedTheme());
  window.matchMedia(lightQuery).addEventListener("change", () => {
    if (savedTheme() === "system") {
      applyTheme("system");
      for (const listener of themeListeners) listener();
    }
  });
}
