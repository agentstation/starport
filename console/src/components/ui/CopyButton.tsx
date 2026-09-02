import { Check, Copy } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { report } from "@/lib/mutations";

// CopyButton copies text, says "Copied" for two seconds where a screen
// reader hears it, and reports a copy the browser refused.
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
      timer.current = setTimeout(() => setCopied(false), 2000);
    } catch {
      report("Copy failed. Select the text and copy it by hand.");
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
      <span role="status">{copied ? "Copied" : label}</span>
    </button>
  );
}
