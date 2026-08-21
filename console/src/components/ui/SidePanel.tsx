import { X } from "lucide-react";
import { useEffect, useRef, type ReactNode } from "react";

// SidePanel is the right-hand detail surface (DESIGN.md popover contract:
// raised ground, border-2, layered shadow). Escape and the backdrop close it.
export function SidePanel({
  title,
  onClose,
  children,
  footer,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
}) {
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    panelRef.current?.focus();
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50">
      <button
        type="button"
        aria-label="Close panel"
        onClick={onClose}
        className="absolute inset-0 cursor-default bg-black/60"
      />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        className="absolute inset-y-0 right-0 flex w-[480px] max-w-full flex-col border-l border-border-2 bg-bg-raised shadow-[0_8px_24px_rgba(0,0,0,0.4)] outline-none"
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
          <div className="border-t border-border-1 px-5 py-3">{footer}</div>
        )}
      </div>
    </div>
  );
}
