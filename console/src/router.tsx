import type { QueryClient } from "@tanstack/react-query";
import { createRouter, type RouterHistory } from "@tanstack/react-router";

import { RouteError, RouteNotFound, RoutePending } from "./components/shell/RouteStates";
import { routeTree } from "./routeTree.gen";

// createConsoleRouter builds the one router the console runs on, so a test
// that renders the route tree meets the same loaders, preload, and default
// components a browser does. The router carries the query client so a route
// loader warms its reads before the route renders. A hovered link runs that
// loader early, and the preload stale time is zero because the query client,
// not the router, owns how long a read stays fresh. The three default
// components own what a route shows while it loads, when it fails, and when
// it names nothing.
export function createConsoleRouter(queryClient: QueryClient, history?: RouterHistory) {
  return createRouter({
    routeTree,
    history,
    context: { queryClient },
    defaultPreload: "intent",
    defaultPreloadStaleTime: 0,
    scrollRestoration: true,
    defaultPendingComponent: RoutePending,
    defaultErrorComponent: RouteError,
    defaultNotFoundComponent: RouteNotFound,
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createConsoleRouter>;
  }
}
