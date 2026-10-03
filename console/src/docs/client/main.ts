// The documentation client entry. The page loads it in the head without
// defer, so the saved theme applies before the first paint; the rest waits
// for the document.
import "../site.css";

import { applyTheme, savedTheme } from "../../lib/theme";
import { initDocs } from "./behavior";

applyTheme(savedTheme());

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => initDocs(document, window), { once: true });
} else {
  initDocs(document, window);
}
