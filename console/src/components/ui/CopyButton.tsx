import { Check, Copy } from "lucide-react";
import { useEffect, useRef, useState } from "react";

// CopyButton copies text and confirms with a brief check mark.
export function CopyButton({
  text,
  label,
}: {
  text: string | (() => string);
  label?: string;
}) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  const copy = async () => {
    const value = typeof text === "function" ? text() : text;
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard access denied; nothing useful to show.
    }
  };

  const Icon = copied ? Check : Copy;
  return (
    <button
      type="button"
      onClick={copy}
      title="Copy"
      aria-label={label ? `Copy ${label}` : "Copy"}
      className={`flex h-7 shrink-0 items-center gap-1.5 rounded-xs px-1.5 text-xs transition-colors duration-150 ease-standard hover:bg-bg-hover ${
        copied ? "text-success" : "text-text-3 hover:text-text-2"
      }`}
    >
      <Icon className="size-3.5" />
      {label}
    </button>
  );
}
