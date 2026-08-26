import { createFileRoute, redirect } from "@tanstack/react-router";

import { FirstContact } from "@/components/auth/FirstContact";
import { destination } from "@/components/auth/destination";
import { hasCredential, isCredentialRejected } from "@/lib/api";

type AuthSearch = { next?: string };

export const Route = createFileRoute("/auth")({
  validateSearch: (search: Record<string, unknown>): AuthSearch =>
    typeof search.next === "string" ? { next: search.next } : {},
  // A reader who already has a working credential has no business on this page;
  // the root guard sent them here, or a stale bookmark did.
  //
  // Rejection has to be part of the test. A credential the gateway has already
  // refused still satisfies `hasCredential` — it is present, it is just no good
  // — so bouncing on presence alone would send a reader with a dead session
  // back to the page that redirected them here, and round again.
  beforeLoad: ({ search }) => {
    if (hasCredential() && !isCredentialRejected()) {
      throw redirect({ href: destination(search.next) });
    }
  },
  component: FirstContactRoute,
});

function FirstContactRoute() {
  const { next } = Route.useSearch();
  return <FirstContact next={next} />;
}
