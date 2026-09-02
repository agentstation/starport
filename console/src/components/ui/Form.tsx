import { cloneElement, isValidElement, useId, type ReactNode } from "react";

// Shared form scaffolding (DESIGN.md): 36px controls on the raised
// ground with a hairline border that turns accent under focus-visible,
// beside the two-layer accent ring that tokens.css gives every
// focus-visible control. Routes compose these instead of restating the
// control contract.

export const INPUT_CLASS =
  "h-9 rounded-sm border border-border-2 bg-bg-raised px-3 text-sm text-text-1 outline-none transition-colors duration-150 ease-standard placeholder:text-text-4 focus-visible:border-accent aria-invalid:border-error";

export const TEXTAREA_CLASS =
  "rounded-sm border border-border-2 bg-bg-raised px-3 py-2 text-sm text-text-1 outline-none transition-colors duration-150 ease-standard placeholder:text-text-4 focus-visible:border-accent aria-invalid:border-error";

// The control props Field wires. A control that spreads its props gets the
// description and error linked and the invalid state marked; a control
// that does not still renders, and the text beside it still reads.
type ControlProps = {
  "aria-describedby"?: string;
  "aria-invalid"?: boolean | "true" | "false";
  "aria-required"?: boolean | "true" | "false";
};

// Field owns one labelled control and the text around it: the label, an
// optional required mark, the error when there is one, and the hint. It
// links the error and the hint to the control through aria-describedby
// and marks the control invalid, so the message a sighted person reads
// beside the box is the message a screen reader hears at the box. The
// error and the hint sit outside the label element, so the control's name
// stays the label and the rest arrives as its description.
export function Field({
  label,
  hint,
  error,
  required = false,
  children,
}: {
  label: string;
  hint?: string;
  error?: string;
  required?: boolean;
  children: ReactNode;
}) {
  const id = useId();
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const control = isValidElement<ControlProps>(children)
    ? cloneElement(children, {
        "aria-describedby":
          [children.props["aria-describedby"], errorId, hintId].filter(Boolean).join(" ") ||
          undefined,
        "aria-invalid": error ? true : children.props["aria-invalid"],
        "aria-required": required ? true : children.props["aria-required"],
      })
    : children;
  return (
    <div className="flex flex-col gap-1.5">
      <label className="flex flex-col gap-1.5">
        <span className="text-xs font-medium text-text-2">
          {label}
          {required && (
            <span aria-hidden="true" className="ml-0.5 text-error">
              *
            </span>
          )}
        </span>
        {control}
      </label>
      {error && (
        <span id={errorId} role="alert" className="text-xs text-error">
          {error}
        </span>
      )}
      {hint && (
        <span id={hintId} className="text-xs text-text-4">
          {hint}
        </span>
      )}
    </div>
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
      className="flex h-11 items-center rounded-xs px-2 text-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-2 disabled:opacity-50 sm:h-7"
    >
      {children}
    </button>
  );
}
