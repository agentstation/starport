import { useState } from "react";

import { Card, CardTitle } from "@/components/ui/Card";
import { setApiKey } from "@/lib/api";

// ConnectCard collects the gateway API key when none is stored. The full
// key workflow lives on the settings page (CM9); this unblocks first run.
export function ConnectCard() {
  const [draft, setDraft] = useState("");
  const save = () => {
    const key = draft.trim();
    if (key) setApiKey(key);
  };
  return (
    <Card>
      <CardTitle>Connect</CardTitle>
      <p className="mb-4 text-base text-text-3">
        Set your gateway API key to unlock the model catalog, chat, providers,
        and key management. The key stays in this browser.
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
          placeholder="sk-starport-…"
          aria-label="Gateway API key"
          autoComplete="off"
          className="h-9 min-w-0 flex-1 rounded-sm border border-border-2 bg-bg-canvas px-3 font-mono text-sm text-text-1 placeholder:text-text-4"
        />
        <button
          type="submit"
          disabled={!draft.trim()}
          className="h-9 shrink-0 rounded-sm bg-accent px-4 text-sm font-medium text-accent-ink transition-colors duration-150 ease-standard hover:bg-accent-hover disabled:opacity-50"
        >
          Set API key
        </button>
      </form>
    </Card>
  );
}
