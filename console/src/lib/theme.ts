// Theme state. Dark is the design-first default on :root; the .light
// class applies the light palette (DESIGN.md, implementation mapping).
// The saved choice wins; "system" follows prefers-color-scheme.

export type ThemeChoice = "dark" | "light" | "system";

const STORAGE_KEY = "starport.theme";
const lightQuery = "(prefers-color-scheme: light)";

export function savedTheme(): ThemeChoice {
  const raw = localStorage.getItem(STORAGE_KEY);
  return raw === "dark" || raw === "light" || raw === "system" ? raw : "system";
}

function resolve(choice: ThemeChoice): "dark" | "light" {
  if (choice === "system") {
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
  localStorage.setItem(STORAGE_KEY, choice);
  applyTheme(choice);
}

// Bootstrap: apply before first paint (imported from main.tsx, which
// runs before render) and track system changes while on "system".
export function initTheme(): void {
  applyTheme(savedTheme());
  window.matchMedia(lightQuery).addEventListener("change", () => {
    if (savedTheme() === "system") {
      applyTheme("system");
    }
  });
}
