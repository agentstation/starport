import type { ReactNode } from "react";

// Page header pattern (DESIGN.md): 20px/600 title, Text 3 description,
// at most one primary action on the right.
export function PageHeader({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <header className="mb-6 flex items-start justify-between gap-4">
      <div className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold text-text-1">{title}</h1>
        {description && <p className="text-base text-text-3">{description}</p>}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </header>
  );
}
