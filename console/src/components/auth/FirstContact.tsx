import { useState } from "react";

import { ApiError, openSession, setApiKey } from "@/lib/api";
import { useGatewayAccessRejected } from "@/lib/useGatewayAccess";

import { CommandBlock } from "./CommandBlock";
import { destination } from "./destination";
import { trustScope } from "./trust";

// FirstContact is the page a browser with no usable credential sees.
//
// It is deliberately not a card inside the console. Someone reading it has no
// session, so every panel around it would be a locked panel, and the one thing
// they need to do would be one thing among many.
//
// It answers the two questions a first reader actually has, in order: what is
// this asking me for, and what can it reach. The token field is a claim about
// *where you are* — you are at the machine that printed it. The gateway API key
// underneath is a different credential entirely, which is why it sits behind a
// disclosure and says so rather than sharing a field with the first.
export function FirstContact({ next }: { next?: string }) {
  const rejected = useGatewayAccessRejected();
  const scope = trustScope(window.location.hostname, window.location.protocol === "https:");
  const [token, setToken] = useState("");
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState("");

  // A full navigation rather than a router push. The credential arrived as a
  // cookie set on the last response, and a reload is what guarantees every query
  // in the shell starts from the credential the browser now holds rather than
  // from the one it did not have a moment ago. It is also what clears the
  // rejection this page may have been reached through.
  const enter = () => window.location.assign(destination(next));

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setPending(true);
    setProblem("");
    try {
      await openSession(token.trim());
      enter();
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
    <main className="mx-auto flex min-h-screen w-full max-w-xl flex-col justify-center gap-6 px-6 py-16">
      <header className="flex flex-col gap-2">
        <h1 className="text-xl font-medium text-text-1">Open this console</h1>
        <p className="text-base text-text-3">
          This gateway does not know who you are and does not need to. The token below is a claim
          about where you are: at the machine running it. Print it there and paste it here.
        </p>
      </header>

      <div className="flex flex-col gap-1 rounded-sm border border-border-1 bg-bg-panel px-3 py-2">
        <div className="flex items-center gap-2">
          <span
            aria-hidden="true"
            className={`size-1.5 rounded-full ${scope.local ? "bg-success" : "bg-warning"}`}
          />
          <span className="font-mono text-sm text-text-2">{scope.label}</span>
        </div>
        <p className="text-sm text-text-3">{scope.detail}</p>
      </div>

      {rejected ? (
        <p role="status" className="text-sm text-text-3">
          The gateway did not accept what this browser was holding. A console session expires, and
          `starport auth rotate` ends every session it had opened, so either can go stale while the
          console still looks connected.
        </p>
      ) : null}

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
          {pending ? "Opening…" : "Open console"}
        </button>
      </form>

      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-medium text-text-2">On the machine running this gateway</h2>
        <CommandBlock
          command="starport auth token --copy"
          note="Puts the token on that machine's clipboard instead of leaving it in the scrollback."
        />
        <CommandBlock
          command="starport auth url --open"
          note="Skips this page entirely: a one-time link that opens the console with nothing to paste."
        />
      </section>

      <details className="group rounded-sm border border-border-1 px-3 py-2">
        <summary className="cursor-pointer text-sm text-text-2 marker:text-text-4">
          Reaching this gateway from another machine
        </summary>
        <div className="mt-3 flex flex-col gap-3">
          <p className="text-sm text-text-3">
            The token above belongs to the machine running the gateway, so a browser somewhere else
            cannot print it. Use a gateway API key instead. It is a different credential: it
            authenticates a caller to this gateway and is metered against a tenant, where the token
            above is the operator of the machine. It stays in this browser.
          </p>
          <form
            className="flex gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              const key = new FormData(event.currentTarget).get("apiKey");
              if (typeof key !== "string" || !key.trim()) return;
              setApiKey(key.trim());
              enter();
            }}
          >
            <input
              type="password"
              name="apiKey"
              placeholder="STARPORT_…"
              aria-label="Gateway API key"
              autoComplete="off"
              className="h-9 min-w-0 flex-1 rounded-sm border border-border-2 bg-bg-canvas px-3 font-mono text-sm text-text-1 placeholder:text-text-4"
            />
            <button
              type="submit"
              className="h-9 shrink-0 rounded-sm border border-border-2 px-4 text-sm text-text-2 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
            >
              Use key
            </button>
          </form>
        </div>
      </details>
    </main>
  );
}
