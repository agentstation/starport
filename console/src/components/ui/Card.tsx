import type { ReactNode } from "react";

// Card surface (DESIGN.md): panel background, hairline border, 8px radius.
export function Card({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={`rounded-md border border-border-1 bg-bg-panel p-5 ${className}`}>
      {children}
    </div>
  );
}

// CardTitle: 13px/500 uppercase-free label row with an optional right slot.
export function CardTitle({
  children,
  aside,
}: {
  children: ReactNode;
  aside?: ReactNode;
}) {
  return (
    <div className="mb-4 flex items-center justify-between gap-2">
      <h2 className="text-sm font-medium text-text-2">{children}</h2>
      {aside && <div className="flex items-center gap-2 text-sm text-text-3">{aside}</div>}
    </div>
  );
}
