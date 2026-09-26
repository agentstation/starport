import { cva, type VariantProps } from "class-variance-authority";
import { ExternalLink as NewTabIcon } from "lucide-react";
import type { ComponentType, ReactNode } from "react";

import { cn } from "@/lib/utils";

// Every console link that leaves the app goes through this anchor: it
// opens a new tab and carries the new-tab glyph so a reader can tell,
// before clicking, that the link navigates away. An optional leading
// icon names the destination (a book for docs, a brand mark for GitHub).
//
// The variants own the color and the hover treatment. `link` is the
// accent link in running text, `quiet` a tertiary link that brightens,
// `plain` primary text that turns accent on hover, and `button` a ghost
// button whose label is a destination. A call site adds layout alone.
const externalLinkVariants = cva(
  "inline-flex items-center gap-1 transition-colors duration-150 ease-standard",
  {
    variants: {
      variant: {
        link: "text-accent-link hover:underline",
        quiet: "text-text-3 hover:text-text-1",
        plain: "text-text-1 hover:text-accent-link",
        button: "h-9 rounded-sm px-3 text-sm text-text-2 hover:bg-bg-hover",
      },
    },
    defaultVariants: { variant: "link" },
  },
);

export function ExternalLink({
  href,
  icon: Icon,
  variant,
  className,
  iconClassName = "size-3.5 shrink-0",
  children,
}: {
  href: string;
  icon?: ComponentType<{ className?: string }>;
  className?: string;
  iconClassName?: string;
  children: ReactNode;
} & VariantProps<typeof externalLinkVariants>) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      data-variant={variant ?? "link"}
      className={cn(externalLinkVariants({ variant }), className)}
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
