import type { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  Navigate,
  Outlet,
  redirect,
  useRouterState,
} from "@tanstack/react-router";

import { AUTH_PATH } from "@/components/auth/destination";
import { Shell } from "@/components/shell/Shell";
import { hasCredential } from "@/lib/api";
import { useGatewayAccessRejected } from "@/lib/useGatewayAccess";

// Route loaders and page components share this query client.
export type RouterContext = { queryClient: QueryClient };

export const Route = createRootRouteWithContext<RouterContext>()({
  // Protect deployment routes before their loaders run. Static documentation
  // and the access page do not require a console session.
  beforeLoad: ({ location }) => {
    if (location.pathname === AUTH_PATH || location.pathname === "/docs") return;
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

  if (location.pathname === "/docs") {
    return <div className="documentation-shell"><Outlet /></div>;
  }
  if (location.pathname === AUTH_PATH) {
    return <Outlet />;
  }
  // Recheck access before the shell mounts during a public-to-protected
  // transition. Rejected credentials also require recovery between navigations.
  if (rejected || !hasCredential()) {
    return <Navigate to={AUTH_PATH} search={{ next: location.href }} replace />;
  }
  return (
    <Shell>
      <Outlet />
    </Shell>
  );
}
