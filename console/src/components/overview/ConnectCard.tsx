import { useState } from "react";

import { Card, CardTitle } from "@/components/ui/Card";
import { setApiKey } from "@/lib/api";
import { useApiKeyRejected } from "@/lib/useApiKey";

// ConnectCard collects the gateway API key. It covers both credential states:
// no key stored yet, and a stored key the gateway has refused. The second is
// routine in development, where each `starport dev` run mints a key that lives
// only in memory, so a restart strands the key in this browser.
export function ConnectCard() {
  const [draft, setDraft] = useState("");
  const rejected = useApiKeyRejected();
  const save = () => {
    const key = draft.trim();
    if (key) setApiKey(key);
  };
  return (
    <Card>
      <CardTitle>{rejected ? "Reconnect" : "Connect"}</CardTitle>
      <p className="mb-4 text-base text-text-3">
        {rejected
          ? "The gateway did not accept the key stored in this browser. A gateway restart mints a new key and prints it once to the terminal that started it. Paste the current key to reconnect."
          : "Set your gateway API key to unlock the model catalog, chat, providers, and key management. The key stays in this browser."}
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
