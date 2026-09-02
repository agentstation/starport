import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import type { AnyRouter } from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";

// Devtools mounts the query and router inspectors. main.tsx loads this module
// in a development build only, so a production bundle carries neither.
export default function Devtools({ router }: { router: AnyRouter }) {
  return (
    <>
      <ReactQueryDevtools initialIsOpen={false} buttonPosition="bottom-left" />
      <TanStackRouterDevtools router={router} position="bottom-right" />
    </>
  );
}
