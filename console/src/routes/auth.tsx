import { createFileRoute, redirect } from "@tanstack/react-router";
import { useState } from "react";

import { ApiError, hasCredential, openSession } from "@/lib/api";

import { AUTH_PATH } from "./__root";

type AuthSearch = { next?: string };

// destination is where a reader goes once the session opens.
//
// Only a path on this origin is accepted. `next` arrives in a URL, so it is
// whatever the last link said; honouring a full URL would make the console a
// redirector — follow a link, sign in to your own gateway, land on somebody
// else's page carrying the confidence of having just authenticated. A leading
// `//` is rejected for the same reason: browsers read it as a host.
export function destination(next: string | undefined): string {
  if (!next) return "/";
  if (!next.startsWith("/") || next.startsWith("//")) return "/";
  if (next.startsWith(AUTH_PATH)) return "/";
  return next;
}

export const Route = createFileRoute("/auth")({
  validateSearch: (search: Record<string, unknown>): AuthSearch =>
    typeof search.next === "string" ? { next: search.next } : {},
  // A reader who already has a credential has no business on this page; the
  // root guard sent them here, or a stale bookmark did.
  beforeLoad: ({ search }) => {
    if (hasCredential()) throw redirect({ href: destination(search.next) });
  },
  component: FirstContact,
});

// FirstContact is the page a browser with no session sees.
//
// It is deliberately not a card inside the console. Someone reading it has no
// session, so every panel around it would be a locked panel, and the one thing
// they need to do would be one thing among many.
function FirstContact() {
  const { next } = Route.useSearch();
  const [token, setToken] = useState("");
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState("");

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setPending(true);
    setProblem("");
    try {
      await openSession(token.trim());
      // A full navigation rather than a router push. The session arrived as a
      // cookie set on this response, and a reload is what guarantees every
      // query in the shell starts from the credential the browser now holds
      // rather than from the one it did not have a moment ago.
      window.location.assign(destination(next));
    } catch (error) {
      setProblem(
        error instanceof ApiError
          ? error.message
          : "The gateway could not be reached. Check that it is still running.",
      );
      setPending(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-screen w-full max-w-lg flex-col justify-center gap-6 px-6 py-16">
      <header className="flex flex-col gap-2">
        <h1 className="text-xl font-medium text-text-1">Sign in to this gateway</h1>
        <p className="text-base text-text-3">
          The console needs a session before it can read the catalog, chat, or manage providers. Run{" "}
          <code className="rounded-sm bg-bg-2 px-1 font-mono text-sm text-text-2">
            starport auth token
          </code>{" "}
          on the machine running this gateway and paste what it prints.
        </p>
      </header>
      <form className="flex flex-col gap-3" onSubmit={submit}>
        <label className="flex flex-col gap-1.5">
          <span className="text-sm text-text-2">Local admin token</span>
          <input
            type="password"
            value={token}
            onChange={(event) => setToken(event.target.value)}
            placeholder="starport_local_…"
            autoComplete="off"
            autoFocus
            className="h-9 w-full rounded-sm border border-border-2 bg-bg-canvas px-3 font-mono text-sm text-text-1 placeholder:text-text-4"
          />
        </label>
        {problem ? (
          <p role="alert" className="whitespace-pre-line text-sm text-error">
            {problem}
          </p>
        ) : null}
        <button
          type="submit"
          disabled={pending || !token.trim()}
          className="h-9 rounded-sm bg-accent px-4 text-sm font-medium text-accent-ink transition-colors duration-150 ease-standard hover:bg-accent-hover disabled:opacity-50"
        >
          {pending ? "Opening session…" : "Open console"}
        </button>
      </form>
    </main>
  );
}
