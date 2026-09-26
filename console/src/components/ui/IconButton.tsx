import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

// IconButton is a square control whose only visible content is an icon.
// One label serves as the accessible name and as the tooltip, so the two
// can never drift apart and no button ships a native title attribute.
//
// The variants own every color and shape (DESIGN.md components). A call
// site adds layout alone: a margin, a flex position, a responsive hide.
const iconButtonVariants = cva(
  "inline-flex shrink-0 items-center justify-center transition-colors duration-150 ease-standard disabled:pointer-events-none disabled:opacity-50 max-sm:min-h-11 max-sm:min-w-11",
  {
    variants: {
      variant: {
        ghost: "text-text-3 hover:bg-bg-hover hover:text-text-1",
        outline:
          "border border-border-2 bg-bg-raised text-text-2 hover:bg-bg-hover hover:text-text-1",
        destructive: "text-text-3 hover:bg-error-tint hover:text-error",
      },
      size: {
        xs: "size-6 rounded-xs",
        sm: "size-7 rounded-xs",
        md: "size-8 rounded-sm",
        lg: "size-11 rounded-sm",
      },
    },
    defaultVariants: { variant: "ghost", size: "sm" },
  },
);

export function IconButton({
  label,
  variant,
  size,
  className,
  children,
  ...props
}: { label: string } & VariantProps<typeof iconButtonVariants> &
  Omit<ComponentProps<"button">, "aria-label" | "title">) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <button
            type="button"
            aria-label={label}
            data-variant={variant ?? "ghost"}
            data-size={size ?? "sm"}
            className={cn(iconButtonVariants({ variant, size }), className)}
            {...props}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}
