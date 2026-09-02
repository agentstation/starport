import type { ReactNode } from "react";

// Shared form scaffolding (DESIGN.md): 36px controls on the raised
// ground with a hairline border that turns accent under focus-visible,
// beside the two-layer accent ring that tokens.css gives every
// focus-visible control. Routes compose these instead of restating the
// control contract.

export const INPUT_CLASS =
  "h-9 rounded-sm border border-border-2 bg-bg-raised px-3 text-sm text-text-1 outline-none transition-colors duration-150 ease-standard placeholder:text-text-4 focus-visible:border-accent";

export const TEXTAREA_CLASS =
  "rounded-sm border border-border-2 bg-bg-raised px-3 py-2 text-sm text-text-1 outline-none transition-colors duration-150 ease-standard placeholder:text-text-4 focus-visible:border-accent";

export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-xs font-medium text-text-2">{label}</span>
      {children}
      {hint && <span className="text-xs text-text-4">{hint}</span>}
    </label>
  );
}

export function GhostButton({
  children,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type="button"
      {...props}
      className="flex h-9 items-center rounded-sm px-3 text-sm text-text-2 transition-colors duration-150 ease-standard hover:bg-bg-hover disabled:opacity-50"
    >
      {children}
    </button>
  );
}

export function PrimaryButton({
  children,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type="button"
      {...props}
      className="flex h-9 items-center gap-1.5 rounded-sm bg-accent px-4 text-sm font-medium text-accent-ink transition-colors duration-150 ease-standard hover:bg-accent-hover disabled:opacity-50"
    >
      {children}
    </button>
  );
}

// DestructiveButton is the error-solid confirm action of a destructive
// dialog (DESIGN.md buttons). The dialog restates the object name; this
// button names the verb.
export function DestructiveButton({
  children,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type="button"
      {...props}
      className="flex h-9 items-center gap-1.5 rounded-sm bg-error px-4 text-sm font-medium text-error-ink transition-opacity duration-150 ease-standard hover:opacity-90 disabled:opacity-50"
    >
      {children}
    </button>
  );
}

export function RowAction({
  children,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type="button"
      {...props}
      className="flex h-7 items-center rounded-xs px-2 text-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-2 disabled:opacity-50"
    >
      {children}
    </button>
  );
}
