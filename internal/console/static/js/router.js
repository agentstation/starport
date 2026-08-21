// Router seam. Pages navigate through this module instead of importing the
// shell, which keeps the module graph acyclic: app.js registers its route
// renderer here at startup.

let handler = (path) => { location.assign(path); };

// onNavigate registers the shell's SPA navigation handler.
export function onNavigate(fn) { handler = fn; }

// navigate moves the console to a page path, e.g. "/models".
export function navigate(path) { handler(path); }
