import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";

import { retryPolicy } from "./lib/queries";
import { initTheme } from "./lib/theme";
import { createConsoleRouter } from "./router";
import "./styles/app.css";

initTheme();

// The client defaults hold for every read the console makes. A record stays
// fresh for half a minute and stays cached for five, a gateway answer is never
// retried, and a window that regains focus does not refetch on its own: the
// screens that need a cadence set refetchInterval, and every mutation
// invalidates what it changed.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      gcTime: 5 * 60_000,
      retry: retryPolicy,
      refetchOnWindowFocus: false,
    },
  },
});

const router = createConsoleRouter(queryClient);

// A development build lazy-loads the devtools; a production build drops the
// import with the branch.
const Devtools = import.meta.env.DEV ? lazy(() => import("./devtools")) : null;

const rootElement = document.getElementById("root");
if (rootElement) {
  createRoot(rootElement).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
        {Devtools && (
          <Suspense fallback={null}>
            <Devtools router={router} />
          </Suspense>
        )}
      </QueryClientProvider>
    </StrictMode>,
  );
}
