import { useState } from "react";

import { Card, CardTitle } from "@/components/ui/Card";
import { setApiKey } from "@/lib/api";
import { useGatewayAccessRejected } from "@/lib/useGatewayAccess";

// ConnectCard is what a reader with no working credential sees. It offers the
// two ways in, in the order they should be tried: `starport ui` from the
// machine running the gateway, which needs nothing pasted, and a gateway API
// key for a browser that is somewhere else.
//
// It covers both unusable states — nothing stored yet, and something the
// gateway has refused. The second is routine in development, where each
// `starport dev` run mints a key that lives only in memory, so a restart
// strands the key in this browser.
export function ConnectCard() {
  const [draft, setDraft] = useState("");
  const rejected = useGatewayAccessRejected();
  const save = () => {
    const key = draft.trim();
    if (key) setApiKey(key);
  };
  return (
    <Card>
      <CardTitle>{rejected ? "Reconnect" : "Connect"}</CardTitle>
      <p className="mb-3 text-base text-text-3">
        {rejected
          ? "The gateway did not accept what this browser presented. A console session expires, and a gateway restart mints a new API key, so either can go stale while the console still looks signed in."
          : "The console needs a credential before it can read the catalog, chat, or manage providers."}
      </p>
      <p className="mb-4 text-base text-text-3">
        On the machine running the gateway, run{" "}
        <code className="rounded-sm bg-bg-2 px-1 font-mono text-sm text-text-2">starport ui</code>. It
        opens a link that signs this browser in with a session, and nothing is pasted or stored here.
        From anywhere else, paste a gateway API key below; it stays in this browser.
      </p>
      <form
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          save();
        }}
      >
        <input
          type="password"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          placeholder="STARPORT_…"
          aria-label="Gateway API key"
          autoComplete="off"
          className="h-9 min-w-0 flex-1 rounded-sm border border-border-2 bg-bg-canvas px-3 font-mono text-sm text-text-1 placeholder:text-text-4"
        />
        <button
          type="submit"
          disabled={!draft.trim()}
          className="h-9 shrink-0 rounded-sm bg-accent px-4 text-sm font-medium text-accent-ink transition-colors duration-150 ease-standard hover:bg-accent-hover disabled:opacity-50"
        >
          {rejected ? "Reconnect" : "Set API key"}
        </button>
      </form>
    </Card>
  );
}
