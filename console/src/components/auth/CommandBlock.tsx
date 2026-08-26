import { useEffect, useState } from "react";

// CommandBlock shows a command and hands it to the clipboard.
//
// The copy control renders only where the browser has a clipboard to write to.
// `navigator.clipboard` is absent on an insecure origin that is not localhost —
// which is the network case this page warns about — and a button that silently
// did nothing there would be worse than no button at all. The command itself is
// plain selectable text either way.
export function CommandBlock({ command, note }: { command: string; note: string }) {
  const [copied, setCopied] = useState(false);
  const [problem, setProblem] = useState(false);
  const clipboard = typeof navigator !== "undefined" ? navigator.clipboard : undefined;

  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 2000);
    return () => window.clearTimeout(timer);
  }, [copied]);

  const copy = async () => {
    if (!clipboard) return;
    try {
      await clipboard.writeText(command);
      setProblem(false);
      setCopied(true);
    } catch {
      // A refused clipboard permission is not worth an error banner. The reader
      // can still select the command, and saying so is more use than saying the
      // write failed.
      setProblem(true);
    }
  };

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-2 rounded-sm border border-border-2 bg-bg-raised px-3 py-2">
        <code className="min-w-0 flex-1 truncate font-mono text-sm text-text-1">{command}</code>
        {clipboard ? (
          <button
            type="button"
            onClick={copy}
            aria-label={`Copy ${command}`}
            className="shrink-0 rounded-sm px-2 py-0.5 text-sm text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
          >
            {copied ? "Copied" : "Copy"}
          </button>
        ) : null}
      </div>
      <p className="text-sm text-text-3">
        {problem ? "This browser refused the clipboard — select the command instead." : note}
      </p>
    </div>
  );
}
