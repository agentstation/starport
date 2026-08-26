import { createRootRoute, Outlet, redirect, useRouterState } from "@tanstack/react-router";

import { Shell } from "@/components/shell/Shell";
import { hasCredential } from "@/lib/api";

// AUTH_PATH is where a reader with no credential meets this gateway. It is the
// one route that renders outside the shell, because a shell is navigation to
// pages that would all answer 401.
export const AUTH_PATH = "/auth";

export const Route = createRootRoute({
  // The guard is here rather than in each route so a route cannot be added
  // without it, and it runs before loading rather than during rendering so a
  // sessionless browser never mounts a component that fetches. A redirect
  // issued from a render pass happens after the queries in that pass have
  // already gone out; the gateway then answers a burst of 401s to a reader who
  // was on the way to being told how to sign in.
  beforeLoad: ({ location }) => {
    if (location.pathname === AUTH_PATH) return;
    if (hasCredential()) return;
    // location.href is the path with its search and hash, so a reader who
    // followed a deep link comes back to it rather than to the overview.
    throw redirect({ to: AUTH_PATH, search: { next: location.href } });
  },
  component: RootLayout,
});

function RootLayout() {
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  if (pathname === AUTH_PATH) {
    return <Outlet />;
  }
  return (
    <Shell>
      <Outlet />
    </Shell>
  );
}
