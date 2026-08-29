import { ExternalLink as NewTabIcon } from "lucide-react";
import type { ComponentType, ReactNode } from "react";

// Every console link that leaves the app goes through this anchor: it
// opens a new tab and carries the new-tab glyph so a reader can tell,
// before clicking, that the link navigates away. An optional leading
// icon names the destination (a book for docs, a brand mark for GitHub).
export function ExternalLink({
  href,
  icon: Icon,
  className = "",
  iconClassName = "size-3.5 shrink-0",
  children,
}: {
  href: string;
  icon?: ComponentType<{ className?: string }>;
  className?: string;
  iconClassName?: string;
  children: ReactNode;
}) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className={`inline-flex items-center gap-1 ${className}`}
    >
      {Icon && <Icon className={iconClassName} />}
      {children}
      <NewTabIcon
        data-testid="new-tab-icon"
        aria-hidden="true"
        className="size-3 shrink-0"
      />
    </a>
  );
}
