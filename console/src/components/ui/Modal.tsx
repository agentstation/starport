import { X } from "lucide-react";
import { useEffect, useRef, type ReactNode } from "react";

// Modal is the centered dialog surface (DESIGN.md: radius 12, raised
// ground, border-2, layered shadow, backdrop black@0.6). Escape and the
// backdrop close it; destructive flows restate the object name in their
// own body copy.
export function Modal({
  title,
  onClose,
  children,
  footer,
  wide = false,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
}) {
  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    dialogRef.current?.focus();
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-6">
      <button
        type="button"
        aria-label="Close dialog"
        onClick={onClose}
        className="absolute inset-0 cursor-default bg-black/60"
      />
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        className={`relative flex max-h-full w-full flex-col rounded-lg border border-border-2 bg-bg-raised shadow-[0_8px_24px_rgba(0,0,0,0.4)] outline-none ${
          wide ? "max-w-xl" : "max-w-md"
        }`}
      >
        <div className="flex items-center justify-between border-b border-border-1 px-5 py-4">
          <h2 className="text-base font-semibold text-text-1">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
          >
            <X className="size-4" />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto px-5 py-4">{children}</div>
        {footer && (
          <div className="flex justify-end gap-2 border-t border-border-1 px-5 py-3">
            {footer}
          </div>
        )}
      </div>
    </div>
  );
}
