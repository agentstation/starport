import { createRootRoute, Navigate, Outlet, redirect, useRouterState } from "@tanstack/react-router";

import { AUTH_PATH } from "@/components/auth/destination";
import { Shell } from "@/components/shell/Shell";
import { hasCredential } from "@/lib/api";
import { useGatewayAccessRejected } from "@/lib/useGatewayAccess";

export const Route = createRootRoute({
  // The guard is here rather than in each route so a route cannot be added
  // without it, and it runs before loading rather than during rendering so a
  // credentialless browser never mounts a component that fetches. A redirect
  // issued from a render pass happens after the queries in that pass have
  // already gone out; the gateway then answers a burst of 401s to a reader who
  // was on the way to being told what to present.
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
  const location = useRouterState({ select: (state) => state.location });
  const rejected = useGatewayAccessRejected();

  if (location.pathname === AUTH_PATH) {
    return <Outlet />;
  }
  // A credential that stops working is discovered by a fetch, not by a
  // navigation, so `beforeLoad` cannot catch it: it ran before the request that
  // learned the news and will not run again until the reader moves. Redirecting
  // from the render pass is right here and wrong above — the 401 this reacts to
  // has already happened, so there is no burst of requests left to prevent.
  if (rejected) {
    return <Navigate to={AUTH_PATH} search={{ next: location.href }} replace />;
  }
  return (
    <Shell>
      <Outlet />
    </Shell>
  );
}
