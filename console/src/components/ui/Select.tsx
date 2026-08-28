import { ChevronDown } from "lucide-react";
import type { ReactNode, SelectHTMLAttributes } from "react";

// Select wraps a native <select> in the console's control styling:
// appearance-none removes the platform chrome (the grey bevel and
// system chevron), and the lucide chevron matches every other control.
// The native popup keeps its OS behavior — correct for short lists;
// searchable option sets use FacetFilter instead.
//
// uiSize "sm" matches the h-8 filter-row controls; "md" (default)
// matches the h-9 form fields.
export function Select({
  className = "",
  uiSize = "md",
  children,
  ...props
}: SelectHTMLAttributes<HTMLSelectElement> & {
  children: ReactNode;
  uiSize?: "sm" | "md";
}) {
  const sizing =
    uiSize === "sm" ? "h-8 pl-2.5 pr-7 text-xs" : "h-9 pl-3 pr-8 text-sm";
  return (
    <span className={`relative inline-flex ${className}`}>
      <select
        {...props}
        className={`w-full appearance-none rounded-sm border border-border-2 bg-bg-raised text-text-1 outline-none transition-colors duration-150 ease-standard hover:border-border-3 focus:border-accent disabled:cursor-not-allowed disabled:opacity-60 ${sizing}`}
      >
        {children}
      </select>
      <ChevronDown
        aria-hidden="true"
        className={`pointer-events-none absolute top-1/2 -translate-y-1/2 text-text-4 ${
          uiSize === "sm" ? "right-2 size-3" : "right-2.5 size-3.5"
        }`}
      />
    </span>
  );
}
