import type { ReactNode } from "react";

// Settings share flat sections, dividers, and readable help text.
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
      {description && <p className="mt-1 text-[0.875rem] leading-relaxed text-text-2">{description}</p>}
      <div className="mt-4">{children}</div>
    </section>
  );
}
