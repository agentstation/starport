import type { ReactNode } from "react";

// Calm density (DESIGN.md): sequential content uses flat sections with
// hairline dividers, not cards; 48px between sections. Every settings
// section shares this one shape, so the page reads as one list.
export function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section className="border-t border-border-1 py-6 first:border-t-0 first:pt-0">
      <h2 className="text-sm font-medium text-text-1">{title}</h2>
      {description && <p className="mt-1 text-sm text-text-3">{description}</p>}
      <div className="mt-4">{children}</div>
    </section>
  );
}
