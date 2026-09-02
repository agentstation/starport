import { RotateCw } from "lucide-react";

// LoadFailed is what a panel shows when its read failed and the page around
// it still stands. It names what is missing, quotes the failure, and
// offers the one action that can change the answer, so a failed read never
// looks like an empty result.
export function LoadFailed({
  what,
  error,
  onRetry,
  className = "",
}: {
  what: string;
  error?: unknown;
  onRetry?: () => void;
  className?: string;
}) {
  const message =
    error instanceof Error ? error.message : typeof error === "string" ? error : undefined;
  return (
    <div
      role="alert"
      className={`flex flex-col gap-2 rounded-md border border-border-1 bg-bg-panel p-5 ${className}`}
    >
      <p className="text-sm font-medium text-text-1">Could not load {what}</p>
      {message && <p className="break-words font-mono text-xs text-text-3">{message}</p>}
      {onRetry && (
        <div>
          <button
            type="button"
            onClick={onRetry}
            className="inline-flex h-8 items-center gap-1.5 rounded-sm border border-border-1 bg-bg-raised px-3 text-sm text-text-1 transition-colors duration-150 ease-standard hover:border-border-2"
          >
            <RotateCw aria-hidden="true" className="size-3.5" />
            Try again
          </button>
        </div>
      )}
    </div>
  );
}
